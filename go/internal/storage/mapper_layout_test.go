package storage

import (
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRoomLayoutIsPresentationOnlyAndForks(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRoomLayouts(id, []RoomLayout{{MapSetID: id, Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 4, DisplayY: 9, DisplayL: 0, Pinned: true}}); err != nil {
		t.Fatal(err)
	}
	slim, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	var moved *SlimRoom
	for i := range slim {
		if slim[i].X == -1 && slim[i].Y == 2 {
			moved = &slim[i]
		}
	}
	if moved == nil || moved.PX == nil || *moved.PX != 4 || moved.PY == nil || *moved.PY != 9 || moved.PL == nil || *moved.PL != 0 || !moved.Pinned {
		t.Fatalf("layout override missing from slim payload: %#v", moved)
	}
	rooms, _ := store.ListRooms(id)
	foundLogical := false
	for _, room := range rooms {
		if room.Zone == "Alpha" && room.X == -1 && room.Y == 2 && room.L == 0 {
			foundLogical = true
		}
	}
	if !foundLogical {
		t.Fatal("presentation write changed or removed logical coordinate {-1,2,0}")
	}

	fork, err := store.ForkMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	layouts, err := store.ListRoomLayouts(fork, "Alpha")
	if err != nil || len(layouts) != 1 || layouts[0].MapSetID != fork || layouts[0].DisplayY != 9 {
		t.Fatalf("fork layout = %#v, err=%v", layouts, err)
	}
}

func TestUpsertRoomLayoutsUsesBoundedQueries(t *testing.T) {
	store := newMapperTestStore(t)
	rooms := make([]Room, 100)
	layouts := make([]RoomLayout, 100)
	for i := range rooms {
		rooms[i] = Room{Zone: "Z", X: i, Y: 0, EDirs: "[]", Doors: "[]"}
		layouts[i] = RoomLayout{Zone: "Z", X: i, Y: 0, DisplayX: i, DisplayY: 10}
	}
	id, err := store.CreateMapSet(MapSetInput{Name: "batch", Rooms: rooms})
	if err != nil {
		t.Fatal(err)
	}
	counter := &queryCountingLogger{Interface: logger.Default.LogMode(logger.Silent)}
	store.db = store.db.Session(&gorm.Session{Logger: counter})
	if err := store.UpsertRoomLayouts(id, layouts); err != nil {
		t.Fatal(err)
	}
	if got := counter.queries.Load(); got > 6 {
		t.Fatalf("100 layouts issued %d SQL queries; want bounded bulk validation/write", got)
	}
}

func TestUpsertRoomLayoutsAcceptsExactEightThousandCap(t *testing.T) {
	store := newMapperTestStore(t)
	const count = 8000
	rooms := make([]Room, count)
	layouts := make([]RoomLayout, count)
	for i := range rooms {
		rooms[i] = Room{Zone: "Z", X: i, Y: 0, EDirs: "[]", Doors: "[]"}
		layouts[i] = RoomLayout{Zone: "Z", X: i, Y: 0, DisplayX: i, DisplayY: 100}
	}
	id, err := store.CreateMapSet(MapSetInput{Name: "layout cap", Rooms: rooms})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRoomLayouts(id, layouts); err != nil {
		t.Fatalf("exact cap rejected: %v", err)
	}
	got, err := store.ListRoomLayouts(id, "Z")
	if err != nil || len(got) != count {
		t.Fatalf("stored=%d err=%v, want %d", len(got), err, count)
	}
}

func TestRoomLayoutRejectsDisplayCollisionAtomically(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	err := store.UpsertRoomLayouts(id, []RoomLayout{
		{MapSetID: id, Zone: "Alpha", X: 0, Y: 0, L: 0, DisplayX: 7, DisplayY: 7},
		{MapSetID: id, Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 7, DisplayY: 7},
	})
	if err == nil {
		t.Fatal("expected display collision")
	}
	layouts, _ := store.ListRoomLayouts(id, "Alpha")
	if len(layouts) != 0 {
		t.Fatalf("partial layout write: %#v", layouts)
	}
}

func TestRoomLayoutRejectsCollisionWithImplicitRoomAndUntouchedOverride(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	// Alpha (0,0,0) has no override, so its effective display position is 0,0,0.
	err := store.UpsertRoomLayouts(id, []RoomLayout{{Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 0, DisplayY: 0, DisplayL: 0}})
	if err == nil {
		t.Fatal("expected collision with implicit logical display")
	}
	if got, _ := store.ListRoomLayouts(id, "Alpha"); len(got) != 0 {
		t.Fatalf("partial write: %#v", got)
	}

	if err := store.UpsertRoomLayouts(id, []RoomLayout{{Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 5, DisplayY: 5, DisplayL: 0}}); err != nil {
		t.Fatal(err)
	}
	err = store.UpsertRoomLayouts(id, []RoomLayout{{Zone: "Alpha", X: 0, Y: 0, L: 0, DisplayX: 5, DisplayY: 5, DisplayL: 0}})
	if err == nil {
		t.Fatal("expected collision with untouched override")
	}
}

func TestRoomLayoutBatchCanSwapPositions(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	if err := store.UpsertRoomLayouts(id, []RoomLayout{
		{Zone: "Alpha", X: 0, Y: 0, L: 0, DisplayX: 4, DisplayY: 4, DisplayL: 0},
		{Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 5, DisplayY: 5, DisplayL: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRoomLayouts(id, []RoomLayout{
		{Zone: "Alpha", X: 0, Y: 0, L: 0, DisplayX: 5, DisplayY: 5, DisplayL: 0},
		{Zone: "Alpha", X: -1, Y: 2, L: 0, DisplayX: 4, DisplayY: 4, DisplayL: 0},
	}); err != nil {
		t.Fatalf("atomic swap failed: %v", err)
	}
}

func TestRoomLayoutLifecycleDeleteMapSetAndRoomUndo(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(MapSetInput{Name: "lifecycle", Rooms: []Room{{Zone: "Z", X: 0, Y: 0, EDirs: "[]", Doors: "[]"}}})
	if err := store.UpsertRoomLayouts(id, []RoomLayout{{Zone: "Z", X: 0, Y: 0, DisplayX: 3, DisplayY: 4, DisplayL: 2}}); err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: "Z", X: 0, Y: 0, L: 0})
	if err != nil || !found {
		t.Fatalf("delete found=%v err=%v", found, err)
	}
	if got, _ := store.ListRoomLayouts(id, "Z"); len(got) != 1 {
		t.Fatalf("room delete should leave undoable layout orphan: %#v", got)
	}
	if ok, err := store.RestoreTopologyBefore(id, before); err != nil || !ok {
		t.Fatalf("undo ok=%v err=%v", ok, err)
	}
	if got, _ := store.ListRoomLayouts(id, "Z"); len(got) != 1 || got[0].DisplayL != 2 {
		t.Fatalf("undo lost layout: %#v", got)
	}

	id2, _ := store.CreateMapSet(MapSetInput{Name: "delete set", Rooms: []Room{{Zone: "Z", X: 0, Y: 0, EDirs: "[]", Doors: "[]"}}})
	if err := store.UpsertRoomLayouts(id2, []RoomLayout{{Zone: "Z", X: 0, Y: 0, DisplayX: 3, DisplayY: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMapSet(id2); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := store.db.Model(&RoomLayout{}).Where("map_set_id=?", id2).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("layout rows after set delete=%d err=%v", count, err)
	}
}
