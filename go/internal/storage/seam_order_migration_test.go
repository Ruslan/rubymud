package storage

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigration023BackfillsExactLegacySeamOccurrences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre023.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create migrations table: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('023_map_edge_seam_order.sql')`).Error; err != nil {
		t.Fatalf("fence 023: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatalf("fence 026: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 022: %v", err)
	}
	if edgeHasColumn(t, db, "seam_order") {
		t.Fatal("fencing 023 did not produce a pre-023 schema")
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name, seam_count) VALUES (1, 'pre-023', 3)`).Error; err != nil {
		t.Fatalf("insert set: %v", err)
	}
	if err := db.Exec(`INSERT INTO rooms (map_set_id, zone, x, y, l, hint, exits, edirs, doors, ch, automaps)
		VALUES (1,'A',0,0,0,'source','','[]','[]',0,'["B|go|7","bad|only-two","B|go|7"]'),
		       (1,'B',2,3,0,'target','','[]','[]',0,'[]')`).Error; err != nil {
		t.Fatalf("insert rooms: %v", err)
	}
	if err := db.Exec(`UPDATE rooms SET tag=7 WHERE map_set_id=1 AND zone='B'`).Error; err != nil {
		t.Fatalf("tag target: %v", err)
	}
	// The pre-023 binary stored valid occurrences but discarded malformed ones and
	// had no occurrence column, so identical selectors differed only by row id.
	if err := db.Exec(`INSERT INTO map_edges
		(map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, command, target_zone, target_tag, to_zone, to_x, to_y, to_l, weight)
		VALUES (1,'seam','parsed',1,'A',0,0,0,'go','B',7,'B',2,3,0,1),
		       (1,'seam','parsed',1,'A',0,0,0,'go','B',7,'B',2,3,0,1)`).Error; err != nil {
		t.Fatalf("insert old seams: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='023_map_edge_seam_order.sql'`).Error; err != nil {
		t.Fatalf("unfence 023: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='026_drop_rooms_automaps.sql'`).Error; err != nil {
		t.Fatalf("unfence 026: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer store.Close()
	edges, err := store.ListMapEdges(1)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var seams []MapEdge
	for _, edge := range edges {
		if edge.Kind == EdgeKindSeam {
			seams = append(seams, edge)
		}
	}
	if len(seams) != 3 {
		t.Fatalf("seam rows = %d, want exactly 3 raw occurrences: %+v", len(seams), seams)
	}
	for i, edge := range seams {
		if edge.SeamOrder != i {
			t.Fatalf("seam %d order = %d, want %d: %+v", i, edge.SeamOrder, i, edge)
		}
	}
	if seams[1].TargetZone != "bad" || seams[1].Command != "only-two" || seams[1].TargetTag != nil {
		t.Fatalf("malformed occurrence metadata lost: %+v", seams[1])
	}
}
