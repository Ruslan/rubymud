package session

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// trackerHarness builds a bare Session with a mapper tracker over a small
// two-room corridor and a capturing client, without a live TCP/DB session. It
// exercises the observeIncomingLine wiring (H1/H2) directly.
func trackerHarness(t *testing.T) (*Session, *[]ServerMsg, *sync.Mutex) {
	t.Helper()
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "Первая", "one", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "Вторая", "two", "N"),
	}
	idx := buildTestMapIndex(1, rooms)
	tr := mapper.NewTracker(idx)

	s := &Session{
		clients:     make(map[int]clientSink),
		mapDetector: mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.RMUDDetectorInfo()),
	}
	s.mapTracker = tr

	var mu sync.Mutex
	var got []ServerMsg
	s.AttachClient("test", func(msg ServerMsg) error {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
		return nil
	})
	return s, &got, &mu
}

func mkTrackRoom(zone string, x, y, l, tag int, hint, desc, exits string) storage.Room {
	tg := tag
	edirs := "[]"
	if exits != "" {
		b, _ := json.Marshal([]string{exits})
		edirs = string(b)
	}
	return storage.Room{
		Zone: zone, X: x, Y: y, L: l, Tag: &tg,
		Hint: hint, Desc: desc, Exits: exits, EDirs: edirs, Doors: "[]",
	}
}

// TestObserveIncomingRefusalCancelsHead drives the refusal -> CancelHead path
// through observeIncomingLine (not just the unit assert), covering the wiring
// and the broadcast-after-unlock behavior (H2).
func TestObserveIncomingRefusalCancelsHead(t *testing.T) {
	s, got, mu := trackerHarness(t)

	// Anchor at the first room, then queue a bogus move.
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("N") // no north exit; the MUD will refuse
	if s.mapTracker.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", s.mapTracker.PendingCount())
	}

	// A refusal line flows in.
	s.observeIncomingLine("Вы не можете идти в этом направлении.", "Вы не можете идти в этом направлении.")

	if s.mapTracker.PendingCount() != 0 {
		t.Errorf("refusal should head-cancel the queue, pending=%d", s.mapTracker.PendingCount())
	}
	if s.mapTracker.Position().Coord.X != 0 {
		t.Errorf("position should not move on refusal: %+v", s.mapTracker.Position())
	}

	// The head-cancel changed pending_moves, so a room_position broadcast fired.
	mu.Lock()
	defer mu.Unlock()
	var sawPos bool
	for _, m := range *got {
		if m.Type == "room_position" {
			sawPos = true
			if m.PendingMoves != 0 {
				t.Errorf("broadcast pending_moves = %d, want 0", m.PendingMoves)
			}
		}
	}
	if !sawPos {
		t.Errorf("expected a room_position broadcast after head-cancel, got %+v", *got)
	}
}

// TestObserveIncomingRoomBlockReconciles drives a full room block through the
// pipeline and confirms both a `room` and a `room_position` broadcast fire, and
// the tracker advanced (the accumulator consumes already-stripped plainText).
func TestObserveIncomingRoomBlockReconciles(t *testing.T) {
	s, got, mu := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")

	// Feed the room block for "Вторая" line by line (already stripped).
	for _, line := range []string{
		"Вторая",
		".[ ].....      two",
		"[ Exits: N ]",
	} {
		s.observeIncomingLine(line, line)
	}

	if s.mapTracker.Position().Coord.X != 1 || s.mapTracker.Position().Confidence != mapper.Green {
		t.Errorf("expected green step to x=1, got %+v", s.mapTracker.Position())
	}

	mu.Lock()
	defer mu.Unlock()
	var sawRoom, sawPos bool
	for _, m := range *got {
		switch m.Type {
		case "room":
			sawRoom = true
		case "room_position":
			sawPos = true
		}
	}
	if !sawRoom || !sawPos {
		t.Errorf("expected both room and room_position broadcasts, got %+v", *got)
	}
}

