package session

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

var packetEnd = []byte{0xff, 0xf9}

type clientSink struct {
	id   int
	name string
	send func(msg ServerMsg) error
}

type delayTask struct {
	id      string
	due     time.Time
	command string
}

type Session struct {
	sessionID    int64
	mudAddr      string
	conn         net.Conn
	store        *storage.Store
	vm           *vm.VM
	clients      map[int]clientSink
	nextClientID int
	mu           sync.Mutex
	closed       bool
	timers       map[string]*Timer
	timersMu     sync.Mutex
	delays       map[string]*delayTask
	delaysMu     sync.Mutex
	cmdQueue     chan string
	done         chan struct{}
}

func New(sessionID int64, mudAddr string, store *storage.Store, v *vm.VM) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	s := &Session{
		sessionID: sessionID,
		mudAddr:   mudAddr,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
		timers:    make(map[string]*Timer),
		delays:    make(map[string]*delayTask),
		cmdQueue:  make(chan string, 100),
		done:      make(chan struct{}),
	}

	// Initialize default ticker
	s.timers["ticker"] = NewTimer("ticker", 60*time.Second)

	s.restoreTimers()

	v.SetTimerControl(s)

	go s.runTimerLoop()
	go s.runCommandDispatcher()

	return s, nil
}

func (s *Session) runCommandDispatcher() {
	for {
		select {
		case cmd := <-s.cmdQueue:
			if err := s.SendCommand(cmd, "scheduler"); err != nil {
				log.Printf("scheduler send command error: %v", err)
			}
			// Pacing: wait 50ms between commands
			time.Sleep(50 * time.Millisecond)
		case <-s.done:
			return
		}
	}
}


func (s *Session) runTimerLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastRemSecs := make(map[string]int)

	for {
		select {
		case <-ticker.C:
			s.timersMu.Lock()
			changed := false
			var commandsToRun []string

			for name, t := range s.timers {
				rem, cmds := t.CheckSubscriptions()
				if last, ok := lastRemSecs[name]; ok && last != rem && rem >= 0 {
					// Second boundary crossed
					if len(cmds) > 0 {
						commandsToRun = append(commandsToRun, cmds...)
					}
				}
				lastRemSecs[name] = rem

				if t.Check() {
					changed = true
				}
			}
			if changed {
				s.BroadcastTick()
			}
			s.timersMu.Unlock()

			// Handle delays
			now := time.Now()
			s.delaysMu.Lock()
			for id, task := range s.delays {
				if now.After(task.due) {
					commandsToRun = append(commandsToRun, task.command)
					delete(s.delays, id)
				}
			}
			s.delaysMu.Unlock()

			// Queue commands
			for _, cmd := range commandsToRun {
				select {
				case s.cmdQueue <- cmd:
				default:
					log.Printf("command queue full, dropping scheduled command: %s", cmd)
				}
			}
		case <-s.done:
			return
		}
	}
}


func (s *Session) BroadcastTick() {
	// Caller must hold s.timersMu
	s.mu.Lock()
	if len(s.clients) == 0 {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	var snapshots []TimerSnapshot
	for name, t := range s.timers {
		if name == "ticker" || t.Enabled {
			snapshots = append(snapshots, t.Snapshot())
		}
	}

	s.broadcastMsg(ServerMsg{
		Type:   "tick",
		Timers: snapshots,
	})
}

// TimerControl implementation
func (s *Session) TickOn(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, 60*time.Second)
		s.timers[name] = t
	}
	if t.On() {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) TickOff(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		if t.Off() {
			s.BroadcastTick()
			s.persistTimer(t)
		}
	}
}

func (s *Session) TickReset(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		if t.Reset() {
			s.BroadcastTick()
			s.persistTimer(t)
		}
	}
}

func (s *Session) TickSet(name string, seconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, time.Duration(seconds*float64(time.Second)))
		s.timers[name] = t
	}
	if t.Set(time.Duration(seconds * float64(time.Second))) {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) TickSize(name string, seconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, time.Duration(seconds*float64(time.Second)))
		s.timers[name] = t
	}
	if t.Size(time.Duration(seconds * float64(time.Second))) {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) TickIcon(name string, icon string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, 60*time.Second)
		s.timers[name] = t
	}
	t.SetIcon(icon)
	s.BroadcastTick()
	s.persistTimer(t)
}

func (s *Session) SubscribeTimer(name string, second int, command string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, 60*time.Second)
		s.timers[name] = t
	}

	t.mu.Lock()
	t.Subscriptions[second] = append(t.Subscriptions[second], command)
	t.mu.Unlock()

	s.persistSubscriptions(t)
	s.persistTimer(t)
}

