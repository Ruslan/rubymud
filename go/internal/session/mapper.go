package session

import (
	"errors"
	"log"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// ErrNoActiveMapSet is returned by the topology write-path when the session has
// no (or a cleared) active map set — the caller soft-fails with a reason.
var ErrNoActiveMapSet = errors.New("no active map set for this session")

// (exported for the web layer to distinguish the soft-fail from a real error.)

// This file wires the mapper position tracker (plan §5 phase 2+3) into the
// session. Design constraints:
//   - The room-block parser runs in processLine (a latency-tracked HOT path) but
//     stays off the DB/critical path: a cheap prefix guard runs first, and the
//     bounded accumulator only does regex/reconstruction on the rare exits line.
//   - The tracker index is rebuilt from storage (never drifted) on connect /
//     active-set change / import affecting the active set (AGENTS #2).
//   - Broadcasts (room, room_position) go through broadcastMsg, off the hot path.

// LoadActiveMapSet (re)builds the tracker index from the session's active map
// set. Call on connect, active-set change, and after an import that affects the
// active set. It reads the DB, so it MUST run off the incoming-line hot path.
// A missing/NULL/dangling set degrades gracefully to an empty (nil-index)
// tracker (soft red), no panics.
func (s *Session) LoadActiveMapSet() {
	idx := s.buildActiveIndex()

	s.mapMu.Lock()
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndex(idx)
	}
	s.mapMu.Unlock()
}

// buildActiveIndex reads the session's active set from storage and builds its
// tracker index (with the DT-annotation overlay applied), or nil when there is
// no/dangling active set. It performs DB reads, so it MUST run off the hot path
// and OUTSIDE mapMu (callers take the lock only to swap the built index in).
func (s *Session) buildActiveIndex() *mapper.Index {
	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		log.Printf("mapper: GetActiveMapSetID failed: %v", err)
	}
	if !ok || setID <= 0 {
		return nil
	}
	rooms, err := s.store.ListRooms(setID)
	if err != nil {
		log.Printf("mapper: ListRooms(%d) failed: %v", setID, err)
		return nil
	}
	idx := mapper.BuildIndex(setID, rooms)
	// Overlay DT annotations onto the effective is_dt so mud_path's DT-refusal and
	// mud_look_map's DT display treat an annotated cell as a death trap (plan §7).
	// Annotations are a separate overlay path — they never fork the set; the index
	// is rebuilt on annotations_changed.
	annos, err := s.store.ListRoomAnnotations(setID, "")
	if err != nil {
		log.Printf("mapper: ListRoomAnnotations(%d) failed: %v", setID, err)
		return idx
	}
	var dt []mapper.Coord
	for _, a := range annos {
		if a.DT {
			dt = append(dt, mapper.Coord{Zone: a.Zone, X: a.X, Y: a.Y, L: a.L})
		}
	}
	idx.ApplyAnnotationDT(dt)
	return idx
}

// observeIncomingLine is the mapper hook called from processLine for every
// incoming line. It is intentionally cheap: it feeds the accumulator (which does
// a prefix guard before any regex) and, only when a full room block completes,
// runs tracker reconciliation and broadcasts. It also watches for movement
// refusals ("не можете идти") to head-cancel the pending queue. No DB access.
//
// `plainText` is the already-ANSI/control-stripped line (processLine computes it
// once). The accumulator consumes it directly — NO second ANSI strip on the hot
// path. On a non-exits line the only work is a prefix guard + a ring append.
//
// Client broadcasts are built while mapMu is held but sent AFTER the lock is
// released, so a slow/stuck WS client never stalls under the map lock (which
// would block a concurrent MCP mud_where).
func (s *Session) observeIncomingLine(plainText string) {
	var toBroadcast []ServerMsg

	s.mapMu.Lock()

	// Movement refusal: head-cancel the queue, no position move. Cheap substring
	// check; only meaningful when a tracker exists.
	if s.mapTracker != nil && mapper.IsRefusal(plainText) {
		if s.mapTracker.CancelHead() {
			toBroadcast = append(toBroadcast, s.roomPositionMsgLocked())
		}
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		return
	}

	if s.mapAccum == nil {
		s.mapAccum = mapper.NewAccumulator()
	}
	ev, ok := s.mapAccum.Feed(plainText)
	if !ok {
		s.mapMu.Unlock()
		return
	}

	// Room event to clients (UI current-room signal).
	toBroadcast = append(toBroadcast, ServerMsg{
		Type:      "room",
		RoomHint:  ev.Hint,
		RoomDesc:  ev.Desc,
		RoomExits: ev.Exits,
	})

	if s.mapTracker != nil {
		if _, changed := s.mapTracker.Reconcile(ev); changed {
			toBroadcast = append(toBroadcast, s.roomPositionMsgLocked())
		}
	}
	s.mapMu.Unlock()

	s.broadcastAll(toBroadcast)
}

