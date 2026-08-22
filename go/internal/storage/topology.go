package storage

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"gorm.io/gorm"
)

// This file is the storage half of the unified topology write-path.
// It defines the concrete topology write operations, how each applies to an
// EDITABLE map set, the BEFORE-STATE snapshot each write captures for undo, and
// the in-memory per-map-set undo journal. The session layer (session/mapper.go)
// wraps ApplyTopologyOp with fork-if-frozen + broadcast + position-preserving
// tracker refresh — serialized per map_set_id — so UI edits and MCP CRUD share
// ONE path ("никаких параллельных путей" scoped to topology).

// TopologyOpKind enumerates the topology write ops. The same machinery handles
// exit patches and CRUD kinds (upsert/link/unlink/delete room).
// Every kind plugs into ONE mechanism: add the kind, its apply arm in
// ApplyTopologyOp (capturing the affected cells' full before-snapshots), and its
// UndoLabel — the journal, per-set serialization, fork, broadcast, and
// position-preservation machinery are reused unchanged. A single generalized
// undo (RestoreRoomSnapshots over a list of full-room snapshots) reverses ALL of
// them, so create/delete/link/unlink and the exit patch share one undo path.
type TopologyOpKind string

const (
	// TopoPatchExits adds AddExits and removes RemoveExits on ONE room's edirs /
	// ch bitmask / display exits (the PatchRoomExits primitive).
	TopoPatchExits TopologyOpKind = "patch_exits"
	// TopoSetMutableExits replaces the per-room set of directions whose presence
	// may legitimately change without changing room identity.
	TopoSetMutableExits TopologyOpKind = "set_mutable_exits"
	// TopoUpsertRoom creates a room at the target cell, or partially updates an
	// existing one (only the fields the caller provided change; omitted fields are
	// preserved on update / defaulted on create). Keyed by (zone,x,y,l).
	TopoUpsertRoom TopologyOpKind = "upsert_room"
	// TopoLink adds exit Dir to the target cell and, when a room exists at the grid
	// neighbor, the reverse opposite(Dir) there (a bidirectional edge). With no
	// neighbor room it records the one-sided exit (still valid).
	TopoLink TopologyOpKind = "link"
	// TopoUnlink removes exit Dir from the target cell and the reverse on the
	// neighbor if that neighbor room advertises it.
	TopoUnlink TopologyOpKind = "unlink"
	// TopoDeleteRoom deletes the room at the target cell. Its room_annotations /
	// room_images are left dangling (see deleteRoomWithSnapshot).
	TopoDeleteRoom TopologyOpKind = "delete_room"
	// --- the hand-drawn layer (hidden exits and their negation) ---------------
	//
	// All three write map_edges rows with source='manual' and touch NO room field.
	// That is the whole point, and the difference from TopoLink/TopoUnlink, which
	// edit the room's own exits (the parsed layer) and are therefore erased by the
	// next «обновить комнату» / mud_room_upsert. A manual row survives every
	// re-derivation and OWNS its face, per (cell, dir).

	// TopoAddManualEdge marks a HIDDEN EXIT out of the target cell in Dir (and, unless
	// OneWay, the way back): "there IS a way here, whatever the exits line says".
	TopoAddManualEdge TopologyOpKind = "add_manual_edge"
	// TopoBlockManualEdge marks the NEGATION: "there is NO way out of this cell in
	// Dir, whatever the exits line says". It is the same kind of row with
	// passable=0 — the derivation skips the face (so re-parsing the room cannot
	// resurrect the edge) and the walk refuses to traverse it. This is how a
	// fabricated edge into a displaced room is taken back (Phasing п.9).
	TopoBlockManualEdge TopologyOpKind = "block_manual_edge"
	// TopoRemoveManualEdge clears the hand-drawn row on the face (whether it marked
	// a hidden exit or a negation), giving the face back to the derivation.
	TopoRemoveManualEdge TopologyOpKind = "remove_manual_edge"
	// TopoSetEdgeAttrs writes the ATTRIBUTES of one directed face: routing weight,
	// the phrase that takes the passage, the door + lock + opening command, and the
	// "do not route me through here" veto. Always exactly one face — every one of
	// those is a statement about the side you approach from, so a paired write would
	// be asserting something about the other side that the operator never said.
	//
	// It makes the face HAND-DRAWN, keeping whatever passability the face already
	// had. It has to: only `door` is derivable, so a face carrying another changed
	// attribute would lose it on the next «обновить комнату» if it stayed derived —
	// which is the exact failure the manual layer exists to prevent. The face goes
	// back to the derivation through TopoRemoveManualEdge ("auto"), as before.
	TopoSetEdgeAttrs TopologyOpKind = "set_edge_attrs"
	// TopoCreatePortal creates one directed, exact-target jump.  It is stored as a
	// manual seam row (kind="seam"): unlike legacy automaps seams its identity and
	// traversal target are the concrete endpoint coordinates, not zone/tag lookup.
	TopoCreatePortal TopologyOpKind = "create_portal"
	// TopoUpdatePortal changes one seam occurrence selected by its stable order.
	// Parsed imports are deliberately eligible: updating promotes that one row to
	// manual ownership, preventing a later room write from resurrecting its old
	// legacy representation.
	TopoUpdatePortal TopologyOpKind = "update_portal"
	// TopoDeletePortal deletes exactly one seam occurrence. Orders remain sparse;
	// an occurrence order is an identity, never a display position to renumber.
	TopoDeletePortal TopologyOpKind = "delete_portal"
	// TopoMoveRooms moves a selected aggregate namespace slice (or a whole source
	// zone for rename) without changing coordinates.
	TopoMoveRooms TopologyOpKind = "move_rooms"
	// TopoAutorecordRoom atomically creates one provisional room and the one
	// directed exact corridor that led into it. It never mirrors the reverse
	// traversal; that requires a later observed move.
	TopoAutorecordRoom TopologyOpKind = "autorecord_room"
)

