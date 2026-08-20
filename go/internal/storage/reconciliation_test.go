package storage

import (
	"slices"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func reconciliationFixture(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newMapperTestStore(t)
	rooms := []Room{
		{Zone: "Z", X: 0, Y: 0, Hint: "1", EDirs: `[]`, Doors: `[]`},
		{Zone: "Z", X: 0, Y: 1, Hint: "2", EDirs: `[]`, Doors: `[]`},
		{Zone: "Z", X: 0, Y: 2, Hint: "3", EDirs: `[]`, Doors: `[]`},
		{Zone: "Z", X: 1, Y: 2, Hint: "4", EDirs: `[]`, Doors: `[]`},
		{Zone: "Z", X: 1, Y: 1, Hint: "5", EDirs: `[]`, Doors: `[]`},
	}
	id, err := s.CreateMapSet(MapSetInput{Name: "five", Rooms: rooms})
	if err != nil {
		t.Fatal(err)
	}
	return s, id
}

func TestValidateReconciliationPreviewNormalizesZoneNames(t *testing.T) {
	s, id := reconciliationFixture(t)
	composed := "Зона"
	if _, _, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: composed, X: 9, Y: 9, L: 0, Fields: RoomFields{Hint: ptrStr("remote")}}); err != nil {
		t.Fatal(err)
	}
	decomposed := norm.NFD.String(composed)
	target := EdgeCell{Zone: decomposed, X: 9, Y: 9, L: 0}
	source := EdgeCell{Zone: "Z", X: 1, Y: 1, L: 0}
	layout := &RoomLayout{DisplayX: 2, DisplayY: 2, DisplayL: 0}
	if err := s.ValidateReconciliationPreview(id, target, []LayoutMutation{{Cell: source, Layout: layout}}); err != nil {
		t.Fatalf("preview with NFC-equivalent zones: %v", err)
	}
}

