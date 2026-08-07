package storage

import "testing"

func seamOf(t *testing.T, store *Store, id int64, source EdgeCell, zone string, tag int, command string) *MapEdge {
	t.Helper()
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	for i := range edges {
		e := &edges[i]
		if e.Kind == EdgeKindSeam && e.From() == source && e.TargetZone == zone &&
			e.TargetTag != nil && *e.TargetTag == tag && e.Command == command {
			return e
		}
	}
	return nil
}

func seamSelector(zone string, tag int, command string) *SeamSelector {
	return &SeamSelector{TargetZone: zone, TargetTag: ptrInt(tag), Command: command}
}

// A seam has no grid direction. Its stable selector is its source plus the
// parsed target semantics, and promoting it must preserve the markup whenever
// its source room is re-recorded or the operation is undone.
func TestSetSeamAttrsSurviveRederiveAndUndo(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam:  seamSelector("Beta", 5, "go"),
		Attrs: EdgeAttrPatch{Weight: floatPtr(.5), Phrase: strPtr("enter gate"), Door: boolPtr(true), Locked: boolPtr(true), OpenCmd: strPtr("open gate"), NoRoute: boolPtr(true)},
	})
	if err != nil || !found {
		t.Fatalf("set seam attrs: err=%v found=%v", err, found)
	}
	e := seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceManual || e.Weight != .5 || e.Phrase != "enter gate" || !e.Door || !e.Locked || e.OpenCmd != "open gate" || !e.NoRoute {
		t.Fatalf("manual seam attrs = %+v, want complete promoted seam", e)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	var slim *SlimRoom
	for i := range rooms {
		if rooms[i].X == from.X && rooms[i].Y == from.Y && rooms[i].L == from.L {
			slim = &rooms[i]
		}
	}
	if err != nil || slim == nil || len(slim.SE) != 1 || slim.SE[0].Command != "go" || slim.SE[0].Weight == nil || *slim.SE[0].Weight != .5 {
		t.Fatalf("slim seam attrs = %+v, err=%v", rooms, err)
	}

	hint := "renamed start"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Fields: RoomFields{Hint: &hint}}); err != nil || !found {
		t.Fatalf("re-record seam source: err=%v found=%v", err, found)
	}
	e = seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceManual || e.Weight != .5 || e.Phrase != "enter gate" || !e.Door || !e.Locked || e.OpenCmd != "open gate" || !e.NoRoute {
		t.Fatalf("re-derivation erased seam attrs: %+v", e)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatalf("rematerialize: %v", err)
	}
	e = seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceManual || e.Weight != .5 || e.Phrase != "enter gate" || !e.NoRoute {
		t.Fatalf("rematerialization erased seam attrs: %+v", e)
	}

	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo seam attrs: err=%v applied=%v", err, applied)
	}
	e = seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceParsed || e.Weight != 1 || e.Phrase != "" || e.Door || e.Locked || e.OpenCmd != "" || e.NoRoute {
		t.Fatalf("undo did not restore parsed seam: %+v", e)
	}
}

func TestSetSeamAttrsRejectsAmbiguousDuplicate(t *testing.T) {
	in := sampleInputWithSeams("Beta|go|5", "Beta|go|5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0,
		Seam: seamSelector("Beta", 5, "go"), Attrs: EdgeAttrPatch{Weight: floatPtr(2)},
	})
	if err != nil {
		t.Fatalf("ambiguous seam should be a soft failure, got %v", err)
	}
	if found {
		t.Fatal("ambiguous semantic seam selector must not choose an arbitrary duplicate")
	}
}

func TestSetSeamAttrsSelectsDuplicateByOccurrence(t *testing.T) {
	in := sampleInputWithSeams("Beta|go|5", "Beta|go|5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	order := 1
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0,
		Seam:  &SeamSelector{TargetZone: "Beta", TargetTag: ptrInt(5), Command: "go", Order: &order},
		Attrs: EdgeAttrPatch{Phrase: strPtr("second only")},
	})
	if err != nil || !found {
		t.Fatalf("set duplicate seam by order: err=%v found=%v", err, found)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind != EdgeKindSeam || edge.FromZone != "Alpha" || edge.Command != "go" {
			continue
		}
		if edge.SeamOrder == 0 && edge.Phrase != "" {
			t.Fatalf("first duplicate changed: %+v", edge)
		}
		if edge.SeamOrder == 1 && (edge.Phrase != "second only" || edge.Source != EdgeSourceManual) {
			t.Fatalf("second duplicate not selected: %+v", edge)
		}
	}
}

