package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRecentLogsLoadsButtonOverlays(t *testing.T) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	defer sqlDB.Close()

	// Apply canonical migrations
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)

	logEntryID, err := store.AppendLogEntry(1, "main", "R.I.P.", "R.I.P.")
	if err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}
	if err := store.AppendCommandHintToLatestLogEntry(1, "look"); err != nil {
		t.Fatalf("AppendCommandHintToLatestLogEntry: %v", err)
	}
	if err := store.AppendButtonOverlay(logEntryID, "loot", "get all corpse"); err != nil {
		t.Fatalf("AppendButtonOverlay: %v", err)
	}

	entries, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("RecentLogs entries = %d, want 1", len(entries))
	}
	if len(entries[0].Commands) != 1 || entries[0].Commands[0] != "look" {
		t.Fatalf("command overlays = %v, want [look]", entries[0].Commands)
	}
	if len(entries[0].Buttons) != 1 {
		t.Fatalf("button overlays = %v, want 1 button", entries[0].Buttons)
	}
	if entries[0].Buttons[0].Label != "loot" || entries[0].Buttons[0].Command != "get all corpse" {
		t.Fatalf("button overlay = %+v, want loot/get all corpse", entries[0].Buttons[0])
	}
}
