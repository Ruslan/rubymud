package mapper

import (
	"reflect"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

// Weight is a cost of this directed transition, not a cost of the room it lands
// in.  A shorter mountain route must lose to a longer cheap road.
func TestFindPathPrefersLowerDirectedWeight(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "start", "", "", 0),
		mkRoom("Z", 0, 1, 0, 2, "mountain", "", "", 0),
		mkRoom("Z", 0, 2, 0, 3, "goal", "", "", 0),
		mkRoom("Z", 1, 0, 0, 4, "road one", "", "", 0),
		mkRoom("Z", 1, 1, 0, 5, "road two", "", "", 0),
	}
	edge := func(from, to Coord, dir string, weight float64) storage.MapEdge {
		e := storage.MapEdge{MapSetID: 1, Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceManual, Passable: true,
			FromZone: from.Zone, FromX: from.X, FromY: from.Y, FromL: from.L, Dir: dir,
			EdgeAttrs: storage.EdgeAttrs{Weight: weight}}
		e.SetTo(&storage.EdgeCell{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L})
		return e
	}
	s := Coord{"Z", 0, 0, 0}
	m := Coord{"Z", 0, 1, 0}
	g := Coord{"Z", 0, 2, 0}
	r1 := Coord{"Z", 1, 0, 0}
	r2 := Coord{"Z", 1, 1, 0}
	idx := BuildIndexWithEdges(1, rooms, []storage.MapEdge{
		edge(s, m, "E", 1), edge(m, g, "E", 1),
		edge(s, r1, "S", .5), edge(r1, r2, "E", .5), edge(r2, g, "N", .5),
	})
	res := idx.FindPath(s, func(r *IndexRoom) bool { return r.Hint == "goal" })
	if !res.Reachable || len(res.Steps) != 3 {
		t.Fatalf("cheap road should win: %+v", res)
	}
	got := []string{res.Steps[0].Command, res.Steps[1].Command, res.Steps[2].Command}
	if want := []string{"s", "e", "n"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

func TestFindPathWeightIsDirected(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 0, 0, "E") || isFace(e, 0, 1, 0, "E") {
			e.Weight = 10
		}
	})
	idx := BuildIndexWithEdges(1, rooms, edges)
	forward := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool { return r.Hint == "C" })
	back := idx.FindPath(Coord{"Z", 0, 2, 0}, func(r *IndexRoom) bool { return r.Hint == "A" })
	if !forward.Reachable || !back.Reachable {
		t.Fatalf("directed edges should remain traversable: forward=%+v back=%+v", forward, back)
	}
	if forward.TotalWeight != 20 || back.TotalWeight != 2 {
		t.Errorf("directed costs = %v/%v, want 20/2", forward.TotalWeight, back.TotalWeight)
	}
}

func TestFindPathLegacyZeroWeightKeepsExactDefaultRoute(t *testing.T) {
	// Two equal two-hop routes. The old BFS visited S before E (dirOrder), and
	// old MapEdge literals leave Weight at zero, which must still mean one.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "start", "", "", 0),
		mkRoom("Z", 1, 0, 0, 2, "south", "", "", 0),
		mkRoom("Z", 0, 1, 0, 3, "east", "", "", 0),
		mkRoom("Z", 1, 1, 0, 4, "goal", "", "", 0),
	}
	add := func(from, to Coord, dir string) storage.MapEdge {
		e := storage.MapEdge{MapSetID: 1, Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceManual, Passable: true,
			FromZone: from.Zone, FromX: from.X, FromY: from.Y, FromL: from.L, Dir: dir}
		e.SetTo(&storage.EdgeCell{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L})
		return e
	}
	s, south, east, goal := Coord{"Z", 0, 0, 0}, Coord{"Z", 1, 0, 0}, Coord{"Z", 0, 1, 0}, Coord{"Z", 1, 1, 0}
	idx := BuildIndexWithEdges(1, rooms, []storage.MapEdge{add(s, south, "S"), add(south, goal, "E"), add(s, east, "E"), add(east, goal, "S")})
	res := idx.FindPath(s, func(r *IndexRoom) bool { return r.Hint == "goal" })
	if !res.Reachable || res.TotalWeight != 2 || len(res.Steps) != 2 {
		t.Fatalf("legacy defaults changed route: %+v", res)
	}
	if got := []string{res.Steps[0].Command, res.Steps[1].Command}; !reflect.DeepEqual(got, []string{"s", "e"}) {
		t.Errorf("legacy route = %v, want [s e]", got)
	}
}