// TestObserveIncomingRoomBlockRetainsLastRoom covers the live-room retention work:
// the session RETAINS the last completed room block so a human "record this
// room" write can reuse the game's own text. Nothing is retained before a block
// completes; the completed block is then readable through LastRoomEvent, and a
// later block replaces it.
func TestObserveIncomingRoomBlockRetainsLastRoom(t *testing.T) {
	s, _, _ := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	if _, ok := s.LastRoomEvent(); ok {
		t.Fatal("no room block has completed yet — LastRoomEvent must report false")
	}

	// A partial block (header only) must NOT be retained: only a completed block
	// (terminated by the exits line) is a room event.
	s.observeIncomingLine("Вторая", "Вторая")
	if _, ok := s.LastRoomEvent(); ok {
		t.Error("an incomplete room block must not be retained")
	}

	s.observeIncomingLine(".[ ].....      two", ".[ ].....      two")
	s.observeIncomingLine("[ Exits: N ]", "[ Exits: N ]")

	ev, ok := s.LastRoomEvent()
	if !ok {
		t.Fatal("a completed room block should be retained")
	}
	if ev.Hint != "Вторая" || ev.Exits != "N" {
		t.Errorf("retained event = %+v, want hint=Вторая exits=N", ev)
	}
	if !strings.Contains(ev.Desc, "two") {
		t.Errorf("retained desc = %q, want the prose with the minimap column stripped", ev.Desc)
	}

	// A later block replaces it (the "live" room is always the most recent one).
	s.mapTracker.PushMove("N")
	s.observeIncomingLine("Первая", "Первая")
	s.observeIncomingLine(".[ ].....      one", ".[ ].....      one")
	s.observeIncomingLine("[ Exits: S ]", "[ Exits: S ]")
	ev2, _ := s.LastRoomEvent()
	if ev2.Hint != "Первая" {
		t.Errorf("LastRoomEvent should track the MOST RECENT block, got %+v", ev2)
	}
}

// --- lastRoom + tracker COHERENCE (review round 1) -------------------------
//
// These drive raw lines through observeIncomingLine (the real parser path) and
// assert the retained room text and the tracker position TOGETHER. The web-layer
// tests inject lastRoom and the tracker state from the same event by
// construction, so they structurally cannot catch a mismatch between the two —
// which is exactly how the record endpoint came to write one room's text into a
// different room's cell.

// TestLiveRoomAndPositionDivergeOnTeleport: the classic corruption shape. The
// player walks S and the MUD teleports them somewhere unrelated. The tracker goes
// 🔴 (text veto) but KEEPS Valid=true at the last-known coord, while lastRoom has
// already been replaced by the teleport DESTINATION's text. Recording here would
// overwrite the last-known cell with a different room — so the coherent snapshot
// must expose both the red confidence and EventDescribesCell=false.
func TestLiveRoomAndPositionDivergeOnTeleport(t *testing.T) {
	s, _, _ := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")

	// The block that arrives is NOT the room S of us — it is a far-away jail.
	for _, line := range []string{
		"Тюрьма",
		".[ ].....      a far away jail",
		"[ Exits: N ]",
	} {
		s.observeIncomingLine(line, line)
	}

	st := s.LiveRecordState()
	if !st.RoomSeen {
		t.Fatal("the teleport block should still have been retained as the live room")
	}
	if st.Room.Hint != "Тюрьма" {
		t.Fatalf("live room = %q, want the teleport destination", st.Room.Hint)
	}
	if !st.Position.Valid {
		t.Fatal("precondition: the tracker holds the last-known coord")
	}
	if st.Position.Confidence != mapper.Red {
		t.Errorf("confidence = %q, want red (hint contradicts the assumed cell)", st.Position.Confidence)
	}
	// THE point: the retained text and the held coord describe different rooms.
	if st.Position.EventDescribesCell {
		t.Error("EventDescribesCell must be false — the live text does not describe the held cell")
	}
	if st.Position.Coord != (mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}) {
		t.Errorf("held coord = %v, want the last-known (0,0,0)", st.Position.Coord)
	}
}