// broadcastAll sends each message to clients. Called OUTSIDE mapMu.
func (s *Session) broadcastAll(msgs []ServerMsg) {
	for _, m := range msgs {
		s.broadcastMsg(m)
	}
}

// observeOutgoingMove is called from the command write path (after alias/VM
// expansion) for each command actually written to the MUD. If the command is a
// movement direction it is pushed onto the tracker's FIFO pending-moves queue.
func (s *Session) observeOutgoingMove(cmd string) {
	dir, ok := mapper.MoveDir(cmd)
	if !ok {
		return
	}
	s.mapMu.Lock()
	if s.mapTracker != nil {
		s.mapTracker.PushMove(dir)
	}
	s.mapMu.Unlock()
}

// roomPositionMsgLocked builds the current tracker-position ServerMsg. Caller
// must hold mapMu; the caller is responsible for sending it AFTER releasing the
// lock (do not couple the map lock to client I/O). Returns an empty ServerMsg
// (Type=="") when there is no tracker; broadcastAll/broadcastMsg on it is
// harmless but callers generally only append when there is a tracker.
func (s *Session) roomPositionMsgLocked() ServerMsg {
	if s.mapTracker == nil {
		return ServerMsg{}
	}
	pos := s.mapTracker.Position()
	msg := ServerMsg{
		Type:            "room_position",
		Confidence:      string(pos.Confidence),
		PendingMoves:    s.mapTracker.PendingCount(),
		PositionValid:   pos.Valid,
		PositionReason:  pos.Reason,
		ExitsAddedLive:  pos.ExitsAddedLive,
		ExitsRemovedMap: pos.ExitsRemovedMap,
	}
	if pos.Valid {
		msg.Zone = pos.Coord.Zone
		msg.RoomX = pos.Coord.X
		msg.RoomY = pos.Coord.Y
		msg.RoomL = pos.Coord.L
		if room := s.mapTracker.CurrentRoom(); room != nil {
			msg.RoomHint = room.Hint
			msg.IsDT = room.IsDT
			msg.Pipe = room.Pipe
		}
	}
	return msg
}

// --- MCP / accessor surface (read-only over tracker state) -----------------

// MapTracker exposes the tracker under lock for MCP tools. The callback runs
// while mapMu is held; it must not block or call back into the session. Returns
// false if there is no tracker yet.
func (s *Session) WithMapTracker(fn func(*mapper.Tracker)) bool {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return false
	}
	fn(s.mapTracker)
	return true
}

// BroadcastMapPosition broadcasts the current tracker position to UI clients
// (used after an MCP re-anchor). Builds the message under mapMu, then sends it
// after releasing the lock (client I/O never runs under the map lock).
func (s *Session) BroadcastMapPosition() {
	s.mapMu.Lock()
	msg := s.roomPositionMsgLocked()
	s.mapMu.Unlock()
	if msg.Type != "" {
		s.broadcastMsg(msg)
	}
}

// ApplyAnnotationDT updates ONE cell's effective is_dt in the live tracker index
// in place (native is_dt || annoDT), WITHOUT rebuilding the index — so a
// DT-touching annotation on the current room does NOT reset the tracker's
// position/confidence or flush the pending queue (a full reload would). Called by
// the MCP annotate write for dt-touching annotates only. Returns false when there
// is no tracker/index yet. Off the incoming-line hot path.
func (s *Session) ApplyAnnotationDT(zone string, x, y, l int, annoDT bool) bool {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return false
	}
	idx := s.mapTracker.Index()
	if idx == nil {
		return false
	}
	idx.SetAnnotationDT(mapper.Coord{Zone: zone, X: x, Y: y, L: l}, annoDT)
	return true
}

