package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSessionStoreTestStore(t *testing.T) *Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	return NewTestStore(db)
}

func TestSessionAnsiThemeDefaultsAndNormalizes(t *testing.T) {
	store := newSessionStoreTestStore(t)

	record, err := store.CreateSession("test", "example.org", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if record.AnsiTheme != "classic" {
		t.Fatalf("new session ansi theme = %q, want classic", record.AnsiTheme)
	}

	allowedThemes := []string{"high-contrast", "tango-dark", "dracula", "gruvbox-dark"}
	var loaded SessionRecord
	for _, theme := range allowedThemes {
		record.AnsiTheme = theme
		if err := store.UpdateSession(record); err != nil {
			t.Fatalf("UpdateSession %s: %v", theme, err)
		}
		var err error
		loaded, err = store.GetSession(record.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if loaded.AnsiTheme != theme {
			t.Fatalf("updated ansi theme = %q, want %q", loaded.AnsiTheme, theme)
		}
		record = loaded
	}

	loaded.AnsiTheme = "unexpected"
	if err := store.UpdateSession(loaded); err != nil {
		t.Fatalf("UpdateSession invalid theme: %v", err)
	}
	loaded, err = store.GetSession(record.ID)
	if err != nil {
		t.Fatalf("GetSession after invalid update: %v", err)
	}
	if loaded.AnsiTheme != "classic" {
		t.Fatalf("invalid ansi theme normalized to %q, want classic", loaded.AnsiTheme)
	}
}
