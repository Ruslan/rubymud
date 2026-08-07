package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// This file is the materialized topology. It owns THE rule for "is
// there an edge here" and the derivation of map_edges rows from rooms.
//
// WHY THE RULE LIVES IN storage AND NOT IN mapper. It has exactly two consumers:
// the walk (mapper) and the materializer (import, migration backfill, every
// topology write). mapper already imports storage, storage must not import mapper
// (that would cycle), and having the rule in two places is precisely the drift the
// materialization is meant to end — the acceptance criterion is
// that the materialized edge set equals the inferred one ELEMENT BY ELEMENT, which
// only means anything if both sides call the same function. So the rule is here,
// and mapper.IndexRoom.HasExplicitExits / ExitsInDir / ConnectsTo are thin
// forwarders that keep their existing signatures and doc contract.
//
// storage already carries the neighbouring half of this vocabulary for the same
// reason (chBitOrder, dirDelta, oppositeDir, buildExitFields in mapper.go).

// Edge kinds and sources. See migrations/016_map_edges.sql for the full contract.
const (
	// EdgeKindGrid is a step between two grid-adjacent cells of one zone.
	EdgeKindGrid = "grid"
	// EdgeKindSeam is an inter-zone jump imported from one legacy AutoMaps entry:
	// a command plus a SYMBOLIC target (zone + tag) resolved into to_* here.
	EdgeKindSeam = "seam"

	// EdgeSourceParsed — derived from the room's own exit data (ch/edirs) or from
	// an imported map entry, i.e. from what the map/MUD said.
	EdgeSourceParsed = "parsed"
	// EdgeSourceGrid — the permissive grid fallback: BOTH endpoints carry no exit
	// data at all, so bare adjacency connects them.
	EdgeSourceGrid = "grid"
	// EdgeSourceManual owns a face outside ordinary derivation. Most such rows are
	// hand-drawn; ZoneGuard marks the narrow system-owned exception. Re-derivation
	// never creates or destroys either, which preserves exact cross-zone endpoints.
	EdgeSourceManual = "manual"
)

// MapEdge is one materialized DIRECTED edge of a map set: one traversal, one way.
// Endpoints are logical coords (zone,x,y,l), not rooms.id — see the migration
// header for why, and for why a two-way passage is two rows.
//
// Dir is the grid direction walked (empty for a seam, which is described by its
// Command instead). To* is nil when the edge leads nowhere known (a dangling exit
// or a dead seam): such a row is recorded but not traversable.
// Passable=false is only ever produced by hand: it is a NEGATION ("no way here,
// whatever the room's exits line says"), stored as a row because the row is what
// owns the face and therefore what survives the re-derivation. The walk refuses it;
// the derivation skips its face. Every derived row is Passable=true.
type MapEdge struct {
	ID         int64   `gorm:"primaryKey" json:"id"`
	MapSetID   int64   `gorm:"column:map_set_id" json:"map_set_id"`
	Kind       string  `json:"kind"`
	Source     string  `json:"source"`
	Passable   bool    `json:"passable"`
	FromZone   string  `gorm:"column:from_zone" json:"from_zone"`
	FromX      int     `gorm:"column:from_x" json:"from_x"`
	FromY      int     `gorm:"column:from_y" json:"from_y"`
	FromL      int     `gorm:"column:from_l" json:"from_l"`
	Dir        string  `json:"dir"`
	ToZone     *string `gorm:"column:to_zone" json:"to_zone"`
	ToX        *int    `gorm:"column:to_x" json:"to_x"`
	ToY        *int    `gorm:"column:to_y" json:"to_y"`
	ToL        *int    `gorm:"column:to_l" json:"to_l"`
	Command    string  `json:"command"`
	TargetZone string  `gorm:"column:target_zone" json:"target_zone"`
	TargetTag  *int    `gorm:"column:target_tag" json:"target_tag"`
	// SeamOrder is the exact occurrence in the legacy import list. It is topology
	// data, not a presentation hint: duplicate seams must keep their BFS order
	// after room writes and rematerialization. Grid rows use zero.
	SeamOrder int `gorm:"column:seam_order" json:"seam_order"`
	// ZoneGuard identifies a manual grid row created solely to preserve a derived
	// face while its endpoints live in different zones. It is storage provenance,
	// not user markup, and is intentionally omitted from the public topology API.
	ZoneGuard bool `gorm:"column:zone_guard" json:"-"`

	EdgeAttrs
}

// EdgeAttrs is the attribute half of a directed row (migration 018): what you must
// DO to take this passage, and whether the router is allowed to. Embedded in
// MapEdge rather than listed inline so the write-path can carry "the attributes a
// caller is setting" as one value, and so the mapper's Edge can mirror it field for
// field.
//
// All of it is per-DIRECTION, which is the whole reason the table is directed: the
// phrase, the lock and the open command are properties of the side you arrive from,
// not of the hole in the wall.
//
// Only `Door` is derivable (from rooms.doors — see the migration header); the other
// four exist solely because a person or an agent asserted them, and nothing about
// the rooms can recreate them. That is why setting any of them makes the face
// hand-drawn (source='manual'): a derived row is recomputed from the rooms on the
// next write, and a recomputed row cannot carry an assertion the rooms do not make.
type EdgeAttrs struct {
	// Weight is this directed traversal's routing cost. Zero is the legacy Go
	// zero-value used by pre-weight test literals; EffectiveWeight treats it as 1.
	Weight float64 `json:"weight"`
	// Phrase replaces the direction letter when walking this edge ("двигать ящик").
	Phrase string `json:"phrase"`
	// Door: a door stands on this face. Derived from rooms.doors on parsed rows.
	Door bool `json:"door"`
	// Locked: that door is locked by default from this side. Reported, not refused.
	Locked bool `json:"locked"`
	// OpenCmd opens the door from this side; empty falls back to the default.
	OpenCmd string `gorm:"column:open_cmd" json:"open_cmd"`
	// NoRoute: walkable, but the router must not plan through it.
	NoRoute bool `gorm:"column:no_route" json:"no_route"`
}

// EffectiveWeight preserves the pre-weight meaning for old in-memory rows. DB
// writes always normalize zero to 1, so zero can only come from legacy literals.
func (a EdgeAttrs) EffectiveWeight() float64 {
	if a.Weight == 0 || math.IsNaN(a.Weight) || math.IsInf(a.Weight, 0) || a.Weight < 0 {
		return 1
	}
	return a.Weight
}

// ValidExplicitWeight is the storage write boundary for caller-supplied weights.
func ValidExplicitWeight(weight float64) bool {
	return weight > 0 && !math.IsNaN(weight) && !math.IsInf(weight, 0)
}

// Any reports whether the attribute set says anything at all. Used to decide
// whether a face is worth sending to the UI and whether a write has content.
func (a EdgeAttrs) Any() bool {
	return a.Weight != 0 && a.Weight != 1 || a.Phrase != "" || a.Door || a.Locked || a.OpenCmd != "" || a.NoRoute
}

func (MapEdge) TableName() string { return "map_edges" }

// EdgeCell is a logical room coordinate — the identity of an edge endpoint.
type EdgeCell struct {
	Zone string
	X    int
	Y    int
	L    int
}

// From returns the edge's source cell.
func (e MapEdge) From() EdgeCell { return EdgeCell{e.FromZone, e.FromX, e.FromY, e.FromL} }

// To returns the edge's resolved target cell, or ok=false when the edge leads
// nowhere known (dangling exit / dead seam).
func (e MapEdge) To() (EdgeCell, bool) {
	if e.ToZone == nil || e.ToX == nil || e.ToY == nil || e.ToL == nil {
		return EdgeCell{}, false
	}
	return EdgeCell{*e.ToZone, *e.ToX, *e.ToY, *e.ToL}, true
}

