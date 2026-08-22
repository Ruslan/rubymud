package storage

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestMigration026BackfillsAndDropsLegacyAutomaps exercises the complete upgrade
// path, including a database that predates map_edges itself. Every raw
// occurrence must become exactly one ordered seam row before the source column is
// removed; malformed/dead seams are data too, not entries to silently discard.
func TestMigration026BackfillsAndDropsLegacyAutomaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-016.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create migrations: %v", err)
	}
	// Build the real schema up to 015, so map_edges, its later columns/indexes and
	// the drop migration all run in production order over pre-existing room data.
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES
		('016_map_edges.sql'), ('017_map_edges_passable.sql'), ('018_map_edge_attrs.sql'),
		('019_map_edge_weight.sql'), ('020_manual_seam_attrs.sql'), ('021_log_buffer_cursor_index.sql'),
		('022_mcp_settings.sql'), ('023_map_edge_seam_order.sql'), ('024_exact_portal_endpoint.sql'),
		('025_map_set_topology_revision.sql'), ('025a_map_edge_zone_guard.sql'),
		('026_drop_rooms_automaps.sql'), ('027_canonical_zone_names.sql'),
		('028_room_inhabitants.sql'), ('029_room_layouts.sql'), ('030_exact_corridors.sql'),
		('031_app_language.sql'), ('032_autorecord_provenance.sql'),
		('033_autorecord_bidirectional.sql'), ('034_mutable_room_exits.sql')`).Error; err != nil {
		t.Fatalf("fence future migrations: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 015: %v", err)
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'legacy')`).Error; err != nil {
		t.Fatalf("insert set: %v", err)
	}
	if err := db.Exec(`INSERT INTO rooms (map_set_id, zone, x, y, l, tag, edirs, doors, ch, automaps) VALUES
		(1, 'A', 0, 0, 0, 1, '[]', '[]', 0, '["B|go|7","B|go|7","Dead|gone|99","B|partial","A|loop|2","Dead \"Quote\"|gone|100","B|  go  |7"]'),
		(1, 'A', 1, 0, 0, 2, '[]', '[]', 0, '[]'),
		(1, 'B', 2, 3, 0, 7, '[]', '[]', 0, '[]')`).Error; err != nil {
		t.Fatalf("insert legacy rooms: %v", err)
	}
	if err := db.Exec(`INSERT INTO room_images (id, room_id, prompt) VALUES (11, 1, 'keep-room-id')`).Error; err != nil {
		t.Fatalf("insert child room image: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version >= '016_map_edges.sql'`).Error; err != nil {
		t.Fatalf("unfence migrations: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer store.Close()
	assertNoAutomapsColumn(t, store.db)
	var roomIDs []int64
	if err := store.db.Model(&Room{}).Where("map_set_id = ?", 1).Order("id ASC").Pluck("id", &roomIDs).Error; err != nil || len(roomIDs) != 3 || roomIDs[0] != 1 || roomIDs[1] != 2 || roomIDs[2] != 3 {
		t.Fatalf("room ids after DROP = %v, err=%v; want [1 2 3]", roomIDs, err)
	}
	var image RoomImage
	if err := store.db.First(&image, 11).Error; err != nil || image.RoomID != 1 || image.Prompt != "keep-room-id" {
		t.Fatalf("room image after DROP = %+v, err=%v", image, err)
	}
	var roomIndexes int64
	if err := store.db.Raw(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_rooms_coord','idx_rooms_fingerprint','idx_rooms_zone')`).Scan(&roomIndexes).Error; err != nil || roomIndexes != 3 {
		t.Fatalf("retained room indexes=%d err=%v, want 3", roomIndexes, err)
	}
	var fingerprintDefault string
	if err := store.db.Raw(`SELECT dflt_value FROM pragma_table_info('rooms') WHERE name='fingerprint'`).Scan(&fingerprintDefault).Error; err != nil || fingerprintDefault != "''" {
		t.Fatalf("fingerprint default=%q err=%v, want two quotes", fingerprintDefault, err)
	}
	assertSQLiteChecks(t, store.db)
	edges, err := store.ListMapEdges(1)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var got []MapEdge
	for _, e := range edges {
		if e.Kind == EdgeKindSeam {
			got = append(got, e)
		}
	}
	if len(got) != 7 {
		t.Fatalf("seams = %d, want all 7 legacy occurrences: %+v", len(got), got)
	}
	for order, e := range got {
		if e.FromZone != "A" || e.FromX != 0 || e.FromY != 0 || e.FromL != 0 || e.SeamOrder != order {
			t.Fatalf("seam %d lost source/order: %+v", order, e)
		}
	}
	if got[0].TargetZone != "B" || got[1].TargetZone != "B" || got[0].TargetTag == nil || got[1].TargetTag == nil || *got[0].TargetTag != 7 || *got[1].TargetTag != 7 {
		t.Fatalf("duplicate occurrences not retained exactly: %+v", got[:2])
	}
	if _, ok := got[2].To(); ok || got[2].TargetZone != "Dead" || got[2].TargetTag == nil || *got[2].TargetTag != 99 {
		t.Fatalf("dead seam lost: %+v", got[2])
	}
	if _, ok := got[3].To(); ok || got[3].TargetZone != "B" || got[3].TargetTag != nil || got[3].Command != "partial" {
		t.Fatalf("partial seam lost: %+v", got[3])
	}
	to, ok := got[4].To()
	if !ok || to != (EdgeCell{Zone: "A", X: 1, Y: 0, L: 0}) || got[4].TargetZone != "A" {
		t.Fatalf("same-zone seam lost: %+v", got[4])
	}
	if got[5].TargetZone != `Dead "Quote"` || got[5].TargetTag == nil || *got[5].TargetTag != 100 {
		t.Fatalf("escaped JSON seam lost: %+v", got[5])
	}
	if got[6].Command != "  go  " || got[6].TargetZone != "B" {
		t.Fatalf("command whitespace changed: %+v", got[6])
	}
}

func TestZoneGuardMigrationUpgradesDatabaseThatAlreadyApplied026(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-026.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES
		('025a_map_edge_zone_guard.sql'), ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatal(err)
	}
	// Reproduce a database migrated by the previous binary: 026 is complete, while
	// 025a did not exist yet and map_edges therefore has no zone_guard column.
	if err := runMigrations(db); err != nil {
		t.Fatalf("build pre-025a schema: %v", err)
	}
	if err := db.Exec(`ALTER TABLE rooms DROP COLUMN automaps;
		DELETE FROM schema_migrations WHERE version='025a_map_edge_zone_guard.sql';
		INSERT INTO map_sets (id, name) VALUES (1, 'old');
		INSERT INTO map_edges (map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, dir, weight)
		VALUES (1, 'grid', 'manual', 1, 'A', 0, 0, 0, 'E', 1)`).Error; err != nil {
		t.Fatalf("construct post-026 database: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("apply prospective guard migration: %v", err)
	}
	var columns, guarded int64
	if err := db.Raw(`SELECT count(*) FROM pragma_table_info('map_edges') WHERE name='zone_guard'`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT count(*) FROM map_edges WHERE zone_guard<>0`).Scan(&guarded).Error; err != nil {
		t.Fatal(err)
	}
	if columns != 1 || guarded != 0 {
		t.Fatalf("historical upgrade columns=%d guarded_rows=%d, want 1 and 0", columns, guarded)
	}
}

// A completed old authority migration means map_edges was already the source of
// truth. 026 must not overwrite a later discrepancy from raw provenance before
// dropping it: it has to fail as one transaction and leave the old schema intact.
func TestMigration026RefusesLegacyAuthorityMismatchAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mismatch.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create migrations: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatalf("fence 026: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 025: %v", err)
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'mismatch');
		INSERT INTO rooms (map_set_id, zone, x, y, l, edirs, doors, ch, automaps) VALUES (1, 'A', 0, 0, 0, '[]', '[]', 0, '["B|raw|7"]');
		INSERT INTO app_settings (key, value) VALUES ('mapper.edge_authority_backfilled_v1', '1');
		INSERT INTO map_edges (map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, command, target_zone, target_tag, seam_order, weight)
		VALUES (1, 'seam', 'parsed', 1, 'A', 0, 0, 0, 'different', 'B', 7, 0, 1);`).Error; err != nil {
		t.Fatalf("seed mismatch: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql'`).Error; err != nil {
		t.Fatalf("unfence 026: %v", err)
	}
	if err := runMigrations(db); err == nil || !strings.Contains(err.Error(), "legacy automaps audit") {
		t.Fatalf("runMigrations mismatch error = %v, want audit refusal", err)
	}
	if !hasTableColumn(t, db, "rooms", "automaps") {
		t.Fatal("failed audit still dropped rooms.automaps")
	}
	var applied int64
	if err := db.Raw(`SELECT count(*) FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql'`).Scan(&applied).Error; err != nil || applied != 0 {
		t.Fatalf("failed audit recorded 026: count=%d err=%v", applied, err)
	}
	var marker int64
	if err := db.Raw(`SELECT count(*) FROM app_settings WHERE key='mapper.edge_authority_backfilled_v1' AND value='1'`).Scan(&marker).Error; err != nil || marker != 1 {
		t.Fatalf("failed audit changed completion marker: count=%d err=%v", marker, err)
	}
	var command string
	if err := db.Raw(`SELECT command FROM map_edges WHERE map_set_id=1 AND kind='seam'`).Scan(&command).Error; err != nil || command != "different" {
		t.Fatalf("failed audit changed authoritative seam: command=%q err=%v", command, err)
	}
}

func TestMigration026RefusesInvalidLegacySeamSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-source.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatalf("fence 026: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 025: %v", err)
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'invalid-source');
		INSERT INTO rooms (map_set_id, zone, x, y, l, edirs, doors, ch, automaps)
		VALUES (1, 'A', 0, 0, 0, '[]', '[]', 0, '[]');
		INSERT INTO app_settings (key, value) VALUES ('mapper.edge_authority_backfilled_v1', '1');
		INSERT INTO map_edges (map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, command, target_zone, target_tag, seam_order, weight)
		VALUES (1, 'seam', 'grid', 1, 'A', 0, 0, 0, 'go', 'B', 7, 0, 1);
		DELETE FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql';`).Error; err != nil {
		t.Fatalf("seed invalid source: %v", err)
	}

	if err := runMigrations(db); err == nil || !strings.Contains(err.Error(), "legacy automaps audit") {
		t.Fatalf("runMigrations invalid source error = %v, want audit refusal", err)
	}
	if !hasTableColumn(t, db, "rooms", "automaps") {
		t.Fatal("invalid source audit still dropped rooms.automaps")
	}
}

