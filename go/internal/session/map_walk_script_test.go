package session

import (
	"errors"
	"net"
	"sync"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

type secondScriptWriteBlockConn struct {
	recordingConn
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	writes  int
}

func (c *secondScriptWriteBlockConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	block := c.writes == 2
	c.mu.Unlock()
	if block {
		close(c.entered)
		<-c.release
	}
	return c.recordingConn.Write(p)
}

func phraseWalkHarness(t *testing.T, phrase string) (*Session, *recordingConn, mapper.Coord) {
	t.Helper()
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "A", "a", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "B", "b", "N"),
	}
	edges := storage.DeriveEdges(1, rooms)
	for i := range edges {
		if edges[i].Kind == storage.EdgeKindGrid && edges[i].FromX == 0 && edges[i].Dir == "S" {
			edges[i].Source = storage.EdgeSourceManual
			edges[i].Phrase = phrase
		}
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{
		sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{},
		mapTracker: mapper.NewTracker(mapper.BuildIndexWithEdges(1, rooms, edges)),
		timers:     map[string]*Timer{}, delays: map[string]*delayTask{},
	}
	s.vm.SetTimerControl(s)
	from := mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}
	to := mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}
	s.mapTracker.Anchor(from)
	if err := store.EnsureSessionProfiles(1, "test"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	if err := s.vm.Reload(); err != nil {
		t.Fatalf("Reload VM: %v", err)
	}
	return s, conn, to
}

func TestMapWalkPhraseExpandsVariablesAliasesAndSemicolons(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "portal")
	if _, err := s.SendCommandWithTrace("#variable {mount} {horse}", "input"); err != nil {
		t.Fatalf("set variable: %v", err)
	}
	if _, err := s.SendCommandWithTrace("#alias {portal} {prepare;слезть $mount}", "input"); err != nil {
		t.Fatalf("set alias: %v", err)
	}

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "prepare\nслезть horse\n" {
		t.Fatalf("scripted phrase start = %+v writes=%q", st, conn.String())
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 1 {
		t.Fatalf("grid scripted phrase FIFO = %d, want exactly one planned direction", pending)
	}
}

func TestMapWalkPhraseKeepsBracedSemicolonsInOneCommand(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "say {red;blue};enter portal")
	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "say {red;blue}\nenter portal\n" {
		t.Fatalf("braced phrase start = %+v writes=%q", st, conn.String())
	}
}

func TestMapWalkCanonicalGridDirectionBypassesAlias(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "")
	if _, err := s.SendCommandWithTrace("#alias {s} {say hijacked}", "input"); err != nil {
		t.Fatalf("define direction alias: %v", err)
	}

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "s\n" {
		t.Fatalf("canonical grid start = %+v writes=%q, want raw direction", st, conn.String())
	}
}

func TestMapWalkScriptUsesInlineDefinitionsConditionalsAndRepeat(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "#alias {portal} {enter $mount};#variable {mount} {arch};#if {1 == 1} {#2 portal}")

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "enter arch\nenter arch\n" {
		t.Fatalf("inline VM script start = %+v writes=%q", st, conn.String())
	}
}