// SetTo sets (cell) or clears (nil) the resolved target.
func (e *MapEdge) SetTo(c *EdgeCell) {
	if c == nil {
		e.ToZone, e.ToX, e.ToY, e.ToL = nil, nil, nil, nil
		return
	}
	z, x, y, l := c.Zone, c.X, c.Y, c.L
	e.ToZone, e.ToX, e.ToY, e.ToL = &z, &x, &y, &l
}

// --- THE RULE --------------------------------------------------------------

// edgeDirOrder is the canonical iteration order over direction letters (mirrors
// mapper.dirOrder) so derivation output is deterministic.
var edgeDirOrder = []string{"N", "S", "E", "W", "U", "D"}

// RoomHasExplicitExits reports whether a room carries authoritative connectivity
// data (ch bits or an edirs list). A room with neither is "permissive" — its exits
// are unknown and it only participates in the grid fallback.
func RoomHasExplicitExits(ch int, edirs []string) bool { return ch != 0 || len(edirs) > 0 }

// RoomExitsInDir reports whether a room has a real exit in dir, using the
// AUTHORITATIVE connectivity data. `ch` (ChN..ChD) is more authoritative than
// the `exits`/`edirs` string, so direction derivation must NOT be inferred from
// raw visual x,y,l deltas (which are wrong for displaced rooms and special
// entrances). Priority:
//  1. if the room has any ch bits, ch is authoritative — exit iff that bit is set;
//  2. else if the room lists edirs, exit iff dir ∈ edirs;
//  3. else (no connectivity data at all) permissive — treat as an exit (blank
//     cells stay reachable, matching the reference grid fallback).
func RoomExitsInDir(ch int, edirs []string, dir string) bool {
	if ch != 0 {
		bit, ok := chBitOrder[dir]
		if !ok {
			return false
		}
		return ch&(1<<bit) != 0
	}
	if len(edirs) > 0 {
		for _, d := range edirs {
			if d == dir {
				return true
			}
		}
		return false
	}
	return true
}

// RoomsConnect reports whether there is a real edge from room a toward the
// grid-adjacent room b in direction dir. Connectivity is bidirectional, so the
// edge is real when EITHER authoritative endpoint records it:
//   - a explicitly advertises dir, OR
//   - b explicitly advertises the reverse (opposite dir) — this recovers
//     single-exit dead-end targets whose neighbor's map data failed to record the
//     forward link (the live "final turn into a dead-end dropped" bug).
//
// When NEITHER endpoint is explicit, fall back to permissive grid adjacency.
// Crucially, a *permissive* departing room does NOT fabricate an edge into an
// explicit neighbor that denies the back-link — that is the displaced-room guard
// (bank entered via the wrong visual direction).
//
// The relation is SYMMETRIC — RoomsConnect(a,b,dir) == RoomsConnect(b,a,opp(dir))
// — so today it derives the two directed rows of a passage together, and the live
// corpus has zero asymmetric pairs. That is a property of THIS rule, not of the
// storage: a one-way passage is representable, it just is not derivable yet.
func RoomsConnect(aCh int, aEDirs []string, bCh int, bEDirs []string, dir string) bool {
	opp := oppositeDir[dir]
	aExplicit := RoomHasExplicitExits(aCh, aEDirs)
	bExplicit := RoomHasExplicitExits(bCh, bEDirs)
	if aExplicit && RoomExitsInDir(aCh, aEDirs, dir) {
		return true
	}
	if bExplicit && RoomExitsInDir(bCh, bEDirs, opp) {
		return true
	}
	if !aExplicit && !bExplicit {
		return true // both permissive: grid fallback
	}
	return false
}

// --- derivation ------------------------------------------------------------

// edgeRoom is a room reduced to exactly what the edge rule needs, with the JSON
// array columns decoded once.
type edgeRoom struct {
	cell  EdgeCell
	ch    int
	edirs []string
	// doors is rooms.doors — the door faces the exits line put in parentheses. It
	// is the ONE attribute the derivation produces (migration 018), which is what
	// moves doors from the room onto the edges without a second source of truth.
	doors []string
}

func newGridEdgeRoom(r *Room) edgeRoom {
	return edgeRoom{
		cell:  EdgeCell{r.Zone, r.X, r.Y, r.L},
		ch:    r.Ch,
		edirs: DecodeJSONStrArray(r.EDirs),
		doors: DecodeJSONStrArray(r.Doors),
	}
}

// hasDoor reports whether the room records a door on the given face.
func (r *edgeRoom) hasDoor(dir string) bool {
	for _, d := range r.doors {
		if d == dir {
			return true
		}
	}
	return false
}

// DecodeJSONStrArray decodes one of the JSON string-array columns (edirs, doors).
// Deriving a whole set means decoding two of these per room (~16k
// unmarshals on the live corpus) and encoding/json dominated that, so the scanner
// below handles the shape these columns actually have — short, escape-free tokens —
// and hands anything containing a backslash back to encoding/json. Correctness
// therefore does not depend on that assumption.
//
// Exported because the mapper index decodes edirs/doors through the same parser.
func DecodeJSONStrArray(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	if strings.IndexByte(s, '\\') >= 0 {
		return decodeStrArray(s)
	}
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		j := strings.IndexByte(s[i+1:], '"')
		if j < 0 {
			return decodeStrArray(s) // unbalanced — let the real parser judge
		}
		out = append(out, s[i+1:i+1+j])
		i += j + 1
	}
	return out
}

// step returns the cell one grid step from c in dir.
func (c EdgeCell) step(dir string) EdgeCell {
	d := dirDelta[dir]
	return EdgeCell{c.Zone, c.X + d.dx, c.Y + d.dy, c.L + d.dl}
}

// beforeInZone orders two cells of one zone the way ListRooms does (l, x, y) —
// the tie-break that decides which room a seam's (zone,tag) target resolves to.
func (c EdgeCell) beforeInZone(o EdgeCell) bool {
	if c.L != o.L {
		return c.L < o.L
	}
	if c.X != o.X {
		return c.X < o.X
	}
	return c.Y < o.Y
}

// DeriveEdges materializes grid topology from persisted rooms. Imported seams are
// passed separately to DeriveImportedEdges so raw legacy input never becomes a
// room/runtime field.
func DeriveEdges(mapSetID int64, rooms []Room) []MapEdge {
	return DeriveGridEdges(mapSetID, rooms)
}

// DeriveImportedEdges adds an import draft's symbolic seams to the persisted-grid
// derivation. It is used only before map_edges are inserted, never on a room
// write, COW copy or runtime read.
func DeriveImportedEdges(mapSetID int64, rooms []Room, drafts []LegacySeamDraft) []MapEdge {
	targets := indexSeamTargets(rooms)

	out := DeriveGridEdges(mapSetID, rooms)
	for _, draft := range drafts {
		r := edgeRoom{cell: draft.From}
		for _, e := range deriveSeamEntries(mapSetID, &r, draft.Entries, func(zone string, tag int) *EdgeCell {
			if c, ok := targets[seamTargetKey{zone: norm.NFC.String(zone), tag: tag}]; ok {
				return &c
			}
			return nil
		}) {
			out = append(out, e)
		}
	}
	return out
}

type seamTargetKey struct {
	zone string
	tag  int
}

// indexSeamTargets is the one symbolic target rule used by both import and later
// re-resolution. Target selectors retain their raw zone text, but lookup uses NFC
// identity and the first coordinate in (l,x,y) order.
func indexSeamTargets(rooms []Room) map[seamTargetKey]EdgeCell {
	targets := make(map[seamTargetKey]EdgeCell)
	for i := range rooms {
		if rooms[i].Tag == nil {
			continue
		}
		cell := EdgeCell{Zone: rooms[i].Zone, X: rooms[i].X, Y: rooms[i].Y, L: rooms[i].L}
		key := seamTargetKey{zone: norm.NFC.String(rooms[i].Zone), tag: *rooms[i].Tag}
		if current, ok := targets[key]; !ok || cell.beforeInZone(current) {
			targets[key] = cell
		}
	}
	return targets
}

