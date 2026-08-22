package storage

import "testing"

func TestDirectedEdgeOpeningDelayRoundTripsAndValidates(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "delay", Rooms: []Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Exits: "D", EDirs: `["D"]`, Doors: `["D"]`, Ch: 32},
		{Zone: "Z", X: 0, Y: 0, L: -1, Exits: "U", EDirs: `["U"]`, Doors: `["U"]`, Ch: 16},
	}})
	if err != nil {
		t.Fatal(err)
	}
	delay := 2000
	door := true
	command := "повернуть рычаг"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "D",
		Attrs: EdgeAttrPatch{Door: &door, OpenCmd: &command, OpenDelayMS: &delay},
	}); err != nil || !found {
		t.Fatalf("set delay found=%v err=%v", found, err)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatal(err)
	}
	var face *MapEdge
	for i := range edges {
		if edges[i].Kind == EdgeKindGrid && edges[i].From() == (EdgeCell{"Z", 0, 0, 0}) && edges[i].Dir == "D" {
			face = &edges[i]
			break
		}
	}
	if face == nil || face.OpenDelayMS != 2000 || face.OpenCmd != command || !face.Door || face.Source != EdgeSourceManual {
		t.Fatalf("delayed face=%+v", face)
	}

	tooLong := MaxOpenDelayMS + 1
	if _, _, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "D",
		Attrs: EdgeAttrPatch{OpenDelayMS: &tooLong},
	}); err == nil {
		t.Fatal("accepted opening delay above the bound")
	}
	if err := store.DB().Exec(`UPDATE map_edges SET open_delay_ms=? WHERE map_set_id=?`, MaxOpenDelayMS+1, id).Error; err == nil {
		t.Fatal("SQLite CHECK accepted opening delay above the bound")
	}
}
