package vm

import (
	"fmt"
	"testing"

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
	if err := store.SaveAlias(1, "eq", "wield $weapon", true, "default"); err != nil {
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

	if err := db.AutoMigrate(
		&storage.Variable{}, &storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{},
		&storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.LogOverlay{},
	); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Create default profile for session 1
	db.Create(&storage.Profile{ID: 1, Name: "Default"})
	db.Create(&storage.SessionProfile{SessionID: 1, ProfileID: 1, OrderIndex: 0})

	return storage.NewTestStore(db)
}


