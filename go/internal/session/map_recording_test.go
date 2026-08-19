package session

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

func TestMapRecordingConflictWireShapeIsStableLowercaseAndBounded(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("south", "input")
	long := strings.Repeat("Ж", 5000)
	s.mapMu.Lock()
	if !s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Observed", Desc: long, Exits: "N"}) {
		t.Fatal("no conflict")
	}
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	wire, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(wire, &root); err != nil {
		t.Fatal(err)
	}
	conf := root["conflict"].(map[string]any)
	move := conf["move"].(map[string]any)
	for _, coord := range []map[string]any{move["from"].(map[string]any), move["expected"].(map[string]any), conf["expected"].(map[string]any)} {
		for _, k := range []string{"zone", "x", "y", "l"} {
			if _, ok := coord[k]; !ok {
				t.Fatalf("coord missing %q: %s", k, wire)
			}
		}
		if _, bad := coord["Zone"]; bad {
			t.Fatalf("unstable uppercase coord: %s", wire)
		}
	}
	observed := conf["observed"].(map[string]any)
	expected := conf["expected_room"].(map[string]any)
	for _, room := range []map[string]any{observed, expected} {
		for _, k := range []string{"hint", "desc", "exits"} {
			if _, ok := room[k]; !ok {
				t.Fatalf("room missing %q: %s", k, wire)
			}
		}
	}
	if len([]rune(observed["desc"].(string))) != 4096 {
		t.Fatalf("observed description not bounded: %d", len([]rune(observed["desc"].(string))))
	}
	exported := s.MapRecordingState()
	exported.Conflict.ExpectedRoom.Desc = "consumer mutation"
	if fresh := s.MapRecordingState(); fresh.Conflict.ExpectedRoom.Desc == "consumer mutation" {
		t.Fatal("expected saved room aliased exported state")
	}
}

