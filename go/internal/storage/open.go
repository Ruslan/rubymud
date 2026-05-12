package storage

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(filepath.Clean(path)), &gorm.Config{
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

	store := &Store{db: db}
	if err := store.BackfillMissingSessionProfiles(); err != nil {
		return nil, err
	}

	return store, nil
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
			if err := tx.Exec(string(content)).Error; err != nil {
				return err
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
