package session

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

type closeWakeConn struct {
	recordingConn
	entered chan struct{}
	wake    chan struct{}
	once    sync.Once
}

func (c *closeWakeConn) Write(p []byte) (int, error) {
	blocked := false
	c.once.Do(func() {
		blocked = true
		close(c.entered)
	})
	if blocked {
		<-c.wake
		return 0, net.ErrClosed
	}
	return c.recordingConn.Write(p)
}

func (c *closeWakeConn) Close() error {
	c.once.Do(func() {})
	select {
	case <-c.wake:
	default:
		close(c.wake)
	}
	return nil
}

type firstWriteBlockConn struct {
	recordingConn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type manualWriteBlockConn struct {
	recordingConn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type failingManualConn struct{ recordingConn }

func (c *failingManualConn) Write(_ []byte) (int, error) { return 0, net.ErrClosed }

type partialWriteConn struct {
	recordingConn
	n   int
	err error
}

// delayedResultConn lets a concurrent StartMapWalk replace the route while the
// first physical command is blocked inside Write.
type delayedResultConn struct {
	recordingConn
	n       int
	err     error
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *delayedResultConn) Write(p []byte) (int, error) {
	block := false
	c.once.Do(func() {
		block = true
		close(c.entered)
	})
	if block {
		<-c.release
		n := c.n
		if n > len(p) {
			n = len(p)
		}
		if n > 0 {
			_, _ = c.recordingConn.Write(p[:n])
		}
		return n, c.err
	}
	return c.recordingConn.Write(p)
}

func (c *partialWriteConn) Write(p []byte) (int, error) {
	n := c.n
	if n > len(p) {
		n = len(p)
	}
	if n > 0 {
		_, _ = c.recordingConn.Write(p[:n])
	}
	return n, c.err
}

type scriptedWriteResult struct {
	n   int
	err error
}

type scriptedWriteConn struct {
	recordingConn
	results []scriptedWriteResult
}

func (c *scriptedWriteConn) Write(p []byte) (int, error) {
	result := scriptedWriteResult{n: len(p)}
	if len(c.results) > 0 {
		result = c.results[0]
		c.results = c.results[1:]
	}
	if result.n > len(p) {
		result.n = len(p)
	}
	if result.n > 0 {
		_, _ = c.recordingConn.Write(p[:result.n])
	}
	return result.n, result.err
}

func (c *manualWriteBlockConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.recordingConn.Write(p)
}

type queuedWalkDetector struct{ events []mapper.RoomEvent }

func (d *queuedWalkDetector) Feed(_ mapper.Line) (mapper.RoomEvent, bool) {
	if len(d.events) == 0 {
		return mapper.RoomEvent{}, false
	}
	e := d.events[0]
	d.events = d.events[1:]
	return e, true
}
func (d *queuedWalkDetector) Reset()                        {}
func (d *queuedWalkDetector) Describe() mapper.DetectorInfo { return mapper.DetectorInfo{} }

func (c *firstWriteBlockConn) Write(p []byte) (int, error) {
	blocked := false
	c.once.Do(func() {
		blocked = true
		close(c.entered)
	})
	if blocked {
		<-c.release
	}
	return c.recordingConn.Write(p)
}

func mapWalkHarness(t *testing.T) (*Session, *recordingConn) {
	t.Helper()
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N S"),
		mkTrackRoom("Z", 2, 0, 0, 3, "C", "c", "N"),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndex(1, rooms))}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	return s, conn
}

func multiTargetMapWalkHarness(t *testing.T) (*Session, *recordingConn) {
	t.Helper()
	rooms := []storage.Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "A", Desc: "a", Exits: "S", EDirs: `["S"]`, Doors: "[]"},
		{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "B", Desc: "b", Exits: "N S", EDirs: `["N","S"]`, Doors: "[]"},
		{Zone: "Z", X: 2, Y: 0, L: 0, Tag: intPtr(3), Hint: "C", Desc: "c", Exits: "N S", EDirs: `["N","S"]`, Doors: "[]"},
		{Zone: "Z", X: 3, Y: 0, L: 0, Tag: intPtr(4), Hint: "D", Desc: "d", Exits: "N", EDirs: `["N"]`, Doors: "[]"},
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndex(1, rooms))}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	return s, conn
}

func doorMapWalkHarness(t *testing.T) (*Session, *recordingConn) {
	t.Helper()
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N"),
	}
	rooms[0].Doors = `["S"]`
	idx := buildTestMapIndex(1, rooms)
	idx.SetDoorOpenTemplate("open door {dir_en}")
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(idx)}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	return s, conn
}

func confirmWalkAt(s *Session, c mapper.Coord) {
	s.mapMu.Lock()
	s.mapTracker.Anchor(c)
	pos := s.mapTracker.Position()
	s.mapMu.Unlock()
	s.ObserveMapWalkPosition(pos)
}

func TestMapWalkSendsOneHopThenWaitsForExpectedConfirmation(t *testing.T) {
	s, conn := mapWalkHarness(t)
	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	got := conn.String()
	if !st.Active || got != "s\n" {
		t.Fatalf("start = %+v, writes=%q; want active and only first hop", st, got)
	}
	// No room confirmation means no speculative tail.
	if got := conn.String(); got != "s\n" {
		t.Fatalf("unconfirmed writes=%q, want only first hop", got)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "s\ns\n" {
		t.Fatalf("confirmed writes=%q, want exactly next hop", got)
	}
}

func TestMapWalkParserConfirmationDispatchesNextHopWithoutDeadlock(t *testing.T) {
	s, conn := mapWalkHarness(t)
	rooms := []storage.Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "A", Desc: "a", Exits: "S", EDirs: `["S"]`, Doors: "[]"},
		{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "B", Desc: "b", Exits: "N S", EDirs: `["N","S"]`, Doors: "[]"},
		{Zone: "Z", X: 2, Y: 0, L: 0, Hint: "C", Desc: "c", Exits: "N", EDirs: `["N"]`, Doors: "[]"},
	}
	s.mapTracker = mapper.NewTracker(buildTestMapIndex(1, rooms))
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapDetector = &queuedWalkDetector{events: []mapper.RoomEvent{{Hint: "B", Desc: "b", Exits: "N S"}}}
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	done := make(chan struct{})
	go func() { s.observeIncomingLine("room", "room"); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("parser confirmation deadlocked next dispatch")
	}
	if got := conn.String(); got != "s\ns\n" {
		s.mapMu.Lock()
		pos := s.mapTracker.Position()
		s.mapMu.Unlock()
		t.Fatalf("parser confirmation writes=%q pos=%+v, want next hop", got, pos)
	}
}