func TestMigration026RefusesMalformedAutomapsJSONAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed-json.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatalf("fence 026: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 025: %v", err)
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'malformed-json');
		INSERT INTO rooms (map_set_id, zone, x, y, l, edirs, doors, ch, automaps)
		VALUES (1, 'A', 0, 0, 0, '[]', '[]', 0, '{"not":"an array"}');
		DELETE FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql';`).Error; err != nil {
		t.Fatalf("seed malformed JSON: %v", err)
	}

	if err := runMigrations(db); err == nil || !strings.Contains(err.Error(), "decoding legacy automaps") {
		t.Fatalf("runMigrations malformed JSON error = %v, want decode refusal", err)
	}
	if !hasTableColumn(t, db, "rooms", "automaps") {
		t.Fatal("malformed JSON audit still dropped rooms.automaps")
	}
	var edgeCount int64
	if err := db.Model(&MapEdge{}).Count(&edgeCount).Error; err != nil || edgeCount != 0 {
		t.Fatalf("malformed JSON rollback edges = %d, err=%v; want 0", edgeCount, err)
	}
}

func TestMigration026RollsBackBackfillWhenDropFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drop-failure.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES ('026_drop_rooms_automaps.sql')`).Error; err != nil {
		t.Fatalf("fence 026: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate through 025: %v", err)
	}
	if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'drop-failure');
		INSERT INTO rooms (map_set_id, zone, x, y, l, edirs, doors, ch, automaps)
		VALUES (1, 'A', 0, 0, 0, '[]', '[]', 0, '["B|go|7"]');
		CREATE INDEX force_drop_failure ON rooms(automaps);
		DELETE FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql';`).Error; err != nil {
		t.Fatalf("seed drop failure: %v", err)
	}

	if err := runMigrations(db); err == nil || !strings.Contains(err.Error(), "026_drop_rooms_automaps.sql") {
		t.Fatalf("runMigrations drop error = %v, want 026 failure", err)
	}
	if !hasTableColumn(t, db, "rooms", "automaps") {
		t.Fatal("failed DROP removed rooms.automaps")
	}
	var edgeCount, markerCount, migrationCount int64
	if err := db.Model(&MapEdge{}).Count(&edgeCount).Error; err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if err := db.Raw(`SELECT count(*) FROM app_settings WHERE key = 'mapper.edge_authority_backfilled_v1'`).Scan(&markerCount).Error; err != nil {
		t.Fatalf("count marker: %v", err)
	}
	if err := db.Raw(`SELECT count(*) FROM schema_migrations WHERE version = '026_drop_rooms_automaps.sql'`).Scan(&migrationCount).Error; err != nil {
		t.Fatalf("count migration: %v", err)
	}
	if edgeCount != 0 || markerCount != 0 || migrationCount != 0 {
		t.Fatalf("failed DROP left half-state: edges=%d marker=%d migration=%d", edgeCount, markerCount, migrationCount)
	}
}

