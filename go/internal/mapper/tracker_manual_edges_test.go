package mapper

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

// The tracker's authority has to be the same as the router's. Before this fix
// predict() and neighborMatch() re-derived connectivity from IndexRoom.EDirs and
// the coordinate deltas, so the hand-drawn layer was invisible to them: a face the
// player DENIED was still walked into and green-anchored, and a face they marked
// as a hidden exit could not be recognised at all when the queue was empty
// (following / teleport). Both are the states the marks exist to describe.

// manualIndex builds a 3-room zone plus a hand-drawn layer, the way storage would
// have materialized it. a(0,0)-b(1,0) advertise the passage between them (derived);
// a-c(0,1) is a face neither room mentions.
func manualIndex(t *testing.T, extra ...storage.MapEdge) *Index {
	t.Helper()
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Проход", "desc a", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "Склад", "desc b", "N", 0),
		mkRoom("Z", 0, 1, 0, 3, "Ящики", "desc c", "", 0),
		// FAR-AWAY TWINS of both neighbours, so neither fingerprint is unique and the
		// global auto-resync (reconcile step 3) cannot fire. Without them the resync
		// relocates the tracker by name alone and every test below passes whatever
		// the neighbour search does — which is exactly how the hidden-exit test
		// managed to be green against the OLD rule it was written to fail on.
		mkRoom("Z", 40, 40, 0, 9, "Склад", "desc b", "N", 0),
		mkRoom("Z", 41, 41, 0, 10, "Ящики", "desc c", "", 0),
	}
	edges := storage.DeriveEdges(1, rooms)
	taken := map[EdgeFaceKey]bool{}
	for _, e := range extra {
		taken[EdgeFaceKey{e.FromX, e.FromY, e.FromL, e.Dir}] = true
	}
	kept := edges[:0]
	for _, e := range edges {
		if !taken[EdgeFaceKey{e.FromX, e.FromY, e.FromL, e.Dir}] {
			kept = append(kept, e)
		}
	}
	return BuildIndexWithEdges(1, rooms, append(kept, extra...))
}

// EdgeFaceKey is a (cell, dir) face, local to this test.
type EdgeFaceKey struct {
	X, Y, L int
	Dir     string
}

func manualRow(x, y, l int, dir string, tx, ty, tl int, passable bool) storage.MapEdge {
	e := storage.MapEdge{
		MapSetID: 1, Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceManual,
		Passable: passable, FromZone: "Z", FromX: x, FromY: y, FromL: l, Dir: dir,
	}
	to := storage.EdgeCell{Zone: "Z", X: tx, Y: ty, L: tl}
	e.SetTo(&to)
	return e
}

// TestTrackerRefusesToAnchorThroughADeniedFace: with the queue empty (following /
// teleport) the neighbour search must not offer a cell the player marked
// unreachable. Anchoring there green is a FALSE fix an agent would then path from.
func TestTrackerRefusesToAnchorThroughADeniedFace(t *testing.T) {
	idx := manualIndex(t,
		manualRow(0, 0, 0, "S", 1, 0, 0, false), // a -> b denied by hand
	)
	tr := NewTracker(idx)
	if _, exact := tr.Anchor(Coord{"Z", 0, 0, 0}); !exact {
		t.Fatal("anchor at a failed")
	}
	// The server reports the neighbour the player says is unreachable.
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Склад", Desc: "desc b", Exits: "N"})
	if pos.Valid && pos.Coord == (Coord{"Z", 1, 0, 0}) && pos.Confidence == Green {
		t.Fatal("tracker green-anchored through a face marked as NO PASSAGE")
	}
}

