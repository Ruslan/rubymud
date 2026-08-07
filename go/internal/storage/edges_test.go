package storage

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// permissivePairInput is the minimal corpus for the grid-fallback risk: two
// adjacent rooms that BOTH carry no exit data at all (ch=0, edirs empty). Nothing
// in either room says "there is a way north"; the edge exists only because the
// pair is adjacent and neither end denies it.
func permissivePairInput() MapSetInput {
	return MapSetInput{
		Name:      "Fallback World",
		RoomCount: 2,
		Rooms: []Room{
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Hint: "South cell", EDirs: "[]", Doors: "[]"},
			{Zone: "Alpha", X: 1, Y: 0, L: 0, Hint: "North cell", EDirs: "[]", Doors: "[]"},
		},
	}
}

func TestDeriveImportedEdgesResolvesNFDTargetZone(t *testing.T) {
	nfc := "Балифой"
	nfd := norm.NFD.String(nfc)
	if nfd == nfc {
		t.Fatal("test zone must have distinct NFC and NFD forms")
	}
	tag := 7
	rooms := []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: nfc, X: 3, Y: 4, L: 0, Tag: &tag, EDirs: "[]", Doors: "[]"},
	}
	edges := DeriveImportedEdges(1, rooms, []LegacySeamDraft{{
		From: EdgeCell{Zone: "A"}, Entries: []string{nfd + "|go|7"},
	}})
	for _, edge := range edges {
		if edge.Kind != EdgeKindSeam {
			continue
		}
		to, ok := edge.To()
		if !ok || to != (EdgeCell{Zone: nfc, X: 3, Y: 4, L: 0}) {
			t.Fatalf("NFD seam target = %+v, resolved=%v; want NFC room", to, ok)
		}
		if edge.TargetZone != nfc {
			t.Fatalf("target zone = %q, want canonical NFC %q", edge.TargetZone, nfc)
		}
		return
	}
	t.Fatal("derived seam missing")
}

// edgeSummary renders a set's STORED edges as sorted, readable strings for stable
// comparison in assertions.
func edgeSummary(t *testing.T, store *Store, mapSetID int64) []string {
	t.Helper()
	edges, err := store.ListMapEdges(mapSetID)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	return summarizeEdges(edges)
}

func coordSuffix(c EdgeCell) string {
	return "(" + strconv.Itoa(c.X) + "," + strconv.Itoa(c.Y) + "," + strconv.Itoa(c.L) + ")"
}

// TestImportMaterializesEdges: importing a set writes its topology, including the
// dangling exit (Alpha Start advertises N with nothing at (-1,0,0)) and the seam
// row with BOTH the symbolic target and the resolved cell.
func TestImportMaterializesEdges(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var grid, dangling, seam int
	for _, e := range edges {
		_, resolved := e.To()
		switch {
		case e.Kind == EdgeKindSeam:
			seam++
			if e.Command != "go" || e.TargetZone != "Beta" || e.TargetTag == nil || *e.TargetTag != 5 {
				t.Errorf("seam lost its symbolic target: %+v", e)
			}
			c, ok := e.To()
			if !ok || c != (EdgeCell{"Beta", 0, 0, 1}) {
				t.Errorf("seam resolved target = %v (ok=%v), want Beta(0,0,1)", c, ok)
			}
		case !resolved:
			dangling++
		default:
			grid++
		}
	}
	// Alpha Start (0,0,0) advertises N and S; (-1,0,0) and (1,0,0) hold no rooms,
	// so both are dangling. Alpha North sits at (-1,2,0) — not adjacent.
	if dangling != 3 || seam != 1 || grid != 0 {
		t.Fatalf("edges = %d grid / %d dangling / %d seam, want 0/3/1\n%v",
			grid, dangling, seam, edgeSummary(t, store, id))
	}
}