func reconciliationSessionHarness(t *testing.T) (*Session, *storage.Store, int64, uint64) {
	t.Helper()
	store := newProfileTestStore(t)
	frozen, err := store.CreateMapSet(storage.MapSetInput{Name: "reconcile", Rooms: []storage.Room{{Zone: "Z", X: 1, Y: 1, Hint: "5", EDirs: `[]`, Doors: `[]`}, {Zone: "Z", X: 0, Y: 1, Hint: "2", EDirs: `[]`, Doors: `[]`}, {Zone: "Z", X: 0, Y: 0, Hint: "1", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	setID, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession("r", "localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveMapSetID(rec.ID, setID); err != nil {
		t.Fatal(err)
	}
	rooms, _ := store.ListRooms(setID)
	edges, _ := store.ListMapEdges(setID)
	tr := mapper.NewTracker(mapper.BuildIndexWithEdges(setID, rooms, edges))
	tr.Anchor(mapper.Coord{Zone: "Z", X: 1, Y: 1})
	cid := uint64(9)
	s := &Session{sessionID: rec.ID, store: store, mapTracker: tr, mapRecordingMode: MapRecordingConflict, mapRecordingEpoch: 3, mapRecordingRevision: 4, mapRecordingConflict: &MapRecordingConflictSnapshot{ID: cid, Move: PendingMapMove{Epoch: 3, Command: "climb exact", Direction: "N", Source: "input", From: mapper.Coord{Zone: "Z", X: 1, Y: 1}, Expected: mapper.Coord{Zone: "Z", X: 0, Y: 1}, HasExpected: true}, Expected: mapper.Coord{Zone: "Z", X: 0, Y: 1}, Observed: mapper.RoomEvent{Hint: "One"}}, clients: map[int]clientSink{}}
	return s, store, setID, cid
}

func TestReconciliationPreviewApplyIsServerOwnedOneShotAndExact(t *testing.T) {
	s, store, setID, cid := reconciliationSessionHarness(t)
	choice := MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}, Layouts: []MapReconciliationLayout{{Zone: "Z", X: 1, Y: 1, DisplayX: 1, DisplayY: 0}}}
	before, _ := store.GetMapSet(setID)
	p, err := s.PreviewMapReconciliation(choice)
	if err != nil {
		t.Fatal(err)
	}
	previewWire, _ := json.Marshal(p)
	if strings.Contains(string(previewWire), `"Source"`) || !strings.Contains(string(previewWire), `"source":{"zone"`) || !strings.Contains(string(previewWire), `"target":{"zone"`) {
		t.Fatalf("unstable preview wire: %s", previewWire)
	}
	afterPreview, _ := store.GetMapSet(setID)
	if afterPreview.TopologyRevision != before.TopologyRevision {
		t.Fatal("preview wrote topology")
	}
	p.Layouts[0].DisplayX = 99 // consumer mutation must not alter reserved preview
	state, gotSet, err := s.ApplyMapReconciliation(cid, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	if gotSet != setID || state.Mode != MapRecordingRecording || state.Conflict != nil {
		t.Fatalf("apply state=%+v set=%d", state, gotSet)
	}
	var e storage.MapEdge
	if err := store.DB().Where("map_set_id=? AND kind=?", setID, storage.EdgeKindCorridor).First(&e).Error; err != nil {
		t.Fatal(err)
	}
	to, ok := e.To()
	if !ok || to != (storage.EdgeCell{Zone: "Z"}) || e.Command != "climb exact" || e.Source != storage.EdgeSourceReconciled {
		t.Fatalf("server-owned corridor=%#v", e)
	}
	var layout storage.RoomLayout
	if err := store.DB().Where("map_set_id=? AND zone='Z' AND x=1 AND y=1", setID).First(&layout).Error; err != nil || layout.DisplayX != 1 || layout.DisplayY != 0 {
		t.Fatalf("preview mutation leaked: layout=%+v err=%v", layout, err)
	}
	if pos := s.mapTracker.Position(); !pos.Valid || pos.Confidence != mapper.Green || pos.Coord != (mapper.Coord{Zone: "Z"}) {
		t.Fatalf("writer not reanchored: %+v", pos)
	}
	if _, _, err := s.ApplyMapReconciliation(cid, p.Token); err == nil {
		t.Fatal("preview token reused")
	}
}

func TestReconciliationConcurrentApplyOneWinnerAndStaleRevisionWritesNothing(t *testing.T) {
	s, store, setID, cid := reconciliationSessionHarness(t)
	choice := MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}}
	p, err := s.PreviewMapReconciliation(choice)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyTopologyOp(setID, storage.TopologyOp{Kind: storage.TopoUpsertRoom, Zone: "Z", X: 0, Y: 0, Fields: storage.RoomFields{Hint: func() *string { v := "changed"; return &v }()}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyMapReconciliation(cid, p.Token); err == nil {
		t.Fatal("stale apply succeeded")
	}
	var n int64
	store.DB().Model(&storage.MapEdge{}).Where("map_set_id=? AND kind=?", setID, storage.EdgeKindCorridor).Count(&n)
	if n != 0 {
		t.Fatal("stale apply wrote corridor")
	}
	s2, _, _, cid2 := reconciliationSessionHarness(t)
	p2, err := s2.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid2, Target: mapper.Coord{Zone: "Z"}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	wins := 0
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if _, _, err := s2.ApplyMapReconciliation(cid2, p2.Token); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent winners=%d", wins)
	}
}

func TestReconciliationPreviewInvalidatedByStop(t *testing.T) {
	s, _, _, cid := reconciliationSessionHarness(t)
	p, err := s.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}})
	if err != nil {
		t.Fatal(err)
	}
	s.StopMapRecording("stop")
	if _, _, err := s.ApplyMapReconciliation(cid, p.Token); err == nil {
		t.Fatal("stopped conflict applied")
	}
}

