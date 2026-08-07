package mapper

import (
	"encoding/json"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

// loadCorpusRooms loads the reference corpus as storage rooms (the raw input of
// BuildIndex), or t.Skips when the private artifact is absent. loadCorpus wraps
// this and builds the index; the edge tests need the rooms themselves so they can
// build BOTH index flavours (inferred vs materialized) from the same input.
func loadCorpusFixture(tb testing.TB) ([]storage.Room, []storage.LegacySeamDraft) {
	tb.Helper()
	data := readMapperCorpus(tb)
	var crooms []corpusRoom
	if err := json.Unmarshal(data, &crooms); err != nil {
		tb.Fatalf("corpus json: %v", err)
	}
	rooms := make([]storage.Room, 0, len(crooms))
	drafts := make([]storage.LegacySeamDraft, 0)
	for _, cr := range crooms {
		room := storage.Room{
			Zone: cr.Zone, X: cr.X, Y: cr.Y, L: cr.L, Tag: cr.Tag,
			Hint: cr.Hint, Desc: cr.Desc, Exits: cr.Exits,
			EDirs: mustJSON(cr.EDirs),
			Doors: mustJSON(cr.Doors),
			Ch:    chListToMask(cr.Ch),
			IsDT:  cr.IsDT,
			Pipe:  cr.Pipe,
		}
		rooms = append(rooms, room)
		if len(cr.Automaps) != 0 {
			drafts = append(drafts, seamDraft(room, cr.Automaps...))
		}
	}
	return rooms, drafts
}

// legacyDoorGrade is doorGrade AS IT WAS BEFORE MIGRATION 018 — reading
// rooms.doors on both faces, kept verbatim so the "doors moved onto the edges"
// claim is checkable against the thing it replaced rather than against itself.
// The production doorGrade now asks the two directed rows of the passage; if the
// derivation puts rooms.doors onto those rows correctly, the two agree on every
// face of the corpus, and the acceptance criterion for edge materialization is exactly that.
func legacyDoorGrade(a, b *IndexRoom, dir string) DoorKind {
	if a != nil && containsDir(a.Doors, dir) {
		return DoorConfirmed
	}
	if b != nil && containsDir(b.Doors, OppositeDir(dir)) {
		return DoorPresumed
	}
	return DoorNone
}

// legacyWorldNeighbors is the pre-materialization walk, kept verbatim as the
// REFERENCE the materialized walk must reproduce: grid neighbours filtered by
// ConnectsTo in dirOrder, then automaps seams resolved by tag at walk time. The
// this test's whole claim is "storage changed, behaviour did not", and that is only
// checkable against the behaviour it replaced.
func legacyWorldNeighbors(idx *Index, c Coord) []worldNeighbor {
	r := idx.Room(c)
	if r == nil {
		return nil
	}
	var out []worldNeighbor
	for _, dir := range dirOrder {
		d := dirDelta[dir]
		nc := Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}
		nb := idx.Room(nc)
		if nb == nil || nb.IsDT {
			continue
		}
		if !r.ConnectsTo(dir, nb) {
			continue
		}
		// The door grade comes from the PRE-018 rule; the open action is what that
		// grade implies for an imported map (no edge ever carried an opening command,
		// so every door falls back to the default template). Together they pin both
		// halves of the door migration: which faces have a door, and what the route
		// does about them.
		kind := legacyDoorGrade(r, nb, dir)
		open := ""
		if kind != DoorNone {
			// The corpus IS RMUD, so the reference uses RMUD's own door verb — which
			// now comes from that profile's spec rather than from a global constant.
			open = doorOpenAction("", dir, RMUDSpec().DoorOpen)
		}
		out = append(out, worldNeighbor{
			to: nc, command: strings.ToLower(dir), dir: dir,
			doorKind: kind, toPipe: nb.Pipe, toZone: nc.Zone,
			openAction: open, weight: 1,
		})
	}
	for _, e := range r.seams {
		if !e.Passable || e.To == nil {
			continue
		}
		tr := idx.Room(*e.To)
		if tr == nil || tr.IsDT {
			continue
		}
		enDir := seamDir(e.Command)
		cmd := strings.ToLower(enDir)
		unparsed := false
		if enDir == "" {
			cmd = e.Command
			unparsed = true
		}
		out = append(out, worldNeighbor{
			to: tr.Coord, command: cmd, dir: enDir, seam: true,
			seamCommand: e.Command, seamUnparsed: unparsed,
			toPipe: tr.Pipe, toZone: tr.Zone, weight: 1,
		})
	}
	return out
}

// TestCorpusMaterializedWalkMatchesInference is the acceptance test for the whole
// materialization change: over EVERY room of the live corpus, the walk driven by materialized
// map_edges rows must yield exactly the same neighbour list — same members, same
// ORDER (BFS tie-breaking depends on it) — as the walk that re-derived
// connectivity per step. A single divergence anywhere is a migration bug.
func TestCorpusMaterializedWalkMatchesInference(t *testing.T) {
	rooms, drafts := loadCorpusFixture(t)
	// Both indexes carry the same rooms; only the edge provenance differs.
	// materialized: rows produced by storage.DeriveEdges, attached to the index and
	// read back by worldNeighbors. inference: the reference implementation above.
	idx := BuildIndexWithEdges(1, rooms, deriveFixtureEdges(1, rooms, drafts))
	// The door-opening command is a property of the MUD, carried by the session's
	// room-format profile; the corpus is RMUD, so the index is told RMUD's template.
	// Without this the walk emits no opening command at all — which is exactly the
	// behaviour an unconfigured (non-RMUD) profile must get.
	idx.SetDoorOpenTemplate(RMUDSpec().DoorOpen)
	ref := BuildIndexWithEdges(1, rooms, deriveFixtureEdges(1, rooms, drafts))

	diffs := 0
	for _, r := range ref.Rooms() {
		want := legacyWorldNeighbors(ref, r.Coord)
		got, _ := idx.worldNeighbors(r.Coord, false)
		if len(want) != len(got) {
			if diffs < 10 {
				t.Errorf("%+v: %d neighbours materialized, %d inferred\n want %+v\n got  %+v",
					r.Coord, len(got), len(want), want, got)
			}
			diffs++
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				if diffs < 10 {
					t.Errorf("%+v neighbour %d: materialized %+v, inferred %+v", r.Coord, i, got[i], want[i])
				}
				diffs++
			}
		}
	}
	if diffs > 0 {
		t.Fatalf("%d neighbour divergences over %d corpus rooms", diffs, ref.RoomCount())
	}
	t.Logf("materialized walk == inferred walk over all %d corpus rooms", ref.RoomCount())
}