// The routing half of edge-attribute behavior: what a phrase, a door and a
// "не водить сюда" veto do to a computed route.
//
// Every test here builds the index from EXPLICIT edge rows (BuildIndexWithEdges)
// rather than letting BuildIndex derive them, because the attributes are precisely
// the part of a row that no derivation produces.

// attrChain is A(0,0) -e-> B(0,1) -e-> C(0,2), a plain three-room corridor, with
// the caller free to decorate the individual directed rows.
func attrChain() []storage.Room {
	return []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "E", chMask("E")),
		mkRoom("Z", 0, 1, 0, 2, "B", "b", "E W", chMask("E", "W")),
		mkRoom("Z", 0, 2, 0, 3, "C", "c", "W", chMask("W")),
	}
}

// decorate returns the derived edge set with `fn` applied to every row, so a test
// can attach attributes to exactly the face it cares about.
func decorate(rooms []storage.Room, fn func(e *storage.MapEdge)) []storage.MapEdge {
	edges := storage.DeriveEdges(1, rooms)
	for i := range edges {
		fn(&edges[i])
	}
	return edges
}

func isFace(e *storage.MapEdge, x, y, l int, dir string) bool {
	return e.Kind == storage.EdgeKindGrid && e.FromX == x && e.FromY == y && e.FromL == l && e.Dir == dir
}

func routeAToC(t *testing.T, idx *Index) PathResult {
	t.Helper()
	return idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool { return r.Hint == "C" })
}

// rmudIndex is an index told the RMUD profile's door verb — the corpus and the
// owner's map are RMUD, and the template is a property of the MUD carried by the
// session's room-format profile, never a constant in shared code.
func rmudIndex(rooms []storage.Room, edges []storage.MapEdge) *Index {
	idx := BuildIndexWithEdges(1, rooms, edges)
	idx.SetDoorOpenTemplate(RMUDSpec().DoorOpen)
	return idx
}

// TestRoutePhraseReplacesTheDirection — п.2.1. A passage taken by "двигать ящик" is
// not taken by "e", so the route has to emit the phrase INSTEAD of the letter. It is
// a step of the same route, not an annotation beside it: everything that walks a
// route reads Command, and Command is what changes.
func TestRoutePhraseReplacesTheDirection(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") { // the B -> C hop only
			e.Source = storage.EdgeSourceManual
			e.Phrase = "двигать ящик"
		}
	})
	res := routeAToC(t, BuildIndexWithEdges(1, rooms, edges))
	if !res.Reachable || len(res.Steps) != 2 {
		t.Fatalf("route A->C should be 2 hops: %+v", res)
	}
	if res.Steps[0].Command != "e" {
		t.Errorf("the undecorated first hop changed: %q", res.Steps[0].Command)
	}
	if res.Steps[1].Command != "двигать ящик" {
		t.Errorf("second hop Command = %q, want the phrase — a direction letter does not take this passage",
			res.Steps[1].Command)
	}
	if res.Steps[1].Phrase != "двигать ящик" {
		t.Errorf("Phrase not surfaced on the step: %+v", res.Steps[1])
	}
	// The geometry is still available: a phrase says HOW, not WHERE.
	if res.Steps[1].Dir != "e" {
		t.Errorf("Dir = %q, want the geometric direction to survive", res.Steps[1].Dir)
	}
}

// TestRoutePhraseIsOneSided — the same hole, the other way round. This is why the
// attribute lives on a directed row: B->C is "двигать ящик", C->B is "лезть ковёр",
// and neither route may borrow the other's command.
func TestRoutePhraseIsOneSided(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		switch {
		case isFace(e, 0, 1, 0, "E"):
			e.Source, e.Phrase = storage.EdgeSourceManual, "двигать ящик"
		case isFace(e, 0, 2, 0, "W"):
			e.Source, e.Phrase = storage.EdgeSourceManual, "лезть ковёр"
		}
	})
	idx := BuildIndexWithEdges(1, rooms, edges)
	fwd := routeAToC(t, idx)
	if !fwd.Reachable || fwd.Steps[len(fwd.Steps)-1].Command != "двигать ящик" {
		t.Fatalf("A->C last hop: %+v", fwd.Steps)
	}
	back := idx.FindPath(Coord{"Z", 0, 2, 0}, func(r *IndexRoom) bool { return r.Hint == "A" })
	if !back.Reachable || back.Steps[0].Command != "лезть ковёр" {
		t.Fatalf("C->A first hop should use the OTHER side's phrase: %+v", back.Steps)
	}
}