func TestMapWalkDoorOpenerUsesAliasesVariablesAndSemicolons(t *testing.T) {
	s, conn := doorMapWalkHarness(t)
	if err := s.store.EnsureSessionProfiles(s.sessionID, "test"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	if err := s.vm.Reload(); err != nil {
		t.Fatalf("Reload VM: %v", err)
	}
	if _, err := s.SendCommandWithTrace("#variable {door} {gate}", "input"); err != nil {
		t.Fatalf("define opener variable: %v", err)
	}
	if _, err := s.SendCommandWithTrace("#alias {opener} {prepare;open $door %1}", "input"); err != nil {
		t.Fatalf("define opener alias: %v", err)
	}
	s.mapMu.Lock()
	s.mapTracker.Index().SetDoorOpenTemplate("opener {dir_en}")
	s.mapMu.Unlock()

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if !st.Active || conn.String() != "prepare\nopen gate s\ns\n" {
		t.Fatalf("scripted door opener = %+v writes=%q", st, conn.String())
	}
}

func TestMapWalkDoorOpenerRefusesZeroCommandsBeforeWrite(t *testing.T) {
	s, conn := doorMapWalkHarness(t)
	s.mapMu.Lock()
	s.mapTracker.Index().SetDoorOpenTemplate("#nop")
	s.mapMu.Unlock()

	st := s.StartMapWalk(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	if st.Active || st.Reason == "" || conn.String() != "" {
		t.Fatalf("zero-command door opener = %+v writes=%q", st, conn.String())
	}
}

func TestMapWalkScriptRunsSynchronousEchoBeforeTraversal(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "#showme {opening route};enter portal")

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "enter portal\n" {
		t.Fatalf("synchronous script start = %+v writes=%q", st, conn.String())
	}
	entries, err := s.store.LogEntriesForBuffer(s.sessionID, "main", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PlainText != "opening route" {
		t.Fatalf("synchronous script echoes = %+v", entries)
	}
}

func TestMapWalkParseableSeamDoesNotPushGridFIFO(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", ""),
		mkTrackRoom("B", 0, 1, 0, 2, "B", "b", ""),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|на восток|2"))}
	from := mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0}
	to := mapper.Coord{Zone: "B", X: 0, Y: 1, L: 0}
	s.mapTracker.Anchor(from)

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "e\n" {
		t.Fatalf("parseable seam start = %+v writes=%q", st, conn.String())
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("parseable seam fabricated %d grid FIFO entries", pending)
	}
}

func TestMapWalkRawSeamScriptIsOneConfirmationPacedHop(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", ""),
		mkTrackRoom("B", 0, 0, 0, 2, "B", "b", "S"),
		mkTrackRoom("B", 1, 0, 0, 3, "C", "c", "N"),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|откр воро;n|2"))}
	from := mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0}
	seamTarget := mapper.Coord{Zone: "B", X: 0, Y: 0, L: 0}
	goal := mapper.Coord{Zone: "B", X: 1, Y: 0, L: 0}
	s.mapTracker.Anchor(from)

	st := s.StartMapWalk(goal)
	if !st.Active || conn.String() != "откр воро\nn\n" {
		t.Fatalf("raw seam script start = %+v writes=%q", st, conn.String())
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("raw seam script fabricated %d FIFO entries", pending)
	}
	if got := conn.String(); got != "откр воро\nn\n" {
		t.Fatalf("next hop escaped before seam confirmation: %q", got)
	}
	confirmWalkAt(s, seamTarget)
	if got := conn.String(); got != "откр воро\nn\ns\n" {
		t.Fatalf("next hop was not dispatched after exact seam target: %q", got)
	}
}

func TestMapWalkParseableRawSeamScriptKeepsItsPrefix(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", ""),
		mkTrackRoom("B", 0, 0, 0, 2, "B", "b", ""),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|откр воро; n|2"))}
	from := mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0}
	to := mapper.Coord{Zone: "B", X: 0, Y: 0, L: 0}
	s.mapTracker.Anchor(from)

	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "откр воро\nn\n" {
		t.Fatalf("parseable raw seam script = %+v writes=%q", st, conn.String())
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("parseable scripted seam fabricated %d FIFO entries", pending)
	}
}