func (s *Session) UnsubscribeTimer(name string, second int) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		t.mu.Lock()
		delete(t.Subscriptions, second)
		t.mu.Unlock()

		s.persistSubscriptions(t)
		s.persistTimer(t)
	}
}

func (s *Session) ScheduleDelay(id string, seconds float64, command string) error {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()

	if len(s.delays) >= 50 {
		return fmt.Errorf("too many pending delays (max 50)")
	}

	// If id is empty, use a generated one
	if id == "" {
		id = "auto_" + time.Now().Format("150405.000000")
	}

	// Guardrail: minimum 100ms effective delay
	delay := time.Duration(seconds * float64(time.Second))
	if delay < 100*time.Millisecond {
		delay = 100 * time.Millisecond
	}

	s.delays[id] = &delayTask{
		id:      id,
		due:     time.Now().Add(delay),
		command: command,
	}
	return nil
}

func (s *Session) CancelDelay(id string) {
	s.delaysMu.Lock()
	defer s.delaysMu.Unlock()
	delete(s.delays, id)
}

func (s *Session) GetTimerCycleSeconds(name string) int {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		cycle := int(t.Cycle.Seconds())
		if cycle == 0 && name == "ticker" {
			return 60 // Default cycle for default ticker
		}
		return cycle
	}
	return 60 // Default cycle for any unknown named timer
}

func (s *Session) TickAdjust(name string, deltaSeconds float64) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	t, ok := s.timers[name]
	if !ok {
		t = NewTimer(name, 60*time.Second)
		s.timers[name] = t
	}
	if t.Adjust(deltaSeconds) {
		s.BroadcastTick()
		s.persistTimer(t)
	}
}

func (s *Session) persistTimer(t *Timer) {
	if s.store == nil {
		return
	}

	snapshot := t.Snapshot()
	record := storage.TimerRecord{
		SessionID:   s.sessionID,
		Name:        snapshot.Name,
		CycleMS:     snapshot.CycleMS,
		RemainingMS: snapshot.RemainingMS,
		Enabled:     snapshot.Enabled,
		Icon:        snapshot.Icon,
	}

	if snapshot.Enabled && snapshot.CycleMS > 0 {
		at := storage.SQLiteTime{Time: snapshot.NextTickAt}
		record.NextTickAt = &at
	}

	if err := s.store.SaveTimer(record); err != nil {
		log.Printf("failed to persist timer %q: %v", t.Name, err)
	}
}

