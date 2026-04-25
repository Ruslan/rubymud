package session

import (
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
	}

	// Initialize default ticker
	s.timers["ticker"] = NewTimer("ticker", 60*time.Second)

	v.SetTimerControl(s)

	go s.runTimerLoop()

	return s, nil
}

func (s *Session) runTimerLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		if s.IsClosed() {
			return
		}

		s.timersMu.Lock()
		changed := false
		for _, t := range s.timers {
			if t.Check() {
				changed = true
			}
		}
		if changed {
			s.BroadcastTick()
		}
		s.timersMu.Unlock()
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
	for _, t := range s.timers {
		snapshots = append(snapshots, t.Snapshot())
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
	}
}

func (s *Session) TickOff(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		if t.Off() {
			s.BroadcastTick()
		}
	}
}

func (s *Session) TickReset(name string) {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if t, ok := s.timers[name]; ok {
		if t.Reset() {
			s.BroadcastTick()
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
	for _, t := range s.timers {
		snapshots = append(snapshots, t.Snapshot())
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
