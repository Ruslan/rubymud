package storage

import (
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestZoneMovePreservesIncidentTopologyAndUndo(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "zone move", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Hint: "moved", EDirs: `["E","N"]`, Doors: "[]"},
		{Zone: "A", X: 0, Y: 1, L: 0, Hint: "stays", EDirs: `["W"]`, Doors: "[]"},
		{Zone: "A", X: -1, Y: 0, L: 0, Hint: "second boundary", EDirs: `["S"]`, Doors: "[]"},
		{Zone: "B", X: 9, Y: 0, L: 0, Hint: "existing target", EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRoomAnnotation(id, "A", 0, 0, 0, AnnotationFields{Note: strPtr("moves too")}); err != nil {
		t.Fatal(err)
	}
	inhabitant, err := store.CreateRoomInhabitant(id, EdgeCell{Zone: "A", X: 0, Y: 0, L: 0}, RoomInhabitantFields{
		InhabitantFields: InhabitantFields{Name: "Keeper", Kind: InhabitantKindNPC, Strategy: "ask first"},
		PresenceText:     "The keeper waits here.", Resident: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same-zone outgoing portal and a separate incoming portal prove that exact
	// endpoint updates do not depend on direction or on zone/tag provenance.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal, Zone: "A", X: 0, Y: 0, L: 0, Portal: &PortalTarget{Zone: "A", X: 0, Y: 1, L: 0}, PortalCommand: "out"}); err != nil || !found {
		t.Fatalf("out portal found=%v err=%v", found, err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal, Zone: "A", X: 0, Y: 1, L: 0, Portal: &PortalTarget{Zone: "A", X: 0, Y: 0, L: 0}, PortalCommand: "in"}); err != nil || !found {
		t.Fatalf("in portal found=%v err=%v", found, err)
	}
	if err := store.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND from_zone=? AND from_x=0", id, EdgeKindGrid, "A").Updates(map[string]any{"weight": 2.5, "phrase": "squeeze"}).Error; err != nil {
		t.Fatal(err)
	}

	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Rooms != 1 || p.BoundaryGridEdges != 4 || p.Incoming != 1 || p.Outgoing != 1 || p.AnnotationsMoved != 1 || p.InhabitantsMoved != 1 || p.TargetExists != true {
		t.Fatalf("preview=%+v", p)
	}
	before, applied, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}, {0, 0, 0}}, ExpectedRevision: p.Revision})
	if err != nil || !found {
		t.Fatalf("apply found=%v err=%v", found, err)
	}
	if applied.Revision != p.Revision+1 {
		t.Fatalf("revision=%d want %d", applied.Revision, p.Revision+1)
	}
	if roomAt(t, store, id, "B", 0, 0, 0) == nil || roomAt(t, store, id, "A", 0, 0, 0) != nil {
		t.Fatal("room did not move")
	}
	var anno RoomAnnotation
	if err := store.DB().Where("map_set_id=? AND zone='B' AND x=0 AND y=0 AND l=0", id).First(&anno).Error; err != nil {
		t.Fatalf("annotation did not move: %v", err)
	}
	movedInhabitants, err := store.ListRoomInhabitants(id, EdgeCell{Zone: "B", X: 0, Y: 0, L: 0})
	if err != nil || len(movedInhabitants) != 1 || movedInhabitants[0].ID != inhabitant.ID {
		t.Fatalf("inhabitant did not move: %+v err=%v", movedInhabitants, err)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatal(err)
	}
	var boundary, out, in *MapEdge
	for i := range edges {
		e := &edges[i]
		if e.Kind == EdgeKindGrid && e.FromZone == "B" && e.ToZone != nil && *e.ToZone == "A" {
			boundary = e
		}
		if e.Kind == EdgeKindSeam && e.Command == "out" {
			out = e
		}
		if e.Kind == EdgeKindSeam && e.Command == "in" {
			in = e
		}
	}
	if boundary == nil || boundary.Source != EdgeSourceManual {
		t.Fatalf("boundary=%+v edges=%+v", boundary, edges)
	}
	for _, zone := range []string{"A", "B"} {
		rooms, err := store.ListSlimRooms(id, zone)
		if err != nil {
			t.Fatalf("ListSlimRooms(%q): %v", zone, err)
		}
		contacts := 0
		for _, room := range rooms {
			contacts += len(room.XZ)
			for _, contact := range room.XZ {
				if contact.ToZone == zone {
					t.Fatalf("same-zone contact after move in %q: %+v", zone, contact)
				}
			}
		}
		if contacts != 2 {
			t.Fatalf("cross-zone contacts in %q = %d, want 2", zone, contacts)
		}
	}
	if out == nil || out.FromZone != "B" || out.ToZone == nil || *out.ToZone != "A" {
		t.Fatalf("out portal=%+v", out)
	}
	if in == nil || in.FromZone != "A" || in.ToZone == nil || *in.ToZone != "B" {
		t.Fatalf("incoming portal=%+v", in)
	}
	if ok, err := store.RestoreTopologyBefore(id, before); err != nil || !ok {
		t.Fatalf("undo ok=%v err=%v", ok, err)
	}
	if roomAt(t, store, id, "A", 0, 0, 0) == nil || roomAt(t, store, id, "B", 0, 0, 0) != nil {
		t.Fatal("undo did not restore room")
	}
	if err := store.DB().Where("map_set_id=? AND zone='A' AND x=0 AND y=0 AND l=0", id).First(&anno).Error; err != nil {
		t.Fatalf("undo did not restore annotation: %v", err)
	}
	restoredInhabitants, err := store.ListRoomInhabitants(id, EdgeCell{Zone: "A", X: 0, Y: 0, L: 0})
	if err != nil || len(restoredInhabitants) != 1 || restoredInhabitants[0].ID != inhabitant.ID {
		t.Fatalf("undo did not restore inhabitant: %+v err=%v", restoredInhabitants, err)
	}
}