// TestLiveRoomAndPositionOnExitsOnlyBlock: a room block with no header (the
// accumulator's exits-only path) yields an EMPTY hint. It is retained as the live
// room, so a naive record would wipe the mapped room's name/desc — the snapshot
// must show the empty hint and a position that does not claim to be described.
func TestLiveRoomAndPositionOnExitsOnlyBlock(t *testing.T) {
	s, _, _ := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	// A bare exits line with no preceding block: no header, no desc.
	s.observeIncomingLine("", "")
	s.observeIncomingLine("[ Exits: S ]", "[ Exits: S ]")

	st := s.LiveRecordState()
	if !st.RoomSeen {
		t.Fatal("an exits-only block should still produce a room event")
	}
	if st.Room.Hint != "" {
		t.Fatalf("expected an empty hint from the exits-only path, got %q", st.Room.Hint)
	}
	if st.Room.Exits != "S" {
		t.Errorf("exits = %q, want S", st.Room.Exits)
	}
	if st.Position.EventDescribesCell {
		t.Error("a hint-less event describes no cell — EventDescribesCell must be false")
	}
}

// TestLiveRoomAndPositionAgreeOnConfirmedStep is the POSITIVE case: a normal
// confirmed step leaves the retained text and the position describing the SAME
// cell, which is what makes a record safe.
func TestLiveRoomAndPositionAgreeOnConfirmedStep(t *testing.T) {
	s, _, _ := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")

	for _, line := range []string{
		"Вторая",
		".[ ].....      two",
		"[ Exits: N ]",
	} {
		s.observeIncomingLine(line, line)
	}

	st := s.LiveRecordState()
	if st.Room.Hint != "Вторая" {
		t.Fatalf("live room = %q, want Вторая", st.Room.Hint)
	}
	if st.Position.Confidence != mapper.Green || st.Position.Coord.X != 1 {
		t.Fatalf("expected a confirmed green step to x=1, got %+v", st.Position)
	}
	if !st.Position.EventDescribesCell {
		t.Error("a confirmed step's event DOES describe the cell it landed on")
	}
	if st.Pending != 0 {
		t.Errorf("pending = %d, want 0 after the step was confirmed", st.Pending)
	}
	if !st.RoomExists {
		t.Error("RoomExists should be true — (1,0,0) is a mapped room")
	}
}

// TestLiveRoomAndPositionOnUnmappedStep: walking a direction the map does not
// record leaves 🟡 at the dead-reckoned coord with the live text describing it —
// the "draw a new room" case that recording exists for.
func TestLiveRoomAndPositionOnUnmappedStep(t *testing.T) {
	s, _, _ := trackerHarness(t)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("E") // the two-room corridor has no E edge

	for _, line := range []string{
		"Новая",
		".[ ].....      brand new",
		"[ Exits: W ]",
	} {
		s.observeIncomingLine(line, line)
	}

	st := s.LiveRecordState()
	if st.Position.Confidence != mapper.Yellow || !st.Position.Valid {
		t.Fatalf("expected 🟡 at the dead-reckoned coord, got %+v", st.Position)
	}
	if st.Position.Coord != (mapper.Coord{Zone: "Z", X: 0, Y: 1, L: 0}) {
		t.Fatalf("dead-reckoned coord = %v, want (0,1,0)", st.Position.Coord)
	}
	if !st.Position.EventDescribesCell {
		t.Error("the event describes the room we just walked into — recording must be allowed")
	}
	if st.RoomExists {
		t.Error("RoomExists should be false — this is the create case")
	}
	if st.Room.Hint != "Новая" {
		t.Errorf("live room = %q", st.Room.Hint)
	}
}
