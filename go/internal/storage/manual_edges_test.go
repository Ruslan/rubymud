package storage

import (
	"sort"
	"testing"
)

// Hidden-exit behavior. Two halves, in the order they were built:
//
//  1. THE DEBT (the tests immediately below). The undo journal used to snapshot
//     rooms only, which is exactly right while every edge is DERIVABLE: restoring
//     the rooms and re-deriving the neighbourhood reproduces the edge set. A
//     source='manual' row is not derivable from anything, so an op that destroys
//     one destroys it for good. TestDeleteRoomDropsManualEdgesOnTheDeletedCell and
//     TestUndoOfDeleteRestoresManualEdges are the repro pair, and they were written
//     against the pre-change code, where they fail — see their comments for WHICH
//     failure each one names.
//  2. THE FEATURE: TopoAddManualEdge / TopoRemoveManualEdge, the write that
//     produces those rows in the first place.

// hiddenFaceInput is three rooms where ONE face is free for a hand-drawn edge:
// a(0,0,0) and b(1,0,0) advertise the passage between them (a derived pair), and
// c(0,1,0) sits east of a with NEITHER side advertising that face — both rooms are
// explicit, so the permissive fallback does not fire either. (a,E)/(c,W) is
// therefore a face the derivation produces nothing on: exactly the shape of the
// live secret room (16077 "В окружении ящиков", whose exits line is empty and
// whose neighbours all deny the back-link).
func hiddenFaceInput() MapSetInput {
	return MapSetInput{
		Name: "Hidden World", RoomCount: 3,
		Rooms: []Room{
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Hint: "a", EDirs: `["S"]`, Ch: 2, Doors: "[]"},
			{Zone: "Alpha", X: 1, Y: 0, L: 0, Hint: "b", EDirs: `["N"]`, Ch: 1, Doors: "[]"},
			// c denies every face toward a: it only claims N, which leads nowhere.
			{Zone: "Alpha", X: 0, Y: 1, L: 0, Hint: "c", EDirs: `["N"]`, Ch: 1, Doors: "[]"},
		},
	}
}

// manualRows returns the set's hand-drawn rows as sorted summary strings.
func manualRows(t *testing.T, store *Store, mapSetID int64) []string {
	t.Helper()
	edges, err := store.ListMapEdges(mapSetID)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	out := []string{}
	for _, e := range edges {
		if e.Source != EdgeSourceManual {
			continue
		}
		to := "-"
		if c, ok := e.To(); ok {
			to = c.Zone + coordSuffix(c)
		}
		out = append(out, e.FromZone+coordSuffix(e.From())+" "+e.Dir+" -> "+to)
	}
	sort.Strings(out)
	return out
}

// drawHiddenPair hand-draws BOTH directions of the a<->c passage directly (the
// pre-change repro tests must not depend on the write this helper adds).
func drawHiddenPair(t *testing.T, store *Store, id int64) {
	t.Helper()
	a := EdgeCell{"Alpha", 0, 0, 0}
	c := EdgeCell{"Alpha", 0, 1, 0}
	manualEdge(t, store, id, a, "E", &c)
	manualEdge(t, store, id, c, "W", &a)
}

// TestDeleteRoomDropsManualEdgesOnTheDeletedCell — REPRO 1 (fails before the
// fix). Deleting a room used to leave every hand-drawn row that touches its cell
// in the table, pointing at a coord with no room. That is not a harmless leftover:
//
//   - it contradicts the invariant the fix writes marks under (a manual edge is
//     an assertion about two EXISTING cells — an edge into nothing is not walkable
//     and cannot be re-marked, because the face is already taken);
//   - it RESURRECTS: create an unrelated room at that coord later and it silently
//     inherits a secret passage somebody drew for a room that no longer exists;
//   - nothing can ever clean it up — no derivation touches manual rows, and the UI
//     cannot show a face of a cell that does not exist.
//
// So a delete must take them with it, which is what makes REPRO 2 (below) matter.
func TestDeleteRoomDropsManualEdgesOnTheDeletedCell(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	drawHiddenPair(t, store, id)
	if len(manualRows(t, store, id)) != 2 {
		t.Fatalf("setup: manual rows = %v, want both directions", manualRows(t, store, id))
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 1, L: 0,
	}); err != nil || !found {
		t.Fatalf("delete c: err=%v found=%v", err, found)
	}

	// BOTH rows touch the deleted cell: c's own outgoing row, and a's row INTO c
	// (owned by the surviving room, so it is not enough to delete by from-cell).
	if got := manualRows(t, store, id); len(got) != 0 {
		t.Fatalf("hand-drawn rows survived the delete of their endpoint: %v", got)
	}
}

