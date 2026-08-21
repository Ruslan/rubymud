package session

import (
	"strings"
	"sync"
	"testing"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

type scanFeedSpy struct{ feeds int }

func (d *scanFeedSpy) Feed(mapper.Line) (mapper.RoomEvent, bool) {
	d.feeds++
	return mapper.RoomEvent{Hint: "scan text became a room"}, true
}
func (*scanFeedSpy) Reset()                        {}
func (*scanFeedSpy) Describe() mapper.DetectorInfo { return mapper.DetectorInfo{Key: "scan-spy"} }

type scanMessageLog struct {
	mu    sync.Mutex
	items []ServerMsg
}

func (l *scanMessageLog) snapshot() []ServerMsg {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ServerMsg(nil), l.items...)
}

func scanHarness(t *testing.T) (*Session, *recordingConn, *scanMessageLog) {
	t.Helper()
	tag := 1
	room := storage.Room{Zone: "Z", X: 0, Y: 0, L: 0, Tag: &tag, Hint: "Start", Desc: "start", Exits: "E U D", EDirs: `["E","U","D"]`, Doors: `[]`}
	tr := mapper.NewTracker(buildTestMapIndex(7, []storage.Room{room}))
	tr.Anchor(mapper.Coord{Zone: "Z"})
	conn := &recordingConn{}
	messages := &scanMessageLog{}
	s := &Session{
		conn: conn, mapTracker: tr,
		mapDetector: mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.DetectorInfo{Key: "rmud"}),
		lastRoom:    mapper.RoomEvent{Hint: "Start", Desc: "start", Exits: "E U D"}, lastRoomOK: true,
		mapRecordingMode: MapRecordingOff, mapWalkEpoch: 42, done: make(chan struct{}),
		clients: map[int]clientSink{1: {id: 1, name: "test", send: func(msg ServerMsg) error {
			messages.mu.Lock()
			defer messages.mu.Unlock()
			messages.items = append(messages.items, msg)
			return nil
		}}},
	}
	return s, conn, messages
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestMapRecordingAutoScanCapturesBoundedSixDirectionOutput(t *testing.T) {
	s, conn, messages := scanHarness(t)
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })

	for _, line := range []string{
		"На востоке: Еле заметная тропинка.",
		"Орк стоит здесь.",
		" Наверху: Таверна.",
		"Внизу: слишком темно.",
		"",
	} {
		s.observeIncomingLine(line, line)
	}
	waitFor(t, func() bool {
		for _, msg := range messages.snapshot() {
			if msg.Type == "room_scan" && len(msg.RoomScan) == 3 {
				return true
			}
		}
		return false
	})
	var got ServerMsg
	for _, msg := range messages.snapshot() {
		if msg.Type == "room_scan" {
			got = msg
		}
	}
	if got.Zone != "Z" || got.RoomX != 0 || got.RoomY != 0 || got.RoomL != 0 {
		t.Fatalf("scan coordinate = %+v", got)
	}
	if got.RoomScanRecordingRevision == 0 {
		t.Fatal("scan lacks recording revision fence")
	}
	joined := ""
	for _, hint := range got.RoomScan {
		joined += hint.Dir + "=" + hint.Hint + "\n"
	}
	for _, want := range []string{"E=Еле заметная тропинка.", "U=Таверна.", "D=слишком темно."} {
		if !strings.Contains(joined, want) {
			t.Errorf("scan hints %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "Орк") {
		t.Fatalf("interleaved noise captured: %q", joined)
	}
	snapshot, ok := s.RoomScanSnapshot()
	if !ok || len(snapshot.RoomScan) != 3 {
		t.Fatalf("scan not available for reconnect: %+v, %v", snapshot, ok)
	}
	snapshot.RoomScan[0].Hint = "consumer mutation"
	fresh, _ := s.RoomScanSnapshot()
	if fresh.RoomScan[0].Hint == "consumer mutation" {
		t.Fatal("scan snapshot aliases caller memory")
	}
}

func TestMapRecordingAutoScanIsOneShotAndMovementFencesLateOutput(t *testing.T) {
	s, conn, messages := scanHarness(t)
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })

	// A repeated room observation does not re-probe the same frontier.
	s.scheduleMapScan()
	time.Sleep(10 * time.Millisecond)
	if got := conn.String(); got != "scan\n" {
		t.Fatalf("duplicate scan writes: %q", got)
	}

	// Once movement is accepted, the old room's response must not paint hints on
	// the destination.
	s.observeOutgoingMove("e", "input")
	s.observeIncomingLine("На востоке: stale.", "На востоке: stale.")
	s.observeIncomingLine("", "")
	for _, msg := range messages.snapshot() {
		if msg.Type == "room_scan" && len(msg.RoomScan) > 0 {
			t.Fatalf("late scan leaked after movement: %+v", msg)
		}
	}
}

