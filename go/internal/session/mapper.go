package session

import (
	"log"

	"rubymud/go/internal/mapper"
)

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
	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		log.Printf("mapper: GetActiveMapSetID failed: %v", err)
	}

	var idx *mapper.Index
	if ok && setID > 0 {
		rooms, err := s.store.ListRooms(setID)
		if err != nil {
			log.Printf("mapper: ListRooms(%d) failed: %v", setID, err)
		} else {
			idx = mapper.BuildIndex(setID, rooms)
		}
	}

	s.mapMu.Lock()
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndex(idx)
	}
	s.mapMu.Unlock()
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

// ReloadActiveMapSet is the exported hook the web layer calls after changing the
// active set or importing rooms into it, to keep the index in sync (AGENTS #2).
func (s *Session) ReloadActiveMapSet() {
	s.LoadActiveMapSet()
	s.BroadcastMapPosition()
}
