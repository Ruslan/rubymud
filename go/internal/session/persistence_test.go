package session

import (
	"testing"
	"time"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestTimerPersistence(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	// 1. Create a session and set some timers
	s1 := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s1)

	// Set primary ticker
	s1.TickSize("ticker", 60)
	s1.TickIcon("ticker", "🕒")
	s1.SubscribeTimer("ticker", 10, "tickcmd")
	s1.TickOn("ticker")

	// Set named timer
	s1.TickSize("herb", 30)
	s1.TickIcon("herb", "🪴")
	s1.SubscribeTimer("herb", 0, "herbcmd")
	// Keep herb disabled for now to test paused state

	// 2. Create a new session (simulating restart) and restore
	v2 := vm.New(store, 1)
	s2 := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v2,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		done:      make(chan struct{}),
	}
	v2.SetTimerControl(s2)

	s2.restoreTimers()

	// 3. Verify ticker
	t1, ok := s2.timers["ticker"]
	if !ok {
		t.Fatal("ticker not restored")
	}
	if t1.CycleMS != 60000 {
		t.Errorf("expected 60000ms cycle, got %d", t1.CycleMS)
	}
	if t1.Icon != "🕒" {
		t.Errorf("expected icon 🕒, got %q", t1.Icon)
	}
	if !t1.Enabled {
		t.Error("expected ticker to be enabled")
	}

	t1.mu.Lock()
	cmds := t1.Subscriptions[10]
	t1.mu.Unlock()
	if len(cmds) != 1 || cmds[0] != "tickcmd" {
		t.Errorf("expected subscription 'tickcmd' at 10s, got %v", cmds)
	}

	// 4. Verify herb
	th, ok := s2.timers["herb"]
	if !ok {
		t.Fatal("herb timer not restored")
	}
	if th.CycleMS != 30000 {
		t.Errorf("expected 30000ms cycle, got %d", th.CycleMS)
	}
	if th.Enabled {
		t.Error("expected herb to be disabled")
	}
	if th.Icon != "🪴" {
		t.Errorf("expected icon 🪴, got %q", th.Icon)
	}
}

func TestTimerPhaseRestore(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	// Create session and start timer
	s1 := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s1)

	s1.TickSize("ticker", 10) // 10s cycle
	s1.TickOn("ticker")

	// Manually manipulate NextTickAt in DB to simulate time passed during downtime
	// Let's set it to 5 seconds ago. Cycle is 10s.
	// So 5 seconds elapsed, 5 seconds should remain.
	fiveSecAgo := time.Now().Add(-5 * time.Second)
	at := storage.SQLiteTime{Time: fiveSecAgo}

	err := store.SaveTimer(storage.TimerRecord{
		SessionID:  1,
		Name:       "ticker",
		CycleMS:    10000,
		Enabled:    true,
		NextTickAt: &at,
	})
	if err != nil {
		t.Fatalf("failed to save mock timer: %v", err)
	}

	// Restore
	v2 := vm.New(store, 1)
	s2 := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v2,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		done:      make(chan struct{}),
	}
	s2.restoreTimers()

	tRestored := s2.timers["ticker"]
	rem := tRestored.RemainingSeconds()
	// Should be around 5.
	if rem < 4 || rem > 6 {
		t.Errorf("expected restored remaining around 5s, got %d", rem)
	}
}