// DeriveGridEdges is the ordinary room-write/rematerialize rule. Seam rows are
// authoritative after import and are not re-derived from room data.
func DeriveGridEdges(mapSetID int64, rooms []Room) []MapEdge {
	prep := make([]edgeRoom, len(rooms))
	byCell := make(map[EdgeCell]*edgeRoom, len(rooms))
	for i := range rooms {
		prep[i] = newGridEdgeRoom(&rooms[i])
		byCell[prep[i].cell] = &prep[i]
	}
	out := make([]MapEdge, 0, len(rooms)*5/2)
	for i := range prep {
		r := &prep[i]
		for _, dir := range edgeDirOrder {
			if e, ok := deriveGridEdge(mapSetID, r, dir, byCell[r.cell.step(dir)]); ok {
				out = append(out, e)
			}
		}
	}
	return out
}

// deriveGridEdge returns the row that LEAVING r in direction dir contributes, if
// any. It is the ONE place the grid edge set is defined; both the full derivation
// and the per-cell write-path recompute go through it, so they cannot drift.
//
// Two outcomes:
//   - no room at the target cell: an explicit exit there yields a DANGLING row
//     (To nil, not traversable, no longer silently dropped); a permissive room
//     yields nothing (it must not fabricate exits it never claimed);
//   - room present: a row iff RoomsConnect, sourced 'grid' when the edge rests
//     purely on both endpoints being permissive, else 'parsed'.
//
// The reverse traversal is a SEPARATE row, emitted when the neighbour is walked.
// RoomsConnect is symmetric, so today the two always appear together; the schema
// no longer requires that.
func deriveGridEdge(mapSetID int64, r *edgeRoom, dir string, nb *edgeRoom) (MapEdge, bool) {
	e := MapEdge{
		MapSetID: mapSetID,
		Kind:     EdgeKindGrid,
		Source:   EdgeSourceParsed,
		Passable: true,
		FromZone: r.cell.Zone, FromX: r.cell.X, FromY: r.cell.Y, FromL: r.cell.L,
		Dir: dir,
		// The door migration (018): a parsed row carries the door the room's own
		// exits line put on this face. Everything else in EdgeAttrs is hand-drawn and
		// therefore lives only on manual rows, which this function never produces.
		EdgeAttrs: EdgeAttrs{Door: r.hasDoor(dir)},
	}
	if nb == nil {
		if !RoomHasExplicitExits(r.ch, r.edirs) || !RoomExitsInDir(r.ch, r.edirs, dir) {
			return MapEdge{}, false
		}
		return e, true // dangling
	}
	if !RoomsConnect(r.ch, r.edirs, nb.ch, nb.edirs, dir) {
		return MapEdge{}, false
	}
	if !RoomHasExplicitExits(r.ch, r.edirs) && !RoomHasExplicitExits(nb.ch, nb.edirs) {
		e.Source = EdgeSourceGrid
	}
	to := nb.cell
	e.SetTo(&to)
	return e, true
}

// deriveSeamEntries turns one import draft's entries into seam rows, in entry order
// (the walk's neighbour order must not change). resolve maps a symbolic
// (zone,tag) target to a cell, or nil when the target does not exist — a DEAD
// seam, which is still stored (To nil) so it can be found by query instead of
// only at route time. A partial/unparsable entry also keeps an occurrence row,
// with whatever selector fields could be recovered and a nil target tag.
func deriveSeamEntries(mapSetID int64, r *edgeRoom, entries []string, resolve func(zone string, tag int) *EdgeCell) []MapEdge {
	if len(entries) == 0 {
		return nil
	}
	out := make([]MapEdge, 0, len(entries))
	for order, a := range entries {
		zone, command, tag, hasTag := parseSeamDraft(a)
		e := MapEdge{
			MapSetID: mapSetID,
			Kind:     EdgeKindSeam,
			Source:   EdgeSourceParsed,
			Passable: true,
			FromZone: r.cell.Zone, FromX: r.cell.X, FromY: r.cell.Y, FromL: r.cell.L,
			Command: command, TargetZone: zone, SeamOrder: order,
		}
		if hasTag {
			t := tag
			e.TargetTag = &t
			e.SetTo(resolve(zone, tag))
		}
		out = append(out, e)
	}
	return out
}

// parseSeamDraft preserves the useful fields of every legacy import entry.
// A bad/missing tag is deliberately not discarded: it is a dead seam with a
// nullable selector, and map_edges must be able to report it without consulting
// any raw room data at runtime.
func parseSeamDraft(s string) (zone, command string, tag int, ok bool) {
	parts := strings.Split(s, "|")
	if len(parts) > 0 {
		// Symbolic target_zone is a zone key just like rooms.zone. Keep the
		// command verbatim, but canonicalize its namespace before materializing
		// the edge so a later semantic selector can find it exactly.
		zone = norm.NFC.String(strings.TrimSpace(parts[0]))
	}
	if len(parts) > 1 {
		command = parts[1]
	}
	if len(parts) < 3 {
		return zone, command, 0, false
	}
	t, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return zone, command, 0, false
	}
	return zone, command, t, true
}

// --- persistence -----------------------------------------------------------

