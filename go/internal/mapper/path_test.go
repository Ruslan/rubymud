package mapper

import (
	"testing"

	"rubymud/go/internal/storage"
)

// chMask builds a ch bitmask from direction letters (test helper).
func chMask(dirs ...string) int {
	m := 0
	for _, d := range dirs {
		if b, ok := chBitOrder[d]; ok {
			m |= 1 << b
		}
	}
	return m
}

// TestFindPathReachesAsymmetricDeadEnd is the regression guard for the dropped-
// final-turn bug: a single-exit dead-end target C whose neighbor B's map data
// OMITS the forward link (asymmetric ch) must still be reachable — C
// authoritatively advertises the back-link, so the edge B->C is real. Under the
// previous strict "departing room must advertise the exit" rule this route
// dropped the final hop and C was unreachable.
func TestFindPathReachesAsymmetricDeadEnd(t *testing.T) {
	// A(0,0) --в--> B(0,1) --в--> C(0,2). Axis: E => y+1.
	// C is single-exit W (points back at B). B's ch OMITS E (the forward link).
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "E", chMask("E")),
		mkRoom("Z", 0, 1, 0, 2, "B", "b", "W", chMask("W")), // B: no E advertised
		mkRoom("Z", 0, 2, 0, 3, "C", "c", "W", chMask("W")), // C: single-exit W back at B
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "C"
	})
	if !res.Reachable {
		t.Fatalf("asymmetric dead-end C should be reachable (C advertises the back-link): %+v", res)
	}
	// Must land INSIDE C at (0,2), with the final hop в.
	last := res.Steps[len(res.Steps)-1]
	if last.Command != DirRU("E") || last.Hint != "C" {
		t.Errorf("final hop into C = %q -> %q, want в -> C", last.Command, last.Hint)
	}
	if len(res.Steps) != 2 {
		t.Errorf("route should be 2 hops (в; в), got %d: %+v", len(res.Steps), res.Steps)
	}
}

// TestFindPathStillRejectsDisplacedFalseEdge guards the previous fix: a
// permissive (no ch, no edirs) cell must NOT fabricate an edge into an explicit
// neighbor that denies the back-link (the displaced-room / wrong-direction bug).
func TestFindPathStillRejectsDisplacedFalseEdge(t *testing.T) {
	// P(0,0) is a blank permissive cell east-adjacent to bank-like B(0,1) whose
	// only exit is S (ch=S, so it does NOT connect W back to P). Stepping в from P
	// into B must be REJECTED (no reverse link); B is only reachable from its S
	// side. Provide that legit S approach via A(1,1)->B going N.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "", "", "", 0),                 // P: permissive blank
		mkRoom("Z", 0, 1, 0, 2, "Bank", "b", "S", chMask("S")), // B: single-exit S
		mkRoom("Z", 1, 1, 0, 3, "SouthNb", "s", "N", chMask("N")),
	}
	idx := BuildIndex(1, rooms)
	// From P, the bank must NOT be reachable by a fabricated в edge; it is only
	// reachable via its south neighbor. Route from the south neighbor works:
	res := idx.FindPath(Coord{"Z", 1, 1, 0}, func(r *IndexRoom) bool { return r.Hint == "Bank" })
	if !res.Reachable || res.Steps[len(res.Steps)-1].Command != DirRU("N") {
		t.Fatalf("bank should be entered via с from its south neighbor: %+v", res)
	}
	// And the permissive P must not offer a в edge into the bank (reverse denied).
	nbs := idx.worldNeighbors(Coord{"Z", 0, 0, 0})
	for _, nb := range nbs {
		if nb.to == (Coord{"Z", 0, 1, 0}) {
			t.Errorf("permissive cell fabricated a false edge into the bank: %+v", nb)
		}
	}
}

func TestFindPathLinear(t *testing.T) {
	idx := linearIndex()
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "Третья"
	})
	if !res.Reachable {
		t.Fatalf("expected reachable: %+v", res)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	// Going south twice: DirRU(S) = "ю".
	if res.Steps[0].Command != "ю" || res.Steps[1].Command != "ю" {
		t.Errorf("commands = %v, want [ю ю]", []string{res.Steps[0].Command, res.Steps[1].Command})
	}
}

func TestFindPathRefusesDTTarget(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Старт", "start", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "Ловушка", "trap", "N", 0, withDT()),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "Ловушка"
	})
	if !res.DTTarget {
		t.Errorf("expected DTTarget refusal, got %+v", res)
	}
}

func TestFindPathExcludesDTOnRoute(t *testing.T) {
	// Straight corridor where the middle cell is a DT: no route past it.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "B", "b", "N S", 0, withDT()),
		mkRoom("Z", 2, 0, 0, 3, "C", "c", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "C"
	})
	if res.Reachable {
		t.Errorf("route should be blocked by DT middle cell: %+v", res)
	}
}