func TestImportEdgeFailureRollsBackSetAndRooms(t *testing.T) {
	store := newMapperTestStore(t)
	if err := store.db.Exec(`CREATE TRIGGER fail_imported_seam
		BEFORE INSERT ON map_edges WHEN NEW.kind = 'seam'
		BEGIN SELECT RAISE(ABORT, 'forced seam insert failure'); END`).Error; err != nil {
		t.Fatalf("create fault trigger: %v", err)
	}
	if _, err := store.CreateMapSet(sampleInput()); err == nil {
		t.Fatal("CreateMapSet succeeded despite forced edge failure")
	}
	for _, table := range []string{"map_sets", "rooms", "map_edges"} {
		var count int64
		if err := store.db.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d row(s) after edge failure", table, count)
		}
	}
}

// TestGridFallbackIsRecomputedAroundAWrite is the repro for the neighbourhood
// risk: the permissive fallback edge is a property of the PAIR, not of either
// room. Writing exits into ONE end makes it explicit, and the edge must vanish
// unless the new exits name that direction — a materializer that only re-derived
// the written room's own exits would leave a ghost edge behind, one the walk would
// never have inferred.
func TestGridFallbackIsRecomputedAroundAWrite(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(permissivePairInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	// Both ends permissive: adjacency alone connects them, sourced as the fallback.
	// Edges are DIRECTED, so the two-way passage is two rows — one per side.
	if got := edgeSummary(t, store, id); !same(got, []string{
		"grid:grid Alpha(0,0,0) S -> Alpha(1,0,0)",
		"grid:grid Alpha(1,0,0) N -> Alpha(0,0,0)",
	}) {
		t.Fatalf("permissive pair edges = %v, want both directions of the fallback", got)
	}

	// Write the SOUTH cell with explicit exits that do NOT include S (the direction
	// toward its neighbour). The pair is no longer "both permissive", and the south
	// room denies the face, so the edge must be gone.
	var f RoomFields
	f.SetEDirs([]string{"E"})
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0, Fields: f,
	}); err != nil || !found {
		t.Fatalf("upsert: err=%v found=%v", err, found)
	}

	// BOTH directed rows must go: the neighbour's row back into the written cell is
	// derived from the same pair and is just as dead.
	got := edgeSummary(t, store, id)
	for _, e := range got {
		if e == "grid:grid Alpha(0,0,0) S -> Alpha(1,0,0)" || e == "grid:grid Alpha(1,0,0) N -> Alpha(0,0,0)" {
			t.Fatalf("ghost fallback edge survived the write: %v", got)
		}
	}
	// What SHOULD be there: the written room's own dangling E exit.
	if len(got) != 1 || got[0] != "grid:parsed Alpha(0,0,0) E -> -" {
		t.Fatalf("edges after write = %v, want only the dangling E exit", got)
	}
}

// TestUndoRestoresEdges: undoing a room write restores the topology, not just the
// room text. The write above deletes a fallback edge; the undo must bring it back.
func TestUndoRestoresEdges(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(permissivePairInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	beforeEdges := edgeSummary(t, store, id)

	var f RoomFields
	f.SetEDirs([]string{"E"})
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0, Fields: f,
	})
	if err != nil || !found {
		t.Fatalf("upsert: err=%v found=%v", err, found)
	}
	if same(beforeEdges, edgeSummary(t, store, id)) {
		t.Fatal("write did not change the topology — the test proves nothing")
	}

	applied, err := store.RestoreTopologyBefore(id, before)
	if err != nil || !applied {
		t.Fatalf("RestoreTopologyBefore: err=%v applied=%v", err, applied)
	}
	if got := edgeSummary(t, store, id); !same(beforeEdges, got) {
		t.Fatalf("undo restored the room but not the topology:\n before %v\n after  %v", beforeEdges, got)
	}
}