func TestReconciliationApplyRejectsMapSwitchAndFrozenPreview(t *testing.T) {
	s, store, setID, cid := reconciliationSessionHarness(t)
	p, err := s.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateMapSet(storage.MapSetInput{Name: "other", Rooms: []storage.Room{{Zone: "Q", Hint: "q", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveMapSetID(s.sessionID, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyMapReconciliation(cid, p.Token); err == nil {
		t.Fatal("map-switched preview applied")
	}
	var n int64
	store.DB().Model(&storage.MapEdge{}).Where("map_set_id=? AND kind=?", setID, storage.EdgeKindCorridor).Count(&n)
	if n != 0 {
		t.Fatal("map-switch failure wrote original set")
	}
	s.mapMu.Lock()
	s.mapReconcilePreview = nil
	s.mapMu.Unlock()
	if _, err := s.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Q"}}); err == nil {
		t.Fatal("frozen map preview exposed apply")
	}
}

func TestReconciliationPeerRefreshPreservesValidPositionAndPending(t *testing.T) {
	writer, store, setID, cid := reconciliationSessionHarness(t)
	rec, err := store.CreateSession("peer", "localhost", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveMapSetID(rec.ID, setID); err != nil {
		t.Fatal(err)
	}
	rooms, _ := store.ListRooms(setID)
	edges, _ := store.ListMapEdges(setID)
	tr := mapper.NewTracker(mapper.BuildIndexWithEdges(setID, rooms, edges))
	tr.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 1})
	tr.PushMove("E")
	peer := &Session{sessionID: rec.ID, store: store, mapTracker: tr, clients: map[int]clientSink{}}
	manager := NewManager(store)
	manager.sessions[writer.sessionID] = writer
	manager.sessions[peer.sessionID] = peer
	p, err := writer.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := writer.ApplyMapReconciliation(cid, p.Token); err != nil {
		t.Fatal(err)
	}
	before := peer.mapTracker.Position()
	pending := peer.mapTracker.PendingCount()
	manager.RefreshSharedTopology(setID, writer.sessionID)
	after := peer.mapTracker.Position()
	if !after.Valid || after.Coord != before.Coord || after.Confidence != before.Confidence || peer.mapTracker.PendingCount() != pending {
		t.Fatalf("peer refresh lost state: before=%+v after=%+v", before, after)
	}
}

func TestReconciliationPostCommitStopIsRejectedAndMapSwitchWaits(t *testing.T) {
	s, store, _, cid := reconciliationSessionHarness(t)
	p, err := s.PreviewMapReconciliation(MapReconciliationChoice{ConflictID: cid, Target: mapper.Coord{Zone: "Z"}})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.mapReconcileAfterCommit = func() { close(entered); <-release }
	applyDone := make(chan error, 1)
	go func() { _, _, err := s.ApplyMapReconciliation(cid, p.Token); applyDone <- err }()
	<-entered
	if state, stopped := s.TryStopMapRecording("concurrent stop"); stopped || state.Mode != MapRecordingConflict || state.Conflict == nil {
		t.Fatalf("stop mutated applying conflict: %+v", state)
	}
	other, err := store.CreateMapSet(storage.MapSetInput{Name: "switch", Rooms: []storage.Room{{Zone: "Q", Hint: "q", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	switchDone := make(chan error, 1)
	go func() { switchDone <- s.SetActiveMapSet(other) }()
	select {
	case err := <-switchDone:
		t.Fatalf("map switch did not wait, err=%v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-applyDone; err != nil {
		t.Fatalf("committed apply failed publication: %v", err)
	}
	if err := <-switchDone; err != nil {
		t.Fatal(err)
	}
	if state := s.MapRecordingState(); state.Mode != MapRecordingOff || state.Reason != "active map changed" {
		t.Fatalf("switch did not follow committed finalization: %+v", state)
	}
}

func recordingHarness() *Session {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "Start", "start", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "Expected", "expected", "N"),
	}
	tr := mapper.NewTracker(buildTestMapIndex(7, rooms))
	tr.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	return &Session{mapTracker: tr, lastRoom: mapper.RoomEvent{Hint: "Start", Desc: "start", Exits: "S"}, lastRoomOK: true, mapWalkEpoch: 42, mapRecordingMode: MapRecordingOff, clients: map[int]clientSink{}}
}

func TestMapRecordingStartRequiresGreenAndFullRoom(t *testing.T) {
	s := recordingHarness()
	if state, err := s.StartMapRecording(); err != nil || state.Mode != MapRecordingArmed {
		t.Fatalf("start state=%+v err=%v", state, err)
	}
	s.StopMapRecording("test")
	s.mapMu.Lock()
	s.lastRoomOK = false
	s.mapMu.Unlock()
	if _, err := s.StartMapRecording(); err == nil {
		t.Fatal("start accepted without full live room")
	}
	s.mapMu.Lock()
	s.lastRoomOK = true
	s.mapTracker.Invalidate("lost")
	s.mapMu.Unlock()
	if _, err := s.StartMapRecording(); err == nil {
		t.Fatal("start accepted red position")
	}
}

func TestAcceptedMoveCarriesExactCommandSourceSequenceEpochAndFailedWriteDoesNot(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("south", "input")
	state := s.MapRecordingState()
	if state.Pending != 1 {
		t.Fatalf("pending=%d", state.Pending)
	}
	s.mapMu.Lock()
	got := s.mapRecordingPending[0]
	s.mapMu.Unlock()
	if got.Command != "south" || got.Direction != "S" || got.Source != "input" || got.Epoch != 1 || got.Sequence == 0 || got.From != (mapper.Coord{Zone: "Z"}) {
		t.Fatalf("move=%+v", got)
	}

	failing := recordingHarness()
	started, err := failing.StartMapRecording()
	if err != nil {
		t.Fatal(err)
	}
	failing.conn = &failingManualConn{}
	if err := failing.commitManualMapMove("s", "S", "input"); err == nil {
		t.Fatal("expected transport failure")
	}
	if state := failing.MapRecordingState(); state.Pending != 0 || state.Mode != MapRecordingArmed || state.Reason != "" || state.Revision != started.Revision {
		t.Fatalf("failed write queued evidence: %+v", state)
	}
}

func TestFirstOccupiedMismatchFreezesConflictAndSecondRoomCannotReplaceIt(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.mapMu.Lock()
	if !s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Observed", Desc: "other", Exits: "N"}) {
		t.Fatal("mismatch did not conflict")
	}
	first := *s.mapRecordingConflict
	if s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Later", Desc: "later", Exits: "E"}) {
		t.Fatal("second event replaced conflict")
	}
	second := *s.mapRecordingConflict
	s.mapMu.Unlock()
	if first.ID != second.ID || second.Observed.Hint != "Observed" || second.Move.Command != "s" || second.Expected != (mapper.Coord{Zone: "Z", X: 1}) {
		t.Fatalf("conflict mutated: first=%+v second=%+v", first, second)
	}
	if state := s.MapRecordingState(); state.Mode != MapRecordingConflict || state.Pending != 0 {
		t.Fatalf("state=%+v", state)
	}
}

func TestConflictSnapshotDeepCopiesPendingTail(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.observeOutgoingMove("e", "trigger")
	s.mapMu.Lock()
	if !s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Wrong", Desc: "wrong", Exits: "W"}) {
		t.Fatal("mismatch did not conflict")
	}
	if len(s.mapRecordingConflict.PendingTail) != 1 || len(s.mapRecordingPending) != 0 {
		t.Fatalf("snapshot/live tail=%+v/%+v", s.mapRecordingConflict.PendingTail, s.mapRecordingPending)
	}
	frozen := s.mapRecordingConflict.PendingTail[0]
	s.cancelAcceptedMapMoveLocked()
	s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Later", Desc: "later", Exits: "N"})
	got := s.mapRecordingConflict.PendingTail[0]
	s.mapMu.Unlock()
	if got != frozen || got.Command != "e" || got.Source != "trigger" {
		t.Fatalf("immutable tail mutated: before=%+v after=%+v", frozen, got)
	}
	exported := s.MapRecordingState()
	exported.Conflict.PendingTail[0].Command = "mutated by consumer"
	if fresh := s.MapRecordingState(); fresh.Conflict.PendingTail[0].Command != "e" {
		t.Fatalf("exported state aliased internal snapshot: %+v", fresh.Conflict.PendingTail)
	}
}

func TestPendingMapMovesAreBounded(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < mapRecordingPendingCap+10; i++ {
		s.observeOutgoingMove("s", "trigger")
	}
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapRecordingMode != MapRecordingOff || len(s.mapRecordingPending) != 0 || s.mapRecordingReason != "pending movement limit exceeded" {
		t.Fatalf("overflow did not stop safely: mode=%s pending=%d reason=%q", s.mapRecordingMode, len(s.mapRecordingPending), s.mapRecordingReason)
	}
}

func TestRecordingRunDropsPreStartEvidenceAndChainsPredictedFrom(t *testing.T) {
	s := recordingHarness()
	s.observeOutgoingMove("s", "input")
	if state := s.MapRecordingState(); state.Pending != 0 {
		t.Fatalf("off mode retained evidence: %+v", state)
	}
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.observeOutgoingMove("s", "input")
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if len(s.mapRecordingPending) != 2 {
		t.Fatalf("pending=%d", len(s.mapRecordingPending))
	}
	if s.mapRecordingPending[0].From != (mapper.Coord{Zone: "Z"}) || s.mapRecordingPending[1].From != (mapper.Coord{Zone: "Z", X: 1}) {
		t.Fatalf("chain=%+v", s.mapRecordingPending)
	}
}

func TestShortWriteDoesNotCreateRecordingEvidence(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.conn = &partialWriteConn{n: 1}
	if err := s.commitManualMapMove("south", "S", "input"); err == nil {
		t.Fatal("short write accepted")
	}
	if state := s.MapRecordingState(); state.Pending != 0 || state.Mode != MapRecordingOff || state.Reason != "manual movement command outcome unknown" {
		t.Fatalf("short write evidence/state=%+v", state)
	}
}

func TestRefusalRecomputesRemainingEvidenceFromActualPosition(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.observeOutgoingMove("e", "input")
	s.observeIncomingLine("Вы не можете идти в этом направлении.", "Вы не можете идти в этом направлении.")
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if len(s.mapRecordingPending) != 1 {
		t.Fatalf("pending=%d", len(s.mapRecordingPending))
	}
	got := s.mapRecordingPending[0]
	if got.Command != "e" || got.From != (mapper.Coord{Zone: "Z"}) || got.Expected != (mapper.Coord{Zone: "Z", Y: 1}) {
		t.Fatalf("tail not rebased after refusal: %+v", got)
	}
}

func TestRepeatedStartCannotResetRunOrBypassConflict(t *testing.T) {
	s := recordingHarness()
	first, err := s.StartMapRecording()
	if err != nil {
		t.Fatal(err)
	}
	if state, err := s.StartMapRecording(); err == nil || state.Revision != first.Revision {
		t.Fatalf("repeated start state=%+v err=%v", state, err)
	}
	s.observeOutgoingMove("s", "input")
	s.mapMu.Lock()
	s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "Different", Desc: "different", Exits: "N"})
	conflictRevision := s.mapRecordingRevision
	s.mapMu.Unlock()
	if state, err := s.StartMapRecording(); err == nil || state.Mode != MapRecordingConflict || state.Revision != conflictRevision {
		t.Fatalf("conflict bypass state=%+v err=%v", state, err)
	}
}

func TestAuthoritativeMoveTargetIsPreserved(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	want := mapper.Coord{Zone: "Other", X: 9, Y: 8, L: 7}
	s.mapMu.Lock()
	s.recordAcceptedMapMoveLocked("portal", "E", "map_walk", &want)
	got := s.mapRecordingPending[0]
	s.mapMu.Unlock()
	if !got.HasExpected || got.Expected != want {
		t.Fatalf("authoritative expected lost: %+v", got)
	}
}

func TestManualPredictionUsesReportedRoomBeyondPipeRun(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "Start", "start", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "", "", "N S"),
		mkTrackRoom("Z", 2, 0, 0, 3, "End", "end", "N"),
	}
	rooms[1].Pipe = true
	tr := mapper.NewTracker(buildTestMapIndex(7, rooms))
	tr.Anchor(mapper.Coord{Zone: "Z"})
	s := &Session{mapTracker: tr, lastRoom: mapper.RoomEvent{Hint: "Start", Desc: "start", Exits: "S"}, lastRoomOK: true, mapRecordingMode: MapRecordingOff, clients: map[int]clientSink{}}
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.mapMu.Lock()
	conflict := s.observeMapRecordingRoomLocked(mapper.RoomEvent{Hint: "End", Desc: "end", Exits: "N"})
	s.mapMu.Unlock()
	if conflict {
		t.Fatal("normal pipe-run landing created a false conflict")
	}
}

