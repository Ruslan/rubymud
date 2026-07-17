package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newMapperTestStore(t *testing.T) *Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	return NewTestStore(db)
}

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }

func sampleInput() MapSetInput {
	tag1 := 1
	return MapSetInput{
		Name:          "Test World",
		SourceArchive: "test.zip",
		ZoneCount:     2,
		RoomCount:     3,
		SeamCount:     1,
		Rooms: []Room{
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Tag: ptrInt(1), Hint: "Alpha Start",
				EDirs: `["N","S"]`, Doors: `["N"]`, Ch: 3, Automaps: `["Beta|go|5"]`,
				BColor: ptrStr("clRed"), IsDT: true, Fingerprint: "fp-a0"},
			{Zone: "Alpha", X: -1, Y: 2, L: 0, Tag: &tag1, Hint: "Alpha North",
				EDirs: `["S"]`, Doors: `[]`, Ch: 2, Pipe: true, Fingerprint: "fp-a1"},
			{Zone: "Beta", X: 0, Y: 0, L: 1, Tag: ptrInt(5), Hint: "Beta Hub",
				EDirs: `[]`, Doors: `[]`, Ch: 0, ImageIndex: ptrInt(7),
				BColor: ptrStr("8404992"), Fingerprint: "fp-b0"},
		},
	}
}

func TestPatchRoomExits(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Alpha Start (0,0,0) starts with edirs [N,S], ch=3 (N|S), N is a door.
	// Add U, remove N.
	found, err := store.PatchRoomExits(id, "Alpha", 0, 0, 0, []string{"U"}, []string{"N"})
	if err != nil {
		t.Fatalf("PatchRoomExits: %v", err)
	}
	if !found {
		t.Fatal("expected room found")
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	var start *SlimRoom
	for i := range rooms {
		if rooms[i].X == 0 && rooms[i].Y == 0 && rooms[i].L == 0 {
			start = &rooms[i]
		}
	}
	if start == nil {
		t.Fatal("start room missing")
	}
	// edirs should be [S,U] in canonical order; ch = S(bit1)|U(bit4) = 2|16 = 18.
	if fmt.Sprint(start.E) != "[S U]" {
		t.Errorf("edirs = %v, want [S U]", start.E)
	}
	if start.Ch != (1<<1 | 1<<4) {
		t.Errorf("ch = %d, want %d (S|U)", start.Ch, 1<<1|1<<4)
	}
}

func TestPatchRoomExitsRoomNotFound(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	found, err := store.PatchRoomExits(id, "Alpha", 99, 99, 0, []string{"U"}, nil)
	if err != nil {
		t.Fatalf("PatchRoomExits: %v", err)
	}
	if found {
		t.Fatal("expected room NOT found for a bogus coord")
	}
}

func TestCreateAndListMapSet(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero map set id")
	}

	sets, err := store.ListMapSets()
	if err != nil {
		t.Fatalf("ListMapSets: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("got %d sets, want 1", len(sets))
	}
	if sets[0].Name != "Test World" || sets[0].ZoneCount != 2 || sets[0].RoomCount != 3 || sets[0].SeamCount != 1 {
		t.Errorf("unexpected set metadata: %+v", sets[0])
	}
	if sets[0].ImportedAt == nil {
		t.Error("ImportedAt not set")
	}
}

func TestListZones(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	zones, err := store.ListZones(id)
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2", len(zones))
	}
	// Ordered by zone name: Alpha (2 rooms) then Beta (1 room).
	if zones[0].Zone != "Alpha" || zones[0].RoomCount != 2 {
		t.Errorf("zone[0] = %+v, want Alpha/2", zones[0])
	}
	if zones[1].Zone != "Beta" || zones[1].RoomCount != 1 {
		t.Errorf("zone[1] = %+v, want Beta/1", zones[1])
	}
}

func TestListSlimRooms(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	if len(rooms) != 2 {
		t.Fatalf("got %d rooms, want 2", len(rooms))
	}
	// Ordered by l,x,y — the north room (x=-1) sorts before start (x=0).
	north := rooms[0]
	if north.X != -1 || north.H != "Alpha North" {
		t.Errorf("room[0] = %+v, want Alpha North @ x=-1", north)
	}
	if !north.P {
		t.Error("Alpha North should be a pipe")
	}
	start := rooms[1]
	if start.H != "Alpha Start" || !start.S {
		t.Errorf("room[1] = %+v, want Alpha Start (DT)", start)
	}
	if len(start.E) != 2 || start.E[0] != "N" {
		t.Errorf("edirs decode wrong: %v", start.E)
	}
	if len(start.D) != 1 || start.D[0] != "N" {
		t.Errorf("doors decode wrong: %v", start.D)
	}
	if len(start.A) != 1 || start.A[0] != "Beta|go|5" {
		t.Errorf("automaps decode wrong: %v", start.A)
	}
	if start.Img != 0 {
		t.Errorf("img = %d, want 0 (no image row)", start.Img)
	}
}

