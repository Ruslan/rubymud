package session

import (
	"encoding/json"
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
	idx := mapper.BuildIndex(1, rooms)
	tr := mapper.NewTracker(idx)

	s := &Session{
		clients:  make(map[int]clientSink),
		mapAccum: mapper.NewAccumulator(),
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
	s.observeIncomingLine("Вы не можете идти в этом направлении.")

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
		s.observeIncomingLine(line)
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