func TestPipeLandingRebasesSecondPendingMoveFromConfirmedRoom(t *testing.T) {
	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "Start", "start", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "", "", "N S"),
		mkTrackRoom("Z", 2, 0, 0, 3, "End", "end", "N E"),
		mkTrackRoom("Z", 2, 1, 0, 4, "East", "east", "W"),
	}
	rooms[1].Pipe = true
	tr := mapper.NewTracker(buildTestMapIndex(7, rooms))
	tr.Anchor(mapper.Coord{Zone: "Z"})
	s := &Session{mapTracker: tr, lastRoom: mapper.RoomEvent{Hint: "Start", Desc: "start", Exits: "S"}, lastRoomOK: true, mapRecordingMode: MapRecordingOff, clients: map[int]clientSink{}}
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.observeOutgoingMove("s", "input")
	s.observeOutgoingMove("e", "input")
	s.mapMu.Lock()
	ev := mapper.RoomEvent{Hint: "End", Desc: "end", Exits: "N E"}
	if s.observeMapRecordingRoomLocked(ev) {
		t.Fatal("pipe landing conflicted")
	}
	s.mapTracker.Reconcile(ev)
	s.rebasePendingMapMovesLocked()
	defer s.mapMu.Unlock()
	if len(s.mapRecordingPending) != 1 || s.mapRecordingPending[0].From != (mapper.Coord{Zone: "Z", X: 2}) {
		t.Fatalf("tail not rebased from confirmed pipe landing: %+v", s.mapRecordingPending)
	}
}