func TestSlimRoomImgFlag(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	// Attach an image to the first Beta room and confirm img flips to 1.
	var beta Room
	if err := store.DB().Where("map_set_id = ? AND zone = ?", id, "Beta").First(&beta).Error; err != nil {
		t.Fatalf("find beta room: %v", err)
	}
	if err := store.DB().Create(&RoomImage{RoomID: beta.ID, FullPath: "x.png"}).Error; err != nil {
		t.Fatalf("create image: %v", err)
	}
	rooms, _ := store.ListSlimRooms(id, "Beta")
	if len(rooms) != 1 || rooms[0].Img != 1 {
		t.Errorf("expected Beta room img=1, got %+v", rooms)
	}
}

// TestBColorRoundTrip confirms the bcolor column round-trips a string ident, a
// numeric string, and NULL byte-for-byte through the store — the exact risk
// flagged in review. The migration column is INTEGER affinity; glebarez binds a
// *string as text and SQLite does not coerce it, so all three survive.
func TestBColorRoundTrip(t *testing.T) {
	store := newMapperTestStore(t)
	in := MapSetInput{
		Name:      "Colors",
		ZoneCount: 1,
		RoomCount: 3,
		Rooms: []Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "ident", BColor: ptrStr("clRed"), Fingerprint: "a"},
			{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "numeric", BColor: ptrStr("8404992"), Fingerprint: "b"},
			{Zone: "Z", X: 2, Y: 0, L: 0, Hint: "null", BColor: nil, Fingerprint: "c"},
		},
	}
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	var rows []Room
	if err := store.DB().Where("map_set_id = ?", id).Order("x ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rooms, want 3", len(rows))
	}
	if rows[0].BColor == nil || *rows[0].BColor != "clRed" {
		t.Errorf("bcolor[0] = %v, want \"clRed\"", derefStr(rows[0].BColor))
	}
	if rows[1].BColor == nil || *rows[1].BColor != "8404992" {
		t.Errorf("bcolor[1] = %v, want \"8404992\"", derefStr(rows[1].BColor))
	}
	if rows[2].BColor != nil {
		t.Errorf("bcolor[2] = %v, want nil (NULL)", derefStr(rows[2].BColor))
	}
}

func derefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestActiveMapSetIDRoundTrip(t *testing.T) {
	store := newMapperTestStore(t)
	rec, err := store.CreateSession("s", "h", 1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Default: unset.
	if _, ok, err := store.GetActiveMapSetID(rec.ID); err != nil || ok {
		t.Fatalf("expected unset, got ok=%v err=%v", ok, err)
	}
	id, _ := store.CreateMapSet(sampleInput())
	if err := store.SetActiveMapSetID(rec.ID, id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	got, ok, err := store.GetActiveMapSetID(rec.ID)
	if err != nil || !ok || got != id {
		t.Fatalf("GetActiveMapSetID = (%d,%v,%v), want (%d,true,nil)", got, ok, err, id)
	}
	// Clearing sets NULL.
	if err := store.SetActiveMapSetID(rec.ID, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok, _ := store.GetActiveMapSetID(rec.ID); ok {
		t.Error("expected cleared active map set")
	}
}

func TestDeleteMapSetCascadesAndClearsSessions(t *testing.T) {
	store := newMapperTestStore(t)
	rec, _ := store.CreateSession("s", "h", 1)
	id, _ := store.CreateMapSet(sampleInput())
	store.SetActiveMapSetID(rec.ID, id)

	// Add an image so cascade removal is exercised.
	var room Room
	store.DB().Where("map_set_id = ?", id).First(&room)
	store.DB().Create(&RoomImage{RoomID: room.ID, FullPath: "x.png"})

	if err := store.DeleteMapSet(id); err != nil {
		t.Fatalf("DeleteMapSet: %v", err)
	}

	var roomCount, imgCount, setCount int64
	store.DB().Model(&Room{}).Where("map_set_id = ?", id).Count(&roomCount)
	store.DB().Model(&RoomImage{}).Count(&imgCount)
	store.DB().Model(&MapSet{}).Where("id = ?", id).Count(&setCount)
	if roomCount != 0 || imgCount != 0 || setCount != 0 {
		t.Errorf("after delete: rooms=%d images=%d sets=%d, want all 0", roomCount, imgCount, setCount)
	}
	if _, ok, _ := store.GetActiveMapSetID(rec.ID); ok {
		t.Error("session active_map_set_id should be NULL after delete")
	}
}