// TestTrackerFollowsAHiddenExit: the mirror case. A secret passage is in no exits
// line, so the old EDirs-constrained neighbour search could never find it and the
// player fell to 🟡/🔴 every time they took it without a queued move.
func TestTrackerFollowsAHiddenExit(t *testing.T) {
	idx := manualIndex(t,
		manualRow(0, 0, 0, "E", 0, 1, 0, true), // a -> c hidden exit
		manualRow(0, 1, 0, "W", 0, 0, 0, true), // and back
	)
	tr := NewTracker(idx)
	if _, exact := tr.Anchor(Coord{"Z", 0, 0, 0}); !exact {
		t.Fatal("anchor at a failed")
	}
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Ящики", Desc: "desc c", Exits: ""})
	if !pos.Valid || pos.Coord != (Coord{"Z", 0, 1, 0}) || pos.Confidence != Green {
		t.Fatalf("tracker did not follow the hand-drawn passage: %+v", pos)
	}
}

// TestTrackerDeadReckoningRefusesADeniedStep: with a move QUEUED, a step onto a
// denied face must not confirm green either — the prediction itself is what the
// mark contradicts.
func TestTrackerDeadReckoningRefusesADeniedStep(t *testing.T) {
	idx := manualIndex(t,
		manualRow(0, 0, 0, "S", 1, 0, 0, false),
	)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Склад", Desc: "desc b", Exits: "N"})
	if pos.Confidence == Green && pos.Coord == (Coord{"Z", 1, 0, 0}) {
		t.Fatal("dead-reckoning confirmed a step the player marked impossible")
	}
}

// TestTrackerDeadReckonsThroughAHiddenExit is the REGRESSION guard on the other
// half: a queued move through a hand-drawn passage keeps working (it did before
// only because the grid delta happens to equal the marked neighbour's coord, so
// moving to edges must not lose it).
func TestTrackerDeadReckonsThroughAHiddenExit(t *testing.T) {
	idx := manualIndex(t,
		manualRow(0, 0, 0, "E", 0, 1, 0, true),
		manualRow(0, 1, 0, "W", 0, 0, 0, true),
	)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("E")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Ящики", Desc: "desc c", Exits: ""})
	if !pos.Valid || pos.Coord != (Coord{"Z", 0, 1, 0}) || pos.Confidence != Green {
		t.Fatalf("dead-reckoning lost the hand-drawn step: %+v", pos)
	}
}

// TestTrackerStillDeadReckonsOntoUnmappedGround is the guard that this change did
// NOT turn "no edge row" into "no prediction": virgin territory has no rows at
// all, and «Записать комнату» depends on the tracker still producing the
// dead-reckoned coord there (the ghost cell).
func TestTrackerStillDeadReckonsOntoUnmappedGround(t *testing.T) {
	idx := manualIndex(t)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 1, 0, 0}) // b: its S neighbour (2,0,0) is not in the map
	tr.PushMove("S")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Неизвестное", Desc: "new", Exits: "N"})
	if !pos.Valid || pos.Coord != (Coord{"Z", 2, 0, 0}) {
		t.Fatalf("lost dead-reckoning onto unmapped ground: %+v", pos)
	}
}

// --- review round 3 ----------------------------------------------------------

// chOnlyIndex is the shape that dominates the live corpus's disagreement between
// the old and new neighbour rules: a room whose connectivity is in `ch` while its
// `edirs` list is EMPTY (629 of the 700 faces the graph rule dropped). The old
// rule read the empty list as "says nothing => offer every adjacent cell"; `ch`
// had in fact said something, and it is authoritative over the empty list.
//
// Here a(0,0) claims only S in ch, and b(-1,0) — its NORTH neighbour — claims only
// N, pointing away. So the graph has no edge between them, while bare adjacency
// does. The twin of b keeps the global fingerprint resync out of the way.
func chOnlyIndex(t *testing.T) *Index {
	t.Helper()
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Центр", "desc a", "", 2),
		mkRoom("Z", -1, 0, 0, 2, "Север", "desc b", "N", 1),
		mkRoom("Z", 40, 40, 0, 9, "Север", "desc b", "N", 1),
	}
	return BuildIndexWithEdges(1, rooms, storage.DeriveEdges(1, rooms))
}

