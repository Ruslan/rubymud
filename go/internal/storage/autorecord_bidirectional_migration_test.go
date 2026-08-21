package storage

import "testing"

func TestMigration033RepairsGeneratedAutorecordTombstoneOnly(t *testing.T) {
	store := newMapperTestStore(t)
	setID, err := store.CreateMapSet(MapSetInput{Rooms: []Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Перед развалинами", Desc: "развалины", EDirs: `["E"]`, Doors: `[]`},
		{Zone: "Z", X: 0, Y: 1, L: 0, Hint: "Еле заметная тропинка", Desc: "карниз", EDirs: `["W"]`, Doors: `[]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Where("map_set_id=?", setID).Delete(&MapEdge{}).Error; err != nil {
		t.Fatal(err)
	}
	toZone, toX, toY, toL := "Z", 0, 1, 0
	fromZone, fromX, fromY, fromL := "Z", 0, 0, 0
	edges := []MapEdge{
		{MapSetID: setID, Kind: EdgeKindGrid, Source: EdgeSourceReconciled, Passable: false, FromZone: fromZone, FromX: fromX, FromY: fromY, FromL: fromL, Dir: "E", ToZone: &toZone, ToX: &toX, ToY: &toY, ToL: &toL, EdgeAttrs: EdgeAttrs{Weight: 1}},
		{MapSetID: setID, Kind: EdgeKindCorridor, Source: EdgeSourceAutorecord, Passable: true, FromZone: toZone, FromX: toX, FromY: toY, FromL: toL, Dir: "W", ToZone: &fromZone, ToX: &fromX, ToY: &fromY, ToL: &fromL, Command: "запад", EdgeAttrs: EdgeAttrs{Weight: 1}},
		{MapSetID: setID, Kind: EdgeKindGrid, Source: EdgeSourceManual, Passable: false, FromZone: fromZone, FromX: fromX, FromY: fromY, FromL: fromL, Dir: "N", EdgeAttrs: EdgeAttrs{Weight: 1}},
	}
	if err := store.DB().Create(&edges).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`DELETE FROM schema_migrations WHERE version='033_autorecord_bidirectional.sql'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(store.DB()); err != nil {
		t.Fatal(err)
	}
	var repaired MapEdge
	if err := store.DB().Where("map_set_id=? AND from_zone='Z' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'", setID).First(&repaired).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.Kind != EdgeKindCorridor || repaired.Source != EdgeSourceAutorecord || !repaired.Passable || repaired.Command != "e" {
		t.Fatalf("repaired face=%+v", repaired)
	}
	var manual MapEdge
	if err := store.DB().Where("map_set_id=? AND from_zone='Z' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='N'", setID).First(&manual).Error; err != nil {
		t.Fatal(err)
	}
	if manual.Source != EdgeSourceManual || manual.Passable {
		t.Fatalf("migration changed explicit one-way/negation face=%+v", manual)
	}
}