// TestUndoOfDeleteRestoresEdges: deleting a room drops its edges and can leave a
// neighbour's exit dangling; undo must restore the whole neighbourhood.
func TestUndoOfDeleteRestoresEdges(t *testing.T) {
	store := newMapperTestStore(t)
	in := permissivePairInput()
	in.Rooms[0].EDirs = `["S"]`
	in.Rooms[0].Ch = 2
	in.Rooms[1].EDirs = `["N"]`
	in.Rooms[1].Ch = 1
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	beforeEdges := edgeSummary(t, store, id)
	if len(beforeEdges) != 2 {
		t.Fatalf("setup edges = %v, want both directions of one passage", beforeEdges)
	}

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: "Alpha", X: 1, Y: 0, L: 0,
	})
	if err != nil || !found {
		t.Fatalf("delete: err=%v found=%v", err, found)
	}
	// The survivor's S exit now points at an unmapped cell — recorded, not dropped.
	if got := edgeSummary(t, store, id); len(got) != 1 || got[0] != "grid:parsed Alpha(0,0,0) S -> -" {
		t.Fatalf("edges after delete = %v, want the survivor's dangling S exit", got)
	}

	applied, err := store.RestoreTopologyBefore(id, before)
	if err != nil || !applied {
		t.Fatalf("RestoreTopologyBefore: err=%v applied=%v", err, applied)
	}
	if got := edgeSummary(t, store, id); !same(beforeEdges, got) {
		t.Fatalf("undo of delete did not restore topology:\n before %v\n after  %v", beforeEdges, got)
	}
}

// TestForkCopiesEdges: fork-on-write copies the topology alongside the rooms, and
// leaves the frozen source untouched.
func TestForkCopiesEdges(t *testing.T) {
	store := newMapperTestStore(t)
	srcID, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	srcEdges := edgeSummary(t, store, srcID)
	if len(srcEdges) == 0 {
		t.Fatal("source set has no edges — the test proves nothing")
	}

	forkID, err := store.ForkMapSet(srcID)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	if got := edgeSummary(t, store, forkID); !same(srcEdges, got) {
		t.Fatalf("fork topology differs:\n source %v\n fork   %v", srcEdges, got)
	}

	// Writing the fork must not touch the frozen source's edges.
	var f RoomFields
	f.SetEDirs([]string{"E"})
	if _, _, err := store.ApplyTopologyOp(forkID, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0, Fields: f,
	}); err != nil {
		t.Fatalf("upsert on fork: %v", err)
	}
	if got := edgeSummary(t, store, srcID); !same(srcEdges, got) {
		t.Fatalf("writing the fork changed the frozen source:\n before %v\n after  %v", srcEdges, got)
	}
}

// TestDeletingATaggedRoomReresolvesSeams: a seam's target is symbolic ("the first
// room of zone Z with tag T"), so removing that room must re-point the seam — to
// the next candidate, or to nothing (a dead seam, recorded rather than discovered
// at route time).
func TestDeletingATaggedRoomReresolvesSeams(t *testing.T) {
	store := newMapperTestStore(t)
	in := sampleInput()
	// A second Beta room with the same tag, ordered AFTER the current target.
	in.Rooms = append(in.Rooms, Room{Zone: "Beta", X: 5, Y: 5, L: 1, Tag: ptrInt(5),
		Hint: "Beta Spare", EDirs: "[]", Doors: "[]"})
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if got := seamTargetOf(t, store, id); got != (EdgeCell{"Beta", 0, 0, 1}) {
		t.Fatalf("seam target = %v, want Beta(0,0,1)", got)
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: "Beta", X: 0, Y: 0, L: 1,
	}); err != nil || !found {
		t.Fatalf("delete tagged room: err=%v found=%v", err, found)
	}
	if got := seamTargetOf(t, store, id); got != (EdgeCell{"Beta", 5, 5, 1}) {
		t.Fatalf("seam target after delete = %v, want the next tag-5 room Beta(5,5,1)", got)
	}

	// Removing the last tag-5 room leaves a DEAD seam that keeps its symbolic target.
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: "Beta", X: 5, Y: 5, L: 1,
	}); err != nil {
		t.Fatalf("delete second tagged room: %v", err)
	}
	edges, _ := store.ListMapEdges(id)
	for _, e := range edges {
		if e.Kind != EdgeKindSeam {
			continue
		}
		if _, ok := e.To(); ok {
			t.Fatalf("seam still resolves after its targets were deleted: %+v", e)
		}
		if e.TargetZone != "Beta" || e.TargetTag == nil || *e.TargetTag != 5 {
			t.Fatalf("dead seam lost its symbolic target: %+v", e)
		}
		return
	}
	t.Fatal("seam row disappeared")
}