// TestRouteThroughDoorEmitsTheOpenAction — the behavior this regression exists to
// fix: a route through a door used to be cut short and the player told to open it by
// hand and ask again. It must now carry the opening action as a step of the walk.
func TestRouteThroughDoorEmitsTheOpenAction(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Door = true
		}
	})
	res := routeAToC(t, rmudIndex(rooms, edges))
	if !res.Reachable {
		t.Fatalf("a door must not make the target unreachable: %+v", res)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("route should still be 2 hops, got %d: %+v", len(res.Steps), res.Steps)
	}
	door := res.Steps[1]
	if !door.Door || door.DoorKind != DoorConfirmed {
		t.Errorf("the decorated hop is not graded as a confirmed door: %+v", door)
	}
	if door.OpenAction != "откр две восток" {
		t.Errorf("OpenAction = %q, want the PROFILE's template filled with the hop's direction word",
			door.OpenAction)
	}
	if res.Steps[0].OpenAction != "" {
		t.Errorf("the door-free hop got an opening action: %+v", res.Steps[0])
	}
}

// TestRouteDoorUsesTheEdgesOwnOpenCommand: the default template is only a fallback,
// and it must lose to whatever the operator recorded — otherwise the markup could
// not describe a lever, a password, or a non-Russian MUD.
func TestRouteDoorUsesTheEdgesOwnOpenCommand(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Source, e.Door, e.OpenCmd = storage.EdgeSourceManual, true, "дёрнуть рычаг"
			e.Locked = true
		}
	})
	res := routeAToC(t, BuildIndexWithEdges(1, rooms, edges))
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	if got := res.Steps[1].OpenAction; got != "дёрнуть рычаг" {
		t.Errorf("OpenAction = %q, want the edge's own command", got)
	}
	// A lock is REPORTED, never routed around: the player may hold the key.
	if !res.Steps[1].Locked {
		t.Error("the lock was not surfaced on the step")
	}
}

// TestDoorGradeIsPresumedFromTheReverseRow: a door recorded on ONE side only is the
// normal case in the imported maps, and it must still be seen from the other end.
// Since edge attributes were materialized, the "other side" is the passage's other directed row, and the
// opening command comes from the side that actually records the door — while the
// LOCK does not, because "locked from over there" says nothing about this side.
func TestDoorGradeIsPresumedFromTheReverseRow(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 2, 0, "W") { // C's reverse face carries the door + lock
			e.Source, e.Door, e.Locked, e.OpenCmd =
				storage.EdgeSourceManual, true, true, "потянуть кольцо"
		}
	})
	res := routeAToC(t, BuildIndexWithEdges(1, rooms, edges))
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	hop := res.Steps[1]
	if hop.DoorKind != DoorPresumed {
		t.Errorf("DoorKind = %v, want presumed (only the far side records the door)", hop.DoorKind)
	}
	if hop.OpenAction != "потянуть кольцо" {
		t.Errorf("OpenAction = %q, want the far side's recorded command", hop.OpenAction)
	}
	if hop.Locked {
		t.Error("a lock on the FAR side was reported as a lock on this one")
	}
}

// TestNoRouteVetoIsSkippedByTheRouter — п.2.3, first half. The veto is not the
// negation: the row stays passable, and the only thing that changes is that the
// router will not choose it.
func TestNoRouteVetoIsSkippedByTheRouter(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Source, e.NoRoute = storage.EdgeSourceManual, true
		}
	})
	idx := BuildIndexWithEdges(1, rooms, edges)
	// The edge is still THERE and still passable — that is the whole distinction
	// from a negation, and it is what keeps the tracker accepting the step.
	e := idx.Room(Coord{"Z", 0, 1, 0}).GridEdge("E")
	if e == nil || !e.Passable || !e.NoRoute {
		t.Fatalf("the vetoed row should remain a passable edge: %+v", e)
	}
	nbsA, _ := idx.worldNeighbors(Coord{"Z", 0, 1, 0}, false)
	for _, nb := range nbsA {
		if nb.to == (Coord{"Z", 0, 2, 0}) {
			t.Errorf("the router still offered the vetoed neighbour: %+v", nb)
		}
	}
	// ...and it IS offered when the vetoes are lifted (the diagnosis pass).
	found := false
	nbsB, _ := idx.worldNeighbors(Coord{"Z", 0, 1, 0}, true)
	for _, nb := range nbsB {
		if nb.to == (Coord{"Z", 0, 2, 0}) {
			found = true
		}
	}
	if !found {
		t.Error("ignoreVetoes did not bring the neighbour back")
	}
}