// ListMapEdges returns every edge of a map set in materialization order (by id).
// Parsed seams begin in automaps order; the runtime index re-applies that source
// order because retained manual rows can outlive and overtake re-derived siblings.
//
// The scan is hand-rolled rather than gorm.Find because this is read on every
// reconnect, every active-set change and after every topology write, and a live
// set is ~19k narrow rows: GORM's reflective scan cost more than reading the 8k
// (much fatter) room rows next to it. The nullable target is scanned into a
// backing array and pointed at only once the slice has stopped growing, so a
// resolved row costs no allocation of its own.
func (s *Store) ListMapEdges(mapSetID int64) ([]MapEdge, error) {
	rows, err := s.db.Raw(`SELECT id, kind, source, passable, from_zone, from_x, from_y, from_l, dir,
		to_zone, to_x, to_y, to_l, command, target_zone, target_tag, seam_order, zone_guard
		FROM map_edges WHERE map_set_id = ? ORDER BY id ASC`, mapSetID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := []MapEdge{}
	targets := []EdgeCell{} // backing store for the resolved to_* pointers
	tags := []int{}         // backing store for target_tag
	resolved := []int{}     // index in edges of each row that has a target
	tagged := []int{}
	for rows.Next() {
		var e MapEdge
		// The narrow hot scan intentionally omits default attributes. Materialize
		// the database default here so storage callers see a complete EdgeAttrs;
		// legacy in-memory literals still use EffectiveWeight for their zero value.
		e.Weight = 1
		var toZone strNull
		var toX, toY, toL, targetTag intNull
		if err := rows.Scan(&e.ID, &e.Kind, &e.Source, &e.Passable, &e.FromZone, &e.FromX, &e.FromY, &e.FromL, &e.Dir,
			&toZone, &toX, &toY, &toL, &e.Command, &e.TargetZone, &targetTag, &e.SeamOrder, &e.ZoneGuard); err != nil {
			return nil, err
		}
		e.MapSetID = mapSetID
		if toZone.valid {
			targets = append(targets, EdgeCell{toZone.v, int(toX.v), int(toY.v), int(toL.v)})
			resolved = append(resolved, len(edges))
		}
		if targetTag.valid {
			tags = append(tags, int(targetTag.v))
			tagged = append(tagged, len(edges))
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, idx := range resolved {
		c := &targets[i]
		edges[idx].ToZone, edges[idx].ToX, edges[idx].ToY, edges[idx].ToL = &c.Zone, &c.X, &c.Y, &c.L
	}
	for i, idx := range tagged {
		edges[idx].TargetTag = &tags[i]
	}
	if err := attachEdgeAttrs(s.db, mapSetID, edges); err != nil {
		return nil, err
	}
	return edges, nil
}

// attachEdgeAttrs fills in the attribute half (migration 018) with a SECOND,
// FILTERED query instead of five more columns on the scan above.
//
// The five columns are set on a vanishing fraction of a set's rows — the live
// editable set carries 308 doors and no other markup at all across 19 183 rows —
// while the scan they would join runs on every reconnect and after every topology
// write. MEASURED on the owner's set (19 183 rows, medians of 12):
//
//	16 columns, one scan (the shape before attribute filtering)   78 ms
//	21 columns, one scan                                 93 ms
//	16 columns + this filtered second pass               79 ms
//
// i.e. widening the scan cost ~19% of the index read for data that is almost
// always the zero value, because the driver marshals every column of every row
// whether or not it says anything. SQLite evaluates the WHERE below natively and
// hands back only the rows that DO say something, so the price of the attribute
// layer tracks the amount of markup rather than the size of the map — the same
// shape (and the same reason) as manualGridFacesAt.
func attachEdgeAttrs(db *gorm.DB, mapSetID int64, edges []MapEdge) error {
	if len(edges) == 0 {
		return nil
	}
	rows, err := db.Raw(`SELECT id, weight, phrase, door, locked, open_cmd, no_route
		FROM map_edges
		WHERE map_set_id = ? AND (weight <> 1 OR door = 1 OR locked = 1 OR no_route = 1 OR phrase <> '' OR open_cmd <> '')`,
		mapSetID).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	// The outer scan is ordered by id, so the row a hit belongs to is a binary
	// search away — no map, no allocation per row.
	find := func(id int64) *MapEdge {
		lo, hi := 0, len(edges)-1
		for lo <= hi {
			mid := (lo + hi) / 2
			switch {
			case edges[mid].ID == id:
				return &edges[mid]
			case edges[mid].ID < id:
				lo = mid + 1
			default:
				hi = mid - 1
			}
		}
		return nil
	}
	for rows.Next() {
		var id int64
		var at EdgeAttrs
		if err := rows.Scan(&id, &at.Weight, &at.Phrase, &at.Door, &at.Locked, &at.OpenCmd, &at.NoRoute); err != nil {
			return err
		}
		if e := find(id); e != nil {
			e.EdgeAttrs = at
		}
	}
	return rows.Err()
}

// strNull / intNull are minimal sql.Scanner shims: sql.NullString/NullInt64 would
// do, but these keep the scan free of the interface conversions that dominate the
// per-row cost at this row count.
type strNull struct {
	v     string
	valid bool
}

func (n *strNull) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.v, n.valid = "", false
	case string:
		n.v, n.valid = v, true
	case []byte:
		n.v, n.valid = string(v), true
	default:
		// Never swallow this: falling through would leave valid=false, which reads
		// as "this edge leads nowhere" and would silently strip a route.
		return fmt.Errorf("map_edges: cannot scan %T into a text column", src)
	}
	return nil
}

type intNull struct {
	v     int64
	valid bool
}

func (n *intNull) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.v, n.valid = 0, false
	case int64:
		n.v, n.valid = v, true
	case float64:
		n.v, n.valid = int64(v), true
	default:
		return fmt.Errorf("map_edges: cannot scan %T into an integer column", src)
	}
	return nil
}

// insertEdges batch-inserts derived rows (ids are assigned by SQLite).
func insertEdges(tx *gorm.DB, edges []MapEdge) error {
	if len(edges) == 0 {
		return nil
	}
	for i := range edges {
		edges[i].ID = 0
		if edges[i].Weight == 0 {
			edges[i].Weight = 1
		} else if !ValidExplicitWeight(edges[i].Weight) {
			return fmt.Errorf("map edge weight must be finite and greater than zero")
		}
	}
	return tx.CreateInBatches(edges, 500).Error
}

// manualGridFaces returns the (cell, direction) faces of a set that a HAND-DRAWN
// grid row already occupies. Derivation must skip those faces: a face holds one
// row (the partial UNIQUE index says so, and worldNeighbors indexes edges by
// direction), manual rows survive re-derivation by design, so emitting the derived
// row too would collide on the index and roll back the whole write — a room edit
// next to a hand-drawn edge would simply fail. Manual therefore WINS on its face.
//
// Seams are exempt: they carry no direction, are not covered by the unique index,
// and a room may legitimately hold both its automaps seams and a hand-drawn jump.
func manualGridFaces(tx *gorm.DB, mapSetID int64) (map[edgeKey]bool, error) {
	return manualFacesQuery(tx.Where("map_set_id = ? AND kind = ? AND source = ?",
		mapSetID, EdgeKindGrid, EdgeSourceManual))
}

// manualGridFacesAt is the same question asked only about the cells a write can
// touch, and it is what the incremental write-path uses. The whole-set form above
// is for the whole-set materializers (import / rematerialize), where the scan is
// the cheap part of the job.
//
// The write-path must NOT use the whole-set form: it runs on every room write, so
// every write would pay for reading all of a set's hand-drawn rows to answer a
// question about six faces — the map getting slower to edit the more of it you had
// annotated. MEASURED on the live editable set (8 043 rooms, 19 189 edges), median
// of 15 upserts of one room:
//
//	whole-set form (manualGridFaces):  9.8 ms with 0 marks -> 23.3 ms with 770
//	this form (manualGridFacesAt):     4.4 ms with 0 marks ->  4.3 ms with 770
//
// i.e. the cost stops tracking the volume of markup entirely, and is lower even at
// zero marks, because ~13 indexed point lookups beat one filtered pass over the
// set. Same shape as the before-snapshot next door, for the same reason.
func manualGridFacesAt(tx *gorm.DB, mapSetID int64, cells []EdgeCell) (map[edgeKey]bool, error) {
	if len(cells) == 0 {
		return nil, nil
	}
	taken := map[edgeKey]bool{}
	for _, c := range cells {
		part, err := manualFacesQuery(tx.Where(
			"map_set_id = ? AND kind = ? AND source = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?",
			mapSetID, EdgeKindGrid, EdgeSourceManual, c.Zone, c.X, c.Y, c.L))
		if err != nil {
			return nil, err
		}
		for k := range part {
			taken[k] = true
		}
	}
	if len(taken) == 0 {
		return nil, nil
	}
	return taken, nil
}

func manualFacesQuery(q *gorm.DB) (map[edgeKey]bool, error) {
	var rows []MapEdge
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	taken := make(map[edgeKey]bool, len(rows))
	for _, e := range rows {
		taken[edgeKey{e.From(), e.Dir}] = true
	}
	return taken, nil
}

// dropFacesTakenByManual filters derived rows whose face a manual row owns.
func dropFacesTakenByManual(edges []MapEdge, taken map[edgeKey]bool) []MapEdge {
	if len(taken) == 0 {
		return edges
	}
	out := edges[:0]
	for _, e := range edges {
		if e.Kind == EdgeKindGrid && taken[edgeKey{e.From(), e.Dir}] {
			continue
		}
		out = append(out, e)
	}
	return out
}

type seamKey struct {
	from  EdgeCell
	zone  string
	tag   int
	cmd   string
	order int
}

func seamKeyOf(e MapEdge) (seamKey, bool) {
	if e.Kind != EdgeKindSeam || e.TargetTag == nil || e.TargetZone == "" {
		return seamKey{}, false
	}
	return seamKey{from: e.From(), zone: e.TargetZone, tag: *e.TargetTag, cmd: e.Command, order: e.SeamOrder}, true
}

func manualSeamKeys(tx *gorm.DB, mapSetID int64) (map[seamKey]bool, error) {
	var rows []MapEdge
	if err := tx.Where("map_set_id = ? AND kind = ? AND source = ?", mapSetID, EdgeKindSeam, EdgeSourceManual).Find(&rows).Error; err != nil {
		return nil, err
	}
	return seamKeys(rows), nil
}

