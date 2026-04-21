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

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
