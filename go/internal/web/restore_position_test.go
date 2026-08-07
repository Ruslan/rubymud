package web

import (
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
)

func restoredMessages(t *testing.T, s *Server, sess *session.Session) []session.ServerMsg {
	t.Helper()
	var got []session.ServerMsg
	if err := s.sendRestoreState(sess, func(next session.ServerMsg) error {
		got = append(got, next)
		return nil
	}); err != nil {
		t.Fatalf("sendRestoreState: %v", err)
	}
	return got
}

func messageIndex(messages []session.ServerMsg, typ string) int {
	for i, message := range messages {
		if message.Type == typ {
			return i
		}
	}
	return -1
}

func TestRestoreStateReplaysAnchoredTrackerPositionBeforeEnd(t *testing.T) {
	s, sess := setupTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	got := restoredMessages(t, s, sess)
	positionAt := messageIndex(got, "room_position")
	endAt := messageIndex(got, "restore_end")
	if positionAt < 0 {
		t.Fatal("restore did not replay room_position")
	}
	if endAt < 0 || positionAt >= endAt {
		t.Fatalf("room_position index=%d, restore_end index=%d; want position before end", positionAt, endAt)
	}
	position := got[positionAt]
	if !position.PositionValid || position.Zone != "Z" || position.RoomX != 1 || position.RoomY != 0 || position.RoomL != 0 {
		t.Errorf("restored position = %+v, want anchored Z 1,0,0", position)
	}
	if position.Confidence != string(mapper.Green) || position.PendingMoves != 0 || !position.EventDescribesCell {
		t.Errorf("restored tracker state = %+v, want green, no pending moves, event describes cell", position)
	}
	if position.RoomHint != "Вторая" || position.IsDT || position.Pipe {
		t.Errorf("restored room metadata = %+v, want non-DT non-pipe room \"Вторая\"", position)
	}
}

func TestRestoreStateOmitsPositionWithoutTracker(t *testing.T) {
	s, sess := setupTestServer(t)
	got := restoredMessages(t, s, sess)
	if at := messageIndex(got, "room_position"); at >= 0 {
		t.Fatalf("room_position at index %d without a tracker, messages=%+v", at, got)
	}
}

func TestRestoreStateReplaysInvalidTrackerPosition(t *testing.T) {
	s, sess := setupTestServer(t)
	sess.LoadActiveMapSet() // no active set: tracker exists but has no position

	got := restoredMessages(t, s, sess)
	positionAt := messageIndex(got, "room_position")
	endAt := messageIndex(got, "restore_end")
	if positionAt < 0 || endAt < 0 || positionAt >= endAt {
		t.Fatalf("invalid tracker restore ordering: room_position=%d restore_end=%d", positionAt, endAt)
	}
	position := got[positionAt]
	if position.PositionValid || position.Confidence != string(mapper.Red) || position.PositionReason == "" {
		t.Errorf("invalid restored position = %+v, want invalid red position with a reason", position)
	}
}

func TestRestoreStateReplaysActiveMapWalkBeforeEnd(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}
	walk := sess.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if !walk.Active || walk.Total != 1 {
		t.Fatalf("StartMapWalk = %+v, want active one-hop route", walk)
	}

	got := restoredMessages(t, s, sess)
	walkAt := messageIndex(got, "map_walk")
	endAt := messageIndex(got, "restore_end")
	if walkAt < 0 || endAt < 0 || walkAt >= endAt {
		t.Fatalf("map walk restore ordering: walk=%d end=%d", walkAt, endAt)
	}
	restored := got[walkAt]
	if !restored.WalkActive || restored.WalkJobID != walk.JobID || restored.WalkCurrent != 0 || restored.WalkTotal != 1 || restored.WalkState != "running" {
		t.Errorf("restored map walk = %+v, want active running initial hop", restored)
	}
}

func TestRestoreStateReplaysPausedMapWalkWithResumeCapability(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	started := sess.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.CancelHead() })
	sess.ObserveMapWalkRefusal()

	got := restoredMessages(t, s, sess)
	walk := got[messageIndex(got, "map_walk")]
	if !walk.WalkActive || walk.WalkState != "paused" || !walk.WalkCanResume || walk.WalkJobID != started.JobID {
		t.Fatalf("restored paused walk = %+v", walk)
	}
}

func TestRestoreStateExplicitlyStopsInactiveMapWalk(t *testing.T) {
	s, sess := setupTestServer(t)
	got := restoredMessages(t, s, sess)
	walkAt := messageIndex(got, "map_walk")
	endAt := messageIndex(got, "restore_end")
	if walkAt < 0 || endAt < 0 || walkAt >= endAt {
		t.Fatalf("inactive map walk restore ordering: walk=%d end=%d", walkAt, endAt)
	}
	walk := got[walkAt]
	if walk.WalkActive || walk.WalkState != "stopped" || walk.WalkEpoch == 0 {
		t.Fatalf("inactive map walk restore = %+v, want stopped live-session epoch", walk)
	}
}

func TestRestoreStateStopsCompletedMapWalkAtHighWater(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	started := sess.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if !started.Active {
		t.Fatalf("start = %+v", started)
	}
	// The test helper's live tracker can provide the exact confirmation without
	// involving a browser; restore itself remains the real web serialization path.
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.Anchor(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}) })
	pos, _ := sess.RoomPositionSnapshot()
	sess.ObserveMapWalkPosition(mapper.Position{Valid: pos.PositionValid, Coord: mapper.Coord{Zone: pos.Zone, X: pos.RoomX, Y: pos.RoomY, L: pos.RoomL}, Confidence: mapper.Green, EventDescribesCell: true})
	got := restoredMessages(t, s, sess)
	walk := got[messageIndex(got, "map_walk")]
	if walk.WalkActive || walk.WalkState != "stopped" || walk.WalkJobID != started.JobID {
		t.Fatalf("restore high-water = %+v, want stopped job %d", walk, started.JobID)
	}
}