// TestUndoOfDeleteRestoresManualEdges — REPRO 2 (fails before the fix). Once a
// delete drops them, the journal is the only thing that can bring them back: a
// manual row is not derivable, so RestoreTopologyBefore's re-derivation reproduces
// every other edge of the neighbourhood and NOTHING of the hand-drawn layer. Undo
// must return the markup to exactly its prior state.
func TestUndoOfDeleteRestoresManualEdges(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	drawHiddenPair(t, store, id)
	wantManual := manualRows(t, store, id)
	wantAll := edgeSummary(t, store, id)

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 1, L: 0,
	})
	if err != nil || !found {
		t.Fatalf("delete c: err=%v found=%v", err, found)
	}

	applied, err := store.RestoreTopologyBefore(id, before)
	if err != nil || !applied {
		t.Fatalf("RestoreTopologyBefore: err=%v applied=%v", err, applied)
	}
	if got := manualRows(t, store, id); !same(wantManual, got) {
		t.Fatalf("undo lost the hand-drawn markup:\n want %v\n got  %v", wantManual, got)
	}
	if got := edgeSummary(t, store, id); !same(wantAll, got) {
		t.Fatalf("undo did not restore the whole topology:\n want %v\n got  %v", wantAll, got)
	}
}

// --- the write: hidden exits and their negation ------------------------------

// TestMarkHiddenExitCreatesTheEdgeAndUndoRemovesIt: the mark writes the directed
// PAIR by default (a found passage is normally walkable both ways) and undo takes
// the markup back to "nothing was drawn here".
func TestMarkHiddenExitCreatesTheEdgeAndUndoRemovesIt(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if got := manualRows(t, store, id); len(got) != 0 {
		t.Fatalf("setup already has hand-drawn rows: %v", got)
	}
	wantAll := edgeSummary(t, store, id)

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
	})
	if err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	if got := manualRows(t, store, id); !same(got, []string{
		"Alpha(0,0,0) E -> Alpha(0,1,0)",
		"Alpha(0,1,0) W -> Alpha(0,0,0)",
	}) {
		t.Fatalf("mark wrote %v, want both directed rows of the passage", got)
	}

	applied, err := store.RestoreTopologyBefore(id, before)
	if err != nil || !applied {
		t.Fatalf("RestoreTopologyBefore: err=%v applied=%v", err, applied)
	}
	if got := edgeSummary(t, store, id); !same(wantAll, got) {
		t.Fatalf("undo of a mark did not restore the topology:\n want %v\n got  %v", wantAll, got)
	}
}

// TestMarkHiddenExitOneWay: OneWay writes only the row leaving the target cell —
// the one thing the derivation can never produce (RoomsConnect is symmetric).
func TestMarkHiddenExitOneWay(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("mark one-way: err=%v found=%v", err, found)
	}
	if got := manualRows(t, store, id); !same(got, []string{"Alpha(0,0,0) E -> Alpha(0,1,0)"}) {
		t.Fatalf("one-way mark wrote %v, want only the outgoing row", got)
	}
}

// TestMarkHiddenExitNeedsBothCells: a manual edge is an assertion about two
// EXISTING cells. Into empty space it soft-fails rather than writing a row that
// nothing could walk, show or clean up.
func TestMarkHiddenExitNeedsBothCells(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// W of a(0,0,0) is (0,-1,0) — no room there.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "W",
	}); err != nil || found {
		t.Fatalf("mark into empty space: err=%v found=%v, want found=false", err, found)
	}
	if got := manualRows(t, store, id); len(got) != 0 {
		t.Fatalf("a refused mark still wrote rows: %v", got)
	}
	// A bad direction is refused the same way.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "X",
	}); err != nil || found {
		t.Fatalf("mark with a bad dir: err=%v found=%v, want found=false", err, found)
	}
}