func (s *Session) persistSubscriptions(t *Timer) {
	if s.store == nil {
		return
	}

	t.mu.Lock()
	// Create a copy of subscriptions to avoid holding lock during DB operations
	subsCopy := make(map[int][]string)
	for k, v := range t.Subscriptions {
		subsCopy[k] = append([]string{}, v...)
	}
	t.mu.Unlock()

	err := s.store.Transaction(func(store *storage.Store) error {
		if err := store.ClearAllSubscriptions(s.sessionID, t.Name); err != nil {
			return err
		}

		for second, commands := range subsCopy {
			for i, cmd := range commands {
				subRecord := storage.TimerSubscriptionRecord{
					SessionID: s.sessionID,
					TimerName: t.Name,
					Second:    second,
					SortOrder: i,
					Command:   cmd,
				}
				if err := store.SaveSubscription(subRecord); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("failed to persist subscriptions for timer %q: %v", t.Name, err)
	}
}

func (s *Session) restoreTimers() {
	if s.store == nil {
		return
	}

	timers, err := s.store.GetTimers(s.sessionID)
	if err != nil {
		log.Printf("failed to load persisted timers: %v", err)
		return
	}

	subs, err := s.store.GetSubscriptions(s.sessionID)
	if err != nil {
		log.Printf("failed to load timer subscriptions: %v", err)
	}

	s.timersMu.Lock()
	defer s.timersMu.Unlock()

	for _, rt := range timers {
		cycleMS := rt.CycleMS
		enabled := rt.Enabled

		if cycleMS <= 0 {
			if rt.Name == "ticker" {
				cycleMS = 60000 // Fallback to 60s for default ticker
			} else {
				enabled = false // Disable broken named timers
				cycleMS = 60000 // Keep a safe positive cycle even if disabled
			}
		}

		if enabled && rt.NextTickAt == nil {
			log.Printf("timer %q in session %d had enabled=true but next_tick_at=NULL, forcing disabled", rt.Name, s.sessionID)
			enabled = false
		}

		t := NewTimer(rt.Name, time.Duration(cycleMS)*time.Millisecond)
		t.Enabled = enabled
		t.Icon = rt.Icon
		t.RemainingMS = rt.RemainingMS

		if enabled && rt.NextTickAt != nil {
			nextAt := rt.NextTickAt.Time
			now := time.Now()

			if now.After(nextAt) {
				// Timer was running while server was down
				elapsed := now.Sub(nextAt)
				cycle := time.Duration(cycleMS) * time.Millisecond
				if cycle > 0 {
					// Recalculate phase: remaining = cycle - (elapsed % cycle)
					rem := cycle - (elapsed % cycle)
					t.NextTickAt = now.Add(rem)
					t.RemainingMS = int(rem.Milliseconds())
				} else {
					t.Enabled = false
					t.RemainingMS = 0
				}
			} else {
				t.NextTickAt = nextAt
				t.RemainingMS = int(time.Until(nextAt).Milliseconds())
			}
		}

		// Attach subscriptions
		for _, rs := range subs {
			if rs.TimerName == rt.Name {
				t.Subscriptions[rs.Second] = append(t.Subscriptions[rs.Second], rs.Command)
			}
		}

		s.timers[rt.Name] = t
	}

	// Ensure default ticker exists
	if _, ok := s.timers["ticker"]; !ok {
		s.timers["ticker"] = NewTimer("ticker", 60*time.Second)
	}
}

func (s *Session) SessionID() int64 {
	return s.sessionID
}

func (s *Session) Variables() map[string]string {
	return s.vm.Variables()
}

func (s *Session) TimerSnapshots() []TimerSnapshot {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	var snapshots []TimerSnapshot
	for name, t := range s.timers {
		if name == "ticker" || t.Enabled {
			snapshots = append(snapshots, t.Snapshot())
		}
	}
	return snapshots
}

func (s *Session) CurrentVariables() ([]VariableJSON, error) {
	vars, err := s.store.ListVariables(s.sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]VariableJSON, 0, len(vars))
	for _, v := range vars {
		result = append(result, VariableJSON{Key: v.Key, Value: v.Value})
	}
	return result, nil
}

func (s *Session) SetVariable(key, value string) error {
	return s.store.SetVariable(s.sessionID, key, value)
}

func (s *Session) DeleteVariable(key string) error {
	return s.store.DeleteVariable(s.sessionID, key)
}

func (s *Session) RecentLogs(limit int) ([]storage.LogEntry, error) {
	return s.store.RecentLogs(s.sessionID, limit)
}

func (s *Session) RecentLogsPerBuffer(limit int) (map[string][]storage.LogEntry, error) {
	return s.store.RecentLogsPerBuffer(s.sessionID, limit)
}

func (s *Session) RecentInputHistory(limit int) ([]string, error) {
	return s.store.RecentInputHistory(s.sessionID, limit)
}

func (s *Session) HighlightText(text string) string {
	if s == nil || s.vm == nil {
		return text
	}
	return s.vm.ApplyHighlights(text)
}

func (s *Session) BroadcastResult(res vm.Result) {
	if res.Kind != vm.ResultEcho {
		return
	}

	target := res.TargetBuffer
	if target == "" {
		target = "main"
	}

	// Echo to database
	id, err := s.store.AppendLogEntry(s.sessionID, target, res.Text, res.Text)
	if err != nil {
		log.Printf("failed to append echo to logs: %v", err)
		return
	}

	// Apply highlights and broadcast
	highlighted := s.HighlightText(res.Text)
	s.broadcastEntryWithText(storage.LogEntry{
		ID:        id,
		Buffer:    target,
		RawText:   res.Text,
		PlainText: res.Text,
	}, highlighted)
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	s.clients = map[int]clientSink{}
	s.mu.Unlock()

	log.Printf("closing session for %s", s.mudAddr)
	if err := s.store.MarkSessionDisconnected(s.sessionID); err != nil {
		log.Printf("mark session disconnected failed: %v", err)
	}
	return s.conn.Close()
}

func (s *Session) NotifySettingsChanged(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := ServerMsg{
		Type: "settings.changed",
		Settings: &SettingsChangedJSON{
			Domain: domain,
		},
	}

	for _, client := range s.clients {
		if err := client.send(msg); err != nil {
			log.Printf("failed to send settings notification to client %d: %v", client.id, err)
		}
	}

	// Also reload VM state
	if err := s.vm.Reload(); err != nil {
		log.Printf("failed to reload vm after settings change: %v", err)
	}
}

func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