// TestNoRouteVetoSaysWhyThereIsNoRoute — п.2.3, second half, and the part that is
// easy to leave out: a veto is INVISIBLE in "no route to target", so the player goes
// hunting for a hole in the map that they themselves closed.
func TestNoRouteVetoSaysWhyThereIsNoRoute(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Source, e.NoRoute = storage.EdgeSourceManual, true
		}
	})
	res := routeAToC(t, BuildIndexWithEdges(1, rooms, edges))
	if res.Reachable {
		t.Fatalf("C must be unreachable while the only way in is vetoed: %+v", res)
	}
	if !res.VetoedOnly {
		t.Fatalf("the refusal does not say the map is intact: %+v", res)
	}
	if !strings.Contains(res.Reason, "не водить") {
		t.Errorf("Reason = %q, want it to name the mark the player has to lift", res.Reason)
	}
}

// TestGenuinelyUnreachableIsNotBlamedOnVetoes: the diagnosis must not fire when the
// map really is disconnected, or every dead end would start advertising a mark that
// is not there. A veto exists elsewhere in the set precisely so the second search
// runs and has to come back empty-handed.
func TestGenuinelyUnreachableIsNotBlamedOnVetoes(t *testing.T) {
	rooms := append(attrChain(),
		mkRoom("Z", 9, 9, 0, 4, "Island", "i", "", 0))
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Source, e.NoRoute = storage.EdgeSourceManual, true
		}
	})
	idx := BuildIndexWithEdges(1, rooms, edges)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool { return r.Hint == "Island" })
	if res.Reachable {
		t.Fatalf("the island should be unreachable: %+v", res)
	}
	if res.VetoedOnly {
		t.Errorf("a genuinely disconnected target was blamed on the vetoes: %+v", res)
	}
}

// --- where the door-opening command comes from ------------------------------
//
// The template was a package-level constant for one commit, which meant an English
// Diku got "откр две north" in its walk. It is a property of the MUD on the other
// end — the same class of thing as the minimap glyph set, which belongs in
// ProfileSpec rather than shared code — so it lives in the room-format
// profile, and an index nobody told sends nothing.

// TestDoorOpenActionComesFromTheSessionProfile: the same map and the same door,
// three profiles, three different (or absent) commands.
func TestDoorOpenActionComesFromTheSessionProfile(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Door = true
		}
	})
	for _, tc := range []struct {
		name, tmpl, want string
	}{
		// The default for EVERY profile but the builtin rmud. No command is sent and
		// the route behaves exactly as it did before doors became markup — the one
		// answer that is never wrong on a mud we know nothing about.
		{"a format that does not describe doors", "", ""},
		{"rmud", RMUDSpec().DoorOpen, "откр две восток"},
		// An english mud asks for the canonical letter the router already speaks.
		{"an english format", "open door {dir_en}", "open door e"},
		// ...or for no argument at all.
		{"a verb with no argument", "open door", "open door"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx := BuildIndexWithEdges(1, rooms, edges)
			idx.SetDoorOpenTemplate(tc.tmpl)
			res := routeAToC(t, idx)
			if !res.Reachable || len(res.Steps) != 2 {
				t.Fatalf("route: %+v", res)
			}
			hop := res.Steps[1]
			if !hop.Door {
				t.Fatalf("the door was lost: %+v", hop)
			}
			if hop.OpenAction != tc.want {
				t.Errorf("OpenAction = %q, want %q", hop.OpenAction, tc.want)
			}
		})
	}
}