// TestTrackerFallsBackToAdjacencyAtYellow: moving the neighbour search onto the
// graph is right, but it must not COST the player their position where the map
// data is merely poor. When the graph knows no candidate, adjacency is retried and
// the result is held at 🟡 — position kept, confidence honest. Without this the
// same event yields 🔴 "no directional context", which stops «Записать комнату»
// and demands a re-anchor.
func TestTrackerFallsBackToAdjacencyAtYellow(t *testing.T) {
	idx := chOnlyIndex(t)
	tr := NewTracker(idx)
	if _, exact := tr.Anchor(Coord{"Z", 0, 0, 0}); !exact {
		t.Fatal("anchor failed")
	}
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Север", Desc: "desc b", Exits: "N"})
	if !pos.Valid || pos.Coord != (Coord{"Z", -1, 0, 0}) {
		t.Fatalf("the adjacency fallback lost the position: %+v", pos)
	}
	if pos.Confidence != Yellow {
		t.Fatalf("adjacency is a guess and must cap at yellow, got %s: %+v", pos.Confidence, pos)
	}
	if !pos.EventDescribesCell {
		t.Error("the event describes the cell we assumed — «Записать комнату» depends on this flag")
	}
}

// TestTrackerFallbackNeverOutranksHardEvidence: a UNIQUE fingerprint is evidence,
// adjacency is a guess, so the resync must still win and still be 🟢.
func TestTrackerFallbackNeverOutranksHardEvidence(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Центр", "desc a", "", 2),
		mkRoom("Z", -1, 0, 0, 2, "Север", "desc b", "N", 1), // adjacent, no edge
		mkRoom("Z", 9, 9, 0, 3, "Дальняя", "desc far", "N", 1),
	}
	idx := BuildIndexWithEdges(1, rooms, storage.DeriveEdges(1, rooms))
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	// "Север" is unique here, so the resync should green-anchor it rather than the
	// fallback yellow-anchoring the same cell.
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Север", Desc: "desc b", Exits: "N"})
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", -1, 0, 0}) {
		t.Fatalf("a unique fingerprint must still green-anchor: %+v", pos)
	}
}

// TestTrackerFallbackRefusesADeniedFace: the fallback is for map data that is
// merely incomplete. A negation is the player's explicit statement, not missing
// data, so bare adjacency must not walk through it either.
func TestTrackerFallbackRefusesADeniedFace(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Центр", "desc a", "", 2),
		mkRoom("Z", -1, 0, 0, 2, "Север", "desc b", "N", 1),
		mkRoom("Z", 40, 40, 0, 9, "Север", "desc b", "N", 1),
	}
	edges := append(storage.DeriveEdges(1, rooms), manualRow(0, 0, 0, "N", -1, 0, 0, false))
	idx := BuildIndexWithEdges(1, rooms, edges)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Север", Desc: "desc b", Exits: "N"})
	if pos.Valid && pos.Coord == (Coord{"Z", -1, 0, 0}) {
		t.Fatalf("the adjacency fallback walked through a hand-drawn negation: %+v", pos)
	}
}

// TestDeniedStepReportsTheMarkNotATeleport: refusing the step is right, but the
// REASON has to name the cause. It used to come out as "lost — hint contradicts
// assumed cell (likely teleport); re-anchor with mud_anchor_here": wrong cause
// (nothing teleported) and a remedy that does not work (a re-anchor does not clear
// a mark, so the next step through the same face lands right back here).
func TestDeniedStepReportsTheMarkNotATeleport(t *testing.T) {
	idx := manualIndex(t, manualRow(0, 0, 0, "S", 1, 0, 0, false))
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Склад", Desc: "desc b", Exits: "N"})
	if !strings.Contains(pos.Reason, "no-passage") {
		t.Errorf("the reason does not name the hand-drawn mark: %q", pos.Reason)
	}
	if strings.Contains(pos.Reason, "teleport") {
		t.Errorf("the reason still blames a teleport: %q", pos.Reason)
	}
	if !strings.Contains(pos.Reason, "auto") {
		t.Errorf("the reason does not say how to clear the mark: %q", pos.Reason)
	}
}

