package mapper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rubymud/go/internal/storage"
)

// corpusRoom mirrors the reference rmud_index.json schema (ch/edirs/doors/
// automaps are string lists there; ch is converted to our bitmask on load).
type corpusRoom struct {
	Zone     string   `json:"zone"`
	Tag      *int     `json:"tag"`
	Hint     string   `json:"hint"`
	Desc     string   `json:"desc"`
	Exits    string   `json:"exits"`
	EDirs    []string `json:"edirs"`
	Doors    []string `json:"doors"`
	Ch       []string `json:"ch"`
	X        int      `json:"x"`
	Y        int      `json:"y"`
	L        int      `json:"l"`
	IsDT     bool     `json:"is_dt"`
	Pipe     bool     `json:"pipe"`
	Automaps []string `json:"automaps"`
}

// loadCorpus loads the reference corpus (offline golden) as an *Index, or
// t.Skips when the private artifact is absent (it is gitignored / not in CI).
func loadCorpus(t *testing.T) *Index {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "..", "tmp", "mapper-artifacts", "rmud_index.json"),
		filepath.Join("..", "..", "..", "..", "tmp", "mapper-artifacts", "rmud_index.json"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		t.Skip("reference corpus rmud_index.json not present (private artifact) — skipping corpus goldens")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read corpus: %v", err)
	}
	var crooms []corpusRoom
	if err := json.Unmarshal(data, &crooms); err != nil {
		t.Fatalf("corpus json: %v", err)
	}
	rooms := make([]storage.Room, 0, len(crooms))
	for _, cr := range crooms {
		rooms = append(rooms, storage.Room{
			Zone: cr.Zone, X: cr.X, Y: cr.Y, L: cr.L, Tag: cr.Tag,
			Hint: cr.Hint, Desc: cr.Desc, Exits: cr.Exits,
			EDirs:    mustJSON(cr.EDirs),
			Doors:    mustJSON(cr.Doors),
			Ch:       chListToMask(cr.Ch),
			IsDT:     cr.IsDT,
			Pipe:     cr.Pipe,
			Automaps: mustJSON(cr.Automaps),
		})
	}
	return BuildIndex(1, rooms)
}

