package session

import (
	"strings"
	"sync"
	"testing"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// persistedRecordingHarness is intentionally small: one real editable set,
// one real active-session row, and the same tracker/index shape as production.
// The lifecycle tests below must exercise the worker against SQLite rather than
// a map-only fake.
func persistedRecordingHarness(t *testing.T) (*Session, *storage.Store, int64) {
	t.Helper()
	store := newProfileTestStore(t)
	frozen, err := store.CreateMapSet(storage.MapSetInput{
		Name:  "autorecord lifecycle",
		Rooms: []storage.Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `[]`, Doors: `[]`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	setID, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession("autorecord-lifecycle", "localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveMapSetID(rec.ID, setID); err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(setID)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := store.ListMapEdges(setID)
	if err != nil {
		t.Fatal(err)
	}
	tracker := mapper.NewTracker(mapper.BuildIndexWithEdges(setID, rooms, edges))
	tracker.Anchor(mapper.Coord{Zone: "Z"})
	s := &Session{
		sessionID:        rec.ID,
		store:            store,
		mapTracker:       tracker,
		lastRoom:         mapper.RoomEvent{Hint: "Start", Desc: "start", Exits: "S"},
		lastRoomOK:       true,
		mapRecordingMode: MapRecordingOff,
		clients:          map[int]clientSink{},
		done:             make(chan struct{}),
	}
	return s, store, setID
}

func queueAutorecordForSafetyTest(t *testing.T, s *Session) (<-chan struct{}, chan<- struct{}) {
	t.Helper()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.mapAutorecordBeforeCommit = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	s.mapMu.Lock()
	if s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "New", Desc: "new", Exits: "N"}) {
		s.mapMu.Unlock()
		t.Fatal("new target became a conflict")
	}
	s.mapMu.Unlock()
	return entered, release
}

func assertNoAutorecordTarget(t *testing.T, store *storage.Store, setID int64) {
	t.Helper()
	var rooms, edges int64
	if err := store.DB().Model(&storage.Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", setID, "Z", 1, 0, 0).Count(&rooms).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&storage.MapEdge{}).Where("map_set_id=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=? AND to_zone=? AND to_x=? AND to_y=? AND to_l=?", setID, "Z", 0, 0, 0, "S", "Z", 1, 0, 0).Count(&edges).Error; err != nil {
		t.Fatal(err)
	}
	if rooms != 0 || edges != 0 {
		t.Fatalf("stale autorecord mutated DB: rooms=%d edges=%d", rooms, edges)
	}
}

func waitAutorecordBarrier(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("autorecord worker did not reach precommit barrier")
	}
}

func TestAutorecordStopCloseProfileAndInternalStopsFencePrecommit(t *testing.T) {
	cases := []struct {
		name string
		stop func(*Session) <-chan struct{}
	}{
		{
			name: "user stop waits",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				stopped := make(chan struct{})
				go func() {
					state, ok := s.TryStopMapRecording("stopped by user")
					if !ok || state.Mode != MapRecordingOff {
						t.Errorf("user stop state=%+v ok=%v", state, ok)
					}
					close(stopped)
				}()
				select {
				case <-stopped:
					// The worker barrier is before mapReconcileMu, so the user stop
					// is allowed to win immediately. If the worker had already
					// reserved the lifecycle lock it would wait here instead.
				case <-time.After(30 * time.Millisecond):
				}
				close(release)
				return stopped
			},
		},
		{
			name: "close waits",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				closed := make(chan struct{})
				go func() {
					if err := s.Close(); err != nil {
						t.Errorf("Close: %v", err)
					}
					close(closed)
				}()
				select {
				case <-closed:
					// Close may win before the worker takes its lifecycle lock.
				case <-time.After(30 * time.Millisecond):
				}
				close(release)
				return closed
			},
		},
		{
			name: "profile reload waits",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				loaded := make(chan struct{})
				go func() {
					s.LoadMapperProfile()
					close(loaded)
				}()
				select {
				case <-loaded:
					// Profile reload may win before the worker takes its lifecycle
					// lock; otherwise it waits outside mapMu.
				case <-time.After(30 * time.Millisecond):
				}
				close(release)
				return loaded
			},
		},
		{
			name: "internal stop is immediate but fenced",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				state := s.StopMapRecording("manual movement command outcome unknown")
				if state.Mode != MapRecordingOff {
					t.Fatalf("internal stop state=%+v", state)
				}
				close(release)
				finished := make(chan struct{})
				go func() {
					// Awaiting here is test cleanup only; the production internal stop
					// above returned without waiting on SQLite.
					s.StopMapRecordingAndWait("manual movement command outcome unknown")
					close(finished)
				}()
				return finished
			},
		},
		{
			name: "pending timeout is fenced",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				state := s.StopMapRecording("movement confirmation timed out")
				if state.Mode != MapRecordingOff {
					t.Fatalf("timeout stop state=%+v", state)
				}
				close(release)
				finished := make(chan struct{})
				go func() {
					s.StopMapRecordingAndWait("movement confirmation timed out")
					close(finished)
				}()
				return finished
			},
		},
		{
			name: "map switch is fenced",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				other, err := s.store.CreateMapSet(storage.MapSetInput{Name: "switch", Rooms: []storage.Room{{Zone: "Q", Hint: "q", Desc: "q", EDirs: `[]`, Doors: `[]`}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SetActiveMapSet(other); err != nil {
					t.Fatal(err)
				}
				close(release)
				finished := make(chan struct{})
				go func() {
					s.StopMapRecordingAndWait("test cleanup")
					close(finished)
				}()
				return finished
			},
		},
		{
			name: "conflict is fenced",
			stop: func(s *Session) <-chan struct{} {
				entered, release := queueAutorecordForSafetyTest(t, s)
				waitAutorecordBarrier(t, entered)
				s.mapMu.Lock()
				s.mapRecordingMode = MapRecordingConflict
				s.mapRecordingEpoch++
				s.mapRecordingWrites = 0
				s.mapMu.Unlock()
				close(release)
				finished := make(chan struct{})
				go func() {
					s.StopMapRecordingAndWait("test cleanup")
					close(finished)
				}()
				return finished
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, store, setID := persistedRecordingHarness(t)
			done := tc.stop(s)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("lifecycle operation did not finish")
			}
			assertNoAutorecordTarget(t, store, setID)
		})
	}
}