func TestApplyReconciliationExactDirectedFaceSurvivesRecomputeAndUndo(t *testing.T) {
	s, id := reconciliationFixture(t)
	set, _ := s.GetMapSet(id)
	source := EdgeCell{"Z", 1, 1, 0}
	target := EdgeCell{"Z", 0, 0, 0}
	// The recorded loop is 1-E->2-E->3-S->4-W->5. Before reconciliation
	// dead reckoning from 5 north points at the false occupied target 2.
	for _, want := range []struct {
		from EdgeCell
		dir  string
		to   EdgeCell
	}{
		{EdgeCell{"Z", 0, 0, 0}, "E", EdgeCell{"Z", 0, 1, 0}},
		{EdgeCell{"Z", 0, 1, 0}, "E", EdgeCell{"Z", 0, 2, 0}},
		{EdgeCell{"Z", 0, 2, 0}, "S", EdgeCell{"Z", 1, 2, 0}},
		{EdgeCell{"Z", 1, 2, 0}, "W", EdgeCell{"Z", 1, 1, 0}},
		{source, "N", EdgeCell{"Z", 0, 1, 0}},
	} {
		var e MapEdge
		if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", id, EdgeKindGrid, want.from.Zone, want.from.X, want.from.Y, want.from.L, want.dir).First(&e).Error; err != nil {
			t.Fatalf("missing loop face %+v: %v", want, err)
		}
		if got, ok := e.To(); !ok || got != want.to {
			t.Fatalf("loop face %+v -> %+v", want, got)
		}
	}
	before, err := s.ApplyReconciliation(id, ReconciliationInput{Source: source, Dir: "n", Target: target, Command: "climb rope", Phrase: "shown north", Attrs: EdgeAttrs{Weight: 2, Door: true, Locked: true, OpenCmd: "untie", NoRoute: true}, Layouts: []LayoutMutation{{Cell: source, Layout: &RoomLayout{DisplayX: 1, DisplayY: 0}}}, ExpectedRevision: set.TopologyRevision})
	if err != nil {
		t.Fatal(err)
	}
	assert := func() {
		var edges []MapEdge
		if err := s.DB().Where("map_set_id=? AND from_zone='Z' AND from_x=1 AND from_y=1 AND from_l=0 AND dir='N'", id).Find(&edges).Error; err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 {
			t.Fatalf("face rows=%d: %#v", len(edges), edges)
		}
		e := edges[0]
		to, ok := e.To()
		if e.Kind != EdgeKindCorridor || e.Source != EdgeSourceReconciled || !ok || to != target || e.Command != "climb rope" || e.Phrase != "shown north" || e.Weight != 2 || !e.Door || !e.Locked || e.OpenCmd != "untie" || !e.NoRoute {
			t.Fatalf("corridor changed: %#v", e)
		}
		var reverse int64
		s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=0 AND to_x=1 AND to_y=1", id, EdgeKindCorridor).Count(&reverse)
		if reverse != 0 {
			t.Fatal("phantom reverse corridor")
		}
		var suppressed MapEdge
		if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND from_l=0 AND dir='S'", id, EdgeKindGrid).First(&suppressed).Error; err != nil {
			t.Fatalf("missing persistent reverse suppression: %v", err)
		}
		if suppressed.Source != EdgeSourceReconciled || suppressed.Passable {
			t.Fatalf("reverse face is still traversable: %#v", suppressed)
		}
	}
	assert()
	var logical []Room
	if err := s.DB().Where("map_set_id=?", id).Order("hint").Find(&logical).Error; err != nil {
		t.Fatal(err)
	}
	wantLogical := []EdgeCell{{"Z", 0, 0, 0}, {"Z", 0, 1, 0}, {"Z", 0, 2, 0}, {"Z", 1, 2, 0}, {"Z", 1, 1, 0}}
	for i, r := range logical {
		if r.Hint != string(rune('1'+i)) || (EdgeCell{r.Zone, r.X, r.Y, r.L}) != wantLogical[i] {
			t.Fatalf("logical room changed: %#v", logical)
		}
	}
	var reverse int64
	s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=0 AND to_x=1 AND to_y=1", id, EdgeKindCorridor).Count(&reverse)
	if reverse != 0 {
		t.Fatal("reconciliation created reverse 1->5")
	}
	slim, err := s.ListSlimRooms(id, "Z")
	if err != nil {
		t.Fatal(err)
	}
	var payload []SlimCorridor
	var room2, room4, room5 *SlimRoom
	for _, r := range slim {
		if r.H == "2" {
			rr := r
			room2 = &rr
		}
		if r.X == source.X && r.Y == source.Y {
			payload = r.CO
			rr := r
			room5 = &rr
		}
		if r.H == "4" {
			rr := r
			room4 = &rr
		}
	}
	if len(payload) != 1 || payload[0].ToX != target.X || payload[0].ToY != target.Y || payload[0].Command != "climb rope" {
		t.Fatalf("corridor slim payload=%#v", payload)
	}
	if room2 == nil || slices.Contains(room2.G, "S") || slices.Contains(room2.GF, "S") || slices.Contains(room2.M, "S") || slices.Contains(room2.MB, "S") {
		t.Fatalf("suppressed reverse leaked into slim payload: %#v", room2)
	}
	if room4 == nil || room5 == nil || room5.PX == nil || room5.PY == nil || *room5.PX != 1 || *room5.PY != 0 || room4.Y-*room5.PY != 2 || (!slices.Contains(room4.G, "W") && !slices.Contains(room4.GF, "W")) {
		t.Fatalf("4-5 did not become one long display grid passage: 4=%#v 5=%#v", room4, room5)
	}
	if _, _, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Z", X: 1, Y: 1, L: 0, Fields: RoomFields{Hint: ptrStr("five")}}); err != nil {
		t.Fatal(err)
	}
	assert()
	if _, err := s.RematerializeMapEdges(id); err != nil {
		t.Fatal(err)
	}
	assert()
	set, _ = s.GetMapSet(id)
	if ok, err := s.RestoreTopologyBeforeAtRevision(id, before, set.TopologyRevision); err != nil || !ok {
		t.Fatalf("undo ok=%v err=%v", ok, err)
	}
	var old MapEdge
	if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=1 AND from_y=1 AND dir='N'", id, EdgeKindGrid).First(&old).Error; err != nil {
		t.Fatalf("literal old grid not restored: %v", err)
	}
	var restoredReverse MapEdge
	if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&restoredReverse).Error; err != nil || !restoredReverse.Passable || restoredReverse.Source == EdgeSourceReconciled {
		t.Fatalf("literal reverse not restored: %#v err=%v", restoredReverse, err)
	}
	var layouts int64
	s.DB().Model(&RoomLayout{}).Where("map_set_id=? AND zone='Z' AND x=1 AND y=1", id).Count(&layouts)
	if layouts != 0 {
		t.Fatal("layout before-image not restored")
	}
}

