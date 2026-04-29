package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHistoryStore(t *testing.T) {
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

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	store := NewTestStore(db)
	sessionID := int64(1)

	// Test Append and List
	if err := store.AppendHistoryEntry(sessionID, "input", "test command 1"); err != nil {
		t.Fatalf("AppendHistoryEntry: %v", err)
	}
	if err := store.AppendHistoryEntry(sessionID, "input", "test command 2"); err != nil {
		t.Fatalf("AppendHistoryEntry: %v", err)
	}

	entries, err := store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	if entries[0].Line != "test command 2" {
		t.Errorf("got %s, want test command 2", entries[0].Line)
	}

	// Test Delete
	entryID := entries[0].ID
	if err := store.DeleteHistoryEntry(sessionID, entryID); err != nil {
		t.Fatalf("DeleteHistoryEntry: %v", err)
	}

	entries, err = store.ListHistory(sessionID, 10)
	if err != nil {
		t.Fatalf("ListHistory after delete: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d entries after delete, want 1", len(entries))
	}

	if entries[0].Line != "test command 1" {
		t.Errorf("got %s, want test command 1", entries[0].Line)
	}
}