func TestAutorecordWinningCommitIsPublishedAcrossInternalStopWindows(t *testing.T) {
	for _, stage := range []string{"after-current-check", "after-db-commit"} {
		t.Run(stage, func(t *testing.T) {
			s, store, setID := persistedRecordingHarness(t)
			entered := make(chan struct{})
			var enteredOnce sync.Once
			var mu sync.Mutex
			roomsChanged := 0
			positions := 0
			s.AttachClient("commit-publication", func(msg ServerMsg) error {
				mu.Lock()
				defer mu.Unlock()
				switch msg.Type {
				case "rooms_changed":
					roomsChanged++
				case "room_position":
					positions++
				}
				return nil
			})

			stopInsideFence := func() {
				enteredOnce.Do(func() { close(entered) })
				s.mapMu.Lock()
				s.stopMapRecordingLocked("position lost")
				s.mapMu.Unlock()
			}
			if stage == "after-current-check" {
				s.mapAutorecordAfterCurrentCheck = stopInsideFence
			} else {
				s.mapAutorecordAfterCommit = stopInsideFence
			}

			if _, err := s.StartMapRecording(); err != nil {
				t.Fatal(err)
			}
			s.observeOutgoingMove("s", "input")
			s.mapMu.Lock()
			if s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Committed", Desc: "committed", Exits: "N"}) {
				s.mapMu.Unlock()
				t.Fatal("new target became a conflict")
			}
			s.mapMu.Unlock()
			waitAutorecordBarrier(t, entered)
			s.StopMapRecordingAndWait("test cleanup")

			var rooms int64
			if err := store.DB().Model(&storage.Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", setID, "Z", 1, 0, 0).Count(&rooms).Error; err != nil {
				t.Fatal(err)
			}
			if rooms != 1 {
				t.Fatalf("winning commit rooms=%d, want 1", rooms)
			}
			mu.Lock()
			gotRoomsChanged, gotPositions := roomsChanged, positions
			mu.Unlock()
			if gotRoomsChanged != 1 || gotPositions == 0 {
				t.Fatalf("committed write was hidden: rooms_changed=%d positions=%d", gotRoomsChanged, gotPositions)
			}
		})
	}
}