// TestHiddenExitSurvivesRoomUpdate is the point of the whole layer: «обновить
// комнату» re-parses the exits line and rewrites the DERIVED rows, and the
// hand-drawn row must be there afterwards — that is what a mark in rooms.edirs
// (mud_room_link) would NOT be.
func TestHiddenExitSurvivesRoomUpdate(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	want := manualRows(t, store, id)

	// Re-record the room from a live block whose exits line (as always) says nothing
	// about the secret way east.
	exits := "S"
	hint := "a, seen again"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0,
		Fields: RoomFields{Hint: &hint, Exits: &exits},
	}); err != nil || !found {
		t.Fatalf("re-record the room: err=%v found=%v", err, found)
	}
	if got := manualRows(t, store, id); !same(want, got) {
		t.Fatalf("«обновить комнату» ate the hand-drawn markup:\n want %v\n got  %v", want, got)
	}
	// And no derived twin appeared on the taken face.
	n := 0
	for _, e := range edgeSummary(t, store, id) {
		if e == "grid:parsed Alpha(0,0,0) E -> Alpha(0,1,0)" {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("the derivation produced a twin on the hand-drawn face")
	}
}

// TestBlockDeniesOneFaceOnly: the negation is a manual row with passable=0. It
// must (a) replace the derived row, (b) survive a room write, and (c) deny ONE
// face — ownership is per (cell, dir), so the way back stays open unless it is
// denied too. That last property is what makes a one-way wall expressible.
func TestBlockDeniesOneFaceOnly(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// a(0,0,0) <-> b(1,0,0) is a derived pair.
	if !contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatalf("setup: expected the derived a->b row: %v", edgeSummary(t, store, id))
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("block: err=%v found=%v", err, found)
	}
	got := edgeSummary(t, store, id)
	if contains(got, "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatalf("the derived row survived the negation: %v", got)
	}
	if !contains(got, "grid:manual Alpha(0,0,0) S -> Alpha(1,0,0) [blocked]") {
		t.Fatalf("no negation row on the denied face: %v", got)
	}
	// The way back is untouched: face ownership is per (cell, dir), not per pair.
	if !contains(got, "grid:parsed Alpha(1,0,0) N -> Alpha(0,0,0)") {
		t.Fatalf("the negation of a->b also killed b->a: %v", got)
	}

	// A room write must not resurrect the denied edge.
	exits := "S"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0, Fields: RoomFields{Exits: &exits},
	}); err != nil || !found {
		t.Fatalf("re-record: err=%v found=%v", err, found)
	}
	if contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatalf("«обновить комнату» resurrected a denied edge: %v", edgeSummary(t, store, id))
	}
}

// TestClearingANegationGivesTheFaceBack: clearing the hand-drawn row hands the
// face back to the derivation, so a denied-but-real passage comes back. Without
// this a negation would be a one-way door out of the map.
func TestClearingANegationGivesTheFaceBack(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	want := edgeSummary(t, store, id)

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("block: err=%v found=%v", err, found)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoRemoveManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("clear: err=%v found=%v", err, found)
	}
	if got := edgeSummary(t, store, id); !same(want, got) {
		t.Fatalf("clearing the negation did not restore the derived topology:\n want %v\n got  %v", want, got)
	}
}

// TestUndoOfABlockRestoresTheDerivedEdge: undo of a negation is the journal's
// job too — the derived row it replaced comes back from the re-derivation, but
// only once the manual row that owns the face is gone.
func TestUndoOfABlockRestoresTheDerivedEdge(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	want := edgeSummary(t, store, id)

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	})
	if err != nil || !found {
		t.Fatalf("block: err=%v found=%v", err, found)
	}
	applied, err := store.RestoreTopologyBefore(id, before)
	if err != nil || !applied {
		t.Fatalf("RestoreTopologyBefore: err=%v applied=%v", err, applied)
	}
	if got := edgeSummary(t, store, id); !same(want, got) {
		t.Fatalf("undo of a negation did not restore the topology:\n want %v\n got  %v", want, got)
	}
}

