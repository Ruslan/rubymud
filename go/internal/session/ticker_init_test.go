package session

import (
	"net"
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestDefaultTickerInitFromProfile(t *testing.T) {
	store := newTestStoreWithDeclarations(t)

	// Create a TCP listener to satisfy net.Dial in session.New
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	// 1. Setup profile with ticker declaration
	profile, _ := store.CreateProfile("Default", "")
	if err := store.AddProfileToSession(1, profile.ID, 0); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}
	store.SaveProfileTimer(storage.ProfileTimer{
		ProfileID:  profile.ID,
		Name:       "ticker",
		CycleMS:    45000,
		Icon:       "🕒",
		RepeatMode: "repeating",
	})
	store.SaveProfileTimerSubscription(storage.ProfileTimerSubscription{
		ProfileID: profile.ID, TimerName: "ticker", Second: 3, Command: "stand",
	})

	v := vm.New(store, 1)
	s, err := New(1, addr, store, v, "", false)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer s.Close()

	tTicker := s.timers["ticker"]
	if tTicker == nil {
		t.Fatal("ticker missing")
	}
	if tTicker.CycleMS != 45000 {
		t.Errorf("expected 45000ms cycle from profile, got %d", tTicker.CycleMS)
	}

	s.TickSet("ticker", 60)
	updatedDecl, err := store.GetProfileTimer(profile.ID, "ticker")
	if err != nil {
		t.Fatalf("GetProfileTimer after TickSet: %v", err)
	}
	if updatedDecl.CycleMS != 60000 {
		t.Errorf("expected TickSet to persist 60000ms profile cycle, got %d", updatedDecl.CycleMS)
	}

	if tTicker.Icon != "🕒" {
		t.Errorf("expected icon 🕒, got %q", tTicker.Icon)
	}
	tTicker.mu.Lock()
	if len(tTicker.Subscriptions[3]) != 1 || tTicker.Subscriptions[3][0] != "stand" {
		t.Errorf("expected subscription 'stand' at second 3, got %v", tTicker.Subscriptions[3])
	}
	tTicker.mu.Unlock()

	// 2. Runtime state override
	// Create session 2 with its own runtime state
	store.SaveTimer(storage.TimerRecord{
		SessionID: 2, Name: "ticker", CycleMS: 20000, Enabled: true,
	})
	// Add profile declaration for session 2 (same profile)
	if err := store.AddProfileToSession(2, profile.ID, 0); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}

	v2 := vm.New(store, 2)
	s2, err := New(2, addr, store, v2, "", false)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer s2.Close()

	if s2.timers["ticker"].CycleMS != 20000 {
		t.Errorf("expected runtime cycle 20000ms to override profile declaration, got %d", s2.timers["ticker"].CycleMS)
	}

	// 3. No store fallback
	s3, err := New(3, addr, nil, vm.New(nil, 3), "", false)
	if err != nil {
		t.Fatalf("session.New (no store): %v", err)
	}
	defer s3.Close()

	if s3.timers["ticker"] == nil {
		t.Fatal("ticker missing in session without store")
	}
	if s3.timers["ticker"].CycleMS != 60000 {
		t.Errorf("expected 60s fallback without store, got %d", s3.timers["ticker"].CycleMS)
	}
}