func TestMapWalkParserRefusalPausesWithoutTailOrDeadlock(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	done := make(chan struct{})
	go func() {
		s.observeIncomingLine("Вы не можете идти", "Вы не можете идти")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("parser refusal deadlocked pause")
	}
	if got := conn.String(); got != "s\n" {
		t.Fatalf("parser refusal writes=%q, want no route tail", got)
	}
	st, active := s.MapWalkSnapshot()
	if !active || st.State != "paused" || !st.CanResume {
		t.Fatalf("refusal state = %+v active=%v, want resumable pause", st, active)
	}
}

func TestMapWalkCancelAndRefusalNeverSendTail(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.CancelMapWalk("cancelled by user")
	// An unrelated event cannot release the drain barrier.
	s.ObserveMapWalkPosition(mapper.Position{Valid: true, Confidence: mapper.Green, Coord: mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("cancelled writes=%q, want no tail", got)
	}
	s.ObserveMapWalkRefusal()
	if got := conn.String(); got != "s\n" {
		t.Fatalf("refused writes=%q, want no tail", got)
	}
}

func TestMapWalkReplacementWaitsForDrainThenReplans(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	got := conn.String()
	if st.State != "stopping" || got != "s\n" {
		t.Fatalf("replacement = %+v writes=%q, want barrier", st, got)
	}
	// Stale/non-expected event cannot advance replacement.
	s.ObserveMapWalkPosition(mapper.Position{Valid: true, Confidence: mapper.Green, Coord: mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("stale event wrote tail: %q", got)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "s\ns\n" {
		t.Fatalf("drained replan writes=%q, want one new hop", got)
	}
}

func TestMapWalkNewestRequestWinsDuringDeferredReplacement(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) // queued old replacement
	s.AttachClient("interleave", func(msg ServerMsg) error {
		if msg.Type == "map_walk" && !msg.WalkActive && msg.WalkReason == "stopped" {
			// This arrives between the old job's stopped broadcast and its deferred
			// StartMapWalk call. It is the newest user request and must win.
			s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
		}
		return nil
	})

	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("old deferred replacement revived after newer request reached destination")
	}
	if got := conn.String(); got != "s\n" {
		t.Fatalf("old deferred replacement sent a stale tail: %q", got)
	}
}

func TestMapWalkMismatchDoesNotDrainReplacement(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})

	// A genuine, but unexpected, room event is not proof that the in-flight move
	// landed. The desired replacement must remain queued until exact confirmation
	// or a refusal, rather than being silently discarded as the old code did.
	s.ObserveMapWalkPosition(mapper.Position{
		Valid: true, Confidence: mapper.Green, EventDescribesCell: true,
		Coord: mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0},
	})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("mismatch wrote a tail: %q", got)
	}
	s.mapWalkMu.Lock()
	desired := s.mapWalk != nil && s.mapWalk.desired != nil
	s.mapWalkMu.Unlock()
	if !desired {
		t.Fatal("mismatch dropped the queued replacement")
	}

	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "s\ns\n" {
		t.Fatalf("exact confirmation did not release replacement: %q", got)
	}
}

func TestMapWalkTimeoutInvalidatesTrackerAndPausesRoute(t *testing.T) {
	s, conn := mapWalkHarness(t)
	first := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})

	// Exercise the timer callback directly: wall-clock sleeps make this safety
	// boundary flaky and add nothing to the state-machine coverage.
	s.expireMapWalk(first.JobID, 1)
	if got := conn.String(); got != "s\n" {
		t.Fatalf("timeout dispatched a replacement: %q", got)
	}
	st, active := s.MapWalkSnapshot()
	if !active || st.State != "paused" || st.CanResume {
		t.Fatalf("timed-out route = %+v active=%v, want non-resumable pause", st, active)
	}
	s.mapMu.Lock()
	pos, pending := s.mapTracker.Position(), s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pos.Confidence != mapper.Red || pos.Valid || pending != 0 {
		t.Fatalf("timeout tracker = pos=%+v pending=%d, want invalid red and empty FIFO", pos, pending)
	}
}

func TestMapWalkGateRefusalCancelsFIFOAndResumesExplicitly(t *testing.T) {
	s, conn := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты.", "Ворота закрыты.")

	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	paused, active := s.MapWalkSnapshot()
	if pending != 0 || !active || paused.State != "paused" || !paused.CanResume {
		t.Fatalf("gate refusal pending=%d state=%+v active=%v, want empty FIFO resumable pause", pending, paused, active)
	}
	if got := conn.String(); got != "s\n" {
		t.Fatalf("gate refusal wrote route tail: %q", got)
	}
	if err := s.SendCommand("open gate", "trigger"); err != nil {
		t.Fatalf("trigger command during pause: %v", err)
	}
	if got := conn.String(); got != "s\nopen gate\n" {
		t.Fatalf("trigger command changed paused route: %q", got)
	}
	resumed := s.ResumeMapWalk()
	if !resumed.Active || resumed.State != "running" || resumed.JobID <= started.JobID {
		t.Fatalf("resume = %+v, want newer running job", resumed)
	}
	if got := conn.String(); got != "s\nopen gate\ns\n" {
		t.Fatalf("resume writes=%q, want exactly one new hop", got)
	}
}

