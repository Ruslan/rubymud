package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

// We need a helper that includes our new tables
func newTestStoreWithDeclarations(t *testing.T) *storage.Store {
	t.Helper()
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	db.Exec("PRAGMA foreign_keys = ON")

	// Use AutoMigrate for simplicity in tests if we don't want to depend on unexported runMigrations
	// but we MUST include the new types.
	err = db.AutoMigrate(
		&storage.AppSetting{}, &storage.SessionRecord{}, &storage.Variable{},
		&storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{},
		&storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.ProfileVariable{},
		&storage.LogRecord{}, &storage.LogOverlay{}, &storage.HistoryEntry{},
		&storage.TimerRecord{}, &storage.TimerSubscriptionRecord{},
		&storage.ProfileTimer{}, &storage.ProfileTimerSubscription{},
	)
	if err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	return storage.NewTestStore(db)
}

func TestTimerDeclarationWiring(t *testing.T) {
	store := newTestStoreWithDeclarations(t)
	v := vm.New(store, 1)

	// Setup primary profile
	profile, err := store.CreateProfile("Default", "")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := store.AddProfileToSession(1, profile.ID, 0); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}

	s := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	// 1. named #tickat on a not-yet-started timer creates declaration
	v.ProcessInputDetailed("#tickat {herb} {5} {say herb 5}")
	
	decls, _ := store.GetProfileTimers(profile.ID)
	if len(decls) != 1 || decls[0].Name != "herb" {
		t.Fatalf("expected 'herb' timer declaration, got %v", decls)
	}
	
	subs, _ := store.GetProfileTimerSubscriptions(profile.ID, "herb")
	if len(subs) != 1 || subs[0].Command != "say herb 5" {
		t.Errorf("expected subscription declaration, got %v", subs)
	}

	// 2. repeated identical #tickat is a no-op
	v.ProcessInputDetailed("#tickat {herb} {5} {say herb 5}")
	subs, _ = store.GetProfileTimerSubscriptions(profile.ID, "herb")
	if len(subs) != 1 {
		t.Errorf("expected no duplicate subscription in declaration, got %d", len(subs))
	}
	
	// Also check memory state
	tHerb := s.timers["herb"]
	tHerb.mu.Lock()
	if len(tHerb.Subscriptions[5]) != 1 {
		t.Errorf("expected no duplicate subscription in memory, got %d", len(tHerb.Subscriptions[5]))
	}
	tHerb.mu.Unlock()

	// 3. different commands on same second coexist
	v.ProcessInputDetailed("#tickat {herb} {5} {say herb 5 again}")
	subs, _ = store.GetProfileTimerSubscriptions(profile.ID, "herb")
	if len(subs) != 2 {
		t.Errorf("expected 2 subscriptions on same second, got %d", len(subs))
	}

	// 4. repeated identical #ticker does not duplicate second 0 command
	v.ProcessInputDetailed("#ticker {buff} {10} {say buffing}")
	v.ProcessInputDetailed("#ticker {buff} {10} {say buffing}")
	
	subs, _ = store.GetProfileTimerSubscriptions(profile.ID, "buff")
	count0 := 0
	for _, sub := range subs {
		if sub.Second == 0 && sub.Command == "say buffing" {
			count0++
		}
	}
	if count0 != 1 {
		t.Errorf("expected only 1 'say buffing' at second 0, got %d", count0)
	}

	// 5. named #tickicon and #ticksize create/update declaration for missing timer
	v.ProcessInputDetailed("#tickicon {gold} {💰}")
	v.ProcessInputDetailed("#ticksize {gold} {120}")
	
	decls, _ = store.GetProfileTimers(profile.ID)
	foundGold := false
	for _, d := range decls {
		if d.Name == "gold" {
			foundGold = true
			if d.Icon != "💰" {
				t.Errorf("expected gold icon 💰, got %q", d.Icon)
			}
			if d.CycleMS != 120000 {
				t.Errorf("expected gold cycle 120s, got %dms", d.CycleMS)
			}
		}
	}
	if !foundGold {
		t.Error("gold timer declaration missing after #tickicon/#ticksize")
	}

	// 6. #tickset does NOT become a declaration-writing path
	v.ProcessInputDetailed("#ticksize {manual} {10}") // creates declaration
	decls, _ = store.GetProfileTimers(profile.ID)
	foundManual := false
	for _, d := range decls {
		if d.Name == "manual" {
			foundManual = true
			if d.CycleMS != 10000 {
				t.Errorf("expected manual cycle 10s, got %dms", d.CycleMS)
			}
		}
	}
	if !foundManual {
		t.Fatal("manual timer declaration missing after #ticksize")
	}

	v.ProcessInputDetailed("#tickset {manual} {20}") // should NOT update declaration
	decls, _ = store.GetProfileTimers(profile.ID)
	for _, d := range decls {
		if d.Name == "manual" {
			if d.CycleMS != 10000 {
				t.Errorf("expected manual declaration cycle to remain 10s, but got %dms", d.CycleMS)
			}
		}
	}

	// Iteration 3 Tests

	// 7. a named timer with declared cycle can be started by #tickon {name} without prior runtime size setup
	// Setup a declaration first
	declHerb := storage.ProfileTimer{
		ProfileID:  profile.ID,
		Name:       "herb",
		CycleMS:    45000,
		Icon:       "🌿",
		RepeatMode: "repeating",
	}
	store.SaveProfileTimer(declHerb)
	store.SaveProfileTimerSubscription(storage.ProfileTimerSubscription{
		ProfileID: profile.ID, TimerName: "herb", Second: 0, Command: "eat herb",
	})

	// Ensure it's not in runtime
	delete(s.timers, "herb")

	v.ProcessInputDetailed("#tickon {herb}")
	tHerbAfter := s.timers["herb"]
	if tHerbAfter == nil {
		t.Fatal("herb timer not started from declaration")
	}
	if tHerbAfter.CycleMS != 45000 {
		t.Errorf("expected 45000ms from declaration, got %d", tHerbAfter.CycleMS)
	}
	if tHerbAfter.Icon != "🌿" {
		t.Errorf("expected icon 🌿 from declaration, got %q", tHerbAfter.Icon)
	}
	tHerbAfter.mu.Lock()
	if len(tHerbAfter.Subscriptions[0]) != 1 || tHerbAfter.Subscriptions[0][0] != "eat herb" {
		t.Errorf("subscriptions not loaded from declaration, got %v", tHerbAfter.Subscriptions)
	}
	tHerbAfter.mu.Unlock()

	// 8. named one-shot timer stops after expiry
	v.ProcessInputDetailed("#ticksize {once} {1}")
	v.ProcessInputDetailed("#tickmode {once} {one_shot}")
	v.ProcessInputDetailed("#tickon {once}")
	
	tOnce := s.timers["once"]
	if tOnce.RepeatMode != "one_shot" {
		t.Errorf("expected one_shot mode, got %q", tOnce.RepeatMode)
	}
	
	// Fast-forward time
	tOnce.mu.Lock()
	tOnce.NextTickAt = time.Now().Add(-100 * time.Millisecond)
	tOnce.mu.Unlock()
	
	if !tOnce.Check() {
		t.Error("expected timer to fire")
	}
	if tOnce.Enabled {
		t.Error("expected one-shot timer to be disabled after firing")
	}

	// 9. named repeating timer still repeats after expiry
	v.ProcessInputDetailed("#ticksize {rep} {1}")
	v.ProcessInputDetailed("#tickmode {rep} {repeating}")
	v.ProcessInputDetailed("#tickon {rep}")
	
	tRep := s.timers["rep"]
	tRep.mu.Lock()
	tRep.NextTickAt = time.Now().Add(-100 * time.Millisecond)
	tRep.mu.Unlock()
	
	if !tRep.Check() {
		t.Error("expected timer to fire")
	}
	if !tRep.Enabled {
		t.Error("expected repeating timer to remain enabled after firing")
	}

	// 10. default ticker behavior remains intact
	v.ProcessInputDetailed("#tickon") // default ticker
	tTicker := s.timers["ticker"]
	if tTicker == nil || !tTicker.Enabled {
		t.Error("default ticker not enabled")
	}
	if tTicker.RepeatMode != "repeating" {
		t.Errorf("default ticker should be repeating, got %q", tTicker.RepeatMode)
	}

	// 11. round-trip one-shot declaration
	v.ProcessInputDetailed("#ticksize {rtonce} {30}")
	v.ProcessInputDetailed("#tickmode {rtonce} {one_shot}")
	
	// Verify declaration in storage
	decls, _ = store.GetProfileTimers(profile.ID)
	foundRT := false
	for _, d := range decls {
		if d.Name == "rtonce" {
			foundRT = true
			if d.RepeatMode != "one_shot" {
				t.Errorf("expected declaration repeat_mode one_shot, got %q", d.RepeatMode)
			}
		}
	}
	if !foundRT {
		t.Fatal("rtonce declaration missing")
	}

	// Clear from runtime
	s.timersMu.Lock()
	delete(s.timers, "rtonce")
	s.timersMu.Unlock()

	// Restart from declaration
	v.ProcessInputDetailed("#tickon {rtonce}")
	tRT := s.timers["rtonce"]
	if tRT == nil {
		t.Fatal("rtonce failed to restart from declaration")
	}
	if tRT.RepeatMode != "one_shot" {
		t.Errorf("expected restarted timer to be one_shot, got %q", tRT.RepeatMode)
	}

	// 12. a declared named timer with cycle < 60 rejects out-of-range #tickat even when not loaded in runtime
	store.SaveProfileTimer(storage.ProfileTimer{
		ProfileID: profile.ID, Name: "shorty", CycleMS: 30000, RepeatMode: "repeating",
	})
	delete(s.timers, "shorty") // Ensure not in runtime
	
	// Should fail (50 > 30)
	res := v.ProcessInputDetailed("#tickat {shorty} {50} {say fail}")
	if len(res) == 0 || res[0].Kind != vm.ResultEcho || !strings.Contains(res[0].Text, "out of range") {
		t.Errorf("expected out of range diagnostic, got %v", res)
	}
	
	// Should succeed (20 < 30)
	res = v.ProcessInputDetailed("#tickat {shorty} {20} {say ok}")
	if len(res) != 0 && res[0].Kind == vm.ResultEcho {
		t.Errorf("expected success for in-range #tickat, got %v", res)
	}

	// 13. restoring a past-due one_shot timer does not reschedule it
	past := time.Now().Add(-10 * time.Minute)
	pastSQL := storage.SQLiteTime{Time: past}
	store.SaveTimer(storage.TimerRecord{
		SessionID: 1, Name: "past_once", CycleMS: 10000, Enabled: true, RepeatMode: "one_shot", NextTickAt: &pastSQL,
	})
	
	s2 := &Session{sessionID: 1, store: store, timers: make(map[string]*Timer)}
	s2.restoreTimers()
	
	tOncePast := s2.timers["past_once"]
	if tOncePast == nil {
		t.Fatal("past_once not restored")
	}
	if tOncePast.Enabled {
		t.Error("past-due one-shot timer should be disabled after restore")
	}

	// 14. restoring repeating timers still behaves as before
	store.SaveTimer(storage.TimerRecord{
		SessionID: 1, Name: "past_rep", CycleMS: 60000, Enabled: true, RepeatMode: "repeating", NextTickAt: &pastSQL,
	})
	
	s3 := &Session{sessionID: 1, store: store, timers: make(map[string]*Timer)}
	s3.restoreTimers()
	
	tRepPast := s3.timers["past_rep"]
	if tRepPast == nil {
		t.Fatal("past_rep not restored")
	}
	if !tRepPast.Enabled {
		t.Error("past-due repeating timer should be re-enabled after restore")
	}

	// 15. #tickset {name} {+delta} on a declared-but-not-loaded named timer uses the declared cycle
	store.SaveProfileTimer(storage.ProfileTimer{
		ProfileID: profile.ID, Name: "adj_dormant", CycleMS: 15000, RepeatMode: "repeating",
	})
	delete(s.timers, "adj_dormant") // Ensure not in runtime
	
	v.ProcessInputDetailed("#tickset {adj_dormant} {+5}")
	tAdj := s.timers["adj_dormant"]
	if tAdj == nil {
		t.Fatal("adj_dormant not loaded by TickAdjust")
	}
	if tAdj.CycleMS != 15000 {
		t.Errorf("expected 15000ms cycle for adj_dormant, got %d", tAdj.CycleMS)
	}

	// 16. #tickset {name} on a declared-but-not-loaded named timer initializes/resets it correctly
	store.SaveProfileTimer(storage.ProfileTimer{
		ProfileID: profile.ID, Name: "reset_dormant", CycleMS: 40000, RepeatMode: "repeating",
	})
	delete(s.timers, "reset_dormant") // Ensure not in runtime
	
	v.ProcessInputDetailed("#tickset {reset_dormant}")
	tReset := s.timers["reset_dormant"]
	if tReset == nil {
		t.Fatal("reset_dormant not loaded by TickReset")
	}
	if tReset.CycleMS != 40000 {
		t.Errorf("expected 40000ms cycle for reset_dormant, got %d", tReset.CycleMS)
	}
	if !tReset.Enabled {
		t.Error("reset_dormant should be enabled after TickReset")
	}
}
