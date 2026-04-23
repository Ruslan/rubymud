package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newProfileTestStore(t *testing.T) *Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	return &Store{db: db}
}

func TestProfileCRUD(t *testing.T) {
	s := newProfileTestStore(t)

	// Create
	p1, err := s.CreateProfile("Base", "Base rules")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if p1.ID == 0 || p1.Name != "Base" {
		t.Errorf("Invalid profile created: %+v", p1)
	}

	// Read
	profiles, err := s.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "Base" {
		t.Errorf("Expected 1 profile 'Base', got: %+v", profiles)
	}

	// Update
	p1.Name = "Core"
	p1.Description = "Core rules"
	if err := s.UpdateProfile(p1); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, err := s.GetProfile(p1.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "Core" {
		t.Errorf("Expected updated name 'Core', got %q", got.Name)
	}

	// Delete
	if err := s.DeleteProfile(p1.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	profiles, _ = s.ListProfiles()
	if len(profiles) != 0 {
		t.Errorf("Expected empty profiles after delete, got %d", len(profiles))
	}
}

func TestSessionProfilesOrder(t *testing.T) {
	s := newProfileTestStore(t)

	s.db.Create(&SessionRecord{ID: 100, Name: "Test Session"})
	p1, _ := s.CreateProfile("A", "")
	p2, _ := s.CreateProfile("B", "")

	if err := s.AddProfileToSession(100, p1.ID, 0); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}
	if err := s.AddProfileToSession(100, p2.ID, 1); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}

	// Read ordered IDs (DESC order_index means highest priority first in GetOrderedProfileIDs)
	ids, err := s.GetOrderedProfileIDs(100)
	if err != nil {
		t.Fatalf("GetOrderedProfileIDs: %v", err)
	}
	// order_index 1 (p2) > order_index 0 (p1), so DESC means [p2.ID, p1.ID]
	if len(ids) != 2 || ids[0] != p2.ID || ids[1] != p1.ID {
		t.Errorf("Expected [p2, p1], got %v", ids)
	}

	// Get primary profile ID
	primary, err := s.GetPrimaryProfileID(100)
	if err != nil {
		t.Fatalf("GetPrimaryProfileID: %v", err)
	}
	if primary != p2.ID {
		t.Errorf("Expected primary %d, got %d", p2.ID, primary)
	}

	// Reorder
	if err := s.ReorderSessionProfiles(100, []ProfileOrder{
		{ProfileID: p1.ID, OrderIndex: 10},
		{ProfileID: p2.ID, OrderIndex: 5},
	}); err != nil {
		t.Fatalf("ReorderSessionProfiles: %v", err)
	}

	ids, _ = s.GetOrderedProfileIDs(100)
	// order_index 10 (p1) > order_index 5 (p2), so DESC means [p1.ID, p2.ID]
	if len(ids) != 2 || ids[0] != p1.ID || ids[1] != p2.ID {
		t.Errorf("Expected [p1, p2] after reorder, got %v", ids)
	}
}

func TestRulesPositionAssignment(t *testing.T) {
	s := newProfileTestStore(t)
	p, _ := s.CreateProfile("P", "")

	// Create 3 aliases
	s.SaveAlias(p.ID, "a", "b", true, "def")
	s.SaveAlias(p.ID, "c", "d", true, "def")
	s.SaveAlias(p.ID, "e", "f", true, "def")

	aliases, _ := s.ListAliases(p.ID)
	if len(aliases) != 3 {
		t.Fatalf("Expected 3 aliases, got %d", len(aliases))
	}
	
	// They should have positions 1, 2, 3
	for i, a := range aliases {
		if a.Position != i+1 {
			t.Errorf("Expected alias %s to have position %d, got %d", a.Name, i+1, a.Position)
		}
	}
}

func TestEnsureSessionProfilesBackfillsMissingProfiles(t *testing.T) {
	s := newProfileTestStore(t)

	if err := s.db.Create(&SessionRecord{ID: 200, Name: "default"}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.EnsureSessionProfiles(200, "default"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}

	entries, err := s.GetSessionProfiles(200)
	if err != nil {
		t.Fatalf("GetSessionProfiles: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 session profile, got %d", len(entries))
	}

	ids, err := s.GetOrderedProfileIDs(200)
	if err != nil {
		t.Fatalf("GetOrderedProfileIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 ordered profile, got %d", len(ids))
	}
}