func TestNFDSeamStaysResolvedAfterTaggedRoomMutation(t *testing.T) {
	store := newMapperTestStore(t)
	nfc := "Балифой"
	nfd := norm.NFD.String(nfc)
	if nfd == nfc {
		t.Fatal("test zone must have distinct NFC and NFD forms")
	}
	tag := 5
	in := MapSetInput{
		Name: "NFD lifecycle",
		Rooms: []Room{
			{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
			{Zone: nfc, X: 1, Y: 0, L: 0, Tag: &tag, EDirs: "[]", Doors: "[]"},
			{Zone: nfc, X: 2, Y: 0, L: 0, Tag: &tag, EDirs: "[]", Doors: "[]"},
		},
		LegacySeams: []LegacySeamDraft{{From: EdgeCell{Zone: "A"}, Entries: []string{nfd + "|go|5"}}},
	}
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if got := seamTargetOf(t, store, id); got != (EdgeCell{Zone: nfc, X: 1, Y: 0, L: 0}) {
		t.Fatalf("initial NFD seam target = %+v", got)
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoDeleteRoom, Zone: nfc, X: 1, Y: 0, L: 0,
	}); err != nil || !found {
		t.Fatalf("delete tagged room: err=%v found=%v", err, found)
	}
	if got := seamTargetOf(t, store, id); got != (EdgeCell{Zone: nfc, X: 2, Y: 0, L: 0}) {
		t.Fatalf("NFD seam target after reresolve = %+v, want second NFC room", got)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges {
		if edge.Kind == EdgeKindSeam && edge.TargetZone != nfc {
			t.Fatalf("reresolve target zone = %q, want canonical %q", edge.TargetZone, nfc)
		}
	}
}

func seamTargetOf(t *testing.T, store *Store, id int64) EdgeCell {
	t.Helper()
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	for _, e := range edges {
		if e.Kind == EdgeKindSeam {
			c, _ := e.To()
			return c
		}
	}
	t.Fatal("no seam edge")
	return EdgeCell{}
}

// TestLinkUnlinkKeepsEdgesConsistent: the CRUD link/unlink ops move the edge with
// the exits they patch, on both sides of the pair.
func TestLinkUnlinkKeepsEdgesConsistent(t *testing.T) {
	store := newMapperTestStore(t)
	in := permissivePairInput()
	in.Rooms[0].EDirs = `["E"]`
	in.Rooms[0].Ch = 4
	in.Rooms[1].EDirs = `["E"]`
	in.Rooms[1].Ch = 4
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Neither room advertises the face between them, so there is no edge.
	for _, e := range edgeSummary(t, store, id) {
		if e == "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)" {
			t.Fatalf("unexpected edge before link: %v", e)
		}
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoLink, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("link: err=%v found=%v", err, found)
	}
	if !contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatalf("link did not materialize the edge: %v", edgeSummary(t, store, id))
	}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUnlink, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("unlink: err=%v found=%v", err, found)
	}
	if contains(edgeSummary(t, store, id), "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)") {
		t.Fatalf("unlink left the edge behind: %v", edgeSummary(t, store, id))
	}
}

