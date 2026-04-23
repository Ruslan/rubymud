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

	store := NewTestStore(db)
	for _, stmt := range []string{
		`CREATE TABLE log_entries (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, buffer TEXT NOT NULL DEFAULT 'main', stream TEXT NOT NULL, window_name TEXT, raw_text TEXT NOT NULL, plain_text TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE log_overlays (id INTEGER PRIMARY KEY, log_entry_id INTEGER NOT NULL, overlay_type TEXT NOT NULL, payload_json TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("Exec(%q): %v", stmt, err)
		}
	}

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