func TestMapWalkManualMoveWhilePausedPreservesDestination(t *testing.T) {
	s, conn := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.ObserveMapWalkRefusal()
	if err := s.SendCommand("s", "input"); err != nil {
		t.Fatalf("manual movement during pause: %v", err)
	}
	paused, active := s.MapWalkSnapshot()
	if !active || paused.State != "paused" || paused.CanResume {
		t.Fatalf("paused route after manual send = %+v active=%v, want target retained and Continue disabled", paused, active)
	}
	if denied := s.ResumeMapWalk(); denied.State != "paused" || denied.CanResume {
		t.Fatalf("resume with manual pending = %+v, want unchanged disabled pause", denied)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	paused, _ = s.MapWalkSnapshot()
	if !paused.CanResume {
		t.Fatalf("confirmed manual move should re-enable Continue: %+v", paused)
	}
	resumed := s.ResumeMapWalk()
	if !resumed.Active || resumed.JobID <= started.JobID {
		t.Fatalf("resume after manual move = %+v", resumed)
	}
	if got := conn.String(); got != "s\ns\ns\n" {
		t.Fatalf("manual+resume writes=%q, want first walk, manual move, one replanned hop", got)
	}
}

func TestMapWalkStopDiscardsPausedDestination(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.mapMu.Lock()
	s.mapTracker.CancelHead()
	s.mapMu.Unlock()
	s.ObserveMapWalkRefusal()
	st := s.CancelMapWalk("cancelled by user")
	if st.Active || st.State != "stopped" {
		t.Fatalf("stop paused route = %+v", st)
	}
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("paused destination survived Stop")
	}
	if resumed := s.ResumeMapWalk(); resumed.State != "stopped" {
		t.Fatalf("resume after Stop = %+v, want no route", resumed)
	}
	if got := conn.String(); got != "s\n" {
		t.Fatalf("Stop/Resume wrote unexpected command: %q", got)
	}
}

func TestMapWalkResumeWaitsForBlockedManualReservation(t *testing.T) {
	s, initial := mapWalkHarness(t)
	rooms := []storage.Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "A", Desc: "a", Exits: "S", EDirs: `["S"]`, Doors: "[]"},
		{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "B", Desc: "b", Exits: "N S", EDirs: `["N","S"]`, Doors: "[]"},
		{Zone: "Z", X: 2, Y: 0, L: 0, Hint: "C", Desc: "c", Exits: "N", EDirs: `["N"]`, Doors: "[]"},
	}
	s.mapTracker = mapper.NewTracker(buildTestMapIndex(1, rooms))
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	var messages []ServerMsg
	s.AttachClient("manual-confirm", func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	blocked := &manualWriteBlockConn{entered: make(chan struct{}), release: make(chan struct{})}
	s.conn = blocked

	manualDone := make(chan error, 1)
	go func() { manualDone <- s.SendCommand("s", "input") }()
	select {
	case <-blocked.entered:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("manual write did not block")
	}

	resumed := make(chan MapWalkState, 1)
	go func() { resumed <- s.ResumeMapWalk() }()
	select {
	case st := <-resumed:
		if st.State != "paused" || st.CanResume {
			t.Fatalf("resume while manual write reserved = %+v, want disabled pause", st)
		}
	case <-time.After(100 * time.Millisecond):
		close(blocked.release)
		<-manualDone
		<-resumed
		t.Fatal("resume waited for a manual write instead of preserving the pause")
	}
	if got := initial.String(); got != "s\n" || blocked.String() != "" {
		t.Fatalf("resume wrote during manual reservation: initial=%q manual=%q", got, blocked.String())
	}
	close(blocked.release)
	if err := <-manualDone; err != nil {
		t.Fatalf("manual write: %v", err)
	}
	s.mapDetector = &queuedWalkDetector{events: []mapper.RoomEvent{{Hint: "B", Desc: "b", Exits: "N S"}}}
	messages = nil
	s.processLine("room")
	if !sawPausedResume(messages) {
		t.Fatalf("manual room confirmation did not refresh paused eligibility: %+v", messages)
	}
	st := s.ResumeMapWalk()
	if !st.Active || st.State != "running" || blocked.String() != "s\ns\n" {
		t.Fatalf("explicit resume after manual confirmation = %+v writes=%q", st, blocked.String())
	}
}

func sawPausedResume(messages []ServerMsg) bool {
	for _, msg := range messages {
		if msg.Type == "map_walk" && msg.WalkState == "paused" && msg.WalkCanResume {
			return true
		}
	}
	return false
}

func TestMapWalkAnchorRefreshesPausedResumeEligibility(t *testing.T) {
	s, _ := mapWalkHarness(t)
	var messages []ServerMsg
	s.AttachClient("paused-state", func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.expireMapWalk(started.JobID, 1)
	messages = nil
	if ok, exact := s.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v)", ok, exact)
	}
	if sawPausedResume(messages) {
		return
	}
	t.Fatalf("anchor did not broadcast resumable paused walk: %+v", messages)
}

func TestMapWalkCancelThenParserRefusalIsTerminal(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	st := s.CancelMapWalk("cancelled by user")
	if st.State != "stopping" {
		t.Fatalf("cancel = %+v", st)
	}
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	st, active := s.MapWalkSnapshot()
	if active || st.State != "stopped" {
		t.Fatalf("refusal after Stop = %+v active=%v, want terminal stop", st, active)
	}
	if resumed := s.ResumeMapWalk(); resumed.State != "stopped" || conn.String() != "s\n" {
		t.Fatalf("resume after stopped refusal = %+v writes=%q", resumed, conn.String())
	}
}

func TestMapWalkCancelThenTimeoutIsTerminal(t *testing.T) {
	s, conn := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if st := s.CancelMapWalk("cancelled by user"); st.State != "stopping" {
		t.Fatalf("CancelMapWalk = %+v", st)
	}
	s.expireMapWalk(started.JobID, 1)
	st, active := s.MapWalkSnapshot()
	if active || st.State != "stopped" {
		t.Fatalf("timeout after Stop = %+v active=%v, want terminal stop", st, active)
	}
	if resumed := s.ResumeMapWalk(); resumed.State != "stopped" || conn.String() != "s\n" {
		t.Fatalf("resume after timeout-stop = %+v writes=%q", resumed, conn.String())
	}
}