// TestIncrementalRecomputeEqualsFullDerivation is the guard on the write-path's
// narrow re-derivation: after each op the stored edges must be exactly what
// DeriveEdges would produce from the resulting rooms. That is the property the
// per-cell recompute exists to preserve while touching only a neighbourhood, and
// it is where a wrong affected-key set (the ghost-edge failure mode) shows up.
func TestIncrementalRecomputeEqualsFullDerivation(t *testing.T) {
	store := newMapperTestStore(t)
	in := sampleInput()
	// A connected 3x1 strip plus a permissive pair, so ops have real neighbours.
	in.Rooms = append(in.Rooms,
		Room{Zone: "Gamma", X: 0, Y: 0, L: 0, Hint: "g0", EDirs: `["S"]`, Ch: 2, Doors: "[]"},
		Room{Zone: "Gamma", X: 1, Y: 0, L: 0, Hint: "g1", EDirs: `["N","S"]`, Ch: 3, Doors: "[]"},
		Room{Zone: "Gamma", X: 2, Y: 0, L: 0, Hint: "g2", EDirs: `["N"]`, Ch: 1, Doors: "[]"},
		Room{Zone: "Gamma", X: 5, Y: 0, L: 0, Hint: "p0", EDirs: "[]", Doors: "[]"},
		Room{Zone: "Gamma", X: 6, Y: 0, L: 0, Hint: "p1", EDirs: "[]", Doors: "[]"},
	)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	var clearExits RoomFields
	clearExits.SetEDirs(nil)
	var newExits RoomFields
	newExits.SetEDirs([]string{"E", "W"})

	ops := []TopologyOp{
		{Kind: TopoUpsertRoom, Zone: "Gamma", X: 5, Y: 0, L: 0, Fields: newExits},   // kills a fallback pair
		{Kind: TopoUpsertRoom, Zone: "Gamma", X: 1, Y: 0, L: 0, Fields: clearExits}, // makes a middle cell permissive
		{Kind: TopoLink, Zone: "Gamma", X: 0, Y: 0, L: 0, Dir: "E"},                 // one-sided link into empty space
		{Kind: TopoUnlink, Zone: "Gamma", X: 2, Y: 0, L: 0, Dir: "N"},
		{Kind: TopoPatchExits, Zone: "Gamma", X: 2, Y: 0, L: 0, AddExits: []string{"S", "U"}},
		{Kind: TopoUpsertRoom, Zone: "Gamma", X: 3, Y: 0, L: 0, Fields: newExits}, // create next to g2
		{Kind: TopoDeleteRoom, Zone: "Gamma", X: 1, Y: 0, L: 0},
		{Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 0, L: 0}, // the seam's source room
	}
	for i, op := range ops {
		if _, _, err := store.ApplyTopologyOp(id, op); err != nil {
			t.Fatalf("op %d (%s): %v", i, op.Kind, err)
		}
		rooms, err := store.ListRooms(id)
		if err != nil {
			t.Fatalf("ListRooms: %v", err)
		}
		want := summarizeEdges(DeriveEdges(id, rooms))
		got := edgeSummary(t, store, id)
		got = withoutSeams(got)
		if !same(want, got) {
			t.Fatalf("after op %d (%s) the incremental recompute diverged from a full derivation:\n want %v\n got  %v",
				i, op.Kind, want, got)
		}
	}
}

func withoutSeams(edges []string) []string {
	out := make([]string, 0, len(edges))
	for _, edge := range edges {
		if !strings.HasPrefix(edge, "seam:") {
			out = append(out, edge)
		}
	}
	return out
}

// summarizeEdges renders a list of edges as sorted "kind:source from dir -> to"
// strings, so stored rows and freshly derived ones compare directly.
func summarizeEdges(edges []MapEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		to := "-"
		if c, ok := e.To(); ok {
			to = c.Zone + coordSuffix(c)
		}
		blocked := ""
		if !e.Passable {
			blocked = " [blocked]"
		}
		out = append(out, e.Kind+":"+e.Source+" "+e.FromZone+coordSuffix(e.From())+" "+e.Dir+" -> "+to+blocked)
	}
	sort.Strings(out)
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- review fixes -----------------------------------------------------------