func TestMapWalkRawSeamRefusalPausesWithoutFIFO(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("A", 0, 0, 0, 1, "A", "a", ""),
		mkTrackRoom("B", 0, 0, 0, 2, "B", "b", ""),
	}
	store := newTestStore(t)
	conn := &recordingConn{}
	s := &Session{sessionID: 1, store: store, conn: conn, vm: vm.New(store, 1), clients: map[int]clientSink{}, mapTracker: mapper.NewTracker(buildTestMapIndexWithSeams(1, rooms, "B|откр воро;n|2"))}
	s.mapTracker.Anchor(mapper.Coord{Zone: "A", X: 0, Y: 0, L: 0})

	started := s.StartMapWalk(mapper.Coord{Zone: "B", X: 0, Y: 0, L: 0})
	if !started.Active || conn.String() != "откр воро\nn\n" {
		t.Fatalf("raw seam start = %+v writes=%q", started, conn.String())
	}
	s.processLine("Ворота закрыты")
	paused, active := s.MapWalkSnapshot()
	if !active || paused.State != "paused" || paused.Reason == "" {
		t.Fatalf("raw seam refusal = %+v active=%v", paused, active)
	}
	if got := conn.String(); got != "откр воро\nn\n" {
		t.Fatalf("raw seam refusal sent a tail: %q", got)
	}
	s.mapMu.Lock()
	pending := s.mapTracker.PendingCount()
	s.mapMu.Unlock()
	if pending != 0 {
		t.Fatalf("raw seam refusal FIFO = %d, want zero", pending)
	}
}

func TestMapWalkScriptLocalOnlyAndAsyncResultsFailBeforeTheHop(t *testing.T) {
	for _, phrase := range []string{"#showme {local only}", "#exec {/bin/echo} {nope}"} {
		t.Run(phrase, func(t *testing.T) {
			s, conn, to := phraseWalkHarness(t, phrase)
			st := s.StartMapWalk(to)
			if st.Active || st.Reason == "" {
				t.Fatalf("local/async script start = %+v, want terminal failure", st)
			}
			if got := conn.String(); got != "" {
				t.Fatalf("local/async script wrote %q", got)
			}
		})
	}
}

func TestMapWalkScriptRejectsSchedulerEffectsWithoutCreatingThem(t *testing.T) {
	for _, phrase := range []string{
		"#delay {walk-delay} {10} {say later};enter portal",
		"#ticker {walk-ticker} {10} {say later};enter portal",
	} {
		t.Run(phrase, func(t *testing.T) {
			s, conn, to := phraseWalkHarness(t, phrase)
			st := s.StartMapWalk(to)
			if st.Active || st.Reason == "" {
				t.Fatalf("scheduler script start = %+v, want terminal refusal", st)
			}
			if got := conn.String(); got != "" {
				t.Fatalf("scheduler script wrote %q", got)
			}
			s.delaysMu.Lock()
			delays := len(s.delays)
			s.delaysMu.Unlock()
			s.timersMu.Lock()
			timers := len(s.timers)
			s.timersMu.Unlock()
			if delays != 0 || timers != 0 {
				t.Fatalf("rejected scheduler script left delays=%d timers=%d", delays, timers)
			}
		})
	}
}

func TestMapWalkScriptRejectsTTSWithoutRunningIt(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "#tts {route speech};enter portal")
	called := 0
	s.vm.SetTTS(func(string) { called++ })
	st := s.StartMapWalk(to)
	if st.Active || st.Reason == "" || conn.String() != "" || called != 0 {
		t.Fatalf("TTS script start=%+v writes=%q calls=%d", st, conn.String(), called)
	}
}

func TestMapWalkScriptBroadcastsVariableChangesAndSendsExpandedCommand(t *testing.T) {
	s, conn, to := phraseWalkHarness(t, "#variable {mount} {horse};слезть $mount")
	var messages []ServerMsg
	s.clients[1] = clientSink{id: 1, send: func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	}}
	st := s.StartMapWalk(to)
	if !st.Active || conn.String() != "слезть horse\n" {
		t.Fatalf("variable script start = %+v writes=%q", st, conn.String())
	}
	variables := 0
	for _, msg := range messages {
		if msg.Type == "variables" {
			variables++
		}
	}
	if variables != 1 {
		t.Fatalf("variable broadcasts = %d, want one", variables)
	}
}

