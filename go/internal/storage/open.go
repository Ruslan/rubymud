package storage

import (
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"rubymud/go/internal/config"
)

func Open(path string, hotkeys []config.Hotkey) (*Store, error) {
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
		&Profile{}, &SessionProfile{}, &HotkeyRule{},
	); err != nil {
		return nil, err
	}

	if err := migrateToProfiles(db, hotkeys); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func migrateToProfiles(db *gorm.DB, hotkeys []config.Hotkey) error {
	var count int64
	db.Model(&Profile{}).Count(&count)
	if count > 0 {
		return nil // Already migrated
	}

	type OldSession struct {
		ID   int64
		Name string
	}
	var sessions []OldSession
	db.Table("session_records").Scan(&sessions)

	// If there are no sessions yet, we just create a default profile so one always exists
	if len(sessions) == 0 {
		p := Profile{Name: "Default", Description: "Default profile"}
		db.Create(&p)
	}

	for _, sess := range sessions {
		p := Profile{Name: sess.Name}
		if p.Name == "" {
			p.Name = "Default"
		}
		if err := db.Create(&p).Error; err != nil {
			return err
		}

		db.Exec("UPDATE alias_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0", p.ID, sess.ID)
		db.Exec("UPDATE trigger_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0", p.ID, sess.ID)
		db.Exec("UPDATE highlight_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0", p.ID, sess.ID)

		sp := SessionProfile{
			SessionID:  sess.ID,
			ProfileID:  p.ID,
			OrderIndex: 1, // session profile is primary (highest priority, DESC ordering)
		}
		db.Create(&sp)
	}

	if len(hotkeys) > 0 {
		p := Profile{Name: "Hotkeys", Description: "Migrated hotkeys"}
		if err := db.Create(&p).Error; err != nil {
			return err
		}
		for i, hk := range hotkeys {
			db.Create(&HotkeyRule{
				ProfileID: p.ID,
				Position:  i + 1,
				Shortcut:  hk.Shortcut,
				Command:   hk.Command,
			})
		}
		for _, sess := range sessions {
			db.Create(&SessionProfile{
				SessionID:  sess.ID,
				ProfileID:  p.ID,
				OrderIndex: 0, // hotkeys are base layer (lowest priority)
			})
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
