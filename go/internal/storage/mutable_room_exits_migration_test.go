package storage

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigration034AddsEmptyMutableExitSetWithoutChangingRooms(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('034_mutable_room_exits.sql')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 033: %v", err)
	}
	if db.Migrator().HasColumn("rooms", "mutable_exits") {
		t.Fatal("fenced migration unexpectedly added mutable_exits")
	}
	if err := db.Exec(`INSERT INTO map_sets (id,name) VALUES (1,'legacy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO rooms (map_set_id,zone,x,y,hint,desc,edirs,doors) VALUES (1,'Z',3,4,'Box','still','["N"]','[]')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='034_mutable_room_exits.sql'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("apply 034: %v", err)
	}
	var got struct {
		Hint         string
		Desc         string
		MutableExits string
	}
	if err := db.Raw(`SELECT hint,desc,mutable_exits FROM rooms WHERE map_set_id=1 AND zone='Z' AND x=3 AND y=4`).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Hint != "Box" || got.Desc != "still" || got.MutableExits != "[]" {
		t.Fatalf("upgraded room=%+v", got)
	}
}