func TestMapWalkManualRefusalRefreshesPausedEligibility(t *testing.T) {
	s, _ := mapWalkHarness(t)
	var messages []ServerMsg
	s.AttachClient("manual-refusal", func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	if err := s.SendCommand("s", "input"); err != nil {
		t.Fatalf("manual move while paused: %v", err)
	}
	if st, _ := s.MapWalkSnapshot(); st.CanResume {
		t.Fatalf("manual pending unexpectedly resumable: %+v", st)
	}
	messages = nil
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 || !sawPausedResume(messages) {
		t.Fatalf("manual refusal pending=%d messages=%+v, want resumable paused refresh", pending, messages)
	}
}

func TestMapWalkStartDuringPausedManualReservationReturnsCanonicalPause(t *testing.T) {
	s, _ := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	s.mapWalkMu.Lock()
	s.manualMoveReserved = true
	s.mapWalkMu.Unlock()
	state := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if !state.Active || state.State != "paused" || state.JobID != started.JobID || state.CanResume {
		t.Fatalf("start while manual reservation = %+v, want canonical disabled pause", state)
	}
}

func TestMapWalkStopDuringDispatchAbortIsTerminal(t *testing.T) {
	s, conn := mapWalkHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var hookOnce sync.Once
	s.mapWalkBeforeDispatchWrite = func() {
		hookOnce.Do(func() {
			close(entered)
			<-release
		})
	}
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
	<-entered
	if st := s.CancelMapWalk("cancelled by user"); st.State != "stopping" {
		t.Fatalf("CancelMapWalk = %+v", st)
	}
	close(release)
	<-started
	st, active := s.MapWalkSnapshot()
	if active || st.State != "stopped" || conn.String() != "" {
		t.Fatalf("dispatch abort after Stop = %+v active=%v writes=%q", st, active, conn.String())
	}
}

func TestMapWalkStartReplacementDuringDispatchAbortReplansOnce(t *testing.T) {
	s, conn := mapWalkHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var hookOnce sync.Once
	s.mapWalkBeforeDispatchWrite = func() {
		hookOnce.Do(func() {
			close(entered)
			<-release
		})
	}
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
	<-entered
	queued := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if queued.State != "stopping" {
		t.Fatalf("replacement while dispatching = %+v", queued)
	}
	close(release)
	<-started
	st, active := s.MapWalkSnapshot()
	if !active || st.State != "running" || st.JobID <= 1 || conn.String() != "s\n" {
		t.Fatalf("dispatch abort replacement = %+v active=%v writes=%q", st, active, conn.String())
	}
}

func TestMapWalkManualWriteErrorRefreshesPausedEligibility(t *testing.T) {
	s, _ := mapWalkHarness(t)
	var messages []ServerMsg
	s.AttachClient("manual-error", func(msg ServerMsg) error {
		if msg.Type == "map_walk" && msg.WalkState == "paused" {
			messages = append(messages, msg)
		}
		return nil
	})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	before, _ := s.MapWalkSnapshot()
	if !before.CanResume {
		t.Fatalf("initial pause = %+v", before)
	}
	s.conn = &failingManualConn{}
	messages = nil
	if err := s.SendCommand("s", "input"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("manual write error = %v, want net.ErrClosed", err)
	}
	after, _ := s.MapWalkSnapshot()
	broadcastRevision := uint64(0)
	for _, msg := range messages {
		if msg.WalkCanResume && msg.WalkRevision > broadcastRevision {
			broadcastRevision = msg.WalkRevision
		}
	}
	if !after.CanResume || after.Revision <= before.Revision || broadcastRevision <= before.Revision {
		t.Fatalf("manual error pause before=%+v after=%+v messages=%+v", before, after, messages)
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("failed manual write left FIFO pending=%d", pending)
	}
}

func TestMapWalkPartialManualWriteInvalidatesPosition(t *testing.T) {
	for _, n := range []int{1, len("s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := mapWalkHarness(t)
			s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
			s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
			s.conn = &partialWriteConn{n: n, err: net.ErrClosed}
			if err := s.SendCommand("s", "input"); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("manual partial write error = %v", err)
			}
			st, active := s.MapWalkSnapshot()
			s.mapMu.Lock()
			pos, pending := s.mapTracker.Position(), s.mapTracker.PendingCount()
			s.mapMu.Unlock()
			if !active || st.State != "paused" || st.CanResume || pos.Valid || pos.Confidence != mapper.Red || pending != 0 {
				t.Fatalf("partial manual state=%+v pos=%+v pending=%d", st, pos, pending)
			}
			if resumed := s.ResumeMapWalk(); resumed.CanResume || resumed.State != "paused" {
				t.Fatalf("resume before anchor = %+v", resumed)
			}
		})
	}
}

func TestMapWalkPartialRouteWritePausesUntilAnchor(t *testing.T) {
	for _, n := range []int{1, len("s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := mapWalkHarness(t)
			s.conn = &partialWriteConn{n: n, err: net.ErrClosed}
			st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
			s.mapMu.Lock()
			pos, pending := s.mapTracker.Position(), s.mapTracker.PendingCount()
			s.mapMu.Unlock()
			if !st.Active || st.State != "paused" || st.CanResume || pos.Valid || pos.Confidence != mapper.Red || pending != 0 {
				t.Fatalf("partial route state=%+v pos=%+v pending=%d", st, pos, pending)
			}
		})
	}
}

func TestMapWalkZeroByteRouteWriteReplansDesiredReplacement(t *testing.T) {
	s, _ := mapWalkHarness(t)
	conn := &delayedResultConn{n: 0, err: net.ErrClosed, entered: make(chan struct{}), release: make(chan struct{})}
	s.conn = conn
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
	<-conn.entered
	queued := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if queued.State != "stopping" {
		t.Fatalf("replacement while write blocked = %+v", queued)
	}
	close(conn.release)
	<-started
	st, active := s.MapWalkSnapshot()
	if !active || st.State != "running" || conn.String() != "s\n" {
		t.Fatalf("zero-byte replacement = %+v active=%v writes=%q", st, active, conn.String())
	}
}

func TestMapWalkPartialRouteWriteRetainsDesiredReplacementPaused(t *testing.T) {
	for _, n := range []int{1, len("s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := mapWalkHarness(t)
			conn := &delayedResultConn{n: n, err: net.ErrClosed, entered: make(chan struct{}), release: make(chan struct{})}
			s.conn = conn
			started := make(chan MapWalkState, 1)
			go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
			<-conn.entered
			s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
			close(conn.release)
			<-started
			st, active := s.MapWalkSnapshot()
			if !active || st.State != "paused" || st.CanResume || conn.String() != "s\n"[:n] {
				t.Fatalf("partial replacement = %+v active=%v writes=%q", st, active, conn.String())
			}
		})
	}
}

