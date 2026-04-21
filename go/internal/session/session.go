package session

import (
	"log"
	"net"
	"sync"

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
}

func New(sessionID int64, mudAddr string, store *storage.Store, v *vm.VM) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	return &Session{
		sessionID: sessionID,
		mudAddr:   mudAddr,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
	}, nil
}

func (s *Session) SessionID() int64 {
	return s.sessionID
}

func (s *Session) Variables() map[string]string {
	return s.vm.Variables()
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

func (s *Session) ListAliases() ([]storage.AliasRule, error) {
	return s.store.ListAliases(s.sessionID)
}

func (s *Session) SaveAlias(a storage.AliasRule) error {
	// For now SaveAlias in storage uses name-based UPSERT.
	// In future we might want to strictly separate Create/Update by ID.
	return s.store.SaveAlias(s.sessionID, a.Name, a.Template)
}

func (s *Session) UpdateAlias(a storage.AliasRule) error {
	return s.store.UpdateAlias(a)
}

func (s *Session) DeleteAlias(id int64) error {
	return s.store.DeleteAliasByID(id)
}

func (s *Session) ListTriggers() ([]storage.TriggerRule, error) {
	return s.store.ListTriggers(s.sessionID)
}

func (s *Session) CreateTrigger(t storage.TriggerRule) error {
	t.SessionID = s.sessionID
	return s.store.CreateTrigger(t)
}

func (s *Session) UpdateTrigger(t storage.TriggerRule) error {
	return s.store.UpdateTrigger(t)
}

func (s *Session) DeleteTrigger(id int64) error {
	return s.store.DeleteTriggerByID(id)
}

func (s *Session) ListHighlights() ([]storage.HighlightRule, error) {
	return s.store.ListHighlights(s.sessionID)
}

func (s *Session) CreateHighlight(h storage.HighlightRule) error {
	h.SessionID = s.sessionID
	return s.store.CreateHighlight(h)
}

func (s *Session) UpdateHighlight(h storage.HighlightRule) error {
	return s.store.UpdateHighlight(h)
}

func (s *Session) DeleteHighlight(id int64) error {
	return s.store.DeleteHighlightByID(id)
}

func (s *Session) RecentLogs(limit int) ([]storage.LogEntry, error) {
	return s.store.RecentLogs(s.sessionID, limit)
}

func (s *Session) RecentInputHistory(limit int) ([]string, error) {
	return s.store.RecentInputHistory(s.sessionID, limit)
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

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
