package storage

import (
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(filepath.Clean(path)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(
		&AppSetting{}, &SessionRecord{}, &Variable{},
		&AliasRule{}, &TriggerRule{}, &HighlightRule{},
		&Profile{}, &SessionProfile{}, &HotkeyRule{}, &ProfileVariable{},
		&LogRecord{}, &LogOverlay{}, &HistoryEntry{},
	); err != nil {
		return nil, err
	}

	store := &Store{db: db}
	if err := store.BackfillMissingSessionProfiles(); err != nil {
		return nil, err
	}

	return store, nil
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