func TestZoneMoveRejectsCollisionAndStaleRevision(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "collisions", Rooms: []Room{{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}, {Zone: "B", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.CollisionCount != 1 {
		t.Fatalf("preview=%+v", p)
	}
	if _, _, _, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: p.Revision}); err == nil {
		t.Fatal("collision applied")
	}
	set, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	if set.TopologyRevision != p.Revision {
		t.Fatalf("collision advanced revision to %d", set.TopologyRevision)
	}
	// A different target makes the stale compare-and-swap observable.
	if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "C", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: p.Revision + 1}); err == nil || found {
		t.Fatalf("stale found=%v err=%v", found, err)
	}
}

func TestZoneMoveUndoMovesOnlyCapturedInhabitantPlacements(t *testing.T) {
	for _, tc := range []struct {
		name string
		move ZoneMoveInput
	}{
		{name: "partial move", move: ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}}},
		{name: "whole-zone rename", move: ZoneMoveInput{SourceZone: "A", TargetZone: "B", AllSource: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMapperTestStore(t)
			id, err := store.CreateMapSet(MapSetInput{Name: tc.name, Rooms: []Room{
				{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
				{Zone: "B", X: 9, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			source, err := store.CreateRoomInhabitant(id, EdgeCell{Zone: "A", X: 0, Y: 0, L: 0}, RoomInhabitantFields{
				InhabitantFields: InhabitantFields{Name: "source", Kind: InhabitantKindNPC},
			})
			if err != nil {
				t.Fatal(err)
			}
			preexisting, err := store.CreateRoomInhabitant(id, EdgeCell{Zone: "B", X: 0, Y: 0, L: 0}, RoomInhabitantFields{
				InhabitantFields: InhabitantFields{Name: "preexisting target", Kind: InhabitantKindOther},
			})
			if err != nil {
				t.Fatal(err)
			}
			preview, err := store.PreviewZoneMove(id, tc.move)
			if err != nil || preview.CollisionCount != 0 {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			move := tc.move
			move.ExpectedRevision = preview.Revision
			before, _, found, err := store.ApplyZoneMove(id, move)
			if err != nil || !found {
				t.Fatalf("apply found=%v err=%v", found, err)
			}
			createdAfter, err := store.CreateRoomInhabitant(id, EdgeCell{Zone: "B", X: 0, Y: 0, L: 0}, RoomInhabitantFields{
				InhabitantFields: InhabitantFields{Name: "created after move", Kind: InhabitantKindMonster},
			})
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := store.RestoreTopologyBefore(id, before); err != nil || !ok {
				t.Fatalf("undo ok=%v err=%v", ok, err)
			}
			sourceRows, err := store.ListRoomInhabitants(id, EdgeCell{Zone: "A", X: 0, Y: 0, L: 0})
			if err != nil || len(sourceRows) != 1 || sourceRows[0].ID != source.ID {
				t.Fatalf("source after undo=%+v err=%v", sourceRows, err)
			}
			targetRows, err := store.ListRoomInhabitants(id, EdgeCell{Zone: "B", X: 0, Y: 0, L: 0})
			if err != nil || len(targetRows) != 2 {
				t.Fatalf("target after undo=%+v err=%v", targetRows, err)
			}
			got := map[int64]bool{}
			for _, row := range targetRows {
				got[row.ID] = true
			}
			if !got[preexisting.ID] || !got[createdAfter.ID] || got[source.ID] {
				t.Fatalf("undo moved unrelated target knowledge: %+v", targetRows)
			}
		})
	}
}

func TestZoneMovePreviewRejectsSameEntityPlacementCollision(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "placement collision", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: "B", X: 9, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entity, err := store.CreateRoomInhabitant(id, EdgeCell{Zone: "A", X: 0, Y: 0, L: 0}, RoomInhabitantFields{
		InhabitantFields: InhabitantFields{Name: "traveller", Kind: InhabitantKindNPC},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PlaceRoomInhabitant(id, EdgeCell{Zone: "B", X: 0, Y: 0, L: 0}, entity.ID, "already here", false); err != nil {
		t.Fatal(err)
	}
	move := ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}}
	preview, err := store.PreviewZoneMove(id, move)
	if err != nil || preview.CollisionCount != 1 {
		t.Fatalf("preview=%+v err=%v, want one placement collision", preview, err)
	}
	move.ExpectedRevision = preview.Revision
	if _, _, found, err := store.ApplyZoneMove(id, move); err == nil || found {
		t.Fatalf("same-entity placement collision applied: found=%v err=%v", found, err)
	}
}

func TestZoneMoveDoesNotPromoteUnrelatedBoundaryEdge(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "boundary provenance", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	toZone, toX, toY, toL := "B", 50, 0, 0
	unrelated := MapEdge{MapSetID: id, Kind: EdgeKindGrid, Source: EdgeSourceParsed, Passable: true,
		FromZone: "A", FromX: 49, FromY: 0, FromL: 0, Dir: "E",
		ToZone: &toZone, ToX: &toX, ToY: &toY, ToL: &toL, EdgeAttrs: EdgeAttrs{Weight: 1}}
	if err := store.DB().Create(&unrelated).Error; err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: p.Revision}); err != nil || !found {
		t.Fatalf("apply found=%v err=%v", found, err)
	}
	var got MapEdge
	if err := store.DB().First(&got, unrelated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Source != EdgeSourceParsed {
		t.Fatalf("unrelated boundary provenance changed to %q", got.Source)
	}
}

func TestZoneMoveGuardsOnlyKnownDerivedBoundaryRows(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "foreign edge provenance", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `[]`},
		{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const foreignSource = "external"
	if err := store.DB().Model(&MapEdge{}).
		Where("map_set_id=? AND kind=? AND from_zone='A' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'", id, EdgeKindGrid).
		Update("source", foreignSource).Error; err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{
		SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: p.Revision,
	}); err != nil || !found {
		t.Fatalf("move found=%v err=%v", found, err)
	}
	var got MapEdge
	if err := store.DB().Where("map_set_id=? AND from_zone='B' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'", id).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Source != foreignSource || got.ZoneGuard {
		t.Fatalf("foreign provenance was adopted by zone move: source=%q guard=%v", got.Source, got.ZoneGuard)
	}
}

// A zone split temporarily promotes a derived face so rematerialization cannot
// erase its exact endpoint across namespaces. Merging the two rooms back must
// give that provenance back to the derivation, including the door that derives
// from rooms.doors. A deliberately hand-drawn face must remain manual instead.
func TestZoneMoveMergeDemotesOnlyZoneGuardedNaturalFaces(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "zone guard", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `["E"]`},
		{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
		{Zone: "A", X: 2, Y: 0, L: 0, EDirs: `[]`, Doors: `[]`},
		{Zone: "A", X: 2, Y: 1, L: 0, EDirs: `[]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// This is already a user's manual assertion even though its endpoint happens
	// to match the natural neighbour. A zone move must never adopt it as its own.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "A", X: 0, Y: 1, L: 0, Dir: "W", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("manual edge found=%v err=%v", found, err)
	}

	move := func(source, target string) TopologyBefore {
		t.Helper()
		rooms := []ZoneCoord{{X: 0, Y: 0, L: 0}, {X: 2, Y: 0, L: 0}}
		preview, err := store.PreviewZoneMove(id, ZoneMoveInput{
			SourceZone: source, TargetZone: target, Rooms: rooms,
		})
		if err != nil {
			t.Fatal(err)
		}
		before, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{
			SourceZone: source, TargetZone: target, Rooms: rooms, ExpectedRevision: preview.Revision,
		})
		if err != nil || !found {
			t.Fatalf("move %s -> %s found=%v err=%v", source, target, found, err)
		}
		return before
	}

	first := move("A", "B")
	var guarded, userManual struct {
		Source    string
		ZoneGuard bool `gorm:"column:zone_guard"`
		Door      bool
	}
	if err := store.DB().Raw(`SELECT source, zone_guard, door FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='B' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`, id).Scan(&guarded).Error; err != nil {
		t.Fatal(err)
	}
	if guarded.Source != EdgeSourceManual || !guarded.ZoneGuard || !guarded.Door {
		t.Fatalf("split face = %+v, want guarded manual derived door", guarded)
	}
	if err := store.DB().Raw(`SELECT source, zone_guard, door FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=0 AND from_y=1 AND from_l=0 AND dir='W'`, id).Scan(&userManual).Error; err != nil {
		t.Fatal(err)
	}
	if userManual.Source != EdgeSourceManual || userManual.ZoneGuard {
		t.Fatalf("preexisting manual face was adopted by zone move: %+v", userManual)
	}
	var incomingGrid struct {
		Source    string
		ZoneGuard bool `gorm:"column:zone_guard"`
	}
	if err := store.DB().Raw(`SELECT source, zone_guard FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=2 AND from_y=1 AND from_l=0 AND dir='W'`, id).Scan(&incomingGrid).Error; err != nil {
		t.Fatal(err)
	}
	if incomingGrid.Source != EdgeSourceManual || !incomingGrid.ZoneGuard {
		t.Fatalf("incoming fallback face = %+v, want guarded manual", incomingGrid)
	}

	second := move("B", "A")
	if err := store.DB().Raw(`SELECT source, zone_guard, door FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`, id).Scan(&guarded).Error; err != nil {
		t.Fatal(err)
	}
	if guarded.Source != EdgeSourceParsed || guarded.ZoneGuard || !guarded.Door {
		t.Fatalf("merged face = %+v, want parsed unguarded derived door", guarded)
	}
	if err := store.DB().Raw(`SELECT source, zone_guard FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=0 AND from_y=1 AND from_l=0 AND dir='W'`, id).Scan(&userManual).Error; err != nil {
		t.Fatal(err)
	}
	if userManual.Source != EdgeSourceManual || userManual.ZoneGuard {
		t.Fatalf("user manual face was demoted on merge: %+v", userManual)
	}
	if err := store.DB().Raw(`SELECT source, zone_guard FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=2 AND from_y=1 AND from_l=0 AND dir='W'`, id).Scan(&incomingGrid).Error; err != nil {
		t.Fatal(err)
	}
	if incomingGrid.Source != EdgeSourceGrid || incomingGrid.ZoneGuard {
		t.Fatalf("merged fallback face = %+v, want grid unguarded", incomingGrid)
	}

	if ok, err := store.RestoreTopologyBefore(id, second); err != nil || !ok {
		t.Fatalf("undo merge ok=%v err=%v", ok, err)
	}
	if err := store.DB().Raw(`SELECT source, zone_guard FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='B' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`, id).Scan(&guarded).Error; err != nil {
		t.Fatal(err)
	}
	if guarded.Source != EdgeSourceManual || !guarded.ZoneGuard {
		t.Fatalf("undo merge lost guarded boundary state: %+v", guarded)
	}
	if ok, err := store.RestoreTopologyBefore(id, first); err != nil || !ok {
		t.Fatalf("undo split ok=%v err=%v", ok, err)
	}
}

func TestZoneMoveMergeKeepsEditedGuardedFaceManual(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "edited zone guard", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `[]`},
		{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	move := func(source, target string) {
		t.Helper()
		preview, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: source, TargetZone: target, Rooms: []ZoneCoord{{X: 0, Y: 0, L: 0}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: source, TargetZone: target, Rooms: []ZoneCoord{{X: 0, Y: 0, L: 0}}, ExpectedRevision: preview.Revision}); err != nil || !found {
			t.Fatalf("move %s -> %s found=%v err=%v", source, target, found, err)
		}
	}
	move("A", "B")
	phrase := "squeeze through"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "B", X: 0, Y: 0, L: 0, Dir: "E", Attrs: EdgeAttrPatch{Phrase: &phrase},
	}); err != nil || !found {
		t.Fatalf("edit guarded edge found=%v err=%v", found, err)
	}
	move("B", "A")
	var got struct {
		Source    string
		ZoneGuard bool `gorm:"column:zone_guard"`
		Phrase    string
	}
	if err := store.DB().Raw(`SELECT source, zone_guard, phrase FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='A' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`, id).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Source != EdgeSourceManual || got.ZoneGuard || got.Phrase != phrase {
		t.Fatalf("edited former boundary = %+v, want user-owned manual face", got)
	}
}

func TestExplicitFaceWritesTakeOwnershipFromZoneGuard(t *testing.T) {
	phrase := "claimed"
	tests := []struct {
		name         string
		op           TopologyOp
		wantSource   string
		wantPassable bool
		wantNoOp     bool
	}{
		{name: "add", op: TopologyOp{Kind: TopoAddManualEdge, OneWay: true}, wantSource: EdgeSourceManual, wantPassable: true},
		{name: "block", op: TopologyOp{Kind: TopoBlockManualEdge, OneWay: true}, wantSource: EdgeSourceManual, wantPassable: false},
		{name: "remove", op: TopologyOp{Kind: TopoRemoveManualEdge, OneWay: true}, wantSource: EdgeSourceParsed, wantPassable: true},
		{name: "change attrs", op: TopologyOp{Kind: TopoSetEdgeAttrs, Attrs: EdgeAttrPatch{Phrase: &phrase}}, wantSource: EdgeSourceManual, wantPassable: true},
		{name: "empty attrs", op: TopologyOp{Kind: TopoSetEdgeAttrs}, wantSource: EdgeSourceManual, wantPassable: true, wantNoOp: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMapperTestStore(t)
			id, err := store.CreateMapSet(MapSetInput{Name: "guard ownership", Rooms: []Room{
				{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `[]`},
				{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DB().Model(&MapEdge{}).
				Where("map_set_id=? AND kind=? AND from_zone='A' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'", id, EdgeKindGrid).
				Updates(map[string]any{"source": EdgeSourceManual, "zone_guard": true}).Error; err != nil {
				t.Fatal(err)
			}
			op := tt.op
			op.Zone, op.X, op.Y, op.L, op.Dir = "A", 0, 0, 0, "E"
			before, found, err := store.ApplyTopologyOp(id, op)
			if err != nil || !found {
				t.Fatalf("apply found=%v err=%v", found, err)
			}
			got := faceOf(t, store, id, "A", 0, 0, 0, "E")
			if got == nil || got.Source != tt.wantSource || got.Passable != tt.wantPassable {
				t.Fatalf("face after write = %+v, want source=%s passable=%v", got, tt.wantSource, tt.wantPassable)
			}
			if before.NoOp != tt.wantNoOp {
				t.Fatalf("before.NoOp=%v, want %v", before.NoOp, tt.wantNoOp)
			}
			if tt.wantNoOp {
				if !got.ZoneGuard {
					t.Fatal("no-op attribute patch cleared zone guard")
				}
				return
			}
			if got.ZoneGuard {
				t.Fatal("explicit face write retained zone guard")
			}
			if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
				t.Fatalf("undo applied=%v err=%v", applied, err)
			}
			restored := faceOf(t, store, id, "A", 0, 0, 0, "E")
			if restored == nil || restored.Source != EdgeSourceManual || !restored.ZoneGuard || !restored.Passable {
				t.Fatalf("undo did not restore guard: %+v", restored)
			}
		})
	}
}

func TestZoneMoveDoesNotGuardDanglingDerivedExit(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "dangling zone move", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{X: 0, Y: 0, L: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{X: 0, Y: 0, L: 0}}, ExpectedRevision: preview.Revision}); err != nil || !found {
		t.Fatalf("move dangling exit found=%v err=%v", found, err)
	}
	var got struct {
		Source    string
		ZoneGuard bool `gorm:"column:zone_guard"`
	}
	if err := store.DB().Raw(`SELECT source, zone_guard FROM map_edges
		WHERE map_set_id=? AND kind='grid' AND from_zone='B' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`, id).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Source != EdgeSourceParsed || got.ZoneGuard {
		t.Fatalf("dangling exit = %+v, want ordinary derived face", got)
	}
}

func TestZoneGuardSurvivesListAndForkCopy(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "guard fork", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `[]`},
		{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&MapEdge{}).
		Where("map_set_id=? AND kind=? AND from_zone='A' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'", id, EdgeKindGrid).
		Updates(map[string]any{"source": EdgeSourceManual, "zone_guard": true}).Error; err != nil {
		t.Fatal(err)
	}
	assertGuard := func(setID int64) {
		t.Helper()
		edges, err := store.ListMapEdges(setID)
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range edges {
			if edge.From() == (EdgeCell{Zone: "A", X: 0, Y: 0, L: 0}) && edge.Dir == "E" {
				if edge.Source != EdgeSourceManual || !edge.ZoneGuard {
					t.Fatalf("listed edge lost guard: %+v", edge)
				}
				return
			}
		}
		t.Fatal("guarded edge not listed")
	}
	assertGuard(id)
	forkID, err := store.ForkMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	assertGuard(forkID)
}

func TestWholeZoneRenameKeepsInternalEdgesDerived(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "whole rename", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `["E"]`, Doors: `["E"]`},
		{Zone: "A", X: 0, Y: 1, L: 0, EDirs: `["W"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", AllSource: true})
	if err != nil {
		t.Fatal(err)
	}
	before, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{
		SourceZone: "A", TargetZone: "B", AllSource: true, ExpectedRevision: p.Revision,
	})
	if err != nil || !found {
		t.Fatalf("rename found=%v err=%v", found, err)
	}
	for _, dir := range []string{"E", "W"} {
		var edge MapEdge
		if err := store.DB().Where("map_set_id=? AND kind=? AND from_zone='B' AND dir=?", id, EdgeKindGrid, dir).First(&edge).Error; err != nil {
			t.Fatal(err)
		}
		if edge.Source != EdgeSourceParsed || edge.ZoneGuard {
			t.Fatalf("internal %s face changed ownership on rename: %+v", dir, edge)
		}
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo rename applied=%v err=%v", applied, err)
	}
}

func TestTopologyWriteInvalidatesZonePreviewAtomically(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "revision fence", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}); err != nil || !found {
		t.Fatalf("concurrent topology write found=%v err=%v", found, err)
	}
	if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: preview.Revision}); err == nil || found {
		t.Fatalf("stale preview applied after another topology write: found=%v err=%v", found, err)
	}
}