// TestRematerializeKeepsBothKindsOfMark: `mudhost --rematerialize-edges` rebuilds
// the derived rows from the rooms; it must leave hidden exits AND negations alone
// (they are exactly the rows it cannot re-derive).
func TestRematerializeKeepsBothKindsOfMark(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil {
		t.Fatalf("block: %v", err)
	}
	want := edgeSummary(t, store, id)

	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatalf("RematerializeMapEdges: %v", err)
	}
	if got := edgeSummary(t, store, id); !same(want, got) {
		t.Fatalf("rematerialization changed the hand-drawn layer:\n want %v\n got  %v", want, got)
	}
}

// TestForkCarriesBothKindsOfMark: fork-on-write copies the hand-drawn layer, which
// is what makes the first write to a FROZEN imported set safe for markup made
// after the fork — and would silently lose it if the fork re-derived instead.
func TestForkCarriesBothKindsOfMark(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil {
		t.Fatalf("block: %v", err)
	}
	want := edgeSummary(t, store, id)

	forkID, err := store.ForkMapSet(id)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	if got := edgeSummary(t, store, forkID); !same(want, got) {
		t.Fatalf("fork lost the hand-drawn layer:\n want %v\n got  %v", want, got)
	}
}

// TestSlimRoomsCarryTheHandDrawnLayer: the pane can only show — and therefore only
// let the player TAKE BACK — markup it is told about. Both kinds must reach the
// wire form the map pane renders from.
func TestSlimRoomsCarryTheHandDrawnLayer(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
	}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil {
		t.Fatalf("block: %v", err)
	}

	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	byCoord := map[[3]int]SlimRoom{}
	for _, r := range rooms {
		byCoord[[3]int{r.X, r.Y, r.L}] = r
	}
	a := byCoord[[3]int{0, 0, 0}]
	if !same(a.M, []string{"E"}) {
		t.Errorf("a.m (hidden exits) = %v, want [E]", a.M)
	}
	if !same(a.MB, []string{"S"}) {
		t.Errorf("a.mb (negations) = %v, want [S]", a.MB)
	}
	// The other end of the marked passage carries its own directed row.
	c := byCoord[[3]int{0, 1, 0}]
	if !same(c.M, []string{"W"}) {
		t.Errorf("c.m = %v, want [W]", c.M)
	}
	// b keeps the derived way back (the negation was one-way).
	b := byCoord[[3]int{1, 0, 0}]
	if len(b.M) != 0 || len(b.MB) != 0 {
		t.Errorf("b picked up hand-drawn markup it should not have: m=%v mb=%v", b.M, b.MB)
	}
	if !same(b.G, []string{"N"}) {
		t.Errorf("b.g (derived edges) = %v, want [N]", b.G)
	}
}

// --- review round 2: undo must not collide with a DERIVED row ----------------
//
// The hand-drawn snapshot restores rows onto FACES, and a face holds exactly one
// grid row (the partial UNIQUE index). Between the write and its undo the
// derivation can have taken the face back — that is precisely what "auto"
// (clear the mark) does, and what a room delete does around a dropped mark. So
// restoring the markup has to clear whatever sits on the face now, derived rows
// included; clearing only the MANUAL rows leaves the derived one in place and the
// insert rolls the whole undo back with UNIQUE constraint failed.

// undoLast applies the top-of-stack entry for the set the way the session does.
func undoLast(t *testing.T, store *Store, id int64) {
	t.Helper()
	entry, ok := store.WriteJournal().Pop(id)
	if !ok {
		t.Fatal("journal empty")
	}
	if _, err := store.RestoreTopologyBefore(id, entry.Before); err != nil {
		t.Fatalf("undo %q: %v", entry.Label, err)
	}
}