// ReloadActiveMapSet is the exported hook the web layer calls after changing the
// active set or importing rooms into it, to keep the index in sync (AGENTS #2).
func (s *Session) ReloadActiveMapSet() {
	s.LoadActiveMapSet()
	s.BroadcastMapPosition()
}

// reloadPreservingPosition rebuilds the tracker index for the active set but
// PRESERVES the tracker's current position/confidence/pending when the current
// cell still exists in the rebuilt index (plan §8 footgun: a topology write must
// not reset the tracker to 🔴 or flush pending just because the graph changed).
// It only falls to 🔴 when the current cell no longer exists (e.g. it was
// deleted). Broadcasts the (possibly unchanged) position afterward. DB reads run
// off mapMu; only the index swap is under the lock.
func (s *Session) reloadPreservingPosition() {
	idx := s.buildActiveIndex()
	s.mapMu.Lock()
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndexPreservingPosition(idx)
	}
	s.mapMu.Unlock()
	s.BroadcastMapPosition()
}

// TopologyWriteResult reports the outcome of a WriteTopology / UndoTopology call.
type TopologyWriteResult struct {
	// Applied is false when the target room was not found in the (editable) set —
	// callers soft-fail (200 with a reason) rather than erroring.
	Applied bool
	// Forked is true when the write forked a frozen imported set into an editable
	// copy; ForkedTo is the new editable set id the session is now active on.
	Forked   bool
	ForkedTo int64
	// SetID is the editable set the write actually landed on (the fork when Forked,
	// else the original active set).
	SetID int64
	// NothingToUndo is set by UndoTopology when the set's journal is empty.
	NothingToUndo bool
}

// WriteTopology is the UNIFIED topology write-path (plan §8): the ONE backend
// function UI edits and MCP CRUD funnel through. Per write it:
//  1. resolves the session's active set; if it is frozen (editable=false), forks
//     it copy-on-write, switches the session's active set to the fork, and
//     broadcasts map_sets_changed (a fork is a new set);
//  2. applies the op to the (now editable) set via the single ApplyTopologyOp;
//  3. pushes the write's BEFORE-STATE snapshot onto the in-memory per-map-set undo
//     journal (a literal prior-state record, not a symmetric inverse op — so undo
//     is correct even for idempotent no-op patches);
//  4. broadcasts rooms_changed and refreshes the tracker index PRESERVING position.
//
// The WHOLE read-active-set → fork → setActive → apply → journal sequence runs
// under the shared per-map-set TopologyLock keyed on the active set id at ENTRY,
// so two sessions racing their first write to the same FROZEN set cannot both read
// editable=false and double-fork it: the second blocks on the source set's lock,
// then observes the active set is already the editable fork (no re-fork).
//
// This slice wires exactly one op kind (TopoPatchExits, behind map-cell-patch);
// slice-3 CRUD ops plug in as more TopologyOpKind arms of ApplyTopologyOp with no
// change to this orchestration. Returns Applied=false (no error) when the target
// room is absent so the caller soft-fails.
func (s *Session) WriteTopology(op storage.TopologyOp) (TopologyWriteResult, error) {
	var res TopologyWriteResult

	entryID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return res, err
	}
	if !ok || entryID <= 0 {
		return res, ErrNoActiveMapSet
	}

	// Serialize the whole write against the ENTRY set id (the source set when a
	// fork is about to happen). Re-read the active set INSIDE the lock so a session
	// that lost the fork race sees the fork the winner already installed.
	lock := s.store.TopologyLock(entryID)
	lock.Lock()
	defer lock.Unlock()

	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return res, err
	}
	if !ok || setID <= 0 {
		return res, ErrNoActiveMapSet
	}

	// Resolve editability; fork-if-frozen (copy-on-write).
	set, err := s.store.GetMapSet(setID)
	if err != nil {
		return res, err
	}
	if !set.Editable {
		newID, ferr := s.store.ForkMapSet(setID)
		if ferr != nil {
			return res, ferr
		}
		if serr := s.store.SetActiveMapSetID(s.sessionID, newID); serr != nil {
			return res, serr
		}
		setID = newID
		res.Forked = true
		res.ForkedTo = newID
		// A fork is a NEW set — tell the UI the active set changed so it refetches.
		s.broadcastMapSetChanged(newID)
	}
	res.SetID = setID

	return s.applyTopologyToSet(setID, op, res, true)
}

