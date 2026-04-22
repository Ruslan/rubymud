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

func newProfileRuntimeStore(t *testing.T) *storage.Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(
		&storage.Variable{}, &storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{},
		&storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.LogOverlay{},
	); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return storage.NewTestStore(db)
}

func TestVMMergesProfilesCorrectly(t *testing.T) {
	store := newProfileRuntimeStore(t)
	
	// Create profiles
	pBase, _ := store.CreateProfile("Base", "")
	pUser, _ := store.CreateProfile("User", "")
	
	// Assign to session 1. User has higher order_index = higher priority.
	store.AddProfileToSession(1, pBase.ID, 0)
	store.AddProfileToSession(1, pUser.ID, 10)
	
	// Add aliases
	store.SaveAlias(pBase.ID, "hello", "say hello", true, "")
	store.SaveAlias(pBase.ID, "bye", "say bye", true, "")
	
	store.SaveAlias(pUser.ID, "hello", "shout hello", true, "") // Overrides Base "hello"
	store.SaveAlias(pUser.ID, "dance", "emote dances", true, "")
	
	// Add triggers
	store.SaveTrigger(pBase.ID, "Base trigger", "smile", false, "")
	store.SaveTrigger(pUser.ID, "User trigger", "nod", false, "")

	v := New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	
	// Aliases test (deduplication & override)
	// We expect User's "hello", User's "dance", and Base's "bye"
	aliases := v.Aliases()
	if len(aliases) != 3 {
		t.Fatalf("Expected 3 merged aliases, got %d", len(aliases))
	}
	
	helloAlias := findAlias(aliases, "hello")
	if helloAlias.Template != "shout hello" {
		t.Errorf("Expected 'hello' to resolve to 'shout hello', got '%s'", helloAlias.Template)
	}
	
	// Triggers test (ordered appending)
	// Should be User trigger first, then Base trigger
	triggers := v.Triggers()
	if len(triggers) != 2 {
		t.Fatalf("Expected 2 merged triggers, got %d", len(triggers))
	}
	
	if triggers[0].Command != "nod" {
		t.Errorf("Expected highest priority trigger to be 'nod', got '%s'", triggers[0].Command)
	}
	if triggers[1].Command != "smile" {
		t.Errorf("Expected lowest priority trigger to be 'smile', got '%s'", triggers[1].Command)
	}
}

func findAlias(aliases []storage.AliasRule, name string) *storage.AliasRule {
	for _, a := range aliases {
		if a.Name == name {
			return &a
		}
	}
	return nil
}
