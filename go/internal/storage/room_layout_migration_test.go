package storage

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigration029UpgradesSchemaAt028(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-029.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY); INSERT INTO schema_migrations(version) VALUES ('029_room_layouts.sql')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable("room_layouts") {
		t.Fatal("029 unexpectedly applied while fenced")
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='029_room_layouts.sql'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("room_layouts") {
		t.Fatal("room_layouts missing")
	}
	var indexes int64
	if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_room_layouts_display','idx_room_layouts_zone')`).Scan(&indexes).Error; err != nil || indexes != 2 {
		t.Fatalf("indexes=%d err=%v", indexes, err)
	}
}