func TestRefusalCancelsRecordingEvidenceWithoutTrackerFIFO(t *testing.T) {
	s := recordingHarness()
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	target := mapper.Coord{Zone: "Other", X: 2}
	s.mapMu.Lock()
	s.recordAcceptedMapMoveLocked("portal", "E", "map_walk", &target)
	s.mapMu.Unlock()
	s.observeIncomingLine("Вы не можете идти в этом направлении.", "Вы не можете идти в этом направлении.")
	if state := s.MapRecordingState(); state.Pending != 0 {
		t.Fatalf("seam refusal left stale evidence: %+v", state)
	}
}

func TestMapRecordingBroadcastSynchronizesTwoClients(t *testing.T) {
	s := recordingHarness()
	var mu sync.Mutex
	counts := map[string]int{}
	for _, name := range []string{"one", "two"} {
		n := name
		s.AttachClient(n, func(msg ServerMsg) error {
			if msg.Type == "map_recording_state" {
				mu.Lock()
				counts[n]++
				mu.Unlock()
			}
			return nil
		})
	}
	if _, err := s.StartMapRecording(); err != nil {
		t.Fatal(err)
	}
	s.BroadcastMapRecordingState()
	mu.Lock()
	defer mu.Unlock()
	if counts["one"] != 1 || counts["two"] != 1 {
		t.Fatalf("broadcast counts=%v", counts)
	}
}