// TestRMUDIsTheOnlyBuiltinThatOpensDoors: the value ships with the profile it
// belongs to and with no other. If a starter format for some other mud silently
// inherited it, every route on that mud would carry a Russian command.
func TestRMUDIsTheOnlyBuiltinThatOpensDoors(t *testing.T) {
	if got := RMUDSpec().DoorOpen; got != "откр две {dir}" {
		t.Errorf("rmud DoorOpen = %q, want RMUD's own door verb", got)
	}
	for _, info := range DetectorInfos() {
		det, _, err := SafeNewDetector(info.Key)
		if err != nil || det == nil {
			continue
		}
		got := DetectorDoorOpen(det)
		if info.Key == "rmud" {
			if got == "" {
				t.Errorf("builtin rmud lost its door verb")
			}
			continue
		}
		if got != "" {
			t.Errorf("builtin %q ships a door template %q — it can only be right for one mud",
				info.Key, got)
		}
	}
}

// TestPerEdgeOpenCommandWorksWithoutAProfileTemplate: the per-edge command is the
// mud-independent half of the design, so it must not depend on the profile saying
// anything. A player on an unknown mud can still describe one specific door.
func TestPerEdgeOpenCommandWorksWithoutAProfileTemplate(t *testing.T) {
	rooms := attrChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Source, e.Door, e.OpenCmd = storage.EdgeSourceManual, true, "pull lever"
		}
	})
	idx := BuildIndexWithEdges(1, rooms, edges) // no profile template at all
	res := routeAToC(t, idx)
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	if got := res.Steps[1].OpenAction; got != "pull lever" {
		t.Errorf("OpenAction = %q, want the edge's own command regardless of the profile", got)
	}
}

// --- the pipe-run collapse must not swallow a command ------------------------
//
// collapsePipeRuns folds a run of same-direction cells through a pipe corridor into
// ONE emitted command, because the server walks the whole run with one step. Two
// guards keep that from eating a hop that needs its own command:
//
//   ownCommand — this hop needs one (a traversal phrase, or a door to open), so it
//                cannot be folded into the previous command;
//   prevOwn    — the PREVIOUS emitted hop had one, so this hop must not extend it.
//
// Without them the phrase (or the opening action) disappears from the route
// entirely and the walk silently stops working, which is the failure the whole
// attribute layer is meant to prevent. Each guard has its own test below and each
// test fails when its own line is neutralised.

// pipeChain is A(0,0) -e-> P(0,1) -e-> C(0,2) where P is a PIPE corridor, so the
// two eastward hops collapse into one command unless something stops them.
func pipeChain() []storage.Room {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "E", chMask("E")),
		mkRoom("Z", 0, 1, 0, 2, "P", "p", "E W", chMask("E", "W")),
		mkRoom("Z", 0, 2, 0, 3, "C", "c", "W", chMask("W")),
	}
	rooms[1].Pipe = true
	return rooms
}

// The baseline: with nothing marked, the two hops DO collapse. Without this the
// two tests below would pass for the wrong reason (nothing to collapse).
func TestPipeRunCollapsesWhenNothingNeedsItsOwnCommand(t *testing.T) {
	rooms := pipeChain()
	res := routeAToC(t, rmudIndex(rooms, storage.DeriveEdges(1, rooms)))
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	if len(res.Steps) != 1 || res.Steps[0].Cells != 2 {
		t.Fatalf("want one command spanning 2 cells, got %d steps: %+v", len(res.Steps), res.Steps)
	}
}

// GUARD 1 (ownCommand): the SECOND hop carries a phrase. Folding it into the first
// command would send "e" alone and the player would never take the passage.
func TestPipeRunDoesNotSwallowAHopThatNeedsItsOwnCommand(t *testing.T) {
	rooms := pipeChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") { // P -> C, the hop out of the pipe
			e.Source, e.Phrase = storage.EdgeSourceManual, "двигать ящик"
		}
	})
	res := routeAToC(t, rmudIndex(rooms, edges))
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	cmds := make([]string, 0, len(res.Steps))
	for _, st := range res.Steps {
		cmds = append(cmds, st.Command)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("the phrase hop was folded into the pipe run — the phrase is gone from the route: %v", cmds)
	}
	if cmds[0] != "e" || cmds[1] != "двигать ящик" {
		t.Errorf("commands = %v, want [e двигать ящик]", cmds)
	}
	// A door needing an opening action is the same shape and must behave the same.
	edges = decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "E") {
			e.Door = true
		}
	})
	res = routeAToC(t, rmudIndex(rooms, edges))
	if len(res.Steps) != 2 || res.Steps[1].OpenAction == "" {
		t.Errorf("a door hop was folded into the pipe run, losing its opening command: %+v", res.Steps)
	}
}

