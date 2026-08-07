package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	mcpSessionHeaderName  = "Mcp-Session-Id"
	mcpSessionIDMaxLength = 128
	mcpSessionTTL         = 30 * time.Minute
	mcpSessionMaxClients  = 512
	// A client may mention arbitrary valid buffer names. Bound cursor streams
	// separately from the client count so that one client cannot retain an
	// unbounded map of names for the full session TTL.
	mcpSessionMaxStreamsPerClient = 64
)

// mcpSessionStore keeps best-effort continuity cursors. It is deliberately
// independent of HTTP connection details and bearer credentials: the opaque MCP
// session id is the client identity and each client owns cursors per MUD
// session and output buffer.
type mcpSessionStore struct {
	mu      sync.Mutex
	clients map[string]*mcpClientState
	now     func() time.Time
	ttl     time.Duration
	max     int
	random  io.Reader
	closed  bool
}

type mcpClientState struct {
	lastUsed time.Time
	cursors  map[mcpCursorKey]mcpCursorState
	epoch    uint64
}

// mcpCursorKey scopes continuity to the output stream the client actually
// read. Log IDs are globally ordered, but a cursor from chat has no position in
// main's visible history.
type mcpCursorKey struct {
	mudSessionID int64
	buffer       string
}

type mcpCursorState struct {
	cursor   int64
	lastUsed time.Time
}

func newMCPSessionStore() *mcpSessionStore {
	return &mcpSessionStore{
		clients: make(map[string]*mcpClientState),
		now:     time.Now,
		ttl:     mcpSessionTTL,
		max:     mcpSessionMaxClients,
		random:  rand.Reader,
	}
}

func (s *mcpSessionStore) create() (string, error) {
	return s.createAt(0)
}

func (s *mcpSessionStore) createAt(epoch uint64) (string, error) {
	if s.max <= 0 {
		return "", fmt.Errorf("MCP session capacity is zero")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", fmt.Errorf("MCP session store is closed")
	}
	for range 4 {
		var token [32]byte
		if _, err := io.ReadFull(s.random, token[:]); err != nil {
			return "", err
		}
		id := hex.EncodeToString(token[:])

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return "", fmt.Errorf("MCP session store is closed")
		}
		now := s.now()
		s.evictExpiredLocked(now)
		if _, collision := s.clients[id]; collision {
			s.mu.Unlock()
			continue
		}
		s.evictToCapacityLocked()
		s.clients[id] = &mcpClientState{lastUsed: now, cursors: make(map[mcpCursorKey]mcpCursorState), epoch: epoch}
		s.mu.Unlock()
		return id, nil
	}
	return "", fmt.Errorf("could not generate a unique MCP session id")
}

// exists validates and touches a client. The caller must not use a supplied
// header as identity unless this check succeeds.
func (s *mcpSessionStore) exists(id string) bool {
	return s.existsAt(id, 0)
}

// existsAt binds opaque continuity state to the gate snapshot that created it.
// A configuration generation can change between a request reading its snapshot
// and the store clearing clients; the epoch check closes that race without
// blocking an in-flight tool call.
func (s *mcpSessionStore) existsAt(id string, epoch uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.evictExpiredLocked(now)
	client, ok := s.clients[id]
	if !ok || client.epoch != epoch {
		return false
	}
	client.lastUsed = now
	return true
}

func (s *mcpSessionStore) cursor(id string, mudSessionID int64) (int64, bool) {
	return s.cursorForBuffer(id, mudSessionID, "main")
}

func (s *mcpSessionStore) cursorForBuffer(id string, mudSessionID int64, buffer string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return 0, false
	}
	key := mcpCursorKey{mudSessionID: mudSessionID, buffer: buffer}
	state, ok := client.cursors[key]
	if !ok {
		return 0, true
	}
	state.lastUsed = s.now()
	client.cursors[key] = state
	return state.cursor, true
}

// mergeCursor never moves a client's cursor backwards. Parallel calls can
// complete in any order without losing the furthest output already delivered.
func (s *mcpSessionStore) mergeCursor(id string, mudSessionID, cursor int64) {
	s.mergeCursorForBuffer(id, mudSessionID, "main", cursor)
}

func (s *mcpSessionStore) mergeCursorForBuffer(id string, mudSessionID int64, buffer string, cursor int64) {
	if cursor <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return
	}
	key := mcpCursorKey{mudSessionID: mudSessionID, buffer: buffer}
	now := s.now()
	state, exists := client.cursors[key]
	if !exists && len(client.cursors) >= mcpSessionMaxStreamsPerClient {
		s.evictOldestCursorLocked(client)
	}
	if cursor > state.cursor {
		state.cursor = cursor
	}
	state.lastUsed = now
	client.cursors[key] = state
	client.lastUsed = now
}

func (s *mcpSessionStore) evictOldestCursorLocked(client *mcpClientState) {
	var oldestKey mcpCursorKey
	var oldest mcpCursorState
	haveOldest := false
	for key, state := range client.cursors {
		if !haveOldest || state.lastUsed.Before(oldest.lastUsed) ||
			(state.lastUsed.Equal(oldest.lastUsed) && cursorKeyLess(key, oldestKey)) {
			oldestKey, oldest, haveOldest = key, state, true
		}
	}
	if haveOldest {
		delete(client.cursors, oldestKey)
	}
}

func cursorKeyLess(left, right mcpCursorKey) bool {
	if left.mudSessionID != right.mudSessionID {
		return left.mudSessionID < right.mudSessionID
	}
	return left.buffer < right.buffer
}

func (s *mcpSessionStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.clients = make(map[string]*mcpClientState)
}

// invalidate drops all implicit continuity state while keeping the store ready
// for a new MCP credential or re-enable. It is used after credential rotation
// and disable; clear is reserved for server shutdown.
func (s *mcpSessionStore) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = make(map[string]*mcpClientState)
}

func (s *mcpSessionStore) evictExpiredLocked(now time.Time) {
	for id, client := range s.clients {
		if now.Sub(client.lastUsed) >= s.ttl {
			delete(s.clients, id)
		}
	}
}

func (s *mcpSessionStore) evictToCapacityLocked() {
	if s.max <= 0 {
		s.clients = make(map[string]*mcpClientState)
		return
	}
	// Reserve one slot for the new client. Stable ordering makes capacity tests
	// deterministic when several requests have the same clock value.
	if len(s.clients) < s.max {
		return
	}
	type candidate struct {
		id       string
		lastUsed time.Time
	}
	clients := make([]candidate, 0, len(s.clients))
	for id, client := range s.clients {
		clients = append(clients, candidate{id: id, lastUsed: client.lastUsed})
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].lastUsed.Equal(clients[j].lastUsed) {
			return clients[i].id < clients[j].id
		}
		return clients[i].lastUsed.Before(clients[j].lastUsed)
	})
	delete(s.clients, clients[0].id)
}
