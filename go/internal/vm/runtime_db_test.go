package vm

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/storage"
)

func TestProcessInputDetailedLoadsLatestStateFromDB(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	if err := store.SetVariable(1, "weapon", "sword"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if err := store.SaveAlias(1, "eq", "wield $weapon"); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}

	results := v.ProcessInputDetailed("eq")
	if len(results) != 1 {
		t.Fatalf("ProcessInputDetailed(eq) = %v, want 1 result", results)
	}
	if results[0].Kind != ResultCommand || results[0].Text != "wield sword" {
		t.Fatalf("ProcessInputDetailed(eq) = %+v, want command wield sword", results[0])
	}
}

func TestMatchTriggersLoadsLatestStateFromDB(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	if err := store.SaveTrigger(1, `^You are thirsty\.`, "drink all", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}

	effects := v.MatchTriggers("You are thirsty.", 1)
	if len(effects) != 1 || effects[0].Command != "drink all" {
		t.Fatalf("MatchTriggers = %v, want one send effect", effects)
	}
}

func TestApplyHighlightsLoadsLatestStateFromDB(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	if err := store.SaveHighlight(1, storage.HighlightRule{Pattern: `danger`, FG: "red", Enabled: true, GroupName: "default"}); err != nil {
		t.Fatalf("SaveHighlight: %v", err)
	}

	got := v.ApplyHighlights("danger zone")
	if got == "danger zone" {
		t.Fatalf("ApplyHighlights did not inject ANSI: %q", got)
	}
	if stripANSIFromVM(got) != "danger zone" {
		t.Fatalf("ApplyHighlights plain text = %q, want %q", stripANSIFromVM(got), "danger zone")
	}
}

func newRuntimeTestStore(t *testing.T) *storage.Store {
	t.Helper()

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
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, stmt := range []string{
		`CREATE TABLE variables (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, scope TEXT NOT NULL, key TEXT NOT NULL, value TEXT, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX variables_session_scope_key_idx ON variables(session_id, scope, key);`,
		`CREATE TABLE alias_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, name TEXT NOT NULL, template TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX alias_rules_session_name_idx ON alias_rules(session_id, name);`,
		`CREATE TABLE trigger_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, name TEXT, pattern TEXT NOT NULL, command TEXT NOT NULL, is_button INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, stop_after_match INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT 'default', updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE highlight_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, pattern TEXT NOT NULL, fg TEXT NOT NULL DEFAULT '', bg TEXT NOT NULL DEFAULT '', bold INTEGER NOT NULL DEFAULT 0, faint INTEGER NOT NULL DEFAULT 0, italic INTEGER NOT NULL DEFAULT 0, underline INTEGER NOT NULL DEFAULT 0, strikethrough INTEGER NOT NULL DEFAULT 0, blink INTEGER NOT NULL DEFAULT 0, reverse INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, group_name TEXT NOT NULL DEFAULT 'default', updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX highlight_rules_session_pattern_idx ON highlight_rules(session_id, pattern);`,
		`CREATE TABLE log_overlays (id INTEGER PRIMARY KEY, log_entry_id INTEGER NOT NULL, overlay_type TEXT NOT NULL, payload_json TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("Exec(%q): %v", stmt, err)
		}
	}

	store := storage.NewTestStore(db)
	setRuntimeTestField(t, store, "db", db)
	return store
}

func setRuntimeTestField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}