func TestFindPathCollapsesPipeRun(t *testing.T) {
	// A(0) -> P1(1,pipe) -> P2(2,pipe) -> B(3), all connected N/S along the S
	// axis. The MUD traverses the whole pipe run with a single command, so the
	// three same-direction cells must collapse to ONE emitted "ю" landing on B.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Вход", "entry", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "", "", "N S", 0, withPipe()),
		mkRoom("Z", 2, 0, 0, 3, "", "", "N S", 0, withPipe()),
		mkRoom("Z", 3, 0, 0, 4, "Выход", "exit", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "Выход"
	})
	if !res.Reachable {
		t.Fatalf("expected reachable: %+v", res)
	}
	// Before collapse this would be 3 commands (ю;ю;ю); after collapse it is one.
	if len(res.Steps) != 1 {
		t.Fatalf("pipe run should collapse to 1 command, got %d: %+v", len(res.Steps), res.Steps)
	}
	st := res.Steps[0]
	if st.Command != "ю" {
		t.Errorf("collapsed command = %q, want ю", st.Command)
	}
	if st.Cells != 3 {
		t.Errorf("collapsed step should span 3 cells, got %d", st.Cells)
	}
	if st.Hint != "Выход" {
		t.Errorf("collapsed step should land on Выход, got %q", st.Hint)
	}
}

func TestFindPathMixedNormalAndPipe(t *testing.T) {
	// A(0) --ю--> B(1,normal) --ю--> P(2,pipe) --ю--> C(3,normal).
	// Expect 2 emitted commands: "ю" (A->B) and "ю" (B->P->C collapsed).
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "B", "b", "N S", 0),
		mkRoom("Z", 2, 0, 0, 3, "", "", "N S", 0, withPipe()),
		mkRoom("Z", 3, 0, 0, 4, "C", "c", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "C"
	})
	if !res.Reachable {
		t.Fatalf("expected reachable: %+v", res)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("mixed route should emit 2 commands, got %d: %+v", len(res.Steps), res.Steps)
	}
	if res.Steps[0].Command != "ю" || res.Steps[0].Cells != 1 || res.Steps[0].Hint != "B" {
		t.Errorf("step1 wrong: %+v", res.Steps[0])
	}
	if res.Steps[1].Command != "ю" || res.Steps[1].Cells != 2 || res.Steps[1].Hint != "C" {
		t.Errorf("step2 (collapsed pipe) wrong: %+v", res.Steps[1])
	}
}

func TestFindPathPipeDirectionChangeDoesNotCollapse(t *testing.T) {
	// A pipe cell where the corridor turns: A(0,0) --ю--> P(1,0,pipe) --в--> C(1,1).
	// Different directions must NOT collapse (server steps differ): 2 commands.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "", "", "N E", 0, withPipe()),
		mkRoom("Z", 1, 1, 0, 3, "C", "c", "W", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "C"
	})
	if !res.Reachable || len(res.Steps) != 2 {
		t.Fatalf("turning pipe should not collapse, want 2 commands: %+v", res)
	}
	if res.Steps[0].Command != "ю" || res.Steps[1].Command != "в" {
		t.Errorf("commands = %q,%q, want ю,в", res.Steps[0].Command, res.Steps[1].Command)
	}
}

func TestFindPathFlagsDoor(t *testing.T) {
	// A --ю--> B where the ю exit from A is a DOOR ("(S)"). The step must be
	// flagged Door (CONFIRMED — the source face carries it) so the agent opens it.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "(S)", 0),
		mkRoom("Z", 1, 0, 0, 2, "B", "b", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "B"
	})
	if !res.Reachable || len(res.Steps) != 1 {
		t.Fatalf("expected 1-step route: %+v", res)
	}
	if !res.Steps[0].Door || res.Steps[0].DoorKind != DoorConfirmed {
		t.Errorf("door step should be CONFIRMED: %+v", res.Steps[0])
	}
	if res.Steps[0].Command != "ю" {
		t.Errorf("command = %q, want ю", res.Steps[0].Command)
	}
}

