package storage

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func pre030DB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pre-030.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='030_exact_corridors.sql'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DROP INDEX IF EXISTS idx_map_edges_directional_face_unique; CREATE UNIQUE INDEX idx_map_edges_grid_unique ON map_edges(map_set_id,from_zone,from_x,from_y,from_l,dir) WHERE kind='grid'`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigration030ReplacesDirectionalFaceIndex(t *testing.T) {
	db := pre030DB(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_map_edges_directional_face_unique'`).Scan(&n).Error; err != nil || n != 1 {
		t.Fatalf("index count=%d err=%v", n, err)
	}
	row := `INSERT INTO map_edges(map_set_id,kind,source,passable,from_zone,from_x,from_y,from_l,dir,to_zone,to_x,to_y,to_l,command,target_zone,seam_order,weight,phrase,door,locked,open_cmd,no_route,zone_guard) VALUES(1,?, 'manual',1,'Z',0,0,0,'N','Z',1,0,0,'n','',0,1,'',0,0,'',0,0)`
	if err := db.Exec(row, EdgeKindGrid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(row, EdgeKindCorridor).Error; err == nil {
		t.Fatal("shared grid/corridor face index accepted a duplicate")
	}
}

func TestMigration030RejectsDuplicateGridCorridorFace(t *testing.T) {
	db := pre030DB(t)
	row := `INSERT INTO map_edges(map_set_id,kind,source,passable,from_zone,from_x,from_y,from_l,dir,to_zone,to_x,to_y,to_l,command,target_zone,seam_order,weight,phrase,door,locked,open_cmd,no_route,zone_guard) VALUES(1,?, 'manual',1,'Z',0,0,0,'N','Z',1,0,0,'n','',0,1,'',0,0,'',0,0)`
	if err := db.Exec(row, EdgeKindGrid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(row, EdgeKindCorridor).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err == nil {
		t.Fatal("duplicate directional face upgrade succeeded")
	}
}