func TestMapWalkCancelDuringAmbiguousWriteRemainsTerminal(t *testing.T) {
	for _, n := range []int{1, len("s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := mapWalkHarness(t)
			conn := &delayedResultConn{n: n, err: net.ErrClosed, entered: make(chan struct{}), release: make(chan struct{})}
			s.conn = conn
			started := make(chan MapWalkState, 1)
			go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
			<-conn.entered
			if st := s.CancelMapWalk("cancelled by user"); st.State != "stopping" {
				t.Fatalf("CancelMapWalk = %+v, want stopping", st)
			}
			close(conn.release)
			<-started

			st, active := s.MapWalkSnapshot()
			if active || st.State != "stopped" {
				t.Fatalf("partial write after Cancel = %+v active=%v, want terminal stop", st, active)
			}
			s.mapMu.Lock()
			pos, pending := s.mapTracker.Position(), s.mapTracker.PendingCount()
			s.mapMu.Unlock()
			if pos.Valid || pos.Confidence != mapper.Red || pending != 0 {
				t.Fatalf("partial Cancel tracker = %+v pending=%d, want red/empty", pos, pending)
			}
			confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
			if resumed := s.ResumeMapWalk(); resumed.State != "stopped" || conn.String() != "s\n"[:n] {
				t.Fatalf("resume after cancelled partial = %+v writes=%q", resumed, conn.String())
			}
		})
	}
}

func TestMapWalkLatestReplacementWinsAcrossWriteErrors(t *testing.T) {
	original := mapper.Coord{Zone: "Z", X: 3, Y: 0, L: 0}
	middle := mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}
	latest := mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}
	for _, n := range []int{0, 1, len("s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := multiTargetMapWalkHarness(t)
			conn := &delayedResultConn{n: n, err: net.ErrClosed, entered: make(chan struct{}), release: make(chan struct{})}
			s.conn = conn
			started := make(chan MapWalkState, 1)
			go func() { started <- s.StartMapWalk(original) }()
			released := false
			defer func() {
				if !released {
					close(conn.release)
				}
			}()
			waitForMapWalkWrite(t, conn.entered)
			s.StartMapWalk(middle)
			s.StartMapWalk(latest)
			close(conn.release)
			released = true
			waitForMapWalkStart(t, started)

			st, active := s.MapWalkSnapshot()
			if !active || s.mapWalk.target != latest {
				t.Fatalf("latest target not retained: state=%+v target=%+v", st, s.mapWalk.target)
			}
			if n == 0 {
				if st.State != "running" || conn.String() != "s\n" {
					t.Fatalf("zero-byte latest replacement = %+v writes=%q", st, conn.String())
				}
				confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
				if conn.String() != "s\ns\n" {
					t.Fatalf("middle target dispatched instead of latest: %q", conn.String())
				}
				return
			}
			if st.State != "paused" || st.CanResume || conn.String() != "s\n"[:n] {
				t.Fatalf("ambiguous latest replacement = %+v writes=%q", st, conn.String())
			}
			confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
			if resumed := s.ResumeMapWalk(); !resumed.Active || resumed.State != "running" {
				t.Fatalf("resume latest replacement = %+v", resumed)
			}
			confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
			if conn.String() != "s\n"[:n]+"s\ns\n" {
				t.Fatalf("middle target dispatched after ambiguous write: %q", conn.String())
			}
		})
	}
}

func waitForMapWalkWrite(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked route write")
	}
}

func waitForMapWalkStart(t *testing.T, started <-chan MapWalkState) MapWalkState {
	t.Helper()
	select {
	case state := <-started:
		return state
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for map-walk start to finish")
		return MapWalkState{}
	}
}

func TestMapWalkDoorWriteFailureReplansOnlyWhenNothingWasWritten(t *testing.T) {
	for _, n := range []int{0, 1, len("open door s\n")} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s, _ := doorMapWalkHarness(t)
			conn := &delayedResultConn{n: n, err: net.ErrClosed, entered: make(chan struct{}), release: make(chan struct{})}
			s.conn = conn
			started := make(chan MapWalkState, 1)
			go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}) }()
			<-conn.entered
			s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
			close(conn.release)
			<-started
			st, active := s.MapWalkSnapshot()
			if n == 0 {
				if !active || st.State != "running" || conn.String() != "open door s\ns\n" {
					t.Fatalf("zero-byte door replacement = %+v active=%v writes=%q", st, active, conn.String())
				}
				return
			}
			if !active || st.State != "paused" || st.CanResume || conn.String() != "open door s\n"[:n] {
				t.Fatalf("partial door replacement = %+v active=%v writes=%q", st, active, conn.String())
			}
		})
	}
}

func TestMapWalkDoorDispatchAbortPreventsOpenAndMove(t *testing.T) {
	s, conn := doorMapWalkHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	s.mapWalkBeforeDispatchWrite = func() { close(entered); <-release }
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}) }()
	<-entered
	s.CancelMapWalk("cancelled by user")
	close(release)
	<-started
	if st, active := s.MapWalkSnapshot(); active || st.State != "stopped" || conn.String() != "" {
		t.Fatalf("door Stop dispatch abort = %+v active=%v writes=%q", st, active, conn.String())
	}
}

func TestMapWalkDoorDispatchReplacementPreventsOldOpen(t *testing.T) {
	s, conn := doorMapWalkHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var hookOnce sync.Once
	s.mapWalkBeforeDispatchWrite = func() {
		hookOnce.Do(func() {
			close(entered)
			<-release
		})
	}
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}) }()
	<-entered
	queued := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if queued.State != "stopping" {
		t.Fatalf("door replacement while dispatching = %+v", queued)
	}
	close(release)
	<-started
	st, active := s.MapWalkSnapshot()
	if !active || st.State != "running" || conn.String() != "open door s\ns\n" {
		t.Fatalf("door dispatch replacement = %+v active=%v writes=%q", st, active, conn.String())
	}
}

func TestMapWalkSupersededRequestKeepsActivePauseReason(t *testing.T) {
	s, _ := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.ObserveMapWalkRefusal()

	s.mapWalkMu.Lock()
	staleRequest := s.nextWalkRequest
	s.nextWalkRequest++
	s.mapWalkMu.Unlock()
	st := s.startMapWalkRequest(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}, staleRequest)
	if st.State != "paused" || st.Reason != "movement refused; resolve the obstacle and continue" {
		t.Fatalf("superseded request replaced active pause reason: %+v", st)
	}
}

func TestMapWalkSupersededRequestKeepsActiveStoppingReason(t *testing.T) {
	s, _ := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.CancelMapWalk("cancelled by user")

	s.mapWalkMu.Lock()
	staleRequest := s.nextWalkRequest
	s.nextWalkRequest++
	s.mapWalkMu.Unlock()
	st := s.startMapWalkRequest(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}, staleRequest)
	if st.State != "stopping" || st.Reason != "cancelled by user" {
		t.Fatalf("superseded request replaced active stopping reason: %+v", st)
	}
}

