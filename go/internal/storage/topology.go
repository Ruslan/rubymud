package storage

import "sync"

// This file is the storage half of the unified topology write-path (plan §8).
// It defines the concrete topology write operations, how each applies to an
// EDITABLE map set, the BEFORE-STATE snapshot each write captures for undo, and
// the in-memory per-map-set undo journal. The session layer (session/mapper.go)
// wraps ApplyTopologyOp with fork-if-frozen + broadcast + position-preserving
// tracker refresh — serialized per map_set_id — so UI edits and MCP CRUD share
// ONE path ("никаких параллельных путей" scoped to topology).

// TopologyOpKind enumerates the topology write ops. Slice 2 wired one kind
// (TopoPatchExits); slice 3 adds the CRUD kinds (upsert/link/unlink/delete room).
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
)

// TopologyOp is one topology write against a map set. Coord (zone,x,y,l) targets
// the room; the remaining fields are op-specific:
//   - TopoPatchExits: AddExits / RemoveExits (canonical letters "N".."D").
//   - TopoLink / TopoUnlink: Dir (a single canonical letter).
//   - TopoUpsertRoom: Fields — the partial patch (only set members apply).
type TopologyOp struct {
	Kind        TopologyOpKind
	Zone        string
	X           int
	Y           int
	L           int
	AddExits    []string
	RemoveExits []string
	Dir         string
	Fields      RoomFields
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
// "Existed" flag — captured before an op applies. It generalizes slice-2's
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

// UndoEntry is one journal record: the human label of the write plus the list of
// per-cell before-snapshots that reverse it. It is a LIST because a single op can
// touch more than one cell (link/unlink snapshot both the cell and its neighbor).
// Undo applies the snapshots through RestoreRoomSnapshots — restoring the literal
// prior state (not a symmetric inverse), so undo is correct even for idempotent
// no-op writes.
type UndoEntry struct {
	Label  string
	Before []RoomSnapshot
}

// PrimaryCell returns the coord of the first snapshot (the op's target cell) for
// reporting, plus ok=false when the entry has no snapshots.
func (e UndoEntry) PrimaryCell() (zone string, x, y, l int, ok bool) {
	if len(e.Before) == 0 {
		return "", 0, 0, 0, false
	}
	r := e.Before[0].Room
	return r.Zone, r.X, r.Y, r.L, true
}

// ApplyTopologyOp applies one topology op to the given (already-editable) map set
// and returns the BEFORE-STATE snapshots needed to undo it (one per affected
// cell). found=false (no error) means the op could not apply to a required target
// (e.g. delete/patch of a missing room), so callers soft-fail rather than 500;
// the snapshots are only meaningful when found=true. This is the ONLY function
// that mutates topology; every write-path (UI, MCP CRUD, undo) funnels through it.
func (s *Store) ApplyTopologyOp(mapSetID int64, op TopologyOp) (before []RoomSnapshot, found bool, err error) {
	switch op.Kind {
	case TopoPatchExits:
		return s.patchExitsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.AddExits, op.RemoveExits)
	case TopoUpsertRoom:
		return s.upsertRoomWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Fields)
	case TopoLink:
		return s.linkWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, true)
	case TopoUnlink:
		return s.linkWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.Dir, false)
	case TopoDeleteRoom:
		return s.deleteRoomWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L)
	default:
		return nil, false, nil
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
	case TopoUpsertRoom:
		return "upsert room"
	case TopoLink:
		return "link " + op.Dir
	case TopoUnlink:
		return "unlink " + op.Dir
	case TopoDeleteRoom:
		return "delete room"
	default:
		return string(op.Kind)
	}
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