func TestMapWalkScriptFailureInvalidatesAfterSentPrefixAndKeepsHistoryPrefix(t *testing.T) {
	s, _, to := phraseWalkHarness(t, "prepare;enter portal")
	conn := &scriptedWriteConn{results: []scriptedWriteResult{{n: 99}, {n: 0, err: net.ErrClosed}}}
	s.conn = conn
	st := s.StartMapWalk(to)
	if !st.Active || st.State != "paused" {
		t.Fatalf("zero-byte suffix failure after prefix = %+v, want paused ambiguous route", st)
	}
	if got := conn.String(); got != "prepare\n" {
		t.Fatalf("script failure writes=%q, want sent prefix only", got)
	}
	s.mapMu.Lock()
	pos := s.mapTracker.Position()
	s.mapMu.Unlock()
	if pos.Valid || pos.Confidence != mapper.Red {
		t.Fatalf("sent-prefix failure tracker = %+v, want invalid red", pos)
	}
	history, err := s.store.ListHistory(s.sessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.Kind == "map_walk" && entry.Line == "enter portal" {
			t.Fatalf("unsent suffix leaked into history: %+v", history)
		}
	}
}

func TestMapWalkScriptPartialSuffixWriteInvalidatesAndStopsTail(t *testing.T) {
	s, _, to := phraseWalkHarness(t, "prepare;enter portal")
	conn := &scriptedWriteConn{results: []scriptedWriteResult{{n: 99}, {n: 3, err: errors.New("partial")}}}
	s.conn = conn
	st := s.StartMapWalk(to)
	if !st.Active || st.State != "paused" {
		t.Fatalf("partial suffix failure = %+v, want paused ambiguous route", st)
	}
	if got := conn.String(); got != "prepare\nent" {
		t.Fatalf("partial suffix writes=%q", got)
	}
}

func TestMapWalkScriptCancelBetweenExpandedCommandsStopsSuffixAndInvalidates(t *testing.T) {
	s, _, to := phraseWalkHarness(t, "prepare;dismount;enter portal")
	conn := &secondScriptWriteBlockConn{entered: make(chan struct{}), release: make(chan struct{})}
	s.conn = conn
	done := make(chan MapWalkState, 1)
	go func() { done <- s.StartMapWalk(to) }()
	<-conn.entered
	s.CancelMapWalk("cancel scripted hop")
	close(conn.release)
	<-done
	if got := conn.String(); got != "prepare\ndismount\n" {
		t.Fatalf("cancelled script wrote a suffix: %q", got)
	}
	if _, active := s.MapWalkSnapshot(); active {
		t.Fatal("cancelled sent-prefix script retained an active route")
	}
	s.mapMu.Lock()
	pos := s.mapTracker.Position()
	s.mapMu.Unlock()
	if pos.Valid || pos.Confidence != mapper.Red {
		t.Fatalf("cancelled script tracker = %+v, want invalid red", pos)
	}
}

func TestMapWalkScriptReplacementBetweenCommandsStopsSuffixAndPausesAmbiguous(t *testing.T) {
	s, _, to := phraseWalkHarness(t, "prepare;dismount;enter portal")
	conn := &secondScriptWriteBlockConn{entered: make(chan struct{}), release: make(chan struct{})}
	s.conn = conn
	done := make(chan MapWalkState, 1)
	go func() { done <- s.StartMapWalk(to) }()
	<-conn.entered
	queued := s.StartMapWalk(to)
	if queued.State != "stopping" {
		t.Fatalf("replacement while script dispatches = %+v", queued)
	}
	close(conn.release)
	<-done
	if got := conn.String(); got != "prepare\ndismount\n" {
		t.Fatalf("replaced script wrote a suffix: %q", got)
	}
	paused, active := s.MapWalkSnapshot()
	if !active || paused.State != "paused" || paused.CanResume {
		t.Fatalf("replaced sent-prefix script = %+v active=%v", paused, active)
	}
	s.mapMu.Lock()
	pos := s.mapTracker.Position()
	s.mapMu.Unlock()
	if pos.Valid || pos.Confidence != mapper.Red {
		t.Fatalf("replaced script tracker = %+v, want invalid red", pos)
	}
}
