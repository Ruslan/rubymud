package session

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"rubymud/go/internal/vm"
)

func TestSchedulerDispatcherTermination(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)
	
	before := runtime.NumGoroutine()
	
	s := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 100),
		done:      make(chan struct{}),
	}
	
	go s.runTimerLoop()
	go s.runCommandDispatcher()

	// For testing purpose, we manually close it to see if goroutines stop
	s.Close()
	
	// Wait a bit for goroutines to exit
	time.Sleep(200 * time.Millisecond)
	
	after := runtime.NumGoroutine()
	// New started 2 goroutines, Close should stop them. 
	// NumGoroutine is global, so we use some slack.
	if after > before+5 { 
		t.Errorf("possible goroutine leak: before=%d after=%d", before, after)
	}
}

func TestSchedulerTickBoundaries(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 10),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	// Cycle 2s
	ticker := NewTimer("ticker", 2*time.Second)
	s.timers["ticker"] = ticker
	ticker.On()

	// 1. Test max cycle boundary (2)
	s.SubscribeTimer("ticker", 2, "max_boundary")
	// 2. Test zero boundary (0)
	s.SubscribeTimer("ticker", 0, "zero_boundary")

	go s.runTimerLoop()
	go s.runCommandDispatcher()
	defer s.Close()

	// Immediately after ticker.On(), remSec should be 2 (math.Ceil(2.0))
	// So max_boundary should fire soon.
	
	start := time.Now()
	foundMax := false
	foundZero := false
	
	for time.Since(start) < 3*time.Second {
		out := conn.String()
		if !foundMax && strings.Contains(out, "max_boundary\n") {
			foundMax = true
		}
		if !foundZero && strings.Contains(out, "zero_boundary\n") {
			foundZero = true
		}
		if foundMax && foundZero {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !foundMax {
		t.Error("max cycle boundary subscription didn't fire")
	}
	if !foundZero {
		t.Error("zero boundary subscription didn't fire")
	}
}

func TestSchedulerShortCycle(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 10),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	// Cycle 1s
	ticker := NewTimer("ticker", 1*time.Second)
	s.timers["ticker"] = ticker
	ticker.On()

	s.SubscribeTimer("ticker", 0, "short_zero")

	go s.runTimerLoop()
	go s.runCommandDispatcher()
	defer s.Close()

	time.Sleep(1500 * time.Millisecond)
	
	if !strings.Contains(conn.String(), "short_zero\n") {
		t.Error("zero boundary subscription didn't fire on 1s cycle")
	}
}

func TestSchedulerSubscriptions(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 10),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	// Set a short cycle for the ticker
	ticker := NewTimer("ticker", 2*time.Second)
	s.timers["ticker"] = ticker
	ticker.On()

	// Subscribe to second 1
	s.SubscribeTimer("ticker", 1, "stand")

	go s.runTimerLoop()
	go s.runCommandDispatcher()
	defer s.Close()

	// Wait for the "stand" command to appear in conn
	start := time.Now()
	found := false
	for time.Since(start) < 3*time.Second {
		if strings.Contains(conn.String(), "stand\n") {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Errorf("scheduled command 'stand' not found in output, got %q", conn.String())
	}
}

func TestSchedulerDelay(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 10),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	go s.runTimerLoop()
	go s.runCommandDispatcher()
	defer s.Close()

	// Schedule a delay
	s.ScheduleDelay("testdelay", 0.5, "say hello")

	// Wait for the command
	start := time.Now()
	found := false
	for time.Since(start) < 2*time.Second {
		if strings.Contains(conn.String(), "say hello\n") {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Errorf("delayed command 'say hello' not found in output, got %q", conn.String())
	}
}

func TestSchedulerCancelDelay(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 10),
		done:      make(chan struct{}),
	}
	v.SetTimerControl(s)

	go s.runTimerLoop()
	go s.runCommandDispatcher()
	defer s.Close()

	// Schedule and immediately cancel
	s.ScheduleDelay("cancelme", 1.0, "shouldnotsee")
	s.CancelDelay("cancelme")

	time.Sleep(1200 * time.Millisecond)

	if strings.Contains(conn.String(), "shouldnotsee") {
		t.Error("cancelled delay command was executed")
	}
}

func TestSchedulerDelayGuardrails(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	s := &Session{
		sessionID: 1,
		store:     store,
		vm:        v,
		delays:    make(map[string]*delayTask),
	}

	// 1. Min delay guardrail (100ms)
	_ = s.ScheduleDelay("min", 0.01, "cmd")
	if time.Until(s.delays["min"].due) < 90*time.Millisecond {
		t.Errorf("delay was not clamped to min 100ms, due in %v", time.Until(s.delays["min"].due))
	}

	// 2. Max delays guardrail (50)
	// We already have 1 delay ("min") scheduled.
	for i := 0; i < 49; i++ {
		err := s.ScheduleDelay(string(rune(i)), 10, "cmd")
		if err != nil {
			t.Fatalf("failed to schedule delay %d: %v", i, err)
		}
	}

	err := s.ScheduleDelay("one_too_many", 10, "cmd")
	if err == nil {
		t.Error("expected error when scheduling 51st delay, got nil")
	} else if !strings.Contains(err.Error(), "too many pending delays") {
		t.Errorf("expected 'too many pending delays' error, got %v", err)
	}
}