func TestSetSeamAttrsKeepsDuplicateOccurrencesIndependent(t *testing.T) {
	in := sampleInputWithSeams("Beta|go|5", "Beta|go|5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	for order, phrase := range []string{"first only", "second only"} {
		o := order
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
			Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0,
			Seam:  &SeamSelector{TargetZone: "Beta", TargetTag: ptrInt(5), Command: "go", Order: &o},
			Attrs: EdgeAttrPatch{Phrase: strPtr(phrase)},
		}); err != nil || !found {
			t.Fatalf("set duplicate seam order %d: err=%v found=%v", order, err, found)
		}
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	got := map[int]string{}
	for _, edge := range edges {
		if edge.Kind == EdgeKindSeam && edge.FromZone == "Alpha" && edge.Command == "go" {
			got[edge.SeamOrder] = edge.Phrase
		}
	}
	if got[0] != "first only" || got[1] != "second only" || len(got) != 2 {
		t.Fatalf("duplicate seam attrs collapsed: %v", got)
	}
}

func TestSetSeamAttrsSupportsEmptyRawCommand(t *testing.T) {
	in := sampleInputWithSeams("Beta||5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("Beta", 5, ""), Attrs: EdgeAttrPatch{Phrase: strPtr("enter portal")},
	}); err != nil || !found {
		t.Fatalf("annotate empty-command seam: err=%v found=%v", err, found)
	}
	e := seamOf(t, store, id, from, "Beta", 5, "")
	if e == nil || e.Source != EdgeSourceManual || e.Phrase != "enter portal" {
		t.Fatalf("empty-command seam attrs = %+v", e)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatalf("rematerialize: %v", err)
	}
	e = seamOf(t, store, id, from, "Beta", 5, "")
	if e == nil || e.Source != EdgeSourceManual || e.Phrase != "enter portal" {
		t.Fatalf("rematerialization lost empty-command seam attrs: %+v", e)
	}
}

func TestSetSeamAttrsLeavesSiblingSeamsIndependent(t *testing.T) {
	in := sampleInputWithSeams("Beta|go|5", "Beta|leave|5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0,
		Seam: seamSelector("Beta", 5, "go"), Attrs: EdgeAttrPatch{Weight: floatPtr(3)},
	}); err != nil || !found {
		t.Fatalf("set first seam: err=%v found=%v", err, found)
	}
	first := seamOf(t, store, id, EdgeCell{Zone: "Alpha"}, "Beta", 5, "go")
	second := seamOf(t, store, id, EdgeCell{Zone: "Alpha"}, "Beta", 5, "leave")
	if first == nil || first.Source != EdgeSourceManual || first.Weight != 3 {
		t.Fatalf("first seam attrs = %+v", first)
	}
	if second == nil || second.Source != EdgeSourceParsed || second.Weight != 1 {
		t.Fatalf("sibling seam was changed by first selector: %+v", second)
	}
}

func TestSetSeamAttrsPreservesMaterializationOrder(t *testing.T) {
	in := sampleInputWithSeams("Beta|first|5", "Beta|second|5")
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	before := seamOf(t, store, id, from, "Beta", 5, "second")
	if before == nil {
		t.Fatal("second seam missing before annotation")
	}
	secondID := before.ID
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("Beta", 5, "second"), Attrs: EdgeAttrPatch{Weight: floatPtr(2)},
	}); err != nil || !found {
		t.Fatalf("set second seam attrs: err=%v found=%v", err, found)
	}
	after := seamOf(t, store, id, from, "Beta", 5, "second")
	if after == nil || after.ID != secondID {
		t.Fatalf("annotation replaced selected seam: before=%d after=%+v", secondID, after)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var commands []string
	for _, e := range edges {
		if e.Kind == EdgeKindSeam && e.From() == from {
			commands = append(commands, e.Command)
		}
	}
	if len(commands) != 2 || commands[0] != "first" || commands[1] != "second" {
		t.Fatalf("annotation changed sibling seam order: %v", commands)
	}
}

func TestResetSeamAttrsKeepsManualOwnership(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	selector := seamSelector("Beta", 5, "go")
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: selector, Attrs: EdgeAttrPatch{Phrase: strPtr("enter")},
	}); err != nil || !found {
		t.Fatalf("set phrase: err=%v found=%v", err, found)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: selector, Attrs: EdgeAttrPatch{Phrase: strPtr("")},
	}); err != nil || !found {
		t.Fatalf("reset phrase: err=%v found=%v", err, found)
	}
	e := seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceManual || e.Phrase != "" || e.Weight != 1 {
		t.Fatalf("defaulted seam ownership = %+v", e)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil || len(rooms) == 0 {
		t.Fatalf("default attrs must be omitted from slim feed: rooms=%+v err=%v", rooms, err)
	}
	for _, room := range rooms {
		for _, seam := range room.SE {
			if seam.Weight != nil || seam.Phrase != "" || seam.Door || seam.Locked || seam.OpenCmd != "" || seam.NoRoute {
				t.Fatalf("default attrs leaked into slim feed: %+v", rooms)
			}
		}
	}
}