func mustJSON(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func chListToMask(list []string) int {
	mask := 0
	for _, d := range list {
		if bit, ok := chBitOrder[d]; ok {
			mask |= 1 << bit
		}
	}
	return mask
}

// findRoomByHint returns the first room in a zone whose hint matches exactly.
func findRoomByHint(idx *Index, zone, hint string) *IndexRoom {
	for _, r := range idx.Rooms() {
		if r.Zone == zone && r.Hint == hint {
			return r
		}
	}
	return nil
}

// TestCorpusRouteBankEntryDirection is the golden case: a route into Городской
// банк (Хилло, ch=S-only) must ENTER from the south, i.e. the last emitted
// command is с (N) from the bank's south neighbor — NOT в (E), which the old
// visual-delta derivation wrongly emitted via a blank-Exits cell.
func TestCorpusRouteBankEntryDirection(t *testing.T) {
	idx := loadCorpus(t)
	start := findRoomByHint(idx, "Хилло", "Главная улица")
	bank := findRoomByHint(idx, "Хилло", "Городской банк")
	if start == nil || bank == nil {
		t.Fatalf("corpus missing Главная улица or Городской банк")
	}
	res := idx.FindPath(start.Coord, func(r *IndexRoom) bool {
		return r.Zone == "Хилло" && r.Hint == "Городской банк"
	})
	if !res.Reachable {
		t.Fatalf("bank unreachable from Главная улица: %+v", res)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Command != DirRU("N") {
		t.Errorf("bank entry command = %q, want %q (с/N — the bank's only real exit is S, so enter from its south neighbor going N)",
			last.Command, DirRU("N"))
	}
	assertRouteInvariant(t, idx, start.Coord, res)
	assertRouteReachesGoal(t, idx, start.Coord, res, bank.Coord)
}

// TestCorpusRouteBankMultiHopReachesInside is the multi-hop dead-end golden: a
// far-start to_hint-style route to Городской банк (single-exit S) must NOT stop
// on the bank's neighbor. It must end "…в; с" (the axis-changing final turn) and
// terminate AT the bank cell (-5,-8,0). This covers the dropped-final-turn bug
// that the 1-hop entry golden above cannot catch.
func TestCorpusRouteBankMultiHopReachesInside(t *testing.T) {
	idx := loadCorpus(t)
	start := findRoomByHint(idx, "Хилло", "Портовые ворота") // a far Хилло start
	bank := findRoomByHint(idx, "Хилло", "Городской банк")
	if start == nil || bank == nil {
		t.Fatalf("corpus missing Портовые ворота or Городской банк")
	}
	res := idx.FindPath(start.Coord, func(r *IndexRoom) bool {
		return r.Zone == "Хилло" && r.Hint == "Городской банк"
	})
	if !res.Reachable {
		t.Fatalf("bank unreachable from Портовые ворота: %+v", res)
	}
	// Must terminate INSIDE the bank, not on its neighbor.
	assertRouteReachesGoal(t, idx, start.Coord, res, bank.Coord)
	assertRouteInvariant(t, idx, start.Coord, res)
	if last := res.Steps[len(res.Steps)-1]; last.Command != DirRU("N") {
		t.Errorf("final hop into bank = %q, want %q (с)", last.Command, DirRU("N"))
	}
	if bank.Coord != (Coord{"Хилло", -5, -8, 0}) {
		t.Fatalf("corpus bank coord unexpected: %+v", bank.Coord)
	}
}

// TestCorpusRouteYuzhnayaMultiHopReachesInside is the second dead-end golden:
// route to "Южная часть зала" (single-exit N) from a far start must end inside
// it at (2,-5,0) and include the final turn, not stop on the neighbor.
func TestCorpusRouteYuzhnayaMultiHopReachesInside(t *testing.T) {
	idx := loadCorpus(t)
	start := findRoomByHint(idx, "Хилло", "Портовые ворота")
	yuzh := findRoomByHint(idx, "Хилло", "Южная часть зала")
	if start == nil || yuzh == nil {
		t.Fatalf("corpus missing Портовые ворота or Южная часть зала")
	}
	res := idx.FindPath(start.Coord, func(r *IndexRoom) bool {
		return r.Zone == "Хилло" && r.Hint == "Южная часть зала"
	})
	if !res.Reachable {
		t.Fatalf("Южная часть зала unreachable: %+v", res)
	}
	assertRouteReachesGoal(t, idx, start.Coord, res, yuzh.Coord)
	assertRouteInvariant(t, idx, start.Coord, res)
	if last := res.Steps[len(res.Steps)-1]; last.Command != DirRU("S") {
		t.Errorf("final hop into Южная = %q, want %q (ю — its only exit is N, enter from north going S)", last.Command, DirRU("S"))
	}
	if yuzh.Coord != (Coord{"Хилло", 2, -5, 0}) {
		t.Fatalf("corpus Южная coord unexpected: %+v", yuzh.Coord)
	}
}

// TestCorpusRouteGostinitsaEntryDirection asserts the Гостиница-Хилло entry
// (real entrance is в/E, per live ground truth) is emitted correctly when
// approached from Перед гостиницей (its west neighbor).
func TestCorpusRouteGostinitsaEntryDirection(t *testing.T) {
	idx := loadCorpus(t)
	start := findRoomByHint(idx, "Хилло", "Перед гостиницей")
	if start == nil {
		t.Fatalf("corpus missing Перед гостиницей")
	}
	res := idx.FindPath(start.Coord, func(r *IndexRoom) bool {
		return r.Zone == "Хилло" && r.Hint == "Гостиница Хилло"
	})
	if !res.Reachable {
		t.Fatalf("Гостиница unreachable: %+v", res)
	}
	gost := findRoomByHint(idx, "Хилло", "Гостиница Хилло")
	last := res.Steps[len(res.Steps)-1]
	if last.Command != DirRU("E") {
		t.Errorf("Гостиница entry command = %q, want %q (в/E)", last.Command, DirRU("E"))
	}
	assertRouteInvariant(t, idx, start.Coord, res)
	if gost != nil {
		assertRouteReachesGoal(t, idx, start.Coord, res, gost.Coord)
	}
}

// assertRouteInvariant is the strong, server-independent correctness check:
// EVERY emitted step's direction must be a real exit of the room it departs from
// (present in that room's authoritative ch, or edirs when ch is empty). A step
// whose direction isn't a real exit of its source room is the wrong-direction
// bug. Seam steps (RU command words, not single letters) are exempt — they carry
// the MUD's own seam command. Pipe-collapsed steps are checked at their entry
// direction (the run's departing direction).
func assertRouteInvariant(t *testing.T, idx *Index, start Coord, res PathResult) {
	t.Helper()
	cur := start
	for i, st := range res.Steps {
		if st.Seam {
			// A seam hops zones by the stored command; the next cur is its target.
			cur = seamNextCoord(idx, cur, st)
			continue
		}
		dir := ruToDir(st.Command)
		if dir == "" {
			t.Fatalf("step %d command %q is not a canonical grid direction", i+1, st.Command)
		}
		room := idx.Room(cur)
		if room == nil {
			t.Fatalf("step %d departs from an unknown cell %+v", i+1, cur)
		}
		// The emitted direction must be a REAL edge from the departing cell to its
		// neighbor, by authoritative bidirectional connectivity (ConnectsTo): the
		// departing room advertises the exit, OR the neighbor advertises the reverse
		// back-link. A direction confirmed by neither endpoint is the
		// wrong-direction bug (a fabricated visual-adjacency edge).
		d := dirDelta[dir]
		nb := idx.Room(Coord{Zone: cur.Zone, X: cur.X + d.DX, Y: cur.Y + d.DY, L: cur.L + d.DL})
		if nb == nil {
			t.Errorf("INVARIANT VIOLATION step %d: emit %q (%s) from %q lands on no cell",
				i+1, st.Command, dir, room.Hint)
		} else if !room.ConnectsTo(dir, nb) {
			t.Errorf("INVARIANT VIOLATION step %d: emit %q (%s) from %q -> %q is not a real edge (A ch=%d edirs=%v; B ch=%d edirs=%v)",
				i+1, st.Command, dir, room.Hint, nb.Hint, room.Ch, room.EDirs, nb.Ch, nb.EDirs)
		}
		// advance cur by the run's total cells in this direction
		n := st.Cells
		if n < 1 {
			n = 1
		}
		cur = Coord{Zone: cur.Zone, X: cur.X + d.DX*n, Y: cur.Y + d.DY*n, L: cur.L + d.DL*n}
	}
}

// assertRouteReachesGoal asserts the route's final landed cell equals the target
// coord (not a neighbor). A truncated route is still all-valid-exits, so this is
// a SEPARATE assertion from the direction invariant — it catches the dropped
// final-turn-into-a-dead-end bug.
func assertRouteReachesGoal(t *testing.T, idx *Index, start Coord, res PathResult, goal Coord) {
	t.Helper()
	cur := start
	for _, st := range res.Steps {
		if st.Seam {
			cur = seamNextCoord(idx, cur, st)
			continue
		}
		d := dirDelta[ruToDir(st.Command)]
		n := st.Cells
		if n < 1 {
			n = 1
		}
		cur = Coord{Zone: cur.Zone, X: cur.X + d.DX*n, Y: cur.Y + d.DY*n, L: cur.L + d.DL*n}
	}
	if cur != goal {
		t.Errorf("route did not REACH goal: ended at %+v, want %+v (dropped final hop?)", cur, goal)
	}
}

// seamNextCoord resolves the coord a seam step lands on (best effort by matching
// the step's target zone + hint).
func seamNextCoord(idx *Index, from Coord, st PathStep) Coord {
	for _, r := range idx.Rooms() {
		if r.Zone == st.ToZone && r.Hint == st.Hint {
			return r.Coord
		}
	}
	return from
}

// ruToDir inverts DirRU (RU command word -> canonical letter).
func ruToDir(cmd string) string {
	for _, d := range dirOrder {
		if DirRU(d) == cmd {
			return d
		}
	}
	return ""
}

// TestCorpusRouteInvariantSample runs the invariant over a sample of routes
// across the whole corpus (many zones): for each, every emitted grid step must
// be a real exit of its departing room. This catches the entire wrong-direction
// bug class server-independently.
func TestCorpusRouteInvariantSample(t *testing.T) {
	idx := loadCorpus(t)
	rooms := idx.Rooms()
	if len(rooms) == 0 {
		t.Skip("empty corpus")
	}
	// Deterministic sample: stride through the corpus, routing between pairs.
	checked := 0
	stride := len(rooms) / 200
	if stride < 1 {
		stride = 1
	}
	for i := 0; i < len(rooms); i += stride {
		start := rooms[i]
		if start.IsDT {
			continue
		}
		// target a room a bit further in the same zone (a concrete, reachable-ish
		// goal) — pick the next room in the same zone by index.
		var target *IndexRoom
		for j := i + 1; j < len(rooms); j++ {
			if rooms[j].Zone == start.Zone && !rooms[j].IsDT {
				target = rooms[j]
				break
			}
		}
		if target == nil {
			continue
		}
		goalCoord := target.Coord
		res := idx.FindPath(start.Coord, func(r *IndexRoom) bool {
			return r.Coord == goalCoord
		})
		if !res.Reachable {
			continue // disconnected pair — not a correctness failure
		}
		assertRouteInvariant(t, idx, start.Coord, res)
		// The route must actually REACH the goal cell, not stop at a neighbor.
		assertRouteReachesGoal(t, idx, start.Coord, res, goalCoord)
		checked++
	}
	if checked == 0 {
		t.Skip("no reachable route pairs sampled")
	}
	t.Logf("route invariant held over %d sampled corpus routes", checked)
}