func TestTopologyRevisionOverflowRollsBackMutation(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "revision overflow", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&MapSet{}).Where("id=?", id).Update("topology_revision", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}); err == nil || found {
		t.Fatalf("overflowing topology write found=%v err=%v", found, err)
	}
	r := roomAt(t, store, id, "A", 0, 0, 0)
	if r == nil || r.EDirs != "[]" {
		t.Fatalf("mutation survived failed revision increment: room=%+v", r)
	}
}

func TestTopologyNoOpDoesNotAdvanceRevision(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "revision no-op", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: `[]`, Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}); err != nil || !found {
		t.Fatalf("initial write found=%v err=%v", found, err)
	}
	set, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}})
	if err != nil || !found || !before.NoOp {
		t.Fatalf("no-op found=%v before=%+v err=%v", found, before, err)
	}
	after, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.TopologyRevision != set.TopologyRevision {
		t.Fatalf("no-op revision=%d want %d", after.TopologyRevision, set.TopologyRevision)
	}
}

func TestTopologyUndoRejectsSnapshotAfterSubsequentWrite(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "stale undo", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}})
	if err != nil || !found {
		t.Fatalf("first write found=%v err=%v", found, err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"E"}}); err != nil || !found {
		t.Fatalf("second write found=%v err=%v", found, err)
	}
	if applied, err := store.RestoreTopologyBeforeAtRevision(id, before, before.Revision+1); err == nil || applied {
		t.Fatalf("stale undo applied=%v err=%v", applied, err)
	}
	r := roomAt(t, store, id, "A", 0, 0, 0)
	if r == nil || r.EDirs != `["N","E"]` {
		t.Fatalf("stale undo changed current topology: room=%+v", r)
	}
}