func TestMapRecordingAutoScanRunsAgainAfterLeavingAndReturning(t *testing.T) {
	tagA, tagB := 1, 2
	rooms := []storage.Room{
		{Zone: "Z", X: 0, Y: 0, L: 0, Tag: &tagA, Hint: "A", Desc: "a", Exits: "E U", EDirs: `["E","U"]`, Doors: `[]`},
		{Zone: "Z", X: 0, Y: 1, L: 0, Tag: &tagB, Hint: "B", Desc: "b", Exits: "W", EDirs: `["W"]`, Doors: `[]`},
	}
	s, conn, _ := scanHarness(t)
	s.mapTracker = mapper.NewTracker(buildTestMapIndex(7, rooms))
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z"})
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })
	s.observeIncomingLine("Наверху: башня.", "Наверху: башня.")
	s.observeIncomingLine("", "")

	visit := func(command, dir string, ev mapper.RoomEvent) {
		t.Helper()
		s.mapMu.Lock()
		s.mapTracker.PushMove(dir)
		s.recordAcceptedMapMoveLocked(command, dir, "input", nil)
		pos, _ := s.mapTracker.Reconcile(ev)
		s.cancelAcceptedMapMoveLocked()
		s.mapMu.Unlock()
		if !pos.Valid || pos.Confidence != mapper.Green {
			t.Fatalf("%s landed at %+v", command, pos)
		}
	}
	visit("e", "E", mapper.RoomEvent{Hint: "B", Desc: "b", Exits: "W"})
	// B has no frontier, so it deliberately sends no scan.
	s.scheduleMapScan()
	time.Sleep(10 * time.Millisecond)
	if got := conn.String(); got != "scan\n" {
		t.Fatalf("fully mapped B sent scan: %q", got)
	}
	visit("w", "W", mapper.RoomEvent{Hint: "A", Desc: "a", Exits: "E U"})
	s.scheduleMapScan()
	waitFor(t, func() bool { return conn.String() == "scan\nscan\n" })
}

func TestMapRecordingAutoScanBlankResponseClosesCapture(t *testing.T) {
	s, conn, messages := scanHarness(t)
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })

	// An empty scan result is still a complete response. A direction-shaped
	// ordinary line arriving later must not be attached to the old request.
	s.observeIncomingLine("", "")
	s.observeIncomingLine("На востоке: обычный игровой текст.", "На востоке: обычный игровой текст.")
	s.observeIncomingLine("", "")
	for _, msg := range messages.snapshot() {
		if msg.Type == "room_scan" {
			t.Fatalf("blank scan captured a later line: %+v", msg)
		}
	}
}

func TestMapRecordingAutoScanIsolatesDetectorAndCancelsOnOutboundCommand(t *testing.T) {
	s, conn, messages := scanHarness(t)
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })

	spy := &scanFeedSpy{}
	s.mapMu.Lock()
	s.mapDetector = spy
	s.mapMu.Unlock()
	store := newTestStore(t)
	s.store = store
	s.sessionID = 1
	s.vm = vm.New(store, 1)
	if err := s.SendCommand("look", "scheduler"); err != nil {
		t.Fatal(err)
	}
	if got := conn.String(); got != "scan\nlook\n" {
		t.Fatalf("writes = %q", got)
	}

	// The scan response is discarded after another outbound command, and every
	// line through its blank boundary stays outside the room detector.
	s.observeIncomingLine("На востоке: stale scan.", "На востоке: stale scan.")
	s.observeIncomingLine("", "")
	if spy.feeds != 0 {
		t.Fatalf("scan response reached room detector %d times", spy.feeds)
	}
	for _, msg := range messages.snapshot() {
		if msg.Type == "room_scan" {
			t.Fatalf("cancelled scan was published: %+v", msg)
		}
	}

	s.observeIncomingLine("ordinary room parser input", "ordinary room parser input")
	if spy.feeds != 1 {
		t.Fatalf("detector did not resume after boundary, feeds=%d", spy.feeds)
	}
}

func TestRoomScanSnapshotRequiresExactGreenRoomEvidence(t *testing.T) {
	s, conn, _ := scanHarness(t)
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return conn.String() == "scan\n" })
	s.observeIncomingLine("На востоке: тропа.", "На востоке: тропа.")
	s.observeIncomingLine("", "")
	if _, ok := s.RoomScanSnapshot(); !ok {
		t.Fatal("green scan snapshot missing")
	}

	// Losing the index and explicitly retaining the same coordinate makes the
	// position valid/yellow. A reconnect must not revive exact-room scan evidence
	// in that state.
	s.mapMu.Lock()
	s.mapTracker.SetIndex(nil)
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z"})
	s.mapMu.Unlock()
	if scan, ok := s.RoomScanSnapshot(); ok {
		t.Fatalf("yellow position replayed scan: %+v", scan)
	}
}
