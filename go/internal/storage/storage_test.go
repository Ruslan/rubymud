package storage

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRecentLogsLoadsButtonOverlays(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	store := &Store{db: db}
	for _, stmt := range []string{
		`CREATE TABLE log_entries (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, stream TEXT NOT NULL, window_name TEXT, raw_text TEXT NOT NULL, plain_text TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE log_overlays (id INTEGER PRIMARY KEY, log_entry_id INTEGER NOT NULL, overlay_type TEXT NOT NULL, payload_json TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec(%q): %v", stmt, err)
		}
	}

	logEntryID, err := store.AppendLogEntry(1, "R.I.P.", "R.I.P.")
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