func TestMapWalkRevisionPreventsStaleManualEligibilityRollback(t *testing.T) {
	s, _ := mapWalkHarness(t)
	rooms := []storage.Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "A", Desc: "a", Exits: "S", EDirs: `["S"]`, Doors: "[]"},
		{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "B", Desc: "b", Exits: "N S", EDirs: `["N","S"]`, Doors: "[]"},
		{Zone: "Z", X: 2, Y: 0, L: 0, Hint: "C", Desc: "c", Exits: "N", EDirs: `["N"]`, Doors: "[]"},
	}
	s.mapTracker = mapper.NewTracker(buildTestMapIndex(1, rooms))
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	var messages []ServerMsg
	s.AttachClient("revision", func(msg ServerMsg) error {
		if msg.Type == "map_walk" && msg.WalkState == "paused" {
			messages = append(messages, msg)
		}
		return nil
	})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.observeIncomingLine("Ворота закрыты", "Ворота закрыты")
	messages = nil
	entered := make(chan struct{})
	release := make(chan struct{})
	s.mapWalkBeforeManualPausedBroadcast = func() {
		close(entered)
		<-release
	}
	s.mapDetector = &queuedWalkDetector{events: []mapper.RoomEvent{{Hint: "B", Desc: "b", Exits: "N S"}}}
	manualDone := make(chan error, 1)
	go func() { manualDone <- s.SendCommand("s", "input") }()
	<-entered             // stale can_resume=false snapshot is captured but not yet broadcast
	s.processLine("room") // publishes the newer can_resume=true state
	close(release)
	if err := <-manualDone; err != nil {
		t.Fatalf("manual move: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("paused revision stream too short: %+v", messages)
	}
	var newestTrue, staleFalse uint64
	for _, msg := range messages {
		if msg.WalkCanResume {
			newestTrue = msg.WalkRevision
		} else if newestTrue != 0 {
			staleFalse = msg.WalkRevision
		}
	}
	if newestTrue == 0 || staleFalse == 0 || staleFalse >= newestTrue {
		t.Fatalf("paused revision stream=%+v, want later true then older false", messages)
	}
}

func TestMapWalkTimeoutDoesNotClearManualFIFO(t *testing.T) {
	s, _ := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	entered := make(chan struct{})
	release := make(chan struct{})
	s.mapWalkBeforeTimeoutInvalidate = func() {
		close(entered)
		<-release
	}
	timeoutDone := make(chan struct{})
	go func() {
		s.expireMapWalk(started.JobID, 1)
		close(timeoutDone)
	}()
	<-entered
	manualDone := make(chan error, 1)
	go func() { manualDone <- s.SendCommand("s", "input") }()
	select {
	case err := <-manualDone:
		if err != nil {
			t.Fatalf("manual move during timeout: %v", err)
		}
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-manualDone:
		if err != nil {
			t.Fatalf("manual move during timeout: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("manual move did not finish after timeout released its reservation")
	}
	select {
	case <-timeoutDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout/manual interleaving deadlocked")
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 1 {
		t.Fatalf("timeout cleared manual FIFO after physical write: pending=%d", pending)
	}
}

func TestMapWalkRefusalRunsBeforeRealTriggerPipeline(t *testing.T) {
	s, conn := mapWalkHarness(t)
	var order []string
	s.AttachClient("trigger-order", func(msg ServerMsg) error {
		if msg.Type == "map_walk" && msg.WalkState == "paused" {
			order = append(order, "paused")
		}
		if msg.Type == "command_hint" && msg.Command == "откр воро" {
			order = append(order, "open")
		}
		return nil
	})
	if err := s.store.EnsureSessionProfiles(s.sessionID, "TestSession"); err != nil {
		t.Fatal(err)
	}
	profileIDs, err := s.store.GetOrderedProfileIDs(s.sessionID)
	if err != nil || len(profileIDs) == 0 {
		t.Fatalf("GetOrderedProfileIDs = %v, %v", profileIDs, err)
	}
	if err := s.store.CreateTrigger(storage.TriggerRule{ProfileID: profileIDs[0], Pattern: `^Ворота закрыты$`, Command: "откр воро", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s.vm = vm.New(s.store, s.sessionID)
	if err := s.vm.Reload(); err != nil {
		t.Fatal(err)
	}
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	order = nil
	s.processLine("Ворота закрыты")
	paused, active := s.MapWalkSnapshot()
	if !active || paused.State != "paused" || conn.String() != "s\nоткр воро\n" {
		t.Fatalf("trigger/refusal pipeline state=%+v active=%v writes=%q", paused, active, conn.String())
	}
	if strings.Join(order, ",") != "paused,open" {
		t.Fatalf("refusal/trigger ordering=%v, want pause before open trigger", order)
	}
	if resumed := s.ResumeMapWalk(); !resumed.Active || conn.String() != "s\nоткр воро\ns\n" {
		t.Fatalf("explicit resume after trigger = %+v writes=%q", resumed, conn.String())
	}
}

func TestMapWalkTimeoutCanResumeOnlyAfterAnchor(t *testing.T) {
	s, conn := mapWalkHarness(t)
	started := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.expireMapWalk(started.JobID, 1)
	if got := conn.String(); got != "s\n" {
		t.Fatalf("timeout writes=%q", got)
	}
	if st := s.ResumeMapWalk(); st.State != "paused" || st.CanResume {
		t.Fatalf("resume without anchor = %+v, want unchanged pause", st)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	resumed := s.ResumeMapWalk()
	if !resumed.Active || resumed.State != "running" || resumed.JobID <= started.JobID {
		t.Fatalf("resume after anchor = %+v", resumed)
	}
}

func TestMapWalkStaleGenerationCannotStopReplacement(t *testing.T) {
	s, conn := mapWalkHarness(t)
	first := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})

	st, active := s.MapWalkSnapshot()
	if !active || st.JobID == first.JobID {
		t.Fatalf("replacement state = %+v active=%v, want a new generation", st, active)
	}
	s.expireMapWalk(first.JobID, 1)
	if got := conn.String(); got != "s\ns\n" {
		t.Fatalf("stale timer changed replacement writes: %q", got)
	}
	stale, active := s.MapWalkSnapshot()
	if !active || stale.JobID != st.JobID {
		t.Fatalf("stale timer stopped replacement: %+v active=%v", stale, active)
	}
}

func TestMapWalkStaleHopTimeoutCannotStopNextHop(t *testing.T) {
	s, conn := mapWalkHarness(t)
	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})

	// A callback that had already begun before Stop() won its race with the first
	// confirmation must not time out the second in-flight hop of the same job.
	s.expireMapWalk(st.JobID, 1)
	if got := conn.String(); got != "s\ns\n" {
		t.Fatalf("stale first-hop timer changed writes: %q", got)
	}
	next, active := s.MapWalkSnapshot()
	if !active || next.Current != 1 {
		t.Fatalf("stale first-hop timer stopped next hop: %+v active=%v", next, active)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("next hop did not still complete normally")
	}
}

func TestMapWalkManualMoveIsRejectedWhileChatContinues(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})

	if err := s.SendCommand("say still here", "input"); err != nil {
		t.Fatalf("chat during map walk: %v", err)
	}
	if got := conn.String(); got != "s\nsay still here\n" {
		t.Fatalf("chat writes = %q", got)
	}
	if err := s.SendCommand("s", "input"); !errors.Is(err, errMapWalkManualBarrier) {
		t.Fatalf("manual move error = %v, want map-walk barrier", err)
	}
	if got := conn.String(); got != "s\nsay still here\n" {
		t.Fatalf("manual move bypassed barrier: %q", got)
	}
}

