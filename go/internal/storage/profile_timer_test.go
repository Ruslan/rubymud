package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestStore(t *testing.T) *Store {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	// Enable foreign keys for SQLite
	db.Exec("PRAGMA foreign_keys = ON")

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	return NewTestStore(db)
}

func TestProfileTimerStorage(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	profile, err := store.CreateProfile("Test Profile", "")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// 1. Test SaveProfileTimer (Create)
	timer := ProfileTimer{
		ProfileID:  profile.ID,
		Name:       "tick",
		Icon:       "⏰",
		CycleMS:    6000,
		RepeatMode: "repeating",
	}

	if err := store.SaveProfileTimer(timer); err != nil {
		t.Fatalf("SaveProfileTimer: %v", err)
	}

	// 2. Test SaveProfileTimer (Upsert/Update)
	timer.Icon = "⏳"
	timer.RepeatMode = "one_shot"
	if err := store.SaveProfileTimer(timer); err != nil {
		t.Fatalf("SaveProfileTimer Upsert: %v", err)
	}

	timers, err := store.GetProfileTimers(profile.ID)
	if err != nil {
		t.Fatalf("GetProfileTimers: %v", err)
	}
	if len(timers) != 1 || timers[0].Icon != "⏳" || timers[0].RepeatMode != "one_shot" {
		t.Errorf("upsert failed, got %+v", timers[0])
	}

	// 3. Test multiple timers
	timer2 := ProfileTimer{
		ProfileID:  profile.ID,
		Name:       "tock",
		CycleMS:    1000,
		RepeatMode: "repeating",
	}
	if err := store.SaveProfileTimer(timer2); err != nil {
		t.Fatalf("Save second timer: %v", err)
	}

	timers, _ = store.GetProfileTimers(profile.ID)
	if len(timers) != 2 {
		t.Errorf("expected 2 timers, got %d", len(timers))
	}

	// 4. Test Subscriptions and Ordering
	subs := []ProfileTimerSubscription{
		{ProfileID: profile.ID, TimerName: "tick", Second: 10, SortOrder: 1, Command: "second 10 order 1"},
		{ProfileID: profile.ID, TimerName: "tick", Second: 5, SortOrder: 0, Command: "second 5 order 0"},
		{ProfileID: profile.ID, TimerName: "tick", Second: 10, SortOrder: 0, Command: "second 10 order 0"},
	}
	for _, s := range subs {
		if err := store.SaveProfileTimerSubscription(s); err != nil {
			t.Fatalf("SaveSubscription: %v", err)
		}
	}

	savedSubs, err := store.GetProfileTimerSubscriptions(profile.ID, "tick")
	if err != nil {
		t.Fatalf("GetSubscriptions: %v", err)
	}
	if len(savedSubs) != 3 {
		t.Fatalf("expected 3 subs, got %d", len(savedSubs))
	}

	// Verify order: (5, 0), (10, 0), (10, 1)
	if savedSubs[0].Second != 5 || savedSubs[1].Second != 10 || savedSubs[1].SortOrder != 0 || savedSubs[2].SortOrder != 1 {
		t.Errorf("incorrect subscription order: %+v", savedSubs)
	}

	// 5. Test Subscription Upsert
	subUpdate := savedSubs[0]
	subUpdate.Command = "updated command"
	if err := store.SaveProfileTimerSubscription(subUpdate); err != nil {
		t.Fatalf("SaveSubscription Upsert: %v", err)
	}
	savedSubs, _ = store.GetProfileTimerSubscriptions(profile.ID, "tick")
	if len(savedSubs) != 3 || savedSubs[0].Command != "updated command" {
		t.Errorf("subscription upsert failed")
	}

	// 6. Test ClearProfileTimerSubscriptions (Targeted)
	// Add a sub for "tock"
	tockSub := ProfileTimerSubscription{ProfileID: profile.ID, TimerName: "tock", Second: 0, SortOrder: 0, Command: "tock command"}
	store.SaveProfileTimerSubscription(tockSub)

	if err := store.ClearProfileTimerSubscriptions(profile.ID, "tick"); err != nil {
		t.Fatalf("ClearSubscriptions: %v", err)
	}

	tickSubs, _ := store.GetProfileTimerSubscriptions(profile.ID, "tick")
	if len(tickSubs) != 0 {
		t.Errorf("tick subs should be cleared, got %d", len(tickSubs))
	}

	tockSubs, _ := store.GetProfileTimerSubscriptions(profile.ID, "tock")
	if len(tockSubs) != 1 {
		t.Errorf("tock subs should remain, got %d", len(tockSubs))
	}

	// 7. Test DeleteProfileTimer (Cascades)
	if err := store.DeleteProfileTimer(profile.ID, "tock"); err != nil {
		t.Fatalf("DeleteProfileTimer: %v", err)
	}
	timers, _ = store.GetProfileTimers(profile.ID)
	if len(timers) != 1 || timers[0].Name != "tick" {
		t.Errorf("timer deletion failed, timers: %v", timers)
	}
	tockSubs, _ = store.GetProfileTimerSubscriptions(profile.ID, "tock")
	if len(tockSubs) != 0 {
		t.Errorf("tock subs should be deleted via cascade")
	}

	// 8. Test cleanup on profile deletion
	if err := store.DeleteProfile(profile.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	timers, _ = store.GetProfileTimers(profile.ID)
	if len(timers) != 0 {
		t.Errorf("expected 0 timers after profile deletion, got %d", len(timers))
	}

	tickSubs, _ = store.GetProfileTimerSubscriptions(profile.ID, "tick")
	if len(tickSubs) != 0 {
		t.Errorf("expected 0 subs after profile deletion, got %d", len(tickSubs))
	}
}