// manualSeamKeysAt is the incremental counterpart to manualSeamKeys. A local
// re-derivation emits seams only from its written source cells, so scanning all
// manual seams on every room write would make edit latency grow with map markup.
func manualSeamKeysAt(tx *gorm.DB, mapSetID int64, cells []EdgeCell) (map[seamKey]bool, error) {
	out := map[seamKey]bool{}
	seen := map[EdgeCell]bool{}
	for _, cell := range cells {
		if seen[cell] {
			continue
		}
		seen[cell] = true
		var rows []MapEdge
		if err := tx.Where("map_set_id = ? AND kind = ? AND source = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?",
			mapSetID, EdgeKindSeam, EdgeSourceManual, cell.Zone, cell.X, cell.Y, cell.L).Find(&rows).Error; err != nil {
			return nil, err
		}
		for k := range seamKeys(rows) {
			out[k] = true
		}
	}
	return out, nil
}

func seamKeys(rows []MapEdge) map[seamKey]bool {
	out := map[seamKey]bool{}
	for _, e := range rows {
		if k, ok := seamKeyOf(e); ok {
			out[k] = true
		}
	}
	return out
}

func dropSeamsTakenByManual(edges []MapEdge, taken map[seamKey]bool) []MapEdge {
	if len(taken) == 0 {
		return edges
	}
	out := edges[:0]
	for _, e := range edges {
		if k, ok := seamKeyOf(e); ok && taken[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// --- the hand-drawn (manual) layer -----------------------------------------
//
// A source='manual' row is the ONE thing in this table that is not derivable:
// nothing about the rooms implies it, so nothing can recreate it. Two consequences
// the rest of this section implements:
//
//   - it must be captured BEFORE any write that can destroy it and restored by
//     undo (ManualEdgeSnapshot), because the room snapshot the journal already
//     takes reproduces every OTHER edge by re-derivation and this one by nothing;
//   - it is an assertion about two EXISTING cells, so deleting either cell takes
//     it with it (deleteManualEdgesIncidentTx). Left behind it would be a passage
//     into nothing that no derivation touches, no UI can show (the face belongs to
//     a cell that is gone) and that a later, unrelated room at the same coord would
//     silently inherit.
//
// SCOPE: manual rows are grid rows between grid-ADJACENT cells. That
// is what makes the capture cheap — see manualEdgesAroundTx.

// ManualEdgeSnapshot is the before-state of the hand-drawn layer around one write:
// the neighbourhood the write can touch, plus every manual row incident to it at
// the time of capture. Restoring it means "make the manual rows incident to Cells
// be exactly Edges" — a literal prior state, like RoomSnapshot, so it is correct
// for a no-op write (marking a face that is already marked restores the same row
// rather than deleting it).
//
// Cells is kept even when Edges is empty: an empty snapshot over a non-empty
// neighbourhood is the assertion "there were no hand-drawn rows here", which is
// what undoes a mark.
type ManualEdgeSnapshot struct {
	Cells []EdgeCell
	Edges []MapEdge
	// Seams are authoritative rows, so undo snapshots their exact prior state too.
	// The historical name remains for wire/source compatibility with the journal.
	Seams []MapEdge
}

// manualEdgesAroundTx returns every manual GRID row incident to cells, plus every
// manual seam owned by a source cell. A seam's target is symbolic (zone, tag), so
// deleting its currently resolved room must leave the assertion dangling and
// available to resolve again when that tag returns or moves.
//
// The arriving half is the reason this is not one WHERE clause: there is no index
// on the to_* side (migration 016 says so deliberately), so a to_zone/to_x/... test
// would scan the set's ~19k rows on every write. Instead we exploit the scope
// invariant above — a manual grid row connects grid-adjacent cells — and look up by
// FROM-cell over the cells AND their six grid neighbours, which the
// idx_map_edges_from index answers as point lookups. Rows that come back but touch
// none of `cells` are filtered out here.
func manualEdgesAroundTx(tx *gorm.DB, mapSetID int64, cells []EdgeCell) ([]MapEdge, error) {
	if len(cells) == 0 {
		return nil, nil
	}
	target := make(map[EdgeCell]bool, len(cells))
	for _, c := range cells {
		target[c] = true
	}
	probe := make([]EdgeCell, 0, len(cells)*7)
	seen := make(map[EdgeCell]bool, len(cells)*7)
	for _, c := range cells {
		for _, p := range append([]EdgeCell{c}, neighbourCells(c)...) {
			if !seen[p] {
				seen[p] = true
				probe = append(probe, p)
			}
		}
	}
	var out []MapEdge
	for _, p := range probe {
		var rows []MapEdge
		if err := tx.Where("map_set_id = ? AND source = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?",
			mapSetID, EdgeSourceManual, p.Zone, p.X, p.Y, p.L).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, e := range rows {
			if target[e.From()] {
				out = append(out, e)
				continue
			}
			if e.Kind == EdgeKindSeam {
				continue
			}
			if to, ok := e.To(); ok && target[to] {
				out = append(out, e)
			}
		}
	}
	// A portal is not grid-adjacent to its endpoint, so the probe above cannot
	// discover an incoming manual portal when its target room is deleted. Include
	// those rows explicitly; otherwise a later room created at the same coord would
	// silently inherit a stale traversal.
	seenIDs := make(map[int64]bool, len(out))
	for _, e := range out {
		seenIDs[e.ID] = true
	}
	for _, c := range cells {
		var incoming []MapEdge
		if err := tx.Where("map_set_id = ? AND kind = ? AND source = ? AND target_tag IS NULL AND to_zone = ? AND to_x = ? AND to_y = ? AND to_l = ?",
			mapSetID, EdgeKindSeam, EdgeSourceManual, c.Zone, c.X, c.Y, c.L).Order("id ASC").Find(&incoming).Error; err != nil {
			return nil, err
		}
		for _, e := range incoming {
			if !seenIDs[e.ID] {
				seenIDs[e.ID] = true
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// neighbourCells returns a cell's six grid neighbours.
func neighbourCells(c EdgeCell) []EdgeCell {
	out := make([]EdgeCell, 0, len(edgeDirOrder))
	for _, dir := range edgeDirOrder {
		out = append(out, c.step(dir))
	}
	return out
}

// captureManualEdgesTx takes the before-snapshot of the hand-drawn layer around
// cells. EVERY topology op takes one, not just the ops that write manual rows: the
// snapshot's contract is "undo returns the markup to exactly its prior state", and
// an op that provably leaves it alone simply produces a snapshot that restores to
// itself.
func captureManualEdgesTx(tx *gorm.DB, mapSetID int64, cells []EdgeCell) (ManualEdgeSnapshot, error) {
	rows, err := manualEdgesAroundTx(tx, mapSetID, cells)
	if err != nil {
		return ManualEdgeSnapshot{}, err
	}
	// Grid rows are restored as face ownership. Seed the seam snapshot with the
	// manual outgoing/incoming portals manualEdgesAroundTx already fetched. Besides
	// avoiding duplicate rows, this avoids repeating its target-endpoint lookup for
	// every affected cell.
	gridRows := make([]MapEdge, 0, len(rows))
	seams := make([]MapEdge, 0, len(rows))
	seenSeams := map[int64]bool{}
	for _, row := range rows {
		if row.Kind == EdgeKindGrid {
			gridRows = append(gridRows, row)
			continue
		}
		seenSeams[row.ID] = true
		seams = append(seams, row)
	}
	for _, c := range cells {
		var at []MapEdge
		if err := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?",
			mapSetID, EdgeKindSeam, c.Zone, c.X, c.Y, c.L).Order("seam_order ASC, id ASC").Find(&at).Error; err != nil {
			return ManualEdgeSnapshot{}, err
		}
		for _, seam := range at {
			if !seenSeams[seam.ID] {
				seenSeams[seam.ID] = true
				seams = append(seams, seam)
			}
		}
	}
	return ManualEdgeSnapshot{Cells: cells, Edges: gridRows, Seams: seams}, nil
}

// deleteManualEdgesIncidentTx removes every manual row incident to cells (see the
// section header for why a delete must). Returns how many rows went.
func deleteManualEdgesIncidentTx(tx *gorm.DB, mapSetID int64, cells []EdgeCell) (int, error) {
	rows, err := manualEdgesAroundTx(tx, mapSetID, cells)
	if err != nil {
		return 0, err
	}
	for _, e := range rows {
		if err := tx.Delete(&MapEdge{}, e.ID).Error; err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

// restoreManualEdgesTx makes the hand-drawn rows incident to snap.Cells be exactly
// snap.Edges: clear what is there now, re-insert what was there before. Ids are
// reassigned (they are not part of an edge's identity — the coord pair is), which
// also makes this correct after a fork re-keyed the whole table.
//
// CLEARING MEANS THE WHOLE FACE, NOT JUST THE MANUAL ROW ON IT. A face holds
// exactly one grid row (the partial UNIQUE index), and between a write and its
// undo the DERIVATION can have taken the face back — that is exactly what happens
// when a mark is cleared ("auto": the manual row goes, the recompute puts the
// parsed row there) or when a room delete drops a mark and leaves the surviving
// neighbour's exit dangling on the same face. Deleting only source='manual' rows
// then leaves the derived row sitting on the face the snapshot is about to
// re-occupy, and the insert fails the UNIQUE index and rolls the ENTIRE undo back —
// a 500 on the REST path, an isError on MCP, and a map edit the user cannot revert.
// So every face a restored row will occupy is emptied first; the derived rows that
// legitimately belong elsewhere are untouched, and the recompute that runs after
// this re-derives the neighbourhood with the restored rows owning their faces
// again (dropFacesTakenByManual).
//
// It must therefore run BEFORE the derived recompute of the same undo, and after
// the rooms have been put back.
//
// Reports whether there was any hand-drawn markup involved at all (rows removed or
// rows put back), which is what lets undo tell "there was nothing here" apart from
// "I restored the markup".
func restoreManualEdgesTx(tx *gorm.DB, mapSetID int64, snap ManualEdgeSnapshot) (changed bool, err error) {
	if len(snap.Cells) == 0 {
		return false, nil
	}
	removed, err := deleteManualEdgesIncidentTx(tx, mapSetID, snap.Cells)
	if err != nil {
		return false, err
	}
	if len(snap.Edges) > 0 {
		rows := make([]MapEdge, len(snap.Edges))
		copy(rows, snap.Edges)
		for i := range rows {
			rows[i].MapSetID = mapSetID
		}
		// Empty every face a restored row claims, whatever provenance holds it now.
		for _, e := range rows {
			if e.Kind != EdgeKindGrid {
				continue
			}
			from := e.From()
			if derr := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND dir = ?",
				mapSetID, EdgeKindGrid, from.Zone, from.X, from.Y, from.L, e.Dir).
				Delete(&MapEdge{}).Error; derr != nil {
				return false, derr
			}
		}
		if err := insertEdges(tx, rows); err != nil {
			return false, err
		}
	}
	// Replace authoritative seams for the captured source cells exactly. This is
	// deliberately independent of importer drafts: undo restores rows, never
	// reimports legacy room data.
	for _, c := range snap.Cells {
		if derr := tx.Where(`map_set_id = ? AND kind = ? AND (
			(from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?)
			OR (source = ? AND target_tag IS NULL AND to_zone = ? AND to_x = ? AND to_y = ? AND to_l = ?))`,
			mapSetID, EdgeKindSeam, c.Zone, c.X, c.Y, c.L,
			EdgeSourceManual, c.Zone, c.X, c.Y, c.L).Delete(&MapEdge{}).Error; derr != nil {
			return false, derr
		}
	}
	if len(snap.Seams) > 0 {
		seams := append([]MapEdge(nil), snap.Seams...)
		for i := range seams {
			seams[i].MapSetID = mapSetID
		}
		if err := insertEdges(tx, seams); err != nil {
			return false, err
		}
	}
	return removed > 0 || len(snap.Edges) > 0 || len(snap.Seams) > 0, nil
}

// rematerializeGridEdgesTx rebuilds only grid-derived rows. Seam rows are already
// authoritative inputs after import/backfill; rewriting them from raw drafts
// would delete duplicate/manual ownership and make ordinary room maintenance a
// hidden legacy import operation.
func rematerializeGridEdgesTx(tx *gorm.DB, mapSetID int64, rooms []Room) error {
	if err := tx.Where("map_set_id = ? AND kind = ? AND source <> ?", mapSetID, EdgeKindGrid, EdgeSourceManual).
		Delete(&MapEdge{}).Error; err != nil {
		return err
	}
	taken, err := manualGridFaces(tx, mapSetID)
	if err != nil {
		return err
	}
	if err := insertEdges(tx, dropFacesTakenByManual(DeriveGridEdges(mapSetID, rooms), taken)); err != nil {
		return err
	}
	return nil
}

// RematerializeMapEdges rebuilds ONE set's derived topology from its current
// rooms, dropping every non-manual row first. It is the repair handle for the one
// thing materialization gave up: before edges were stored, connectivity was re-derived on
// every index build, so any change to `rooms` healed itself on the next reconnect.
// Now the edges are stored, and a change that does not go through the Go
// write-path — a hand-run UPDATE/DELETE on rooms, a restored older DB file, a
// future importer that writes rooms directly — leaves the topology stale
// indefinitely and silently. Nothing detects that drift; the repair command
// this is how it is fixed, and `mudhost --rematerialize-edges` is how it is reached
// without editing code. Returns the number of rows the set ends up with.
func (s *Store) RematerializeMapEdges(mapSetID int64) (int, error) {
	rooms, err := s.ListRooms(mapSetID)
	if err != nil {
		return 0, err
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := rematerializeGridEdgesTx(tx, mapSetID, rooms); err != nil {
			return err
		}
		return reresolveSeamsTx(tx, mapSetID)
	}); err != nil {
		return 0, err
	}
	var count int64
	if err := s.db.Model(&MapEdge{}).Where("map_set_id = ?", mapSetID).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// BackfillEdgeDoors is the DATA half of migration 018: it puts rooms.doors onto the
// grid rows of every set, once, in the session that applies the migration.
//
// WHY IT IS NOT A RE-MATERIALIZATION. Re-deriving the whole set would fill in the
// derived rows and skip the HAND-DRAWN ones by design — and a hand-drawn row owns
// its face, so a face someone had marked would keep door=0 for ever while
// rooms.doors said otherwise, and a route that used to open that door would walk
// into it shut. The rows that need the value are all of them, so this walks all of
// them. (It is also ~40x faster than re-deriving two 8k-room sets.)
//
// WHY IT IS NOT SQL IN THE MIGRATION. The rule "this face has a door iff the room's
// doors list names it" is the derivation's rule (deriveGridEdge / edgeRoom.hasDoor);
// restating it in SQL would create the second implementation that materialization
// exists to remove. This runs the Go rule, per row.
//
// Idempotent: it only writes rows whose stored value disagrees, so a second run
// writes nothing. Returns how many rows it changed, per set id.
func (s *Store) BackfillEdgeDoors() (map[int64]int, error) {
	var setIDs []int64
	if err := s.db.Model(&MapSet{}).Order("id ASC").Pluck("id", &setIDs).Error; err != nil {
		return nil, err
	}
	changed := map[int64]int{}
	for _, setID := range setIDs {
		rooms, err := s.ListRooms(setID)
		if err != nil {
			return nil, err
		}
		doors := make(map[edgeKey]bool, len(rooms))
		for i := range rooms {
			er := newGridEdgeRoom(&rooms[i])
			for _, dir := range edgeDirOrder {
				if er.hasDoor(dir) {
					doors[edgeKey{er.cell, dir}] = true
				}
			}
		}
		var edges []MapEdge
		if err := s.db.Where("map_set_id = ? AND kind = ?", setID, EdgeKindGrid).
			Find(&edges).Error; err != nil {
			return nil, err
		}
		n := 0
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			for _, e := range edges {
				want := doors[edgeKey{e.From(), e.Dir}]
				if want == e.Door {
					continue
				}
				if err := tx.Model(&MapEdge{}).Where("id = ?", e.ID).
					Update("door", want).Error; err != nil {
					return err
				}
				n++
			}
			return nil
		}); err != nil {
			return nil, err
		}
		changed[setID] = n
	}
	return changed, nil
}

// RematerializeAllMapEdges rebuilds the derived topology of every map set and
// reports the resulting row count per set id, ordered by id.
func (s *Store) RematerializeAllMapEdges() ([]int64, map[int64]int, error) {
	var setIDs []int64
	if err := s.db.Model(&MapSet{}).Order("id ASC").Pluck("id", &setIDs).Error; err != nil {
		return nil, nil, err
	}
	counts := make(map[int64]int, len(setIDs))
	for _, id := range setIDs {
		n, err := s.RematerializeMapEdges(id)
		if err != nil {
			return nil, nil, err
		}
		counts[id] = n
	}
	return setIDs, counts, nil
}

// prepareAutomapsDrop is migration 026's data half. `automaps` was retained
// after 023 only so databases that upgraded across a crash could finish the Go
// derivation. The marker distinguishes that unfinished path from a database
// whose map_edges have subsequently been edited: the former is materialized,
// while the latter is audited without allowing stale provenance to overwrite
// authority. It deliberately accepts a manual row that owns an exact legacy
// occurrence; manual ownership is the authoritative replacement for that row.
func prepareAutomapsDrop(tx *gorm.DB) error {
	var marked int64
	if err := tx.Raw(`SELECT count(*) FROM app_settings WHERE "key" = ? AND value <> ''`, "mapper.edge_authority_backfilled_v1").Scan(&marked).Error; err != nil {
		return fmt.Errorf("reading edge authority completion marker: %w", err)
	}

	var setIDs []int64
	if err := tx.Model(&MapSet{}).Order("id ASC").Pluck("id", &setIDs).Error; err != nil {
		return err
	}
	for _, setID := range setIDs {
		legacy, err := loadLegacyRoomsTx(tx, setID)
		if err != nil {
			return err
		}
		rooms, drafts, err := splitLegacyRooms(legacy)
		if err != nil {
			return fmt.Errorf("decoding legacy automaps for map set %d: %w", setID, err)
		}
		if marked == 0 && len(rooms) != 0 {
			if err := backfillLegacyEdgesTx(tx, setID, rooms, drafts); err != nil {
				return fmt.Errorf("backfilling legacy map set %d: %w", setID, err)
			}
		}
		if err := auditLegacyAutomapsTx(tx, setID, rooms, drafts); err != nil {
			return err
		}
	}
	if err := tx.Exec(`DELETE FROM app_settings WHERE "key" = ?`, "mapper.edge_authority_backfilled_v1").Error; err != nil {
		return err
	}
	return nil
}

type legacyRoomRecord struct {
	Room
	Automaps string `gorm:"column:automaps"`
}

func loadLegacyRoomsTx(tx *gorm.DB, mapSetID int64) ([]legacyRoomRecord, error) {
	var legacy []legacyRoomRecord
	err := tx.Raw(`SELECT rooms.*, automaps FROM rooms WHERE map_set_id = ? ORDER BY id ASC`, mapSetID).Scan(&legacy).Error
	return legacy, err
}

func splitLegacyRooms(legacy []legacyRoomRecord) ([]Room, []LegacySeamDraft, error) {
	rooms := make([]Room, 0, len(legacy))
	drafts := make([]LegacySeamDraft, 0)
	for _, row := range legacy {
		rooms = append(rooms, row.Room)
		var entries []string
		raw := strings.TrimSpace(row.Automaps)
		if !strings.HasPrefix(raw, "[") || json.Unmarshal([]byte(raw), &entries) != nil {
			return nil, nil, fmt.Errorf("room %d contains invalid JSON string array", row.ID)
		}
		if len(entries) != 0 {
			drafts = append(drafts, LegacySeamDraft{From: EdgeCell{Zone: row.Zone, X: row.X, Y: row.Y, L: row.L}, Entries: entries})
		}
	}
	return rooms, drafts, nil
}

// auditLegacyAutomapsTx proves that every raw occurrence, including duplicate,
// dead and partial entries, has exactly one same-order authoritative seam. It
// also rejects an extra parsed seam, which would otherwise make an occurrence
// count look right while preserving a stale route.
func auditLegacyAutomapsTx(tx *gorm.DB, mapSetID int64, rooms []Room, drafts []LegacySeamDraft) error {
	expectedAll := DeriveImportedEdges(mapSetID, rooms, drafts)
	expected := make([]MapEdge, 0)
	for _, edge := range expectedAll {
		if edge.Kind == EdgeKindSeam {
			expected = append(expected, edge)
		}
	}
	var actual []MapEdge
	if err := tx.Where("map_set_id = ? AND kind = ?", mapSetID, EdgeKindSeam).Order("id ASC").Find(&actual).Error; err != nil {
		return err
	}
	matched := make(map[int64]bool, len(actual))
	for _, want := range expected {
		found := 0
		for i := range actual {
			got := actual[i]
			if sameLegacySeam(want, got) {
				found++
				matched[got.ID] = true
			}
		}
		if found != 1 {
			return fmt.Errorf("legacy automaps audit map set %d: occurrence %s(%d,%d,%d) order %d matched %d authoritative seams", mapSetID, want.FromZone, want.FromX, want.FromY, want.FromL, want.SeamOrder, found)
		}
	}
	for _, got := range actual {
		if got.Source != EdgeSourceParsed && got.Source != EdgeSourceManual {
			return fmt.Errorf("legacy automaps audit map set %d: seam %s(%d,%d,%d) order %d has invalid source %q", mapSetID, got.FromZone, got.FromX, got.FromY, got.FromL, got.SeamOrder, got.Source)
		}
		if got.Source == EdgeSourceParsed && !matched[got.ID] {
			return fmt.Errorf("legacy automaps audit map set %d: extra parsed seam %s(%d,%d,%d) order %d", mapSetID, got.FromZone, got.FromX, got.FromY, got.FromL, got.SeamOrder)
		}
	}
	return nil
}

func sameLegacySeam(want, got MapEdge) bool {
	if got.Kind != EdgeKindSeam || (got.Source != EdgeSourceParsed && got.Source != EdgeSourceManual) ||
		want.From() != got.From() || want.SeamOrder != got.SeamOrder ||
		want.TargetZone != got.TargetZone || want.Command != got.Command || !sameTargetTag(want.TargetTag, got.TargetTag) {
		return false
	}
	wantTo, wantOK := want.To()
	gotTo, gotOK := got.To()
	return wantOK == gotOK && (!wantOK || wantTo == gotTo)
}

func backfillLegacyEdgesTx(tx *gorm.DB, mapSetID int64, rooms []Room, drafts []LegacySeamDraft) error {
	expected := DeriveImportedEdges(mapSetID, rooms, drafts)
	var existing []MapEdge
	if err := tx.Where("map_set_id = ?", mapSetID).Find(&existing).Error; err != nil {
		return err
	}
	have := make(map[legacyEdgeKey]struct{}, len(existing))
	for _, e := range existing {
		if e.Kind == EdgeKindGrid {
			have[legacyKey(e)] = struct{}{}
		}
	}
	missing := make([]MapEdge, 0)
	for _, e := range expected {
		if e.Kind != EdgeKindGrid {
			continue
		}
		k := legacyKey(e)
		if _, ok := have[k]; !ok {
			missing = append(missing, e)
		}
	}
	manualFaces, err := manualGridFaces(tx, mapSetID)
	if err != nil {
		return err
	}
	if err := insertEdges(tx, dropFacesTakenByManual(missing, manualFaces)); err != nil {
		return err
	}

	// Remove parsed rows before moving manual occurrences: the occurrence unique
	// index must not see a transient collision with the row the manual one replaces.
	if err := tx.Where("map_set_id = ? AND kind = ? AND source <> ?", mapSetID, EdgeKindSeam, EdgeSourceManual).
		Delete(&MapEdge{}).Error; err != nil {
		return err
	}

	// Pre-023 manual rows have a temporary migration order. Align a row to a matching raw
	// occurrence when its current order does not already identify one. Old clients
	// refused ambiguous duplicates, so the deterministic first-match fallback is
	// only for hand-edited/partially upgraded databases.
	expectedSeams := make([]MapEdge, 0)
	for _, e := range expected {
		if e.Kind == EdgeKindSeam {
			expectedSeams = append(expectedSeams, e)
		}
	}
	var manualSeams []MapEdge
	if err := tx.Where("map_set_id = ? AND kind = ? AND source = ?", mapSetID, EdgeKindSeam, EdgeSourceManual).
		Order("id ASC").Find(&manualSeams).Error; err != nil {
		return err
	}
	for i := range manualSeams {
		m := &manualSeams[i]
		matches := make([]int, 0, 1)
		for _, e := range expectedSeams {
			if e.From() == m.From() && e.TargetZone == m.TargetZone && e.Command == m.Command && sameTargetTag(e.TargetTag, m.TargetTag) {
				matches = append(matches, e.SeamOrder)
			}
		}
		if len(matches) == 0 || containsInt(matches, m.SeamOrder) {
			continue
		}
		m.SeamOrder = matches[0]
		if err := tx.Model(&MapEdge{}).Where("id = ?", m.ID).Update("seam_order", m.SeamOrder).Error; err != nil {
			return err
		}
	}
	owned := seamKeys(manualSeams)
	if err := insertEdges(tx, dropSeamsTakenByManual(expectedSeams, owned)); err != nil {
		return err
	}
	return reresolveSeamsTx(tx, mapSetID)
}

func sameTargetTag(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type legacyEdgeKey struct {
	kind, zone, dir, targetZone, command string
	x, y, l, order                       int
	tag                                  int
	hasTag                               bool
}

func legacyKey(e MapEdge) legacyEdgeKey {
	k := legacyEdgeKey{kind: e.Kind, zone: e.FromZone, dir: e.Dir, targetZone: e.TargetZone, command: e.Command,
		x: e.FromX, y: e.FromY, l: e.FromL, order: e.SeamOrder}
	if e.TargetTag != nil {
		k.tag, k.hasTag = *e.TargetTag, true
	}
	return k
}

// --- incremental recompute (the write-path) --------------------------------
//
// AGENTS #2 / risk "fallback is a property of the NEIGHBOURHOOD, not of a room":
// several hundred edges in the live corpus exist only because BOTH endpoints carry
// no exit data. Writing one of those rooms makes it explicit and must therefore
// DELETE the fallback edges around it — a materializer that only re-derived the
// written room's own exits would leave ghost edges the inference never produced.
// So every write re-derives whole PAIRS, from both sides.

// edgeKey identifies a stored grid row: its from-cell plus direction.
type edgeKey struct {
	cell EdgeCell
	dir  string
}

// recomputeEdgesTx re-derives every non-manual edge whose existence can depend on
// the given cells. The affected key set is, for each cell c and each direction d:
// (c,d) — c's own edges, including dangling ones — and (c+d, opposite(d)) — the
// row a neighbour owns because IT is the canonical end of the pair, and the
// dangling row a neighbour has while c does not exist. Nothing outside that set
// can change: RoomsConnect reads only the two endpoints.
//
// tagsChanged additionally re-resolves the set's seam targets: a seam points at
// "the first room of zone Z with tag T", so creating or deleting a TAGGED room can
// move it. Ordinary writes never touch tags (RoomFields carries none), so this is
// off unless a delete/undo made a tagged room appear or disappear.
func recomputeEdgesTx(tx *gorm.DB, mapSetID int64, cells []EdgeCell, tagsChanged bool) error {
	if len(cells) == 0 {
		return nil
	}
	keys := make([]edgeKey, 0, len(cells)*12)
	seen := map[edgeKey]bool{}
	need := map[EdgeCell]bool{}
	for _, c := range cells {
		need[c] = true
		for _, dir := range edgeDirOrder {
			nb := c.step(dir)
			need[nb] = true
			for _, k := range []edgeKey{{c, dir}, {nb, oppositeDir[dir]}} {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
	}

	// Load every room the derivation can read (the cells and their neighbours).
	rooms := map[EdgeCell]*edgeRoom{}
	for cell := range need {
		var r Room
		err := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L).First(&r).Error
		if err == gorm.ErrRecordNotFound {
			continue
		}
		if err != nil {
			return err
		}
		er := newGridEdgeRoom(&r)
		rooms[cell] = &er
	}

	// Drop only derived grid rows under the affected keys. Seam rows are
	// authoritative and must not be recreated from a room write.
	for _, k := range keys {
		if err := tx.Where("map_set_id = ? AND kind = ? AND source <> ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND dir = ?",
			mapSetID, EdgeKindGrid, EdgeSourceManual, k.cell.Zone, k.cell.X, k.cell.Y, k.cell.L, k.dir).
			Delete(&MapEdge{}).Error; err != nil {
			return err
		}
	}

	// Re-derive, leaving faces a manual row owns alone (see manualGridFaces: the
	// derived row would collide with it on the partial UNIQUE index and roll the
	// whole write back). Only the neighbourhood's own faces can matter here — a
	// re-derivation emits nothing outside `keys` — so this asks about those cells
	// and not about the whole set (manualGridFacesAt).
	probe := make([]EdgeCell, 0, len(need))
	for cell := range need {
		probe = append(probe, cell)
	}
	taken, terr := manualGridFacesAt(tx, mapSetID, probe)
	if terr != nil {
		return terr
	}
	var fresh []MapEdge
	for _, k := range keys {
		r := rooms[k.cell]
		if r == nil || taken[k] {
			continue
		}
		if e, ok := deriveGridEdge(mapSetID, r, k.dir, rooms[k.cell.step(k.dir)]); ok {
			fresh = append(fresh, e)
		}
	}
	if err := insertEdges(tx, fresh); err != nil {
		return err
	}
	if !tagsChanged {
		return nil
	}
	return reresolveSeamsTx(tx, mapSetID)
}

// reresolveSeamsTx re-points every seam row of the set at its symbolic target.
// Only needed when the set of TAGGED rooms changed; the symbolic target itself is
// never rewritten (it is what survives a re-import with different geometry).
func reresolveSeamsTx(tx *gorm.DB, mapSetID int64) error {
	var rooms []Room
	if err := tx.Select("zone", "x", "y", "l", "tag").
		Where("map_set_id = ? AND tag IS NOT NULL", mapSetID).Find(&rooms).Error; err != nil {
		return err
	}
	targets := indexSeamTargets(rooms)

	var seams []MapEdge
	if err := tx.Where("map_set_id = ? AND kind = ?", mapSetID, EdgeKindSeam).
		Find(&seams).Error; err != nil {
		return err
	}
	for i := range seams {
		e := &seams[i]
		if e.TargetTag == nil {
			continue
		}
		var cell *EdgeCell
		if target, ok := targets[seamTargetKey{zone: norm.NFC.String(e.TargetZone), tag: *e.TargetTag}]; ok {
			cell = &target
		}
		before, hadBefore := e.To()
		e.SetTo(cell)
		after, hasAfter := e.To()
		if hadBefore == hasAfter && before == after {
			continue
		}
		if err := tx.Model(&MapEdge{}).Where("id = ?", e.ID).
			Updates(map[string]any{"to_zone": e.ToZone, "to_x": e.ToX, "to_y": e.ToY, "to_l": e.ToL}).
			Error; err != nil {
			return err
		}
	}
	return nil
}
