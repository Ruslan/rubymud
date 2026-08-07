package mapper

import (
	"reflect"
	"testing"

	"rubymud/go/internal/storage"
)

func TestOutgoingTopologyCopiesTargetsAndKeepsMalformedSeam(t *testing.T) {
	rooms := []storage.Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Hint: "from", EDirs: `[]`, Doors: `[]`},
		{Zone: "A", X: 0, Y: 1, L: 0, Hint: "grid", EDirs: `[]`, Doors: `[]`},
		{Zone: "B", X: 3, Y: 4, L: 0, Hint: "to", Tag: intPtr(7), EDirs: `[]`, Doors: `[]`},
	}
	idx := BuildIndexWithEdges(1, rooms, storage.DeriveImportedEdges(1, rooms, []storage.LegacySeamDraft{{From: storage.EdgeCell{Zone: "A"}, Entries: []string{"B|войти|7", "bad|only-two"}}}))
	from := idx.Room(Coord{Zone: "A", X: 0, Y: 0, L: 0})
	if from == nil {
		t.Fatal("source room missing")
	}
	got := from.OutgoingTopology()
	if len(got) != 3 { // grid E, resolved seam, dead partial seam
		t.Fatalf("OutgoingTopology len=%d, want 3: %#v", len(got), got)
	}
	if got[0].Kind != storage.EdgeKindGrid || !got[0].HasTarget {
		t.Fatalf("grid snapshot = %#v", got[0])
	}
	if got[1].TargetZone != "B" || !got[1].HasTargetTag || got[1].TargetTag != 7 || !got[1].HasTarget {
		t.Fatalf("seam selector/target = %#v", got[1])
	}
	if got[2].TargetZone != "bad" || got[2].Command != "only-two" || got[2].HasTargetTag || got[2].HasTarget {
		t.Fatalf("partial seam must remain an authoritative nullable edge: %#v", got[2])
	}

	// The DTO has no pointers into the live index. Mutating this caller-owned
	// value must not alter a later read from the frozen index.
	got[0].Target.Zone = "mutated"
	got[1].TargetTag = 99
	again := from.OutgoingTopology()
	if again[0].Target.Zone != "A" || again[1].TargetTag != 7 {
		t.Fatalf("snapshot mutation leaked into index: %#v", again)
	}
}

func TestOutgoingTopologyPreservesDuplicateSelectorOrderAndInputIsolation(t *testing.T) {
	tag := 7
	rooms := []storage.Room{
		{Zone: "A", X: 0, Y: 0, L: 0},
		{Zone: "B", X: 1, Y: 0, L: 0, Tag: intPtr(7)},
	}
	targetZone, targetX, targetY, targetL := "B", 1, 0, 0
	edges := []storage.MapEdge{
		{Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceParsed, Passable: true, SeamOrder: 0,
			FromZone: "A", Command: "go", TargetZone: "B", TargetTag: &tag,
			ToZone: &targetZone, ToX: &targetX, ToY: &targetY, ToL: &targetL,
			EdgeAttrs: storage.EdgeAttrs{Weight: 2}},
		{Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceParsed, Passable: true, SeamOrder: 2,
			FromZone: "A", Command: "go", TargetZone: "B", TargetTag: &tag,
			ToZone: &targetZone, ToX: &targetX, ToY: &targetY, ToL: &targetL,
			EdgeAttrs: storage.EdgeAttrs{Weight: 3}},
	}
	room := BuildIndexWithEdges(1, rooms, edges).Room(Coord{Zone: "A"})

	// The two exact selectors are separate materialized rows and their persisted
	// occurrence controls order without reading the conflicting legacy JSON.
	got := room.OutgoingTopology()
	var order []any
	for _, edge := range got {
		order = append(order, edge.Weight)
	}
	if want := []any{float64(2), float64(3)}; !reflect.DeepEqual(order, want) {
		t.Fatalf("topology order = %#v, want %#v", order, want)
	}

	// BuildIndexWithEdges is a snapshot: later mutation of storage input must not
	// change an already installed index.
	tag = 99
	again := room.OutgoingTopology()
	if !again[0].HasTargetTag || again[0].TargetTag != 7 {
		t.Fatalf("storage input pointer leaked into index: %#v", again[0])
	}
}

func TestOutgoingTopologyManualOccurrenceDoesNotShadowDuplicateSibling(t *testing.T) {
	tag := 7
	targetZone, targetX, targetY, targetL := "B", 1, 0, 0
	edges := []storage.MapEdge{
		{Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceManual, Passable: true, SeamOrder: 0,
			FromZone: "A", Command: "go", TargetZone: "B", TargetTag: &tag,
			ToZone: &targetZone, ToX: &targetX, ToY: &targetY, ToL: &targetL},
		{Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceParsed, Passable: true, SeamOrder: 1,
			FromZone: "A", Command: "go", TargetZone: "B", TargetTag: &tag,
			ToZone: &targetZone, ToX: &targetX, ToY: &targetY, ToL: &targetL},
	}
	rooms := []storage.Room{{Zone: "A"}, {Zone: "B", X: 1, Tag: &tag}}
	got := BuildIndexWithEdges(1, rooms, edges).Room(Coord{Zone: "A"}).OutgoingTopology()
	if len(got) != 2 || got[0].SeamOrder != 0 || got[1].SeamOrder != 1 {
		t.Fatalf("manual occurrence shadowed duplicate sibling: %#v", got)
	}
}

func intPtr(v int) *int { return &v }