// applyJournaled runs an op and journals it, like Session.WriteTopology does.
func applyJournaled(t *testing.T, store *Store, id int64, op TopologyOp) {
	t.Helper()
	before, found, err := store.ApplyTopologyOp(id, op)
	if err != nil || !found {
		t.Fatalf("%s %s: err=%v found=%v", op.Kind, op.Dir, err, found)
	}
	store.WriteJournal().Push(id, UndoEntry{Label: op.UndoLabel(), Before: before})
}

// TestUndoOfClearAfterBlockRestoresTheNegation — the three-click UI repro: deny a
// face the text describes, take the denial back (the derived row returns), then
// «Отменить». The negation has to land back ON the face the derived row now holds.
func TestUndoOfClearAfterBlockRestoresTheNegation(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	applyJournaled(t, store, id, TopologyOp{Kind: TopoBlockManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true})
	afterBlock := edgeSummary(t, store, id)

	applyJournaled(t, store, id, TopologyOp{Kind: TopoRemoveManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true})
	if !contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatal("clearing the negation should have handed the face back to the derivation")
	}

	undoLast(t, store, id) // undo the clear
	if got := edgeSummary(t, store, id); !same(afterBlock, got) {
		t.Fatalf("undo of the clear did not restore the negation:\n want %v\n got  %v", afterBlock, got)
	}
}

// TestUndoOfClearAfterMarkOnADerivableFace: same collision through the other mark.
// The hidden exit sits on a face the room text ALSO describes (marking is still
// meaningful — it pins the passage against a later re-record), so clearing it
// brings the derived row back and the undo must displace it again.
func TestUndoOfClearAfterMarkOnADerivableFace(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	applyJournaled(t, store, id, TopologyOp{Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S"})
	afterMark := edgeSummary(t, store, id)

	applyJournaled(t, store, id, TopologyOp{Kind: TopoRemoveManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S"})
	undoLast(t, store, id)
	if got := edgeSummary(t, store, id); !same(afterMark, got) {
		t.Fatalf("undo of the clear did not restore the mark:\n want %v\n got  %v", afterMark, got)
	}
}

// TestUndoOfDeleteRestoresManualOntoADanglingFace: deleting the far end drops the
// hand-drawn pair, and the surviving room's own exit then materializes as a
// DANGLING derived row on the very face the mark held. Undo must take that face
// back.
func TestUndoOfDeleteRestoresManualOntoADanglingFace(t *testing.T) {
	store := newMapperTestStore(t)
	in := hiddenFaceInput()
	// a advertises E as well, so once c is gone the face carries a dangling row.
	in.Rooms[0].EDirs = `["S","E"]`
	in.Rooms[0].Ch = 2 | 4
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	applyJournaled(t, store, id, TopologyOp{Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E"})
	afterMark := edgeSummary(t, store, id)

	applyJournaled(t, store, id, TopologyOp{Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 1, L: 0})
	if !contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) E -> -") {
		t.Fatal("deleting the far end should have left a dangling derived row on the face")
	}

	undoLast(t, store, id)
	if got := edgeSummary(t, store, id); !same(afterMark, got) {
		t.Fatalf("undo of the delete did not restore the markup:\n want %v\n got  %v", afterMark, got)
	}
}

// TestUndoOfClearRestoresBOTHFacesOfAPair: the default mark writes two directed
// rows, so the clear frees two faces and the derivation can take both. Undo has to
// displace both — a fix that only cleared the first face would still roll back.
func TestUndoOfClearRestoresBOTHFacesOfAPair(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Mark the a<->b pair (both faces derivable) as hand-drawn, then clear it.
	applyJournaled(t, store, id, TopologyOp{Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S"})
	afterMark := manualRows(t, store, id)
	if len(afterMark) != 2 {
		t.Fatalf("setup: want both directed rows, got %v", afterMark)
	}
	applyJournaled(t, store, id, TopologyOp{Kind: TopoRemoveManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S"})
	undoLast(t, store, id)
	if got := manualRows(t, store, id); !same(afterMark, got) {
		t.Fatalf("undo restored only part of the pair:\n want %v\n got  %v", afterMark, got)
	}
}