// manualEdge inserts a hand-drawn PASSABLE grid row directly. It predates the
// write API (TopoAddManualEdge) and is kept because the journal repro tests must
// set up hand-drawn rows WITHOUT going through the write they are testing.
func manualEdge(t *testing.T, store *Store, mapSetID int64, from EdgeCell, dir string, to *EdgeCell) {
	t.Helper()
	e := MapEdge{
		MapSetID: mapSetID, Kind: EdgeKindGrid, Source: EdgeSourceManual, Passable: true,
		FromZone: from.Zone, FromX: from.X, FromY: from.Y, FromL: from.L, Dir: dir,
		EdgeAttrs: EdgeAttrs{Weight: 1},
	}
	e.SetTo(to)
	if err := store.DB().Create(&e).Error; err != nil {
		t.Fatalf("insert manual edge: %v", err)
	}
}

// connectedPairInput is two rooms that each advertise the face between them, so
// the derivation yields the passage in both directions and nothing else.
func connectedPairInput() MapSetInput {
	return MapSetInput{
		Name: "Manual World", RoomCount: 2,
		Rooms: []Room{
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Hint: "a", EDirs: `["S"]`, Ch: 2, Doors: "[]"},
			{Zone: "Alpha", X: 1, Y: 0, L: 0, Hint: "b", EDirs: `["N"]`, Ch: 1, Doors: "[]"},
		},
	}
}

// TestManualEdgeOnFreeFaceSurvivesNeighbourWrite (review item 2a): a hand-drawn
// edge on a face the derivation does not produce must survive a write to the cell
// it leaves, and must not gain a derived twin.
func TestManualEdgeOnFreeFaceSurvivesNeighbourWrite(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(connectedPairInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// A hidden way east out of (0,0,0): nothing in the room's exits says so.
	manualEdge(t, store, id, EdgeCell{"Alpha", 0, 0, 0}, "E", nil)

	var f RoomFields
	f.SetEDirs([]string{"S", "E"}) // the write now DOES claim an east exit
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0, Fields: f,
	}); err != nil || !found {
		t.Fatalf("upsert next to a manual edge: err=%v found=%v", err, found)
	}

	got := edgeSummary(t, store, id)
	n := 0
	for _, e := range got {
		if e == "grid:manual Alpha(0,0,0) E -> -" {
			n++
		}
		if e == "grid:parsed Alpha(0,0,0) E -> -" {
			t.Errorf("derivation produced a twin of the manual edge: %v", got)
		}
	}
	if n != 1 {
		t.Fatalf("manual edge count on E = %d, want 1: %v", n, got)
	}
}

// TestManualEdgeWinsTheFaceItShares (review item 2b): when the derivation would
// produce the SAME face a manual row occupies, the manual row wins and the write
// still applies. Without the skip the insert collides with the partial UNIQUE
// index and rolls the whole topology write back — the room edit is simply lost.
func TestManualEdgeWinsTheFaceItShares(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(connectedPairInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Replace the derived row on (0,0,0)->S with a hand-drawn one on the same face.
	if err := store.DB().Exec(
		"DELETE FROM map_edges WHERE map_set_id = ? AND from_x = 0 AND dir = 'S'", id).Error; err != nil {
		t.Fatalf("clear derived row: %v", err)
	}
	to := EdgeCell{"Alpha", 1, 0, 0}
	manualEdge(t, store, id, EdgeCell{"Alpha", 0, 0, 0}, "S", &to)

	var f RoomFields
	hint := "b renamed"
	f.Hint = &hint
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 1, Y: 0, L: 0, Fields: f,
	}); err != nil || !found {
		t.Fatalf("write next to a manual edge on a derived face: err=%v found=%v", err, found)
	}

	// The write must have landed...
	var room Room
	if err := store.DB().Where("map_set_id = ? AND x = 1", id).First(&room).Error; err != nil {
		t.Fatalf("read back room: %v", err)
	}
	if room.Hint != hint {
		t.Fatalf("room hint = %q, want %q (the write was rolled back)", room.Hint, hint)
	}
	// ...and the face must hold exactly one row, the manual one.
	n := 0
	for _, e := range edgeSummary(t, store, id) {
		if e == "grid:manual Alpha(0,0,0) S -> Alpha(1,0,0)" {
			n++
		} else if e == "grid:parsed Alpha(0,0,0) S -> Alpha(1,0,0)" {
			t.Errorf("derived row re-appeared on a manual face")
		}
	}
	if n != 1 {
		t.Fatalf("rows on the manual face = %d, want exactly 1", n)
	}
}