func TestApplyReconciliationStaleAndSubwriteFailureAreAtomic(t *testing.T) {
	s, id := reconciliationFixture(t)
	set, _ := s.GetMapSet(id)
	src := EdgeCell{"Z", 1, 1, 0}
	dst := EdgeCell{"Z", 0, 0, 0}
	if _, err := s.ApplyReconciliation(id, ReconciliationInput{Source: src, Dir: "N", Target: dst, Command: "n", ExpectedRevision: set.TopologyRevision + 1}); err == nil {
		t.Fatal("stale apply succeeded")
	}
	if err := s.DB().Exec(`CREATE TRIGGER fail_reconcile_layout BEFORE INSERT ON room_layouts BEGIN SELECT RAISE(ABORT,'forced layout failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyReconciliation(id, ReconciliationInput{Source: src, Dir: "N", Target: dst, Command: "n", Layouts: []LayoutMutation{{Cell: src, Layout: &RoomLayout{DisplayX: 1, DisplayY: 0}}}, ExpectedRevision: set.TopologyRevision}); err == nil {
		t.Fatal("forced failure succeeded")
	}
	var corridor int64
	s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=?", id, EdgeKindCorridor).Count(&corridor)
	if corridor != 0 {
		t.Fatal("edge escaped failed transaction")
	}
	var reverse MapEdge
	if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&reverse).Error; err != nil || !reverse.Passable || reverse.Source == EdgeSourceReconciled {
		t.Fatalf("failed apply changed reverse face: %#v err=%v", reverse, err)
	}
	after, _ := s.GetMapSet(id)
	if after.TopologyRevision != set.TopologyRevision {
		t.Fatalf("revision changed %d -> %d", set.TopologyRevision, after.TopologyRevision)
	}
}

func TestApplyReconciliationPreservesAuthoritativeReverseFace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *Store, int64)
	}{
		{"manual", func(t *testing.T, s *Store, id int64) {
			if err := s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).Update("source", EdgeSourceManual).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"reconciled", func(t *testing.T, s *Store, id int64) {
			if err := s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).Update("source", EdgeSourceReconciled).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"nonpassable", func(t *testing.T, s *Store, id int64) {
			if err := s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).Update("passable", false).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"mismatched target", func(t *testing.T, s *Store, id int64) {
			if err := s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).Updates(map[string]any{"to_x": 0, "to_y": 0}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"own exit evidence", func(t *testing.T, s *Store, id int64) {
			if err := s.DB().Model(&Room{}).Where("map_set_id=? AND zone='Z' AND x=0 AND y=1 AND l=0", id).Update("ch", 1<<chBitOrder["S"]).Error; err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, id := reconciliationFixture(t)
			tc.mutate(t, s, id)
			var want MapEdge
			if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&want).Error; err != nil {
				t.Fatal(err)
			}
			set, _ := s.GetMapSet(id)
			if _, err := s.ApplyReconciliation(id, ReconciliationInput{Source: EdgeCell{"Z", 1, 1, 0}, Dir: "N", Target: EdgeCell{"Z", 0, 0, 0}, Command: "climb", ExpectedRevision: set.TopologyRevision}); err != nil {
				t.Fatal(err)
			}
			var reverse MapEdge
			err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&reverse).Error
			wantTo, wantOK := want.To()
			gotTo, gotOK := reverse.To()
			if err != nil || reverse.Source != want.Source || reverse.Passable != want.Passable || gotOK != wantOK || gotTo != wantTo {
				t.Fatalf("authoritative reverse changed: want=%#v got=%#v err=%v", want, reverse, err)
			}
		})
	}
}

func TestReconciledReverseSuppressionDoesNotOutliveEndpoint(t *testing.T) {
	s, id := reconciliationFixture(t)
	set, _ := s.GetMapSet(id)
	source := EdgeCell{"Z", 1, 1, 0}
	if _, err := s.ApplyReconciliation(id, ReconciliationInput{Source: source, Dir: "N", Target: EdgeCell{"Z", 0, 0, 0}, Command: "climb", ExpectedRevision: set.TopologyRevision}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: source.Zone, X: source.X, Y: source.Y, L: source.L}); err != nil || !found {
		t.Fatalf("delete found=%v err=%v", found, err)
	}
	var n int64
	s.DB().Model(&MapEdge{}).Where("map_set_id=? AND source=?", id, EdgeSourceReconciled).Count(&n)
	if n != 0 {
		t.Fatal("reconciled topology survived endpoint deletion")
	}
	if _, _, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: source.Zone, X: source.X, Y: source.Y, L: source.L, Fields: RoomFields{Hint: ptrStr("replacement")}}); err != nil {
		t.Fatal(err)
	}
	s.DB().Model(&MapEdge{}).Where("map_set_id=? AND source=?", id, EdgeSourceReconciled).Count(&n)
	if n != 0 {
		t.Fatal("replacement inherited reconciled topology")
	}
}

func TestApplyReconciliationDoesNotPairNonDerivedOldFace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		updates map[string]any
	}{
		{"manual", map[string]any{"source": EdgeSourceManual}},
		{"reconciled", map[string]any{"source": EdgeSourceReconciled}},
		{"nonpassable", map[string]any{"passable": false}},
		{"mismatched target", map[string]any{"to_x": 0, "to_y": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, id := reconciliationFixture(t)
			if err := s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=1 AND from_y=1 AND dir='N'", id, EdgeKindGrid).Updates(tc.updates).Error; err != nil {
				t.Fatal(err)
			}
			var want MapEdge
			if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&want).Error; err != nil {
				t.Fatal(err)
			}
			set, _ := s.GetMapSet(id)
			if _, err := s.ApplyReconciliation(id, ReconciliationInput{Source: EdgeCell{"Z", 1, 1, 0}, Dir: "N", Target: EdgeCell{"Z", 0, 0, 0}, Command: "climb", ExpectedRevision: set.TopologyRevision}); err != nil {
				t.Fatal(err)
			}
			var got MapEdge
			if err := s.DB().Where("map_set_id=? AND kind=? AND from_zone='Z' AND from_x=0 AND from_y=1 AND dir='S'", id, EdgeKindGrid).First(&got).Error; err != nil {
				t.Fatal(err)
			}
			wantTo, wantOK := want.To()
			gotTo, gotOK := got.To()
			if got.Source != want.Source || got.Passable != want.Passable || gotOK != wantOK || gotTo != wantTo {
				t.Fatalf("reverse changed for non-derived old face: want=%#v got=%#v", want, got)
			}
		})
	}
}

func TestDeleteRoomDropsAndUndoRestoresEveryIncidentCorridorLiterally(t *testing.T) {
	for _, provenance := range []string{EdgeSourceReconciled, EdgeSourceManual} {
		for _, deletedEndpoint := range []string{"source", "target"} {
			t.Run(provenance+"/"+deletedEndpoint, func(t *testing.T) {
				s, id := reconciliationFixture(t)
				set, _ := s.GetMapSet(id)
				source := EdgeCell{"Z", 1, 1, 0}
				target := EdgeCell{"Z", 0, 0, 0} // deliberately non-adjacent
				_, err := s.ApplyReconciliation(id, ReconciliationInput{Source: source, Dir: "N", Target: target, Command: "crawl exact", Phrase: "display phrase", SourceProvenance: provenance, Attrs: EdgeAttrs{Weight: 3, Door: true, Locked: true, OpenCmd: "open exact", NoRoute: true}, ExpectedRevision: set.TopologyRevision})
				if err != nil {
					t.Fatal(err)
				}
				cell := source
				if deletedEndpoint == "target" {
					cell = target
				}
				before, found, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: cell.Zone, X: cell.X, Y: cell.Y, L: cell.L})
				if err != nil || !found {
					t.Fatalf("delete found=%v err=%v", found, err)
				}
				if len(before.Faces) != 1 || before.Faces[0].Edge.Source != provenance {
					t.Fatalf("corridor before=%#v", before.Faces)
				}
				var n int64
				s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=?", id, EdgeKindCorridor).Count(&n)
				if n != 0 {
					t.Fatal("incident corridor survived room delete")
				}
				// An unrelated replacement room must not inherit the deleted edge.
				if _, _, err := s.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: cell.Zone, X: cell.X, Y: cell.Y, L: cell.L, Fields: RoomFields{Hint: ptrStr("replacement")}}); err != nil {
					t.Fatal(err)
				}
				s.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=?", id, EdgeKindCorridor).Count(&n)
				if n != 0 {
					t.Fatal("replacement room inherited stale corridor")
				}
				current, _ := s.GetMapSet(id)
				if ok, err := s.RestoreTopologyBeforeAtRevision(id, before, current.TopologyRevision); err != nil || !ok {
					t.Fatalf("undo ok=%v err=%v", ok, err)
				}
				var restored MapEdge
				if err := s.DB().Where("map_set_id=? AND kind=?", id, EdgeKindCorridor).First(&restored).Error; err != nil {
					t.Fatal(err)
				}
				to, ok := restored.To()
				if !ok || to != target || restored.Source != provenance || restored.Command != "crawl exact" || restored.Phrase != "display phrase" || restored.Weight != 3 || !restored.Door || !restored.Locked || restored.OpenCmd != "open exact" || !restored.NoRoute {
					t.Fatalf("literal corridor not restored: %#v", restored)
				}
			})
		}
	}
}