func TestZoneMoveRevisionCASAcrossStoreInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
			t.Fatal(err)
		}
		return db
	}
	db1 := open()
	if err := MigrateForTest(db1); err != nil {
		t.Fatal(err)
	}
	if err := db1.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		t.Fatal(err)
	}
	db2 := open()
	store1, store2 := NewTestStore(db1), NewTestStore(db2)
	id, err := store1.CreateMapSet(MapSetInput{Name: "shared stores", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: "A", X: 1, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store1.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		found bool
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		store  *Store
		target string
		coord  ZoneCoord
	}{{store1, "B", ZoneCoord{0, 0, 0}}, {store2, "C", ZoneCoord{1, 0, 0}}} {
		wg.Add(1)
		go func(tc struct {
			store  *Store
			target string
			coord  ZoneCoord
		}) {
			defer wg.Done()
			<-start
			_, _, found, err := tc.store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: tc.target, Rooms: []ZoneCoord{tc.coord}, ExpectedRevision: p.Revision})
			results <- result{found: found, err: err}
		}(tc)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for res := range results {
		if res.err == nil && res.found {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent applies succeeded %d times, want exactly one", successes)
	}
	set, err := store1.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	if set.TopologyRevision != p.Revision+1 {
		t.Fatalf("revision=%d want %d", set.TopologyRevision, p.Revision+1)
	}
}

func TestZoneRenameEightThousandRooms(t *testing.T) {
	store := newMapperTestStore(t)
	rooms := make([]Room, zoneSelectionCap)
	for i := range rooms {
		rooms[i] = Room{Zone: "A", X: i * 2, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}
	}
	id, err := store.CreateMapSet(MapSetInput{Name: "8k rename", Rooms: rooms})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", AllSource: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Rooms != zoneSelectionCap {
		t.Fatalf("preview rooms=%d", p.Rooms)
	}
	before, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", AllSource: true, ExpectedRevision: p.Revision})
	if err != nil || !found {
		t.Fatalf("8k apply found=%v err=%v", found, err)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("8k undo applied=%v err=%v", applied, err)
	}
}
