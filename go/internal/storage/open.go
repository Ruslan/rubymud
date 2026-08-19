package storage

import (
	"embed"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(path string) (*Store, error) {
	dsn, dbPath, err := ResolveSQLitePath(path)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	// Optimize SQLite performance
	if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
		return nil, fmt.Errorf("failed to enable WAL: %w", err)
	}
	if err := db.Exec("PRAGMA synchronous=NORMAL;").Error; err != nil {
		return nil, fmt.Errorf("failed to set synchronous=NORMAL: %w", err)
	}
	if err := db.Exec("PRAGMA foreign_keys=ON;").Error; err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Run embedded migrations with version tracking
	if err := runMigrations(db); err != nil {
		return nil, fmt.Errorf("migrations failed: %w", err)
	}

	store := &Store{db: db, dbPath: dbPath}
	if err := store.BackfillMissingSessionProfiles(); err != nil {
		return nil, err
	}
	// The `door` column of migration 018 is the one edge attribute the DERIVATION
	// produces (from rooms.doors), so sets whose rows were materialized before that
	// migration carry door=0 everywhere and mapper.doorGrade — which now asks the
	// edge — would see no doors at all. Recomputing it in SQL inside the migration
	// would restate the derivation rule in a second place, which is exactly what
	// materializing the edges was meant to end, so the migration only adds the
	// columns and the one Go rule fills them in here.
	//
	// THE COMPLETION FLAG IS SEPARATE FROM THE MIGRATION. Keying this off "was 018
	// applied in THIS call" made a crash between the two — the migration commits in
	// its own transaction, the backfill runs after — permanent and invisible: 018
	// stays recorded for ever, the backfill never runs again, some faces silently
	// keep no door, and `--rematerialize-edges` cannot repair them because it does
	// not touch hand-drawn rows. The window is under a second; the failure is not.
	// So the flag records that the DATA step finished, and an unfinished run simply
	// happens again on the next start. Idempotent, so a repeat is free.
	done, err := store.GetSetting(edgeDoorsBackfilledKey)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", edgeDoorsBackfilledKey, err)
	}
	if done == "" {
		if _, err := store.BackfillEdgeDoors(); err != nil {
			return nil, fmt.Errorf("materializing edge doors: %w", err)
		}
		if err := store.SetSetting(edgeDoorsBackfilledKey, "1"); err != nil {
			return nil, fmt.Errorf("recording %s: %w", edgeDoorsBackfilledKey, err)
		}
	}

	return store, nil
}

// ResolveSQLitePath returns the SQLite DSN unchanged where the driver needs it
// and, for a disk-backed database, its safe absolute filesystem path. Memory
// DSNs deliberately return an empty physical path. Callers that create sibling
// directories must use this instead of filepath.Dir(raw): a file: URI is not a
// filesystem path.
func ResolveSQLitePath(raw string) (dsn, dbPath string, err error) {
	if raw == ":memory:" {
		return raw, "", nil
	}
	if !strings.HasPrefix(strings.ToLower(raw), "file:") {
		clean, err := filepath.Abs(filepath.Clean(raw))
		if err != nil {
			return "", "", fmt.Errorf("resolve database path: %w", err)
		}
		return clean, clean, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse SQLite file URI: %w", err)
	}
	if u.Scheme != "file" {
		return "", "", fmt.Errorf("unsupported SQLite URI scheme %q", u.Scheme)
	}
	mode := u.Query().Get("mode")
	if mode == "memory" || u.Opaque == ":memory:" || u.Path == ":memory:" {
		return raw, "", nil
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", "", fmt.Errorf("SQLite file URI host %q is not a local filesystem path", u.Host)
	}
	path := u.Path
	if path == "" {
		path, err = url.PathUnescape(u.Opaque)
		if err != nil {
			return "", "", fmt.Errorf("unescape SQLite file URI path: %w", err)
		}
	}
	if path == ":memory:" {
		return raw, "", nil
	}
	if path == "" {
		return "", "", fmt.Errorf("SQLite file URI has no disk path")
	}
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", "", fmt.Errorf("resolve SQLite file URI path: %w", err)
	}
	return raw, clean, nil
}

// RoomImagesRoot is the filesystem root paired with this SQLite database. It is
// deliberately derived from the database path, not Server.configDir: --config-dir
// is allowed to point at an unrelated profile-export directory.
//
// A test store backed by an in-memory DB has no filesystem root and returns an
// empty string. Callers must treat that as "no managed image directory".
func (s *Store) RoomImagesRoot() string {
	if s == nil || s.dbPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.dbPath), "room_images")
}

// edgeDoorsBackfilledKey marks that migration 018's DATA half (rooms.doors moved
// onto the edge rows) has completed. Its absence — not the absence of the
// migration — is what schedules the work, so an interrupted run is retried instead
// of being lost. On a fresh database the backfill finds no map sets, writes the
// flag, and never runs again.
const edgeDoorsBackfilledKey = "mapper.edge_doors_backfilled"

// MigrateForTest applies the embedded SQL migrations to db. It exists so tests
// in other packages can build a schema identical to production (unique indexes,
// FK cascades, ALTERed columns) instead of a GORM AutoMigrate approximation.
// Not for production use — production goes through Open.
func MigrateForTest(db *gorm.DB) error {
	return runMigrations(db)
}

func runMigrations(db *gorm.DB) error {
	// Ensure migration tracking table exists
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY);`).Error; err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		var count int64
		if err := db.Raw("SELECT count(*) FROM schema_migrations WHERE version = ?", file).Scan(&count).Error; err != nil {
			return err
		}

		if count > 0 {
			continue // Already applied
		}

		content, err := migrationsFS.ReadFile("migrations/" + file)
		if err != nil {
			return err
		}

		// Apply migration in a transaction
		err = db.Transaction(func(tx *gorm.DB) error {
			skipSQL := false
			if file == "030_exact_corridors.sql" {
				var edgesTable int64
				if err := tx.Raw(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='map_edges'`).Scan(&edgesTable).Error; err != nil {
					return err
				}
				// Historical migration probes may intentionally stop before 016.
				// A production upgrade can only reach 030 after map_edges exists.
				skipSQL = edgesTable == 0
			}
			// 026 is intentionally a Go-assisted SQL migration. Its source data is
			// JSON text and seam resolution uses the one storage derivation rule, so
			// SQL cannot safely restate it. The audit, marker removal and DROP all
			// share this transaction; a mismatch leaves both legacy data and the
			// migration ledger untouched.
			if file == "026_drop_rooms_automaps.sql" {
				var roomsTable int64
				if err := tx.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'rooms'`).Scan(&roomsTable).Error; err != nil {
					return err
				}
				// A few focused historical migration tests deliberately build only
				// their owning tables. There is no mapper schema to retire there.
				if roomsTable == 0 {
					skipSQL = true
				} else if err := prepareAutomapsDrop(tx); err != nil {
					return err
				}
			}
			if file == "027_canonical_zone_names.sql" {
				var mapperTables int64
				if err := tx.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('rooms', 'map_edges', 'room_annotations')`).Scan(&mapperTables).Error; err != nil {
					return err
				}
				// Historical migration probes can deliberately fence 016 and build
				// only the pre-edge schema. A real database reaches 027 only after
				// all three mapper tables exist.
				if mapperTables != 3 {
					skipSQL = true
				} else if err := canonicalizePersistedZoneKeys(tx); err != nil {
					return err
				}
			}
			if !skipSQL {
				if err := tx.Exec(string(content)).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", file).Error; err != nil {
				return err
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("file %s: %w", file, err)
		}
	}

	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