// TestRematerializeRepairsDriftFromRawRoomWrites (review item 1): a change to
// `rooms` that bypasses the write-path leaves the stored topology stale forever —
// before materialization it healed itself on the next index build. This is the
// repair handle, and it must not eat hand-drawn rows.
func TestRematerializeRepairsDriftFromRawRoomWrites(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(connectedPairInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	manualEdge(t, store, id, EdgeCell{"Alpha", 0, 0, 0}, "E", nil)

	// Someone edits rooms directly (hand-run SQL / a restored DB / a raw importer):
	// (1,0,0) stops advertising N, so the passage is no longer derivable at all.
	if err := store.DB().Exec(
		"UPDATE rooms SET edirs = '[]', ch = 0, exits = '' WHERE map_set_id = ? AND x = 1", id).Error; err != nil {
		t.Fatalf("raw room update: %v", err)
	}
	rooms, _ := store.ListRooms(id)
	want := summarizeEdges(DeriveEdges(id, rooms))
	if same(want, edgeSummary(t, store, id)) {
		t.Fatal("edges did not go stale — the test proves nothing")
	}

	n, err := store.RematerializeMapEdges(id)
	if err != nil {
		t.Fatalf("RematerializeMapEdges: %v", err)
	}
	got := edgeSummary(t, store, id)
	if n != len(got) {
		t.Errorf("reported row count %d, actual %d", n, len(got))
	}
	// Manual rows are kept, so the result is the derivation PLUS them.
	if !contains(got, "grid:manual Alpha(0,0,0) E -> -") {
		t.Errorf("rematerialization destroyed the hand-drawn edge: %v", got)
	}
	derived := make([]string, 0, len(got))
	for _, e := range got {
		if !strings.HasPrefix(e, "grid:manual") {
			derived = append(derived, e)
		}
	}
	if !same(want, derived) {
		t.Fatalf("rematerialization did not match a full derivation:\n want %v\n got  %v", want, derived)
	}
}

// TestNullScanRejectsUnexpectedTypes (review item 5): a column value of an
// unexpected type must error, not quietly read as NULL — "NULL" here means "this
// edge leads nowhere", i.e. a silently disappearing route.
func TestNullScanRejectsUnexpectedTypes(t *testing.T) {
	var sn strNull
	if err := sn.Scan(42); err == nil {
		t.Error("strNull.Scan(int) must fail, not silently unresolve the edge")
	}
	if err := sn.Scan(nil); err != nil || sn.valid {
		t.Errorf("strNull.Scan(nil) = %v, valid=%v; want no error and valid=false", err, sn.valid)
	}
	var in intNull
	if err := in.Scan("nope"); err == nil {
		t.Error("intNull.Scan(string) must fail")
	}
}

// TestDecodeJSONStrArrayHandlesEscapes (review item 5): automaps carries arbitrary
// MUD text, so the decoder shared with the mapper index must honour escaping.
func TestDecodeJSONStrArrayHandlesEscapes(t *testing.T) {
	in := `["Зона \"Кавычки\"|на север|3"]`
	got := DecodeJSONStrArray(in)
	want := `Зона "Кавычки"|на север|3`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("DecodeJSONStrArray(%s) = %#v, want [%q]", in, got, want)
	}
	if got := DecodeJSONStrArray(`["N","S"]`); len(got) != 2 || got[0] != "N" || got[1] != "S" {
		t.Fatalf("plain array decode broke: %#v", got)
	}
}