// GUARD 2 (prevOwn): the FIRST hop carries a phrase. The second must not extend it,
// or one "двигать ящик" would be claimed to carry the player across both cells.
func TestAHopWithItsOwnCommandDoesNotAbsorbTheNextCell(t *testing.T) {
	rooms := pipeChain()
	edges := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 0, 0, "E") { // A -> P, the hop INTO the pipe
			e.Source, e.Phrase = storage.EdgeSourceManual, "двигать ящик"
		}
	})
	res := routeAToC(t, rmudIndex(rooms, edges))
	if !res.Reachable {
		t.Fatalf("unreachable: %+v", res)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("the next cell was absorbed into the phrase command: %+v", res.Steps)
	}
	if res.Steps[0].Command != "двигать ящик" || res.Steps[0].Cells != 1 {
		t.Errorf("first step = %q over %d cells, want the phrase over exactly 1",
			res.Steps[0].Command, res.Steps[0].Cells)
	}
	if res.Steps[1].Command != "e" {
		t.Errorf("second step = %q, want the plain direction", res.Steps[1].Command)
	}
}

// TestTheSecondPassOnlyRunsWhenAVetoCutSomethingOff: "no route" is an ordinary
// answer, produced under the session's mapMu, and the veto diagnosis costs a whole
// second BFS. It must therefore run only when it could possibly say something —
// when a declined edge led into a cell the search never reached by any other way.
//
// Measured on the live corpus, failed search: 10.3 ms with no veto, 11.6 ms with a
// veto the search routes around, 21.1 ms with one that disconnects a region. Only
// the third pays, and only there is there anything to report.
func TestTheSecondPassOnlyRunsWhenAVetoCutSomethingOff(t *testing.T) {
	// A ring: A(0,0) - B(0,1) - C(0,2) - D(1,2) - E(1,1) - back to A(1,0)... kept
	// simple as a square, so every cell has two ways in.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "E S", chMask("E", "S")),
		mkRoom("Z", 0, 1, 0, 2, "B", "b", "W S", chMask("W", "S")),
		mkRoom("Z", 1, 0, 0, 3, "C", "c", "N E", chMask("N", "E")),
		mkRoom("Z", 1, 1, 0, 4, "D", "d", "N W", chMask("N", "W")),
	}
	nowhere := func(r *IndexRoom) bool { return r.Hint == "\x00nope\x00" }
	from := Coord{"Z", 0, 0, 0}

	// (1) No veto: nothing declined, so no second pass.
	base := storage.DeriveEdges(1, rooms)
	if _, cut := BuildIndexWithEdges(1, rooms, base).findPath(from, nowhere, false); cut {
		t.Errorf("a set with no vetoes asked for the second pass")
	}

	// (2) A veto the ring routes around: B is still reachable via C and D, so
	// lifting the veto could not produce any route the search did not already have.
	routed := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 0, 0, "E") { // A -> B, one of two ways into B
			e.Source, e.NoRoute = storage.EdgeSourceManual, true
		}
	})
	if _, cut := BuildIndexWithEdges(1, rooms, routed).findPath(from, nowhere, false); cut {
		t.Errorf("a veto the search routed around still bought a second BFS")
	}

	// (3) A veto that genuinely disconnects: both ways into D are denied, so D is
	// unreachable and only lifting a veto could change that. Here the second pass
	// earns its cost.
	cutOff := decorate(rooms, func(e *storage.MapEdge) {
		if isFace(e, 0, 1, 0, "S") || isFace(e, 1, 0, 0, "E") { // B->D and C->D
			e.Source, e.NoRoute = storage.EdgeSourceManual, true
		}
	})
	if _, cut := BuildIndexWithEdges(1, rooms, cutOff).findPath(from, nowhere, false); !cut {
		t.Errorf("a veto that disconnected a cell did NOT ask for the second pass")
	}
	// ...and end to end it names itself.
	res := BuildIndexWithEdges(1, rooms, cutOff).FindPath(from, func(r *IndexRoom) bool { return r.Hint == "D" })
	if res.Reachable || !res.VetoedOnly {
		t.Errorf("the disconnected target was not reported as veto-only: %+v", res)
	}
}