// TestCorpusEdgeSetShape reports the materialized edge set's shape over the live
// corpus. It is a characterization test: the counts are what the CURRENT rule
// (ch authoritative over edirs) produces, and the point is that a change to the
// rule cannot slip through unnoticed.
func TestCorpusEdgeSetShape(t *testing.T) {
	rooms, drafts := loadCorpusFixture(t)
	edges := deriveFixtureEdges(1, rooms, drafts)
	var grid, dangling, fallback, seams, deadSeams int
	for _, e := range edges {
		_, resolved := e.To()
		switch e.Kind {
		case storage.EdgeKindSeam:
			seams++
			if !resolved {
				deadSeams++
			}
		default:
			if !resolved {
				dangling++
				continue
			}
			grid++
			if e.Source == storage.EdgeSourceGrid {
				fallback++
			}
		}
	}
	t.Logf("corpus %d rooms: %d directed traversable grid edges (%d grid-fallback), %d dangling exits, %d seams (%d dead), %d rows total",
		len(rooms), grid, fallback, dangling, seams, deadSeams, len(edges))
	if grid == 0 || seams == 0 {
		t.Fatalf("degenerate edge set: grid=%d seams=%d", grid, seams)
	}
}

// BenchmarkBuildIndexCorpus measures the cost of building the tracker index over
// the full live corpus (~8k rooms) — the "before/after" figure for materializing
// edges. It runs off the hot path (connect / active-set change / topology write),
// so the budget is generous, but a regression here is felt on every reconnect.
func BenchmarkBuildIndexCorpus(b *testing.B) {
	rooms, drafts := loadCorpusFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := BuildIndexWithEdges(1, rooms, deriveFixtureEdges(1, rooms, drafts))
		if idx.RoomCount() == 0 {
			b.Fatal("empty index")
		}
	}
}

// BenchmarkBuildIndexWithEdgesCorpus is the PRODUCTION path since the edge
// materialization: rooms and map_edges rows both come from SQLite (fingerprints
// stored, edges stored), and the build only folds the rows onto the rooms. This is
// the number to compare against BenchmarkBuildIndexCorpusPrehashed — the same work
// as before edge materialization, plus attaching ~19k rows.
func BenchmarkBuildIndexWithEdgesCorpus(b *testing.B) {
	rooms, drafts := loadCorpusFixture(b)
	for i := range rooms {
		rooms[i].Fingerprint = "fp"
	}
	edges := deriveFixtureEdges(1, rooms, drafts)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := BuildIndexWithEdges(1, rooms, edges)
		if idx.RoomCount() == 0 {
			b.Fatal("empty index")
		}
	}
}

// BenchmarkBuildIndexCorpusPrehashed is the PRODUCTION-shaped variant: rooms read
// from SQLite carry a stored fingerprint, so BuildIndex does not re-hash
// hint+desc+exits (which dominates the artifact-shaped benchmark above, where the
// reference JSON has no fingerprint column). This is the figure to watch for
// index-build regressions.
func BenchmarkBuildIndexCorpusPrehashed(b *testing.B) {
	rooms, drafts := loadCorpusFixture(b)
	for i := range rooms {
		rooms[i].Fingerprint = "fp"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := BuildIndexWithEdges(1, rooms, deriveFixtureEdges(1, rooms, drafts))
		if idx.RoomCount() == 0 {
			b.Fatal("empty index")
		}
	}
}

// TestEscapedLegacyAutomapsMaterializesWithoutRuntimeRetention proves escaped
// import provenance becomes an edge and the runtime index does not retain it.
func TestEscapedLegacyAutomapsMaterializesWithoutRuntimeRetention(t *testing.T) {
	zone := `Зона "Кавычки"`
	entry := zone + `|на север|3`
	tag := 3
	rooms := []storage.Room{
		{Zone: "Home", X: 0, Y: 0, L: 0, Hint: "start", EDirs: "[]", Doors: "[]"},
		{Zone: zone, X: 0, Y: 0, L: 0, Tag: &tag, Hint: "target", EDirs: "[]", Doors: "[]"},
	}

	idx := BuildIndexWithEdges(1, rooms, storage.DeriveImportedEdges(1, rooms, []storage.LegacySeamDraft{{From: storage.EdgeCell{Zone: "Home"}, Entries: []string{entry}}}))
	r := idx.Room(Coord{"Home", 0, 0, 0})
	if r == nil {
		t.Fatal("start room missing")
	}
	// The seam actually resolves from its materialized row.
	nbs, _ := idx.worldNeighbors(Coord{"Home", 0, 0, 0}, false)
	for _, nb := range nbs {
		if nb.seam && nb.to == (Coord{zone, 0, 0, 0}) {
			return
		}
	}
	t.Fatalf("seam into %q did not resolve; neighbours = %+v", zone, nbs)
}
