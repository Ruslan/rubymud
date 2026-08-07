package storage

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Migration 015 — the room-format reference column.
//
// 015 replaces 014's INTEGER FK sessions.mapper_profile_id with the TEXT column
// sessions.mapper_format, and must be correct in BOTH orders it can meet:
//
//	A. 014 was applied by an earlier release, possibly with real assignments,
//	   and 015 arrives later. This is the order that made a rewritten 014
//	   impossible (migrations are recorded by filename, so a rewrite would be
//	   skipped on exactly the databases that already have data).
//	B. A database that has never seen 014, where 014 and 015 run back to back.
//	   This is the owner's ACTUAL live database, which sits at 013.
//
// Both are exercised here against the real embedded SQL, not a hand-written
// approximation of it.
// ---------------------------------------------------------------------------

// openMigrationProbeDB opens an empty file-backed SQLite DB with the schema
// tracking table only, so a test can drive runMigrations by hand.
func openMigrationProbeDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.New().String() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func sessionHasColumn(t *testing.T, db *gorm.DB, col string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(
		`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = ?`, col).Scan(&n).Error; err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n > 0
}

// TestMigration015CarriesOverAnExisting014Assignment is ORDER A: stop the
// migration runner at 014, write assignments the way the shipped 014 build
// would have, then let 015 run.
func TestMigration015CarriesOverAnExisting014Assignment(t *testing.T) {
	db := openMigrationProbeDB(t)

	// Run everything EXCEPT 015 by pre-marking it applied — the same mechanism
	// runMigrations uses to skip, so this really is "the state after 014".
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY);`).Error; err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ('015_mapper_format.sql')`).Error; err != nil {
		t.Fatalf("fence 015: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate to 014: %v", err)
	}
	if !sessionHasColumn(t, db, "mapper_profile_id") {
		t.Fatal("precondition: 014 should have added mapper_profile_id")
	}
	if sessionHasColumn(t, db, "mapper_format") {
		t.Fatal("precondition: 015 must not have run yet")
	}

	// 014-era data: two assigned sessions and one left at the default (NULL).
	if err := db.Exec(`
		INSERT INTO mapper_profiles (id,name,spec,version) VALUES (7,'my-diku','{}',1),(9,'other','{}',1);
		INSERT INTO sessions (id,name,mud_host,mud_port,mapper_profile_id) VALUES
			(1,'assigned','127.0.0.1',4000,7),
			(2,'default','127.0.0.1',4000,NULL),
			(3,'assigned2','127.0.0.1',4000,9);
	`).Error; err != nil {
		t.Fatalf("seed 014-era data: %v", err)
	}

	// Now 015 arrives.
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version='015_mapper_format.sql'`).Error; err != nil {
		t.Fatalf("unfence 015: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrate 015: %v", err)
	}

	type row struct {
		ID           int64
		MapperFormat *string
	}
	var rows []row
	if err := db.Raw(`SELECT id, mapper_format FROM sessions ORDER BY id`).Scan(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d sessions, want 3", len(rows))
	}
	want := map[int64]*string{
		1: strptr(MapperFormatDBRef(7)),
		// NULL must STAY NULL. Rewriting it to an explicit 'builtin:rmud' would pin
		// every existing session to today's default forever instead of letting it
		// follow the default.
		2: nil,
		3: strptr(MapperFormatDBRef(9)),
	}
	for _, r := range rows {
		w := want[r.ID]
		switch {
		case w == nil && r.MapperFormat != nil:
			t.Errorf("session %d: mapper_format = %q, want NULL", r.ID, *r.MapperFormat)
		case w != nil && r.MapperFormat == nil:
			t.Errorf("session %d: mapper_format = NULL, want %q", r.ID, *w)
		case w != nil && *r.MapperFormat != *w:
			t.Errorf("session %d: mapper_format = %q, want %q", r.ID, *r.MapperFormat, *w)
		}
	}

	// The FK column is gone, and the profile rows it pointed at are untouched.
	if sessionHasColumn(t, db, "mapper_profile_id") {
		t.Error("015 must drop the old mapper_profile_id column")
	}
	var profiles int64
	if err := db.Raw(`SELECT count(*) FROM mapper_profiles`).Scan(&profiles).Error; err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if profiles != 2 {
		t.Errorf("mapper_profiles rows = %d, want 2 — 015 must not touch them", profiles)
	}
}

// TestMigration015OnADatabaseThatNeverSaw014 is ORDER B — and the owner's real
// case: the live database is at 013, so 014 and 015 run back to back on it.
func TestMigration015OnADatabaseThatNeverSaw014(t *testing.T) {
	db := openMigrationProbeDB(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if !sessionHasColumn(t, db, "mapper_format") {
		t.Fatal("mapper_format missing after a full migration run")
	}
	if sessionHasColumn(t, db, "mapper_profile_id") {
		t.Error("the old FK column must not survive a full migration run")
	}

	// A session created after the migration defaults to NULL = the default builtin.
	if err := db.Exec(
		`INSERT INTO sessions (id,name,mud_host,mud_port) VALUES (1,'s','127.0.0.1',4000)`).Error; err != nil {
		t.Fatalf("insert session: %v", err)
	}
	var ref *string
	if err := db.Raw(`SELECT mapper_format FROM sessions WHERE id=1`).Scan(&ref).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if ref != nil {
		t.Errorf("a new session's mapper_format = %q, want NULL (the default)", *ref)
	}
}

// TestMigrationsAreIdempotent: running the whole set twice must be a no-op the
// second time. 015 in particular would fail loudly on a re-run (duplicate
// column, then dropping a column that is gone), so this pins that the
// filename-based tracking really does fence it.
func TestMigrationsAreIdempotent(t *testing.T) {
	db := openMigrationProbeDB(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("second run must be a no-op, got: %v", err)
	}
}

func strptr(s string) *string { return &s }

// TestMigration015LiteralMatchesFormatRefForDB pins the ONE copy of the "db:"
// prefix that cannot import anything.
//
// The scheme lives in the dependency-free `mapref` package precisely so `mapper`
// and `storage` share a single definition. Migration 015 cannot: it is applied
// SQL, frozen history, and rewriting it would be skipped on every database that
// already ran it. So it hard-codes `'db:' || mapper_profile_id`, and this test is
// what keeps that literal honest.
//
// An earlier design claimed a test named TestStorageDBRefMatchesMapperRef pinned
// the duplication. That test did not exist. This one does — and it reads the
// migration's own bytes rather than restating the constant.
func TestMigration015LiteralMatchesFormatRefForDB(t *testing.T) {
	if got := MapperFormatDBRef(7); got != "db:7" {
		t.Errorf("MapperFormatDBRef(7) = %q, but migration 015 writes 'db:' || id, i.e. \"db:7\"", got)
	}

	sql, err := migrationsFS.ReadFile("migrations/015_mapper_format.sql")
	if err != nil {
		t.Fatalf("read 015: %v", err)
	}
	// The literal the migration actually concatenates. If someone changes the
	// scheme in mapref, this fails and points at the migration that cannot follow.
	if !strings.Contains(string(sql), "'db:' || mapper_profile_id") {
		t.Error("migration 015 no longer contains `'db:' || mapper_profile_id` — if the reference scheme changed, 015 CANNOT be edited (it is already applied); a new migration has to rewrite the column instead")
	}
}