func TestMapWalkManualBarrierDoesNotPersistRejectedDirection(t *testing.T) {
	s, _ := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if err := s.SendCommand("s", "input"); !errors.Is(err, errMapWalkManualBarrier) {
		t.Fatalf("manual move error = %v, want map-walk barrier", err)
	}
	history, err := s.store.RecentInputHistory(s.sessionID, 20)
	if err != nil {
		t.Fatalf("RecentInputHistory: %v", err)
	}
	for _, entry := range history {
		if entry == "s" {
			t.Fatalf("blocked direction was persisted in history: %q", history)
		}
	}
}

func TestMapWalkPartialAliasBarrierKeepsSentPrefixOnly(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	commands, err := s.SendCommandWithTrace("say hello;s", "input")
	if !errors.Is(err, errMapWalkManualBarrier) {
		t.Fatalf("error = %v", err)
	}
	if len(commands) != 1 || commands[0] != "say hello" {
		t.Fatalf("trace = %q, want sent prefix", commands)
	}
	if got := conn.String(); got != "s\nsay hello\n" {
		t.Fatalf("writes=%q, blocked direction leaked", got)
	}
	history, herr := s.store.ListHistory(s.sessionID, 20)
	if herr != nil {
		t.Fatal(herr)
	}
	for _, entry := range history {
		if entry.Line == "s" && entry.Kind != "map_walk" {
			t.Fatalf("blocked suffix persisted: %q", history)
		}
	}
}

func TestMapWalkPipeStepWaitsForCollapsedFinalTarget(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "Pipe", "p", "N S"),
		mkTrackRoom("Z", 2, 0, 0, 3, "C", "c", "N"),
	}
	rooms[1].Pipe = true
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndex(1, rooms))}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("pipe start writes=%q, want one collapsed command", got)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("intermediate pipe cell confirmed the hop: %q", got)
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("final pipe target did not complete the walk")
	}
}

func TestMapWalkDoorWritesOpenAndOneMoveOnly(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N"),
	}
	rooms[0].Doors = `["S"]`
	idx := buildTestMapIndex(1, rooms)
	idx.SetDoorOpenTemplate("open door {dir_en}")
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(idx)}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "open door s\ns\n" {
		t.Fatalf("door hop writes=%q, want exactly open+one move", got)
	}
}

func TestMapWalkRunsUnparsedSeamThroughTheCommandPipeline(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", ""),
		mkTrackRoom("B", 0, 0, 0, 2, "B", "b", ""),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|teleport|2"))}
	s.mapTracker.Anchor(mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0})

	st := s.StartMapWalk(mapper.Coord{Zone: "B", X: 0, Y: 0, L: 0})
	if !st.Active || conn.String() != "teleport\n" {
		t.Fatalf("unparsed seam start = %+v writes=%q", st, conn.String())
	}
}

func TestMapWalkAllowsUnparsedSeamWithExplicitPhrase(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", "N"),
		mkTrackRoom("B", 0, 0, 0, 2, "B", "b", "N"),
	}
	edges := storage.DeriveImportedEdges(1, rooms, []storage.LegacySeamDraft{{From: storage.EdgeCell{Zone: "A"}, Entries: []string{"B|teleport|2"}}})
	for i := range edges {
		if edges[i].Kind == storage.EdgeKindSeam {
			edges[i].Source = storage.EdgeSourceManual
			edges[i].Phrase = "enter portal"
		}
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(mapper.BuildIndexWithEdges(1, rooms, edges))}
	from := mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0}
	to := mapper.Coord{Zone: "B", X: 0, Y: 0, L: 0}
	s.mapTracker.Anchor(from)
	s.lastRoom = mapper.RoomEvent{Hint: "A", Desc: "a", Exits: "N"}
	s.lastRoomOK = true
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "enter portal\n" {
		t.Fatalf("explicit seam phrase start = %+v writes=%q", st, conn.String())
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("non-directional seam phrase added %d fake tracker moves", pending)
	}
	s.mapMu.Lock()
	if len(s.mapRecordingPending) != 1 {
		t.Fatalf("recording pending=%+v", s.mapRecordingPending)
	}
	recorded := s.mapRecordingPending[0]
	if recorded.Command != "enter portal" || recorded.Source != "map_walk" || recorded.From != from || recorded.Expected != to || !recorded.ExpectedExact || recorded.Epoch != 1 {
		t.Fatalf("seam evidence=%+v", recorded)
	}
	if s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "B", Desc: "b", Exits: "N"}) {
		t.Fatal("matching seam confirmation conflicted")
	}
	s.mapMu.Unlock()
	confirmWalkAt(s, to)
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("explicit seam phrase did not complete at its exact target")
	}
}