// applyTopologyToSet applies op to an ALREADY-EDITABLE set and refreshes the index
// preserving position. When journal is true (a fresh write) it captures the op's
// BEFORE-STATE snapshot and pushes it onto the per-set undo stack. Undo passes
// journal=false: the snapshot restore it performs is a consumed, one-way revert —
// re-journaling would turn the bounded stack into a two-state redo toggle and break
// the "nothing to undo when empty" contract. Shared so undo re-applies through the
// same single topology mutator (no parallel path). Runs under the caller's
// TopologyLock so the DB write and the journal push stay ordered.
func (s *Session) applyTopologyToSet(setID int64, op storage.TopologyOp, res TopologyWriteResult, journal bool) (TopologyWriteResult, error) {
	before, found, err := s.store.ApplyTopologyOp(setID, op)
	if err != nil {
		return res, err
	}
	if !found {
		return res, nil
	}
	res.Applied = true

	if journal {
		// Journal the BEFORE-STATE snapshot, keyed to the (editable) set — NOT the
		// session. Restoring this literal prior state is a true inverse regardless of
		// the patch's idempotency.
		s.store.WriteJournal().Push(setID, storage.UndoEntry{Label: op.UndoLabel(), Before: before})
	}

	// Refresh the tracker index (the graph changed) preserving position, and tell
	// open map panes to reload the zone (carry the set id so a pane can filter).
	s.reloadPreservingPosition()
	s.broadcastRoomsChanged(setID)
	return res, nil
}

// UndoTopology pops the last topology write on the session's active set and
// restores the recorded BEFORE-STATE through the store (the true inverse), then
// refreshes the index preserving position and broadcasts. It does NOT fork (undo
// only ever targets an editable set — a frozen set never had a write, so its
// journal is empty), and runs under the per-set TopologyLock so undo order matches
// write order. Returns NothingToUndo=true when the set's journal is empty, and the
// UndoEntry that was applied for reporting.
func (s *Session) UndoTopology() (TopologyWriteResult, storage.UndoEntry, error) {
	var res TopologyWriteResult

	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return res, storage.UndoEntry{}, err
	}
	if !ok || setID <= 0 {
		return res, storage.UndoEntry{}, ErrNoActiveMapSet
	}

	lock := s.store.TopologyLock(setID)
	lock.Lock()
	defer lock.Unlock()

	res.SetID = setID

	entry, ok := s.store.WriteJournal().Pop(setID)
	if !ok {
		res.NothingToUndo = true
		return res, storage.UndoEntry{}, nil
	}
	found, err := s.store.RestoreRoomExitState(setID, entry.Before)
	if err != nil {
		return res, entry, err
	}
	if !found {
		// The target cell vanished (deleted by a later write) — nothing to restore.
		return res, entry, nil
	}
	res.Applied = true
	s.reloadPreservingPosition()
	s.broadcastRoomsChanged(setID)
	return res, entry, nil
}

// broadcastRoomsChanged / broadcastMapSetChanged emit the write-path change
// notifications carrying the affected map_set_id so an open map pane bound to a
// now-frozen original can tell a change/fork moved the session to a different set
// and refetch (plan §8 invalidation).
func (s *Session) broadcastRoomsChanged(mapSetID int64) {
	s.BroadcastServerMsg(ServerMsg{Type: "rooms_changed", MapSetID: mapSetID})
}

func (s *Session) broadcastMapSetChanged(mapSetID int64) {
	s.BroadcastServerMsg(ServerMsg{Type: "map_sets_changed", MapSetID: mapSetID})
}