// TestDeniedStepOntoVirginGroundExplainsItself is where this matters most: with no
// mark, a step off the edge of the map is the 🟡 "edge/cell not in map" state that
// «Записать комнату» draws a new room from. A negation takes that away — correctly,
// the player said there is no way — but silently taking it away is the trap, so the
// reason must name the mark and how to drop it.
func TestDeniedStepOntoVirginGroundExplainsItself(t *testing.T) {
	// (1,0,0)'s S neighbour (2,0,0) is not in the map at all.
	base := manualIndex(t)
	tr := NewTracker(base)
	tr.Anchor(Coord{"Z", 1, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Новая", Desc: "virgin", Exits: "N"})
	if pos.Confidence != Yellow || !pos.EventDescribesCell {
		t.Fatalf("baseline: a step onto virgin ground should be the recordable yellow: %+v", pos)
	}

	denied := manualIndex(t, manualRow(1, 0, 0, "S", 2, 0, 0, false))
	tr = NewTracker(denied)
	tr.Anchor(Coord{"Z", 1, 0, 0})
	tr.PushMove("S")
	pos, _ = tr.Reconcile(RoomEvent{Hint: "Новая", Desc: "virgin", Exits: "N"})
	if pos.Confidence != Red {
		t.Fatalf("a denied step must not silently pass as a recordable cell: %+v", pos)
	}
	if !strings.Contains(pos.Reason, "no-passage") || !strings.Contains(pos.Reason, "auto") {
		t.Errorf("the player is not told what blocked them or how to undo it: %q", pos.Reason)
	}
}

// TestTrackerWalksAVetoedFaceNormally is the CONTRACT of the routing veto and the
// line that separates it from the negation above: "не водить сюда" says the router
// must not PLAN through the face, not that the world lacks a passage. The player
// walks it whenever they like, and the tracker has to confirm the step exactly as
// it would any other — green, on the right cell.
//
// If the veto were ever read by predict()/neighborMatch(), a player who marked an
// unpleasant transition would find themselves 🔴 every time they used it, which is
// the opposite of what they asked for.
func TestTrackerWalksAVetoedFaceNormally(t *testing.T) {
	vetoed := manualRow(0, 0, 0, "S", 1, 0, 0, true)
	vetoed.NoRoute = true
	back := manualRow(1, 0, 0, "N", 0, 0, 0, true)
	back.NoRoute = true
	idx := manualIndex(t, vetoed, back)

	// The router refuses to plan it...
	if res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Coord == (Coord{"Z", 1, 0, 0})
	}); res.Reachable {
		t.Fatalf("precondition: the router should not plan through a vetoed face: %+v", res)
	}

	// ...and the tracker follows the player through it without blinking, both with
	// a queued move and with an empty queue (following / teleport).
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(RoomEvent{Hint: "Склад", Desc: "desc b", Exits: "N"})
	if !pos.Valid || pos.Coord != (Coord{"Z", 1, 0, 0}) || pos.Confidence != Green {
		t.Fatalf("dead-reckoning refused a step through a vetoed (but passable) face: %+v", pos)
	}

	tr2 := NewTracker(idx)
	tr2.Anchor(Coord{"Z", 0, 0, 0})
	pos2, _ := tr2.Reconcile(RoomEvent{Hint: "Склад", Desc: "desc b", Exits: "N"})
	if !pos2.Valid || pos2.Coord != (Coord{"Z", 1, 0, 0}) || pos2.Confidence != Green {
		t.Fatalf("the neighbour search refused a vetoed (but passable) face: %+v", pos2)
	}

	// The third reader of the layer: the 🔴 reason that names a hand-drawn
	// no-passage mark. A veto is not one, and a step over it that goes wrong for
	// some unrelated reason must not be blamed on a mark the player did not make.
	if tr2.deniedFace(Coord{"Z", 0, 0, 0}, "S") {
		t.Error("a vetoed (but passable) face is reported as hand-drawn NO PASSAGE")
	}
}
