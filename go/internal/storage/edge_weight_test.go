package storage

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

func TestEdgeWeightWriteValidationAndSlimOmission(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, weight := range []float64{0, -1, math.Inf(1), math.NaN()} {
		_, _, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", Attrs: EdgeAttrPatch{Weight: floatPtr(weight)}})
		if err == nil {
			t.Errorf("weight %v was accepted", weight)
		}
	}
	// Database/default-derived rows have no explicit weight in the slim payload.
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rooms[1].EA {
		if a.Dir == "N" && a.Weight != nil {
			t.Errorf("default weight appeared on slim row: %+v", a)
		}
	}
	if raw, err := json.Marshal(rooms); err != nil || strings.Contains(string(raw), `"weight":1`) {
		t.Errorf("default weight leaked into slim JSON: %s (err=%v)", raw, err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 1, Y: 0, L: 0, Dir: "N", Attrs: EdgeAttrPatch{Weight: floatPtr(.5)}}); err != nil || !found {
		t.Fatalf("set weight: found=%v err=%v", found, err)
	}
	rooms, err = store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range rooms[1].EA {
		if a.Dir == "N" {
			found = true
			if a.Weight == nil || *a.Weight != .5 {
				t.Errorf("slim weight = %v, want .5", a.Weight)
			}
		}
	}
	if !found {
		t.Fatal("non-default weight omitted from slim attrs")
	}
	if raw, err := json.Marshal(rooms); err != nil || !strings.Contains(string(raw), `"weight":0.5`) {
		t.Errorf("non-default weight absent from slim JSON: %s (err=%v)", raw, err)
	}
}

func TestEdgeWeightSurvivesRematerializeAndLegacyZeroIsOne(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", Attrs: EdgeAttrPatch{Weight: floatPtr(.1)}}); err != nil || !found {
		t.Fatalf("set weight: found=%v err=%v", found, err)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatal(err)
	}
	if e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S"); e == nil || e.Weight != .1 {
		t.Errorf("rematerialization lost weight: %+v", e)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Weight: floatPtr(1)},
	}); err != nil || !found {
		t.Fatalf("reset weight: found=%v err=%v", found, err)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatal(err)
	}
	reset := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if reset == nil || reset.Weight != 1 || reset.Source != EdgeSourceManual {
		t.Errorf("reset-to-default changed manual face ownership: %+v", reset)
	}
	if got := (EdgeAttrs{}).EffectiveWeight(); got != 1 {
		t.Errorf("legacy zero effective weight = %v, want 1", got)
	}
}

func TestEdgeWeightSchemaRejectsInvalidDirectWrites(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatal(err)
	}
	edge := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if edge == nil {
		t.Fatal("missing derived edge")
	}
	for _, weight := range []float64{0, -1, math.Inf(1), math.NaN()} {
		if err := store.db.Exec(`UPDATE map_edges SET weight = ? WHERE id = ?`, weight, edge.ID).Error; err == nil {
			t.Errorf("schema accepted direct weight %v", weight)
		}
	}
}

func TestManualEdgePersistsAndOmitsDefaultWeight(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(hiddenFaceInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("add manual edge: found=%v err=%v", found, err)
	}
	var stored float64
	if err := store.db.Raw(`SELECT weight FROM map_edges
		WHERE map_set_id = ? AND from_zone = 'Alpha' AND from_x = 0 AND from_y = 0 AND from_l = 0 AND dir = 'E'`, id).
		Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("stored manual weight = %v, want database default 1", stored)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range rooms {
		for _, attr := range room.EA {
			if attr.Dir == "E" && attr.Weight != nil {
				t.Fatalf("manual default weight leaked into slim payload: %+v", attr)
			}
		}
	}
}