func TestAutorecordPeerTopologyRevisionInvalidatesQueuedJob(t *testing.T) {
	writer, store, setID := persistedRecordingHarness(t)
	rec, err := store.CreateSession("autorecord-peer", "localhost", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveMapSetID(rec.ID, setID); err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(setID)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := store.ListMapEdges(setID)
	if err != nil {
		t.Fatal(err)
	}
	peer := &Session{sessionID: rec.ID, store: store, mapTracker: mapper.NewTracker(mapper.BuildIndexWithEdges(setID, rooms, edges)), clients: map[int]clientSink{}, done: make(chan struct{})}
	peer.mapTracker.Anchor(mapper.Coord{Zone: "Z"})

	entered, release := queueAutorecordForSafetyTest(t, writer)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("autorecord worker did not reach precommit barrier")
	}
	if _, err := peer.WriteTopology(storage.TopologyOp{Kind: storage.TopoPatchExits, Zone: "Z", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	writer.StopMapRecordingAndWait("test cleanup")
	assertNoAutorecordTarget(t, store, setID)
}

func TestNoOpTopologyWriteDoesNotConsumeRealUndo(t *testing.T) {
	s, store, setID := persistedRecordingHarness(t)
	op := storage.TopologyOp{Kind: storage.TopoPatchExits, Zone: "Z", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}
	if res, err := s.WriteTopology(op); err != nil || !res.Applied {
		t.Fatalf("real topology write res=%+v err=%v", res, err)
	}
	if got := store.WriteJournal().Depth(setID); got != 1 {
		t.Fatalf("real write journal depth=%d, want 1", got)
	}
	if res, err := s.WriteTopology(op); err != nil || !res.Applied {
		t.Fatalf("no-op topology write res=%+v err=%v", res, err)
	}
	if got := store.WriteJournal().Depth(setID); got != 1 {
		t.Fatalf("no-op consumed undo journal depth=%d, want 1", got)
	}
	res, entry, err := s.UndoTopology()
	if err != nil || !res.Applied || entry.Before.NoOp {
		t.Fatalf("Undo after no-op res=%+v before=%+v err=%v", res, entry.Before, err)
	}
	if got := store.WriteJournal().Depth(setID); got != 0 {
		t.Fatalf("journal depth after undo=%d, want 0", got)
	}
	rooms, err := store.ListRooms(setID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || rooms[0].EDirs != "[]" {
		t.Fatalf("undo did not reverse real write: rooms=%+v", rooms)
	}
}

func TestAutorecordValidatesPartialPipeAndFullBeforeOccupiedFingerprint(t *testing.T) {
	tests := []struct {
		name  string
		event mapper.RoomEvent
		mode  MapRecordingMode
	}{
		{name: "partial", event: mapper.RoomEvent{Hint: "Expected", Desc: "partial", Exits: ""}, mode: MapRecordingOff},
		{name: "pipe", event: mapper.RoomEvent{Hint: "", Desc: "corridor", Exits: "N"}, mode: MapRecordingRecording},
		{name: "full matching", event: mapper.RoomEvent{Hint: "Expected", Desc: "expected", Exits: "N"}, mode: MapRecordingRecording},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := recordingHarness()
			s.mapRecordingTimerDisabled = true
			if _, err := s.StartMapRecording(); err != nil {
				t.Fatal(err)
			}
			s.observeOutgoingMove("s", "input")
			s.mapMu.Lock()
			conflict := s.observeMapRecordingRoomLocked(tc.event)
			state := s.mapRecordingStateLocked()
			s.mapMu.Unlock()
			if conflict || state.Mode != tc.mode || state.Pending != 0 {
				t.Fatalf("event=%+v conflict=%v state=%+v", tc.event, conflict, state)
			}
		})
	}
}

func TestAutorecordOversizedHintlessKnownTargetStopsBeforeConflict(t *testing.T) {
	s := recordingHarness()
	s.mapRecordingTimerDisabled = true
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	// The target is already occupied in recordingHarness. The raw size fence
	// must run before the occupied-room fingerprint branch, even though Hint is
	// empty and the payload resembles a pipe/partial event.
	s.mapMu.Lock()
	conflict := s.observeMapRecordingRoomLocked(mapper.RoomEvent{Desc: strings.Repeat("x", mapRecordingMaxDescBytes+1), Exits: "N"})
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if conflict || state.Mode != MapRecordingOff || state.Conflict != nil || state.Pending != 0 || state.Reason != "room event too large" {
		t.Fatalf("oversized hintless event escaped raw fence: conflict=%v state=%+v", conflict, state)
	}
}

func TestAutorecordPendingMoveExpiresAndLateLookCannotAttach(t *testing.T) {
	s := recordingHarness()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	s.mapRecordingClock = func() time.Time { return now }
	s.mapRecordingTimerDisabled = true
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.mapMu.Lock()
	epoch, sequence := s.mapRecordingEpoch, s.mapRecordingPending[0].Sequence
	s.mapMu.Unlock()
	now = now.Add(mapRecordingMoveTimeout + time.Second)
	s.expireMapRecordingMove(epoch, sequence)
	state := s.MapRecordingState()
	if state.Mode != MapRecordingOff || state.Reason != "movement confirmation timed out" || state.Pending != 0 {
		t.Fatalf("expired move state=%+v", state)
	}
	s.mapMu.Lock()
	s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Unrelated", Desc: "late", Exits: "N"})
	s.mapMu.Unlock()
	if state := s.MapRecordingState(); state.Pending != 0 || state.Mode != MapRecordingOff {
		t.Fatalf("late look attached after expiry: %+v", state)
	}
	pos := s.mapTracker.Position()
	if pos.Confidence != mapper.Red {
		t.Fatalf("expiry did not invalidate tracker: %+v", pos)
	}
}