// EdgeAttrPatch is a partial write of a face's EdgeAttrs: every member is a
// pointer so "omitted" (leave as it is) is distinct from "set to the zero value"
// (clear the phrase, drop the door). Same shape and reason as RoomFields — an MCP
// agent setting only `no_route` must not silently erase a door someone else marked.
type EdgeAttrPatch struct {
	Weight  *float64
	Phrase  *string
	Door    *bool
	Locked  *bool
	OpenCmd *string
	NoRoute *bool
}

// SeamSelector identifies one existing directed seam without exposing the volatile
// map_edges id. Order optionally selects one persisted occurrence among duplicates.
type SeamSelector struct {
	TargetZone string `json:"target_zone"`
	TargetTag  *int   `json:"target_tag"`
	Command    string `json:"command"`
	Order      *int   `json:"order,omitempty"`
}

// PortalSelector identifies a seam occurrence by its source (the TopologyOp
// coordinate) and stable persisted order. Unlike SeamSelector it does not depend
// on legacy target_zone/tag/command semantics, so it can retarget dead imports.
type PortalSelector struct {
	Order int `json:"order"`
}

// PortalTarget is an exact, authoritative endpoint. Zone/tag remain optional
// provenance on legacy rows only; portal routing always follows these coords.
type PortalTarget struct {
	Zone string `json:"zone"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	L    int    `json:"l"`
}

func (t PortalTarget) Cell() EdgeCell { return EdgeCell{Zone: t.Zone, X: t.X, Y: t.Y, L: t.L} }

// PortalPatch is intentionally partial. Omitted target/command/attrs preserve
// the selected occurrence's existing fields; this makes concurrent UI edits of
// distinct controls non-destructive.
type PortalPatch struct {
	Target  *PortalTarget `json:"target,omitempty"`
	Command *string       `json:"command,omitempty"`
	Attrs   EdgeAttrPatch `json:"attrs"`
}

// AutorecordInput is the immutable, bounded operation reserved by the session
// observation path. Source and target are logical coordinates; Fields contains
// the completed room block and provenance identifies the recording batch.
type AutorecordInput struct {
	Source            EdgeCell
	Target            EdgeCell
	Dir               string
	Command           string
	Fields            RoomFields
	RecordBatchID     string
	RecordOrigin      string
	VerificationState string
	RecordSource      string
}

// PortalWriteResult is the authoritative occurrence selected by a portal
// topology operation. It is filled while the map-set write lock and SQLite
// transaction are still held, then carried through Session to REST/MCP. Callers
// must never re-scan after unlocking to guess which duplicate they created.
type PortalWriteResult struct {
	Selector PortalSelector `json:"selector"`
	Source   PortalTarget   `json:"source"`
	Target   *PortalTarget  `json:"target,omitempty"`
	Command  string         `json:"command"`
	Attrs    EdgeAttrs      `json:"attrs"`
	Deleted  bool           `json:"deleted,omitempty"`
}

func (s SeamSelector) Valid() bool {
	return s.TargetZone != "" && s.TargetTag != nil
}

// Apply returns `base` with the patch's set members overwritten, plus whether the
// patch actually changes anything.
func (p EdgeAttrPatch) Apply(base EdgeAttrs) (EdgeAttrs, bool) {
	out := base
	if p.Weight != nil {
		out.Weight = *p.Weight
	}
	if p.Phrase != nil {
		out.Phrase = *p.Phrase
	}
	if p.Door != nil {
		out.Door = *p.Door
	}
	if p.Locked != nil {
		out.Locked = *p.Locked
	}
	if p.OpenCmd != nil {
		out.OpenCmd = *p.OpenCmd
	}
	if p.NoRoute != nil {
		out.NoRoute = *p.NoRoute
	}
	return out, out != base
}

// Empty reports whether the patch sets nothing at all.
func (p EdgeAttrPatch) Empty() bool {
	return p.Weight == nil && p.Phrase == nil && p.Door == nil && p.Locked == nil && p.OpenCmd == nil && p.NoRoute == nil
}

func (p EdgeAttrPatch) Validate() error {
	if p.Weight != nil && !ValidExplicitWeight(*p.Weight) {
		return fmt.Errorf("edge weight must be finite and greater than zero")
	}
	return nil
}

// TopologyOp is one topology write against a map set. Coord (zone,x,y,l) targets
// the room; the remaining fields are op-specific:
//   - TopoPatchExits: AddExits / RemoveExits (canonical letters "N".."D").
//   - TopoLink / TopoUnlink: Dir (a single canonical letter).
//   - TopoUpsertRoom: Fields — the partial patch (only set members apply).
//   - TopoAddManualEdge / TopoRemoveManualEdge: Dir, plus OneWay.
type TopologyOp struct {
	Kind         TopologyOpKind
	Zone         string
	X            int
	Y            int
	L            int
	AddExits     []string
	RemoveExits  []string
	MutableExits []string
	Dir          string
	Fields       RoomFields
	// Attrs is the TopoSetEdgeAttrs payload (a partial write of one face).
	Attrs EdgeAttrPatch
	// Seam selects an existing seam for TopoSetEdgeAttrs. Dir and Seam are
	// mutually exclusive at external boundaries; storage accepts only one.
	Seam *SeamSelector
	// Portal is the create payload; source is Zone/X/Y/L. PortalSelector selects
	// one existing occurrence for update/delete and PortalPatch carries updates.
	Portal         *PortalTarget
	PortalCommand  string
	PortalAttrs    EdgeAttrPatch
	PortalSelector *PortalSelector
	PortalPatch    PortalPatch
	// ZoneMove is the atomic zone namespace operation. Its report is populated
	// while the same transaction holds the optimistic revision check.
	ZoneMove       *ZoneMoveInput
	ZoneMoveResult *ZoneMovePreview
	// PortalResult is an optional operation-local output. It is written only after
	// the transaction has the exact selected occurrence and is used by the
	// serialized Session write path to return a race-free selector.
	PortalResult *PortalWriteResult
	// OneWay applies to the manual-edge ops. Rows are DIRECTED (one row = one
	// traversal), and the default is the pair, because a passage a player found is
	// normally walkable both ways; OneWay writes only the row leaving the target
	// cell. Expressing a one-way passage is one of the reasons edges are directed
	// (migration 016), and the derivation cannot produce one — RoomsConnect is
	// symmetric — so the hand-drawn layer is the only place one can come from.
	OneWay bool
	// Autorecord is the payload for TopoAutorecordRoom.
	Autorecord *AutorecordInput
}

// NormalizeTopologyOp canonicalizes every zone-bearing member of a topology
// write. It is deliberately shared by Session and Store: Session needs the
// canonical move payload when it refreshes a live tracker after the commit, and
// Store defends its direct callers from bypassing that boundary.
func NormalizeTopologyOp(op TopologyOp) (TopologyOp, error) {
	if op.Kind == TopoMoveRooms {
		if op.ZoneMove == nil {
			return op, fmt.Errorf("zone move is required")
		}
		move, err := NormalizeZoneMove(*op.ZoneMove)
		if err != nil {
			return op, err
		}
		op.ZoneMove = &move
		return op, nil
	}
	switch op.Kind {
	case TopoPatchExits, TopoSetMutableExits, TopoUpsertRoom, TopoLink, TopoUnlink, TopoDeleteRoom,
		TopoAddManualEdge, TopoBlockManualEdge, TopoRemoveManualEdge, TopoSetEdgeAttrs,
		TopoCreatePortal, TopoUpdatePortal, TopoDeletePortal:
		var err error
		if op.Zone, err = NormalizeZoneName(op.Zone); err != nil {
			return op, err
		}
		if op.Kind == TopoSetMutableExits {
			op.MutableExits = decodeStrArray(buildExitFields(normalizeDirLetters(op.MutableExits), nil).edirs)
		}
	case TopoAutorecordRoom:
		if op.Autorecord == nil {
			return op, fmt.Errorf("autorecord payload is required")
		}
		in := *op.Autorecord
		var err error
		if in.Source.Zone, err = NormalizeZoneName(in.Source.Zone); err != nil {
			return op, err
		}
		if in.Target.Zone, err = NormalizeZoneName(in.Target.Zone); err != nil {
			return op, err
		}
		op.Autorecord = &in
	default:
		return op, nil
	}
	if op.Seam != nil {
		seam := *op.Seam
		var err error
		if seam.TargetZone, err = NormalizeZoneName(seam.TargetZone); err != nil {
			return op, err
		}
		op.Seam = &seam
	}
	if op.Portal != nil {
		target := *op.Portal
		var err error
		if target.Zone, err = NormalizeZoneName(target.Zone); err != nil {
			return op, err
		}
		op.Portal = &target
	}
	if op.PortalPatch.Target != nil {
		target := *op.PortalPatch.Target
		var err error
		if target.Zone, err = NormalizeZoneName(target.Zone); err != nil {
			return op, err
		}
		op.PortalPatch.Target = &target
	}
	return op, nil
}

// RoomFields is the partial field set of a mud_room_upsert: every member is a
// pointer so "omitted" (nil, preserved on update / defaulted on create) is
// distinct from "set to zero/empty" (a non-nil pointer to the zero value). Exits
// can be given either as a display string (Exits) OR canonical dir letters
// (via SetEDirs); the apply normalizes whichever is provided into edirs/ch/exits
// consistently (Exits wins if both are set). Ch is accepted but recomputed from
// the resolved edirs so the mask never drifts from the exit set.
type RoomFields struct {
	Hint       *string
	Desc       *string
	Exits      *string
	edirs      []string
	edirsSet   bool // whether edirs was provided (nil-slice "omit" vs empty "clear")
	Ch         *int
	IsDT       *bool
	Pipe       *bool
	ImageIndex *int
	Note       *string
	DX         *int
	DY         *int
	DL         *int
}

// SetEDirs marks edirs as provided (even when empty, meaning "clear all exits").
// The caller uses this so an omitted edirs (preserve) is distinct from an
// explicit empty edirs (clear). Exits (display string) takes precedence if both
// are set.
func (f *RoomFields) SetEDirs(dirs []string) {
	f.edirs = dirs
	f.edirsSet = true
}

// EDirs / EDirsSet expose the parsed edirs patch (read side, for the apply).
func (f RoomFields) EDirs() []string { return f.edirs }
func (f RoomFields) EDirsSet() bool  { return f.edirsSet }

// RoomSnapshot is the exact prior state of ONE cell — a WHOLE room plus an
// "Existed" flag — captured before an op applies. It generalizes the earlier
// exits-only RoomExitState so create/delete/whole-room writes are undoable:
//   - Existed=true  → the room existed; Room holds its exact prior fields, and
//     undo upserts those fields back.
//   - Existed=false → no room was at this cell; undo deletes any room the op
//     created there (so undo of a create removes the room; undo of a patch on an
//     absent cell fabricates nothing).
//
// Room.ID is not part of the logical identity (a fork re-keys ids); undo matches
// by (map_set_id, zone,x,y,l) and preserves the surviving row's id. For an
// Existed=false snapshot only the coord fields of Room are meaningful.
type RoomSnapshot struct {
	Existed bool
	Room    Room
}

// DirectionalFaceSnapshot is a literal before-image of one grid/corridor face.
// Edge is meaningful only when Existed is true; no attribute is re-derived.
type DirectionalFaceSnapshot struct {
	Cell    EdgeCell
	Dir     string
	Existed bool
	Edge    MapEdge
}

// RoomLayoutSnapshot is the literal sparse-layout presence/value before a
// compound topology write.
type RoomLayoutSnapshot struct {
	Cell    EdgeCell
	Existed bool
	Layout  RoomLayout
}

// TopologyBefore is the COMPLETE before-state of one topology write — everything
// undo needs to put the set back exactly as it was.
//
// It has two halves because the topology has two halves. Rooms carries the
// per-cell RoomSnapshots, and restoring those is enough for every DERIVED edge:
// re-deriving the restored neighbourhood reproduces exactly the rows that existed,
// which is why this journal originally snapshotted rooms only. Manual
// carries the hand-drawn rows, which are derivable from nothing, so an op that
// destroys one (a room delete, or an unmark) destroys it permanently unless it was
// written down here first.
//
// Rooms is a LIST because a single op can touch more than one cell (link/unlink
// snapshot both the cell and its neighbor).
type TopologyBefore struct {
	Rooms    []RoomSnapshot
	Manual   ManualEdgeSnapshot
	Faces    []DirectionalFaceSnapshot
	Layouts  []RoomLayoutSnapshot
	ZoneMove *ZoneMoveBefore
	// Revision is the topology revision immediately before this write. The write
	// commits as Revision+1; the session journal uses that value to fence undo.
	Revision int64
	NoOp     bool
}

// UndoEntry is one journal record: the human label of the write plus the
// before-state that reverses it. Undo applies it through RestoreTopologyBefore —
// restoring the literal prior state (not a symmetric inverse). Storage still
// reports NoOp to callers for diagnostics, but the session write journal does not
// record it because there is no mutation to undo.
type UndoEntry struct {
	Label            string
	Before           TopologyBefore
	ExpectedRevision int64
}

// PrimaryCell returns the coord of the first room snapshot (the op's target cell)
// for reporting, plus ok=false when the entry has no room snapshots.
func (e UndoEntry) PrimaryCell() (zone string, x, y, l int, ok bool) {
	if len(e.Before.Rooms) == 0 {
		return "", 0, 0, 0, false
	}
	r := e.Before.Rooms[0].Room
	return r.Zone, r.X, r.Y, r.L, true
}

// ApplyTopologyOp applies one topology op to the given (already-editable) map set
// and returns the BEFORE-STATE snapshots needed to undo it (one per affected
// cell). found=false (no error) means the op could not apply to a required target
// (e.g. delete/patch of a missing room), so callers soft-fail rather than 500;
// the snapshots are only meaningful when found=true. This is the ONLY function
// that mutates topology; every write-path (UI, MCP CRUD, undo) funnels through it.
func (s *Store) ApplyTopologyOp(mapSetID int64, op TopologyOp) (before TopologyBefore, found bool, err error) {
	op, err = NormalizeTopologyOp(op)
	if err != nil {
		return TopologyBefore{}, false, err
	}
	// SQLITE_BUSY/SNAPSHOT can be returned while the deferred read transaction
	// upgrades to a writer. Retry the COMPLETE outer transaction, including the
	// topology revision read and bump. Retrying an autorecord arm alone would
	// reuse a stale revision snapshot and could commit a room/edge without its
	// matching fence.
	apply := func() error {
		before = TopologyBefore{}
		found = false
		return s.db.Transaction(func(tx *gorm.DB) error {
			revision, rerr := topologyRevisionTx(tx, mapSetID)
			if rerr != nil {
				return rerr
			}
			if s.topologyApplyAttemptHook != nil {
				if hookErr := s.topologyApplyAttemptHook(); hookErr != nil {
					return hookErr
				}
			}
			inner := &Store{db: tx}
			if op.Kind == TopoMoveRooms {
				if op.ZoneMove == nil {
					return fmt.Errorf("zone move is required")
				}
				var report ZoneMovePreview
				before, report, found, rerr = applyZoneMoveTx(tx, mapSetID, *op.ZoneMove)
				if op.ZoneMoveResult != nil {
					*op.ZoneMoveResult = report
				}
				return rerr
			}
			if rerr := tx.Exec("SAVEPOINT topology_apply_arm").Error; rerr != nil {
				return rerr
			}
			before, found, rerr = inner.applyTopologyOpUnfenced(mapSetID, op)
			if rerr != nil || !found {
				return rerr
			}
			before.Revision = revision
			unchanged, rerr := topologyMatchesBeforeTx(tx, mapSetID, before)
			if rerr != nil {
				return rerr
			}
			if unchanged {
				if rerr := tx.Exec("ROLLBACK TO topology_apply_arm").Error; rerr != nil {
					return rerr
				}
				if rerr := tx.Exec("RELEASE topology_apply_arm").Error; rerr != nil {
					return rerr
				}
				before.NoOp = true
				return nil
			}
			if rerr := tx.Exec("RELEASE topology_apply_arm").Error; rerr != nil {
				return rerr
			}
			return advanceTopologyRevisionTx(tx, mapSetID, revision)
		})
	}
	for attempt := 0; ; attempt++ {
		err = apply()
		if err == nil || !isSQLiteBusy(err) || attempt >= autorecordBusyRetries-1 {
			break
		}
		time.Sleep(time.Duration(5*(1<<attempt)) * time.Millisecond)
	}
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

func topologyMatchesBeforeTx(tx *gorm.DB, mapSetID int64, before TopologyBefore) (bool, error) {
	for _, want := range before.Rooms {
		got, err := snapshotRoom(tx, mapSetID, want.Room.Zone, want.Room.X, want.Room.Y, want.Room.L)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(got, want) {
			return false, nil
		}
	}
	for _, want := range before.Faces {
		var got MapEdge
		err := tx.Where("map_set_id=? AND kind IN ? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", mapSetID, []string{EdgeKindGrid, EdgeKindCorridor}, want.Cell.Zone, want.Cell.X, want.Cell.Y, want.Cell.L, want.Dir).First(&got).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return false, err
		}
		if (err == nil) != want.Existed {
			return false, nil
		}
		if want.Existed {
			got.ID, got.MapSetID = 0, 0
			w := want.Edge
			w.ID, w.MapSetID = 0, 0
			if !reflect.DeepEqual(got, w) {
				return false, nil
			}
		}
	}
	for _, want := range before.Layouts {
		var got RoomLayout
		err := tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", mapSetID, want.Cell.Zone, want.Cell.X, want.Cell.Y, want.Cell.L).First(&got).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return false, err
		}
		if (err == nil) != want.Existed {
			return false, nil
		}
		if want.Existed {
			got.MapSetID = 0
			w := want.Layout
			w.MapSetID = 0
			if !reflect.DeepEqual(got, w) {
				return false, nil
			}
		}
	}
	if len(before.Manual.Cells) == 0 {
		return true, nil
	}
	got, err := captureManualEdgesTx(tx, mapSetID, before.Manual.Cells)
	if err != nil {
		return false, err
	}
	zeroEdgeIDs := func(edges []MapEdge) []MapEdge {
		out := append([]MapEdge(nil), edges...)
		for i := range out {
			out[i].ID = 0
		}
		return out
	}
	want := before.Manual
	want.Edges = zeroEdgeIDs(want.Edges)
	want.Seams = zeroEdgeIDs(want.Seams)
	got.Edges = zeroEdgeIDs(got.Edges)
	got.Seams = zeroEdgeIDs(got.Seams)
	return reflect.DeepEqual(got, want), nil
}

// applyTopologyOpUnfenced dispatches one arm inside ApplyTopologyOp's outer
// transaction. Individual arms retain their savepoints, but cannot commit apart
// from the revision fence owned by the outer transaction.
func (s *Store) applyTopologyOpUnfenced(mapSetID int64, op TopologyOp) (before TopologyBefore, found bool, err error) {
	switch op.Kind {
	case TopoPatchExits:
		return s.patchExitsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.AddExits, op.RemoveExits)
	case TopoSetMutableExits:
		return s.setMutableExitsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.MutableExits)
	case TopoUpsertRoom:
		return s.upsertRoomWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Fields)
	case TopoLink:
		return s.linkWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, true)
	case TopoUnlink:
		return s.linkWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, false)
	case TopoDeleteRoom:
		return s.deleteRoomWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L)
	case TopoAddManualEdge:
		return s.manualEdgeWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, manualMarkPassable, op.OneWay)
	case TopoBlockManualEdge:
		return s.manualEdgeWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, manualMarkBlocked, op.OneWay)
	case TopoRemoveManualEdge:
		return s.manualEdgeWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, manualMarkCleared, op.OneWay)
	case TopoSetEdgeAttrs:
		if err := op.Attrs.Validate(); err != nil {
			return TopologyBefore{}, false, err
		}
		if op.Seam != nil {
			if op.Dir != "" || !op.Seam.Valid() {
				return TopologyBefore{}, false, fmt.Errorf("invalid seam selector")
			}
			return s.setSeamAttrsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, *op.Seam, op.Attrs)
		}
		return s.setEdgeAttrsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, op.Attrs)
	case TopoCreatePortal:
		if op.Portal == nil {
			return TopologyBefore{}, false, fmt.Errorf("portal target is required")
		}
		if err := op.PortalAttrs.Validate(); err != nil {
			return TopologyBefore{}, false, err
		}
		return s.createPortalWithSnapshot(mapSetID, EdgeCell{op.Zone, op.X, op.Y, op.L}, op.Portal.Cell(), op.PortalCommand, op.PortalAttrs, op.PortalResult)
	case TopoUpdatePortal:
		if op.PortalSelector == nil || op.PortalSelector.Order < 0 {
			return TopologyBefore{}, false, fmt.Errorf("portal order is required")
		}
		if err := op.PortalPatch.Attrs.Validate(); err != nil {
			return TopologyBefore{}, false, err
		}
		return s.updatePortalWithSnapshot(mapSetID, EdgeCell{op.Zone, op.X, op.Y, op.L}, *op.PortalSelector, op.PortalPatch, op.PortalResult)
	case TopoDeletePortal:
		if op.PortalSelector == nil || op.PortalSelector.Order < 0 {
			return TopologyBefore{}, false, fmt.Errorf("portal order is required")
		}
		return s.deletePortalWithSnapshot(mapSetID, EdgeCell{op.Zone, op.X, op.Y, op.L}, *op.PortalSelector, op.PortalResult)
	case TopoAutorecordRoom:
		if op.Autorecord == nil {
			return TopologyBefore{}, false, fmt.Errorf("autorecord payload is required")
		}
		return s.autorecordRoomWithSnapshotTx(s.db, mapSetID, *op.Autorecord)
	default:
		return TopologyBefore{}, false, nil
	}
}

// UndoLabel renders a short human description of what an op did (for the undo
// report). The label is stored in the journal entry at write time.
func (op TopologyOp) UndoLabel() string {
	switch op.Kind {
	case TopoPatchExits:
		var s string
		if len(op.AddExits) > 0 {
			s += "add " + joinFields(op.AddExits)
		}
		if len(op.RemoveExits) > 0 {
			if s != "" {
				s += ", "
			}
			s += "remove " + joinFields(op.RemoveExits)
		}
		if s == "" {
			s = "exit patch (no-op)"
		}
		return s
	case TopoSetMutableExits:
		if len(op.MutableExits) == 0 {
			return "clear mutable exits"
		}
		return "set mutable exits " + joinFields(op.MutableExits)
	case TopoUpsertRoom:
		return "upsert room"
	case TopoLink:
		return "link " + op.Dir
	case TopoUnlink:
		return "unlink " + op.Dir
	case TopoDeleteRoom:
		return "delete room"
	case TopoAddManualEdge:
		return "mark hidden exit " + op.Dir + manualWaySuffix(op.OneWay)
	case TopoBlockManualEdge:
		return "block exit " + op.Dir + manualWaySuffix(op.OneWay)
	case TopoRemoveManualEdge:
		return "clear hand-drawn exit " + op.Dir + manualWaySuffix(op.OneWay)
	case TopoSetEdgeAttrs:
		return "set edge attributes " + op.Dir
	case TopoCreatePortal:
		return "create portal"
	case TopoUpdatePortal:
		return "update portal"
	case TopoDeletePortal:
		return "delete portal"
	case TopoMoveRooms:
		return "move rooms between zones"
	case TopoAutorecordRoom:
		return "autorecord provisional room"
	default:
		return string(op.Kind)
	}
}

// manualWaySuffix labels a manual-edge op's directionality in the undo report,
// because "one-way" is the surprising case and the pair is the default.
func manualWaySuffix(oneWay bool) string {
	if oneWay {
		return " (one-way)"
	}
	return ""
}

// --- undo journal ----------------------------------------------------------

// undoJournalCap bounds the per-map-set undo stack. Beyond this the oldest entry
// is dropped (a bounded, in-memory stack — lost on restart, the owner's chosen
// persistence: MCP writes can happen with no browser attached, so there is no UI
// state to durably mirror and no undo DB table).
const undoJournalCap = 100

// WriteJournal is the in-memory, per-map-set undo journal for topology writes.
// It is keyed by map_set_id (NOT session) because map sets are global and shared
// across sessions — an undo in session A must reverse a write B made to the same
// set, and a write to set X must never be undoable from set Y. One journal is
// owned by the shared Store so every session and the MCP tool reach the same
// stacks. Access is serialized by the journal's mutex.
type WriteJournal struct {
	mu    sync.Mutex
	bySet map[int64][]UndoEntry
}

func newWriteJournal() *WriteJournal {
	return &WriteJournal{bySet: make(map[int64][]UndoEntry)}
}

// Push records the before-state UndoEntry for a write just applied to mapSetID.
// The stack is bounded: the oldest entry is dropped past undoJournalCap.
func (j *WriteJournal) Push(mapSetID int64, entry UndoEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	stack := append(j.bySet[mapSetID], entry)
	if len(stack) > undoJournalCap {
		stack = stack[len(stack)-undoJournalCap:]
	}
	j.bySet[mapSetID] = stack
}

// Pop removes and returns the most recent UndoEntry for mapSetID. ok=false when
// the set's stack is empty ("nothing to undo").
func (j *WriteJournal) Pop(mapSetID int64) (UndoEntry, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	stack := j.bySet[mapSetID]
	if len(stack) == 0 {
		return UndoEntry{}, false
	}
	top := stack[len(stack)-1]
	j.bySet[mapSetID] = stack[:len(stack)-1]
	return top, true
}

// Unpop puts an entry back on top of mapSetID's stack after an undo attempt
// FAILED to apply it. Without it a failed restore silently consumes the record:
// the next "undo" would then reverse the write BEFORE the one that failed, i.e.
// undo the wrong thing without saying so. Leaving a permanently-failing entry on
// top is the lesser evil — it keeps reporting the same error instead of quietly
// rewriting something the user did not ask about.
//
// Ordering: the whole pop-restore-unpop sequence runs under the map set's
// TopologyLock, and so does every write TO THAT SET, so the entry goes back where
// it came from. One case does not hold that exact lock — the FIRST write after a
// fork takes the lock on the frozen SOURCE set and journals under the fork's id —
// but it cannot interleave either: the fork's id does not exist until that write
// has finished, so there is no stack to race with.
func (j *WriteJournal) Unpop(mapSetID int64, entry UndoEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.bySet[mapSetID] = append(j.bySet[mapSetID], entry)
}

// SetTopExpectedRevision advances the fence of the next undo entry after a
// successful undo. Revisions are monotonic, so older entries cannot keep the
// revision that followed their original write once newer entries are undone.
func (j *WriteJournal) SetTopExpectedRevision(mapSetID, revision int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	stack := j.bySet[mapSetID]
	if len(stack) == 0 {
		return
	}
	stack[len(stack)-1].ExpectedRevision = revision
}

// Depth returns the number of undoable entries on mapSetID's stack (for tests /
// diagnostics).
func (j *WriteJournal) Depth(mapSetID int64) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.bySet[mapSetID])
}

// WriteJournal returns the store's shared topology undo journal, lazily creating
// it. Every session write-path and the MCP undo tool reach the same instance so
// the per-map-set stacks are truly global.
func (s *Store) WriteJournal() *WriteJournal {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if s.journal == nil {
		s.journal = newWriteJournal()
	}
	return s.journal
}

// --- per-map-set write serialization ---------------------------------------

// TopologyLock returns the per-map-set mutex serializing the topology write-path
// for a given active set id. The whole read-active-set → fork-if-frozen →
// setActive → apply → journal sequence is run under this lock, keyed on the set
// id at ENTRY, so two sessions racing their first write to the same FROZEN set
// cannot both read editable=false and double-fork it: the first holds the source
// set's lock through its fork+switch, the second blocks and then observes the
// active set is already the editable fork. It also guarantees journal order
// matches DB write order for a set. The lock lives on the shared Store so all
// sessions serialize against the same mutex per set.
func (s *Store) TopologyLock(mapSetID int64) *sync.Mutex {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	if s.topoLocks == nil {
		s.topoLocks = make(map[int64]*sync.Mutex)
	}
	m := s.topoLocks[mapSetID]
	if m == nil {
		m = &sync.Mutex{}
		s.topoLocks[mapSetID] = m
	}
	return m
}