func TestTimerRestoreHardening(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	// 1. Corrupt ticker row (cycle=0)
	err := store.SaveTimer(storage.TimerRecord{
		SessionID: 1,
		Name:      "ticker",
		CycleMS:   0,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("failed to save corrupt ticker: %v", err)
	}

	// 2. Corrupt enabled row (next_tick_at=nil)
	err = store.SaveTimer(storage.TimerRecord{
		SessionID:  1,
		Name:       "broken",
		CycleMS:    10000,
		Enabled:    true,
		NextTickAt: nil,
	})
	if err != nil {
		t.Fatalf("failed to save broken timer: %v", err)
	}

	// Restore
	s := &Session{
		sessionID: 1,
		store:     store,
		vm:        v,
		timers:    make(map[string]*Timer),
	}
	s.restoreTimers()

	// Verify ticker recovered to 60s
	ticker, ok := s.timers["ticker"]
	if !ok {
		t.Fatal("ticker missing after restore")
	}
	if ticker.CycleMS != 60000 {
		t.Errorf("expected ticker recovered to 60000ms, got %d", ticker.CycleMS)
	}

	// Verify broken timer forced to disabled
	broken, ok := s.timers["broken"]
	if !ok {
		t.Fatal("broken timer missing after restore")
	}
	if broken.Enabled {
		t.Error("expected broken timer (missing next_tick_at) to be disabled")
	}
}

func TestTickAdjustClamping(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)
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

	s.TickSize("ticker", 60)
	s.TickOn("ticker") // Remaining = 60

	// Adjust +10 -> should clamp at 60
	s.TickAdjust("ticker", 10)
	rem := s.timers["ticker"].RemainingSeconds()
	if rem != 60 {
		t.Errorf("expected clamped 60s, got %d", rem)
	}

	// Adjust -70 -> should clamp at 0
	s.TickAdjust("ticker", -70)
	rem = s.timers["ticker"].RemainingSeconds()
	if rem != 0 {
		t.Errorf("expected clamped 0s, got %d", rem)
	}
}

func TestTickAdjustParsing(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	// Mock TimerControl to verify calls
	m := &mockTimerControlWithAdjust{}
	v.SetTimerControl(m)

	v.ProcessInputDetailed("#tickset {+5}")
	if m.adjName != "ticker" || m.adjVal != 5 {
		t.Errorf("failed to parse #tickset {+5}, got %s %v", m.adjName, m.adjVal)
	}

	v.ProcessInputDetailed("#tickset {herb} {-2}")
	if m.adjName != "herb" || m.adjVal != -2 {
		t.Errorf("failed to parse #tickset {herb} {-2}, got %s %v", m.adjName, m.adjVal)
	}

	v.ProcessInputDetailed("#tickset {-1.5}")
	if m.adjName != "ticker" || m.adjVal != -1.5 {
		t.Errorf("failed to parse #tickset {-1.5}, got %s %v", m.adjName, m.adjVal)
	}
}

type mockTimerControlWithAdjust struct {
	on        string
	off       string
	reset     string
	set       string
	setVal    float64
	size      string
	sizeVal   float64
	iconName  string
	icon      string
	adjName   string
	adjVal    float64
	subName   string
	subSec    int
	subCmd    string
	unsubName string
	unsubSec  int
}

func (m *mockTimerControlWithAdjust) TickOn(name string)    { m.on = name }
func (m *mockTimerControlWithAdjust) TickOff(name string)   { m.off = name }
func (m *mockTimerControlWithAdjust) TickReset(name string) { m.reset = name }
func (m *mockTimerControlWithAdjust) TickSet(name string, seconds float64) {
	m.set = name
	m.setVal = seconds
}
func (m *mockTimerControlWithAdjust) TickSize(name string, seconds float64) {
	m.size = name
	m.sizeVal = seconds
}
func (m *mockTimerControlWithAdjust) TickIcon(name string, icon string) {
	m.iconName = name
	m.icon = icon
}
func (m *mockTimerControlWithAdjust) TickAdjust(name string, delta float64) {
	m.adjName = name
	m.adjVal = delta
}
func (m *mockTimerControlWithAdjust) TickMode(name string, mode string) {}
func (m *mockTimerControlWithAdjust) SubscribeTimer(name string, second int, command string) {
	m.subName = name
	m.subSec = second
	m.subCmd = command
}
func (m *mockTimerControlWithAdjust) UnsubscribeTimer(name string, second int) {
	m.unsubName = name
	m.unsubSec = second
}
func (m *mockTimerControlWithAdjust) ScheduleDelay(id string, seconds float64, command string) error {
	return nil
}
func (m *mockTimerControlWithAdjust) CancelDelay(id string)                {}
func (m *mockTimerControlWithAdjust) GetTimerCycleSeconds(name string) int { return 60 }