func TestMigration026RealDatabaseBackup(t *testing.T) {
	path := os.Getenv("RUBYMUD_REAL_DB_BACKUP")
	if path == "" {
		t.Skip("set RUBYMUD_REAL_DB_BACKUP to an expendable SQLite backup")
	}
	backupInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if liveInfo, statErr := os.Stat(filepath.Join("..", "..", "..", "data", "mudhost.db")); statErr == nil && os.SameFile(backupInfo, liveInfo) {
		t.Fatal("RUBYMUD_REAL_DB_BACKUP points at data/mudhost.db; refusing to mutate live data")
	}

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open backup before migration: %v", err)
	}
	roomDigest := sqliteRowsDigest(t, db, `SELECT json_array(id,map_set_id,zone,x,y,l,dx,dy,dl,tag,hint,desc,exits,edirs,doors,ch,imageindex,note,is_dt,pipe,bcolor,fingerprint) FROM rooms ORDER BY id`)
	manualDigest := sqliteRowsDigest(t, db, `SELECT json_array(id,map_set_id,kind,source,passable,from_zone,from_x,from_y,from_l,dir,to_zone,to_x,to_y,to_l,command,target_zone,target_tag,weight,phrase,door,locked,open_cmd,no_route) FROM map_edges WHERE kind='seam' AND source='manual' ORDER BY id`)
	var beforeRooms, beforeRoutes int64
	if err := db.Raw(`SELECT count(*) FROM rooms`).Scan(&beforeRooms).Error; err != nil {
		t.Fatalf("count rooms before: %v", err)
	}
	if err := db.Raw(`SELECT count(*) FROM map_edges WHERE kind='seam' AND to_zone IS NOT NULL`).Scan(&beforeRoutes).Error; err != nil {
		t.Fatalf("count routes before: %v", err)
	}
	var setIDs []int64
	if err := db.Model(&MapSet{}).Order("id ASC").Pluck("id", &setIDs).Error; err != nil {
		t.Fatalf("list sets before: %v", err)
	}
	expected := make(map[int64][]MapEdge, len(setIDs))
	for _, setID := range setIDs {
		legacy, loadErr := loadLegacyRoomsTx(db, setID)
		if loadErr != nil {
			t.Fatalf("load set %d before: %v", setID, loadErr)
		}
		rooms, drafts, splitErr := splitLegacyRooms(legacy)
		if splitErr != nil {
			t.Fatalf("split set %d before: %v", setID, splitErr)
		}
		for _, edge := range DeriveImportedEdges(setID, rooms, drafts) {
			if edge.Kind == EdgeKindSeam {
				expected[setID] = append(expected[setID], edge)
			}
		}
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open backup upgrade: %v", err)
	}
	defer store.Close()
	if got := sqliteRowsDigest(t, store.db, `SELECT json_array(id,map_set_id,zone,x,y,l,dx,dy,dl,tag,hint,desc,exits,edirs,doors,ch,imageindex,note,is_dt,pipe,bcolor,fingerprint) FROM rooms ORDER BY id`); got != roomDigest {
		t.Fatal("room identities or retained fields changed")
	}
	if got := sqliteRowsDigest(t, store.db, `SELECT json_array(id,map_set_id,kind,source,passable,from_zone,from_x,from_y,from_l,dir,to_zone,to_x,to_y,to_l,command,target_zone,target_tag,weight,phrase,door,locked,open_cmd,no_route) FROM map_edges WHERE kind='seam' AND source='manual' ORDER BY id`); got != manualDigest {
		t.Fatal("manual seam identity, endpoint, or attributes changed")
	}
	var afterRooms, afterRoutes int64
	if err := store.db.Raw(`SELECT count(*) FROM rooms`).Scan(&afterRooms).Error; err != nil || afterRooms != beforeRooms {
		t.Fatalf("rooms after = %d, err=%v; before=%d", afterRooms, err, beforeRooms)
	}
	if err := store.db.Raw(`SELECT count(*) FROM map_edges WHERE kind='seam' AND to_zone IS NOT NULL`).Scan(&afterRoutes).Error; err != nil || afterRoutes != beforeRoutes {
		t.Fatalf("resolved seam routes after = %d, err=%v; before=%d", afterRoutes, err, beforeRoutes)
	}
	for setID, want := range expected {
		var got []MapEdge
		if err := store.db.Where("map_set_id = ? AND kind = ?", setID, EdgeKindSeam).Find(&got).Error; err != nil {
			t.Fatalf("load seams after set %d: %v", setID, err)
		}
		for _, occurrence := range want {
			matches := 0
			for _, edge := range got {
				if sameLegacySeam(occurrence, edge) {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("set %d occurrence order %d matched %d seams after migration", setID, occurrence.SeamOrder, matches)
			}
		}
	}
	assertNoAutomapsColumn(t, store.db)
	var marker int64
	if err := store.db.Raw(`SELECT count(*) FROM app_settings WHERE key='mapper.edge_authority_backfilled_v1'`).Scan(&marker).Error; err != nil || marker != 0 {
		t.Fatalf("retired marker count=%d err=%v", marker, err)
	}
	assertSQLiteChecks(t, store.db)
}

func sqliteRowsDigest(t *testing.T, db *gorm.DB, query string) [sha256.Size]byte {
	t.Helper()
	rows, err := db.Raw(query).Rows()
	if err != nil {
		t.Fatalf("digest query: %v", err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("digest scan: %v", err)
		}
		_, _ = h.Write([]byte(line))
		_, _ = h.Write([]byte{0})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("digest rows: %v", err)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func assertSQLiteChecks(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows, err := db.Raw(`PRAGMA foreign_key_check`).Rows()
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if rows.Next() {
		_ = rows.Close()
		t.Fatal("foreign_key_check reported a violation")
	}
	_ = rows.Close()
	var integrity string
	if err := db.Raw(`PRAGMA integrity_check`).Scan(&integrity).Error; err != nil || integrity != "ok" {
		t.Fatalf("integrity_check=%q err=%v", integrity, err)
	}
}

func assertNoAutomapsColumn(t *testing.T, db *gorm.DB) {
	t.Helper()
	if hasTableColumn(t, db, "rooms", "automaps") {
		t.Fatal("rooms.automaps still exists")
	}
}

func hasTableColumn(t *testing.T, db *gorm.DB, table, column string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n).Error; err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	return n != 0
}