func TestDeleteSeamSourceAndUndoRestoresManualAttrs(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("Beta", 5, "go"), Attrs: EdgeAttrPatch{NoRoute: boolPtr(true)},
	}); err != nil || !found {
		t.Fatalf("set seam attrs: err=%v found=%v", err, found)
	}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L})
	if err != nil || !found {
		t.Fatalf("delete source: err=%v found=%v", err, found)
	}
	if e := seamOf(t, store, id, from, "Beta", 5, "go"); e != nil {
		t.Fatalf("deleted source retained seam: %+v", e)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo source delete: err=%v applied=%v", err, applied)
	}
	e := seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil || e.Source != EdgeSourceManual || !e.NoRoute {
		t.Fatalf("undo source delete lost seam attrs: %+v", e)
	}
}

func TestManualSeamAttrsResolveWhenTargetTagReturnsElsewhere(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("Beta", 5, "go"), Attrs: EdgeAttrPatch{Phrase: strPtr("enter")},
	}); err != nil || !found {
		t.Fatalf("set seam attrs: err=%v found=%v", err, found)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: "Beta", X: 0, Y: 0, L: 1}); err != nil || !found {
		t.Fatalf("delete target: err=%v found=%v", err, found)
	}
	newTarget := Room{MapSetID: id, Zone: "Beta", X: 8, Y: 9, L: 0, Tag: ptrInt(5), Hint: "new target", EDirs: `[]`, Doors: `[]`}
	if err := store.db.Create(&newTarget).Error; err != nil {
		t.Fatalf("recreate target tag: %v", err)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatalf("rematerialize after target return: %v", err)
	}
	e := seamOf(t, store, id, from, "Beta", 5, "go")
	if e == nil {
		t.Fatal("returned tag removed manual seam")
	}
	to, resolved := e.To()
	if !resolved || to != (EdgeCell{Zone: "Beta", X: 8, Y: 9, L: 0}) || e.Source != EdgeSourceManual || e.Phrase != "enter" {
		t.Fatalf("returned tag did not re-resolve manual seam: %+v resolved=%v to=%v", e, resolved, to)
	}
}

func TestManualSeamUniqueIndexRejectsExactDuplicate(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("Beta", 5, "go"), Attrs: EdgeAttrPatch{Weight: floatPtr(2)},
	}); err != nil || !found {
		t.Fatalf("set seam attrs: err=%v found=%v", err, found)
	}
	dup := *seamOf(t, store, id, from, "Beta", 5, "go")
	dup.ID = 0
	if err := store.db.Create(&dup).Error; err == nil {
		t.Fatal("migration 020 allowed an exact duplicate manual semantic seam")
	}
}

func TestDeleteSeamTargetKeepsSymbolicManualAttrs(t *testing.T) {
	in := MapSetInput{
		Name: "same-zone seam",
		Rooms: []Room{
			{Zone: "A", X: 0, Y: 0, L: 0, Tag: ptrInt(1), Hint: "source", EDirs: `[]`, Doors: `[]`},
			{Zone: "A", X: 1, Y: 0, L: 0, Tag: ptrInt(2), Hint: "target", EDirs: `[]`, Doors: `[]`},
		},
		LegacySeams: []LegacySeamDraft{{From: EdgeCell{Zone: "A"}, Entries: []string{"A|teleport|2"}}},
	}
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	from := EdgeCell{Zone: "A", X: 0, Y: 0, L: 0}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Seam: seamSelector("A", 2, "teleport"), Attrs: EdgeAttrPatch{Phrase: strPtr("enter portal"), Weight: floatPtr(.5)},
	}); err != nil || !found {
		t.Fatalf("set seam attrs: err=%v found=%v", err, found)
	}

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: "A", X: 1, Y: 0, L: 0})
	if err != nil || !found {
		t.Fatalf("delete seam target: err=%v found=%v", err, found)
	}
	e := seamOf(t, store, id, from, "A", 2, "teleport")
	if e == nil || e.Source != EdgeSourceManual || e.Phrase != "enter portal" || e.Weight != .5 {
		t.Fatalf("target delete lost symbolic manual seam attrs: %+v", e)
	}
	if _, resolved := e.To(); resolved {
		t.Fatalf("deleted target left seam resolved to %+v", e)
	}

	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo target delete: err=%v applied=%v", err, applied)
	}
	e = seamOf(t, store, id, from, "A", 2, "teleport")
	if e == nil {
		t.Fatal("undo removed attributed seam")
	}
	to, resolved := e.To()
	if !resolved || to != (EdgeCell{Zone: "A", X: 1, Y: 0, L: 0}) || e.Source != EdgeSourceManual || e.Phrase != "enter portal" || e.Weight != .5 {
		t.Fatalf("undo did not re-resolve attributed seam: %+v resolved=%v to=%v", e, resolved, to)
	}
}