func TestMapRecordingUnparsedSeamMismatchAndRefusal(t *testing.T) {
	makeSession := func() (*Session, mapper.Coord) {
		rooms := []storage.Room{mkTrackRoom("A", 0, 0, 0, 1, "A", "a", "N"), mkTrackRoom("B", 0, 0, 0, 2, "B", "b", "N")}
		store := newTestStore(t)
		conn := &recordingConn{}
		s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|teleport|2")), lastRoom: mapper.RoomEvent{Hint: "A", Desc: "a", Exits: "N"}, lastRoomOK: true, mapRecordingMode: MapRecordingOff}
		s.mapTracker.Anchor(mapper.Coord{Zone: "A"})
		if _, err := s.StartMapRecording(); err != nil {
			t.Fatal(err)
		}
		to := mapper.Coord{Zone: "B"}
		s.StartMapWalk(to)
		return s, to
	}
	mismatch, _ := makeSession()
	mismatch.mapMu.Lock()
	if !mismatch.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Wrong", Desc: "wrong", Exits: "N"}) {
		t.Fatal("seam mismatch did not conflict")
	}
	mismatch.mapMu.Unlock()
	refused, _ := makeSession()
	refused.observeIncomingLine("Вы не можете идти в этом направлении.", "Вы не можете идти в этом направлении.")
	if state := refused.MapRecordingState(); state.Pending != 0 {
		t.Fatalf("seam refusal state=%+v", state)
	}
}

func TestMapWalkRefusesWhileAnotherMoveAwaitsConfirmation(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.mapMu.Lock()
	s.mapTracker.PushMove("S")
	s.mapMu.Unlock()

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if st.Active || st.Reason == "" {
		t.Fatalf("start over pending manual move = %+v, want refusal", st)
	}
	if got := conn.String(); got != "" {
		t.Fatalf("start over pending manual move wrote %q", got)
	}
}

func TestMapWalkReservationCannotSlipPastManualWriteBeforeFIFO(t *testing.T) {
	s, _ := mapWalkHarness(t)
	conn := &firstWriteBlockConn{entered: make(chan struct{}), release: make(chan struct{})}
	s.conn = conn
	manualDone := make(chan error, 1)
	go func() { manualDone <- s.SendCommand("s", "input") }()
	<-conn.entered

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if st.Active || st.Reason != "manual movement is being sent" {
		t.Fatalf("route during manual reservation = %+v, want inactive reservation refusal", st)
	}
	if got := conn.String(); got != "" {
		t.Fatalf("route wrote while manual write owned the slot: %q", got)
	}
	close(conn.release)
	if err := <-manualDone; err != nil {
		t.Fatalf("manual movement: %v", err)
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 1 {
		t.Fatalf("manual move FIFO = %d, want 1", pending)
	}
	if got := conn.String(); got != "s\n" {
		t.Fatalf("manual/write race sent more than one physical hop: %q", got)
	}
}

func TestMapWalkRefusesBareDirectionDoorOpener(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N"),
	}
	rooms[0].Doors = `["S"]`
	idx := buildTestMapIndex(1, rooms)
	idx.SetDoorOpenTemplate("s")
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(idx)}
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if st.Active || st.Reason == "" {
		t.Fatalf("bare direction opener start = %+v, want refusal", st)
	}
	if got := conn.String(); got != "" {
		t.Fatalf("bare direction opener wrote %q", got)
	}
}

func TestMapWalkRefusesAliasHiddenMovementDoorOpener(t *testing.T) {
	s, conn := doorMapWalkHarness(t)
	if err := s.store.EnsureSessionProfiles(s.sessionID, "test"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	if err := s.vm.Reload(); err != nil {
		t.Fatalf("Reload VM: %v", err)
	}
	if _, err := s.SendCommandWithTrace("#alias {opener} {s}", "input"); err != nil {
		t.Fatalf("define opener alias: %v", err)
	}
	s.mapMu.Lock()
	s.mapTracker.Index().SetDoorOpenTemplate("opener")
	s.mapMu.Unlock()

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if st.Active || st.Reason == "" {
		t.Fatalf("alias-hidden move opener start = %+v, want refusal", st)
	}
	if got := conn.String(); got != "" {
		t.Fatalf("alias-hidden movement opener wrote %q", got)
	}
}

func TestMapWalkCloseStopsTimerAndClearsJob(t *testing.T) {
	s, _ := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("closed session retained an active map walk")
	}
}

func TestMapWalkCloseInterruptsBlockedHopWrite(t *testing.T) {
	s, _ := mapWalkHarness(t)
	conn := &closeWakeConn{entered: make(chan struct{}), wake: make(chan struct{})}
	s.conn = conn
	started := make(chan MapWalkState, 1)
	go func() { started <- s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0}) }()
	<-conn.entered

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close blocked behind an in-flight socket write")
	}
	<-started
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("closed session retained a job after blocked write")
	}
}

func TestMapWalkTopologyReloadInvalidatesRemainingRoute(t *testing.T) {
	s, _, _, _, _ := profileHarness(t)
	conn := &recordingConn{}
	s.conn = conn
	s.vm = vm.New(s.store, s.sessionID)
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "active",
		Rooms: []storage.Room{
			mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
			mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N S"),
			mkTrackRoom("Z", 2, 0, 0, 3, "C", "c", "N"),
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(s.sessionID, id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	s.LoadActiveMapSet()
	if ok, exact := s.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v)", ok, exact)
	}
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("start writes = %q", got)
	}

	s.reloadPreservingPosition()
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("topology reload retained route planned on old graph")
	}
	confirmWalkAt(s, mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("topology reload dispatched stale tail: %q", got)
	}
}

func TestMapWalkDTEditInvalidatesJobAndTrackerFIFO(t *testing.T) {
	s, conn := mapWalkHarness(t)
	s.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 1 {
		t.Fatalf("walk pending FIFO = %d, want 1", pending)
	}

	if !s.ApplyAnnotationDT("Z", 1, 0, 0, true) {
		t.Fatal("ApplyAnnotationDT returned false")
	}
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("DT edit retained a route planned on prior topology")
	}
	s.mapMu.Lock()
	pos, pending := s.mapTracker.Position(), s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pos.Valid || pos.Confidence != mapper.Red || pending != 0 {
		t.Fatalf("DT invalidation tracker = pos=%+v pending=%d, want red and empty", pos, pending)
	}
	// No manually anchored confirmation is involved: an old response cannot send
	// a tail after the topology mutation dropped its job and FIFO.
	s.ObserveMapWalkPosition(mapper.Position{Valid: true, Confidence: mapper.Green, EventDescribesCell: true, Coord: mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}})
	if got := conn.String(); got != "s\n" {
		t.Fatalf("DT edit dispatched stale tail: %q", got)
	}
}