// TestFindPathPresumedDoorFromTargetReverseFace is the live field case: hop N
// into a cell whose SOUTH face records the door, while the SOURCE has no door on
// N. The same physical door was recorded one-sided; the emitter must PRESUME it.
// Mirrors Хилло (7,-4)->(6,-4) with (6,-4) doors=S walking N (N: x-1).
func TestFindPathPresumedDoorFromTargetReverseFace(t *testing.T) {
	rooms := []storage.Room{
		// (7,-4) source "A" — exits N (no door recorded on this face).
		mkRoom("Хилло", 7, -4, 0, 1, "A", "a", "N", chMask("N")),
		// (6,-4) target "B" — records a door on its SOUTH face: exits "(S)".
		mkRoom("Хилло", 6, -4, 0, 2, "B", "b", "(S)", chMask("S")),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Хилло", 7, -4, 0}, func(r *IndexRoom) bool {
		return r.Hint == "B"
	})
	if !res.Reachable || len(res.Steps) != 1 {
		t.Fatalf("expected 1-step route: %+v", res)
	}
	st := res.Steps[0]
	if st.Command != "с" {
		t.Errorf("command = %q, want с (north)", st.Command)
	}
	if st.DoorKind != DoorPresumed {
		t.Errorf("hop with only the target's reverse face carrying the door must be PRESUMED, got %+v", st)
	}
	if !st.Door {
		t.Errorf("presumed door should still set Door=true (any-door flag): %+v", st)
	}
}

// TestFindPathNoDoorIsClean: a hop where neither face records a door has no door
// flag of any kind.
func TestFindPathNoDoorIsClean(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "A", "a", "S", chMask("S")),
		mkRoom("Z", 1, 0, 0, 2, "B", "b", "N", chMask("N")),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Hint == "B"
	})
	if !res.Reachable || len(res.Steps) != 1 {
		t.Fatalf("expected 1-step route: %+v", res)
	}
	if res.Steps[0].Door || res.Steps[0].DoorKind != DoorNone {
		t.Errorf("clean hop should carry no door flag: %+v", res.Steps[0])
	}
}

// TestFindPathEnglishDir guards the new PathStep.Dir field: every grid hop
// carries its canonical LOWERCASE ENGLISH letter (n/s/e/w/u/d) so the REST
// map-path endpoint can emit a "w;e;s;n"-style walk for the command input. Uses
// all four planar directions + up/down.
func TestFindPathEnglishDir(t *testing.T) {
	// Start at center (1,1,0). Neighbors: N=(0,1) S=(2,1) W=(1,0) E=(1,2),
	// plus U=(1,1,1) D=(1,1,-1). We route S then E then U from a chain.
	// Build a small L-shaped chain: A(1,1,0)-S->B(2,1,0)-E->C(2,2,0)-U->D(2,2,1).
	rooms := []storage.Room{
		mkRoom("Z", 1, 1, 0, 1, "A", "a", "S", chMask("S")),
		mkRoom("Z", 2, 1, 0, 2, "B", "b", "N E", chMask("N", "E")),
		mkRoom("Z", 2, 2, 0, 3, "C", "c", "W U", chMask("W", "U")),
		mkRoom("Z", 2, 2, 1, 4, "D", "d", "D", chMask("D")),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"Z", 1, 1, 0}, func(r *IndexRoom) bool { return r.Hint == "D" })
	if !res.Reachable || len(res.Steps) != 3 {
		t.Fatalf("expected 3-step route to D: %+v", res)
	}
	got := []string{res.Steps[0].Dir, res.Steps[1].Dir, res.Steps[2].Dir}
	want := []string{"s", "e", "u"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d Dir = %q, want %q (canonical lowercase english)", i, got[i], want[i])
		}
	}
}

// TestFindPathSeamDirEmpty: a seam hop has no single letter, so Dir stays "".
func TestFindPathSeamDirEmpty(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("A", 0, 0, 0, 1, "Берег", "shore", "E", 4, withAutomaps("B|на восток|50")),
		mkRoom("B", 9, 9, 0, 50, "Море", "sea", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"A", 0, 0, 0}, func(r *IndexRoom) bool { return r.Zone == "B" })
	if !res.Reachable || len(res.Steps) != 1 {
		t.Fatalf("seam route wrong: %+v", res)
	}
	if res.Steps[0].Dir != "" {
		t.Errorf("seam step Dir = %q, want empty (no single letter for a seam)", res.Steps[0].Dir)
	}
}

func TestFindPathSeamCrossing(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("A", 0, 0, 0, 1, "Берег", "shore", "E", 4, withAutomaps("B|на восток|50")),
		mkRoom("B", 9, 9, 0, 50, "Море", "sea", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	res := idx.FindPath(Coord{"A", 0, 0, 0}, func(r *IndexRoom) bool {
		return r.Zone == "B"
	})
	if !res.Reachable || len(res.Steps) != 1 {
		t.Fatalf("seam route wrong: %+v", res)
	}
	if !res.Steps[0].Seam || res.Steps[0].Command != "на восток" || res.Steps[0].ToZone != "B" {
		t.Errorf("seam step wrong: %+v", res.Steps[0])
	}
}
