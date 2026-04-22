package session

import (
	"fmt"
	"log"
	"sync"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

type Manager struct {
	store    *storage.Store
	sessions map[int64]*Session
	mu       sync.Mutex
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[int64]*Session),
	}
}

func (m *Manager) ListSessions() ([]storage.SessionRecord, error) {
	records, err := m.store.ListSessions()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range records {
		if sess, ok := m.sessions[records[i].ID]; ok && !sess.IsClosed() {
			records[i].Status = "connected"
		} else {
			records[i].Status = "disconnected"
		}
	}

	return records, nil
}

func (m *Manager) GetSession(id int64) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if ok && sess.IsClosed() {
		delete(m.sessions, id)
		return nil, false
	}
	return sess, ok
}

func (m *Manager) Connect(id int64) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[id]; ok && !sess.IsClosed() {
		return sess, nil
	}

	record, err := m.store.GetSession(id)
	if err != nil {
		return nil, err
	}

	v := vm.New(m.store, record.ID)
	if err := v.Reload(); err != nil {
		return nil, fmt.Errorf("vm reload: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", record.MudHost, record.MudPort)
	sess, err := New(record.ID, addr, m.store, v)
	if err != nil {
		return nil, err
	}

	m.sessions[id] = sess
	go sess.RunReadLoop()

	if err := m.store.MarkSessionConnected(id); err != nil {
		log.Printf("failed to mark session connected in db: %v", err)
	}

	return sess, nil
}

func (m *Manager) Disconnect(id int64) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if ok {
		return sess.Close()
	}

	return m.store.MarkSessionDisconnected(id)
}

func (m *Manager) CreateSession(name, host string, port int) (storage.SessionRecord, error) {
	return m.store.CreateSession(name, host, port)
}

func (m *Manager) UpdateSession(record storage.SessionRecord) error {
	return m.store.UpdateSession(record)
}

func (m *Manager) DeleteSession(id int64) error {
	_ = m.Disconnect(id)
	return m.store.DeleteSession(id)
}
