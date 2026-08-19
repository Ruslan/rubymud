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
			active, comp, decomp, ratio := sess.MCCPStats()
			records[i].MCCPActive = active
			records[i].MCCPCompressedBytes = comp
			records[i].MCCPDecompressedBytes = decomp
			records[i].MCCPCompressionRatio = ratio
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

// ReloadDeletedMapSetSessions clears live tracker indexes after storage has
// reset their active_map_set_id columns. It is intentionally separate from the
// DB mutation so ReloadActiveMapSet never runs while its SQLite transaction is
// open.
func (m *Manager) ReloadDeletedMapSetSessions(ids []int64) {
	for _, id := range ids {
		if sess, ok := m.GetSession(id); ok {
			sess.ReloadActiveMapSet()
		}
	}
}

// BroadcastMapSetsChanged invalidates map-set lists and panes in every live
// session. A deletion is global: even a client following another set may show
// the deleted set in Settings or a selector.
func (m *Manager) BroadcastMapSetsChanged() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if !sess.IsClosed() {
			sessions = append(sessions, sess)
		}
	}
	m.mu.Unlock()
	for _, sess := range sessions {
		sess.BroadcastServerMsg(ServerMsg{Type: "map_sets_changed"})
	}
}

// BroadcastRoomKnowledgeChanged invalidates only the lazy knowledge view for
// one logical room. It deliberately does not reload mapper indexes or rooms:
// inhabitants are a non-topological overlay, and a full zone/tracker refresh
// would both waste work and risk disturbing live position state.
func (m *Manager) BroadcastRoomKnowledgeChanged(mapSetID int64, zone string, x, y, l int) {
	m.BroadcastRoomsKnowledgeChanged(mapSetID, []storage.EdgeCell{{Zone: zone, X: x, Y: y, L: l}})
}

// BroadcastMapKnowledgeChanged coalesces an entity-wide shared-field update to
// one message per active session. Each browser can then refresh its one open
// room dialog, avoiding an unbounded placement read and coordinate fanout.
func (m *Manager) BroadcastMapKnowledgeChanged(mapSetID int64) {
	m.mu.Lock()
	sessions := make([]struct {
		id   int64
		sess *Session
	}, 0, len(m.sessions))
	for id, sess := range m.sessions {
		if !sess.IsClosed() {
			sessions = append(sessions, struct {
				id   int64
				sess *Session
			}{id: id, sess: sess})
		}
	}
	m.mu.Unlock()
	for _, item := range sessions {
		active, ok, err := m.store.GetActiveMapSetID(item.id)
		if err == nil && ok && active == mapSetID {
			item.sess.BroadcastServerMsg(ServerMsg{Type: "map_knowledge_changed", MapSetID: mapSetID})
		}
	}
}

// BroadcastRoomsKnowledgeChanged batches caller-supplied exact-room
// invalidations. Active map-set lookup happens once per session and duplicate
// coordinates are collapsed before WebSocket fanout.
func (m *Manager) BroadcastRoomsKnowledgeChanged(mapSetID int64, cells []storage.EdgeCell) {
	unique := make([]storage.EdgeCell, 0, len(cells))
	seen := make(map[string]bool, len(cells))
	for _, cell := range cells {
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%d", cell.Zone, cell.X, cell.Y, cell.L)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, cell)
		}
	}
	if len(unique) == 0 {
		return
	}
	m.mu.Lock()
	sessions := make([]struct {
		id   int64
		sess *Session
	}, 0, len(m.sessions))
	for id, sess := range m.sessions {
		if !sess.IsClosed() {
			sessions = append(sessions, struct {
				id   int64
				sess *Session
			}{id: id, sess: sess})
		}
	}
	m.mu.Unlock()
	for _, item := range sessions {
		active, ok, err := m.store.GetActiveMapSetID(item.id)
		if err != nil || !ok || active != mapSetID {
			continue
		}
		for _, cell := range unique {
			item.sess.BroadcastServerMsg(ServerMsg{
				Type: "room_knowledge_changed", MapSetID: mapSetID,
				Zone: cell.Zone, RoomX: cell.X, RoomY: cell.Y, RoomL: cell.L,
			})
		}
	}
}

// RefreshSharedZoneMove keeps other live sessions on an editable shared set
// coherent after a namespace move. A COW fork has no peers on its new id, but an
// ordinary editable set can be active in several sessions.
func (m *Manager) RefreshSharedZoneMove(mapSetID, skipSessionID int64, move storage.ZoneMoveInput) {
	m.mu.Lock()
	sessions := make([]struct {
		id int64
		s  *Session
	}, 0, len(m.sessions))
	for id, s := range m.sessions {
		if id != skipSessionID && !s.IsClosed() {
			sessions = append(sessions, struct {
				id int64
				s  *Session
			}{id, s})
		}
	}
	m.mu.Unlock()
	for _, item := range sessions {
		active, ok, err := m.store.GetActiveMapSetID(item.id)
		if err == nil && ok && active == mapSetID {
			item.s.RefreshTopologyAfterZoneMove(mapSetID, move)
		}
	}
}

func (m *Manager) RefreshSharedTopology(mapSetID, skipSessionID int64) {
	m.mu.Lock()
	items := make([]struct {
		id int64
		s  *Session
	}, 0, len(m.sessions))
	for id, s := range m.sessions {
		if id != skipSessionID && !s.IsClosed() {
			items = append(items, struct {
				id int64
				s  *Session
			}{id, s})
		}
	}
	m.mu.Unlock()
	for _, item := range items {
		active, ok, err := m.store.GetActiveMapSetID(item.id)
		if err == nil && ok && active == mapSetID {
			item.s.RefreshSharedTopology(mapSetID)
		}
	}
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
	v.SetLocation(storage.LoadLocationOrUTC(record.Timezone))
	if err := v.Reload(); err != nil {
		return nil, fmt.Errorf("vm reload: %w", err)
	}

	mccpOn := record.MCCPEnabled != 0

	addr := fmt.Sprintf("%s:%d", record.MudHost, record.MudPort)
	sess, err := New(record.ID, addr, m.store, v, record.InitialCommands, mccpOn)
	if err != nil {
		return nil, err
	}

	// Build the mapper tracker index from the session's active map set (off the
	// hot path; degrades to an empty tracker if no/dangling set).
	sess.LoadActiveMapSet()

	// Resolve the session's room-format profile and build the accumulator from it
	// (off the hot path; degrades to the builtin RMUD profile on any problem, and
	// says so on the wire). This is the ONE place the profile is resolved on the
	// connect path — never per incoming line.
	sess.LoadMapperProfile()

	// Install the tracker and detector BEFORE reading the socket. A MUD may send
	// its first room block immediately after TCP connect; the kernel already
	// buffers those bytes while the cheap DB-backed mapper setup runs. Starting
	// the read loop earlier loses that first room for mapping even though it is
	// still shown in the output log.
	m.sessions[id] = sess
	go sess.RunReadLoop()

	sess.QueueStartupCommands()

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
	if err := m.store.UpdateSession(record); err != nil {
		return err
	}
	// Keep a live VM in sync so a manual timezone edit takes effect immediately,
	// mirroring the follow/pin sync on the WS-connect path.
	if sess, ok := m.GetSession(record.ID); ok {
		sess.SetTimezoneLocation(storage.LoadLocationOrUTC(storage.NormalizeTimezone(record.Timezone)))
	}
	return nil
}

func (m *Manager) DeleteSession(id int64) error {
	_ = m.Disconnect(id)
	return m.store.DeleteSession(id)
}
