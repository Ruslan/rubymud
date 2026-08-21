package session

import (
	"io"
	"strings"
	"time"

	"rubymud/go/internal/mapper"
)

const mapScanTimeout = 3 * time.Second

type mapScanKey struct {
	epoch    int64
	setID    int64
	revision int64
	coord    mapper.Coord
}

type mapScanJob struct {
	token   uint64
	key     mapScanKey
	config  mapper.ScanConfig
	unknown map[string]bool
}

type mapScanCapture struct {
	mapScanJob
	lines    int
	hints    map[string]string
	canceled bool
}

// mapAutorecordBoundary bridges the only asynchronous room path: the detector
// emits a new-room event at the exits line, while the SQLite writer may finish
// either before or after the room's trailing blank/prompt. mapMu serializes the
// boundary observation with commit publication so the follow-up scan starts on
// the correct side in both orders.
type mapAutorecordBoundary struct {
	epoch  int64
	move   uint64
	config mapper.ScanConfig
	passed bool
}

// scheduleMapScan is intentionally cheap and best-effort. It probes only while
// single-step recording is active, the tracker is green with no move in flight,
// and the current room advertises at least one exit without a known target.
func (s *Session) scheduleMapScan() {
	s.mapMu.Lock()
	job, ok := s.prepareMapScanLocked()
	s.mapMu.Unlock()
	if !ok {
		return
	}
	go func() {
		defer s.recoverGoroutine("mapper scan writer")
		s.writeMapScan(job)
	}()
}

// scheduleMapScanAfterRoomBoundary reserves the exact current-room probe but
// does not write it until the detector's trailing room boundary has passed.
// Room events are emitted on the exits line; RMUD sends a blank after that
// line, which otherwise races the detached writer and looks like an empty scan
// response. Start/reload paths whose room boundary is already in the past keep
// using scheduleMapScan directly.
func (s *Session) scheduleMapScanAfterRoomBoundary() {
	s.mapMu.Lock()
	job, ok := s.prepareMapScanLocked()
	if ok && job.config.HasRoomBoundary() {
		s.mapScanAfterBoundary = &job
	}
	s.mapMu.Unlock()
	if ok && !job.config.HasRoomBoundary() {
		go func() {
			defer s.recoverGoroutine("mapper scan writer")
			s.writeMapScan(job)
		}()
	}
}

func (s *Session) takeMapScanAfterRoomBoundaryLocked(line string) *mapScanJob {
	job := s.mapScanAfterBoundary
	if job == nil || !job.config.RoomBoundary(line) {
		return nil
	}
	s.mapScanAfterBoundary = nil
	if !s.mapScanJobCurrentLocked(*job) {
		return nil
	}
	copy := *job
	return &copy
}

func (s *Session) reserveMapAutorecordBoundaryLocked(epoch int64, move uint64) {
	s.mapAutorecordBoundary = nil
	config, ok := mapper.DetectorScan(s.mapDetector)
	if !ok || !config.HasRoomBoundary() {
		return
	}
	s.mapAutorecordBoundary = &mapAutorecordBoundary{epoch: epoch, move: move, config: config}
}

func (s *Session) noteMapAutorecordBoundaryLocked(line string) {
	waiting := s.mapAutorecordBoundary
	if waiting != nil && waiting.config.RoomBoundary(line) {
		waiting.passed = true
	}
}

// prepareMapScanAfterAutorecordLocked decides under the same mapMu as incoming
// lines whether the room boundary is already behind us. Returning a job means
// "write now"; nil with mapScanAfterBoundary set means the parser will launch it
// immediately after the pending boundary. Caller holds mapMu.
func (s *Session) prepareMapScanAfterAutorecordLocked(job mapAutorecordJob) *mapScanJob {
	prepared, ok := s.prepareMapScanLocked()
	if !ok {
		s.mapAutorecordBoundary = nil
		return nil
	}
	waiting := s.mapAutorecordBoundary
	matched := waiting != nil && waiting.epoch == job.Epoch && waiting.move == job.Move.Sequence
	if prepared.config.HasRoomBoundary() && matched && !waiting.passed {
		copy := prepared
		s.mapScanAfterBoundary = &copy
		s.mapAutorecordBoundary = nil
		return nil
	}
	s.mapAutorecordBoundary = nil
	copy := prepared
	return &copy
}

func (s *Session) prepareMapScanLocked() (mapScanJob, bool) {
	if s.mapRecordingMode != MapRecordingArmed && s.mapRecordingMode != MapRecordingRecording {
		return mapScanJob{}, false
	}
	// Do not overlap request/response windows. A canceled capture remains here
	// only long enough to discard its bounded response through the boundary.
	if s.mapScanCapture != nil {
		return mapScanJob{}, false
	}
	config, ok := mapper.DetectorScan(s.mapDetector)
	if !ok || s.mapTracker == nil || s.mapTracker.Index() == nil || s.mapTracker.PendingCount() != 0 {
		return mapScanJob{}, false
	}
	pos := s.mapTracker.Position()
	room := s.mapTracker.CurrentRoom()
	if !pos.Valid || pos.Confidence != mapper.Green || !pos.EventDescribesCell || room == nil {
		return mapScanJob{}, false
	}
	configured := make(map[string]bool, len(config.Directions))
	for _, rule := range config.Directions {
		configured[rule.Dir] = true
	}
	unknown := make(map[string]bool, len(room.EDirs))
	for _, dir := range room.EDirs {
		if !configured[dir] {
			continue
		}
		edge := room.GridEdge(dir)
		if edge == nil || (edge.Passable && edge.To == nil) {
			unknown[dir] = true
		}
	}
	if len(unknown) == 0 {
		return mapScanJob{}, false
	}
	idx := s.mapTracker.Index()
	key := mapScanKey{epoch: s.mapRecordingEpoch, setID: idx.MapSetID, revision: idx.TopologyRevision, coord: pos.Coord}
	if s.mapScanLastOK && s.mapScanLast == key {
		return mapScanJob{}, false
	}
	s.mapScanSequence++
	s.mapScanLast, s.mapScanLastOK = key, true
	return mapScanJob{token: s.mapScanSequence, key: key, config: config, unknown: unknown}, true
}

func (s *Session) mapScanJobCurrentLocked(job mapScanJob) bool {
	if s.mapRecordingEpoch != job.key.epoch || s.mapTracker == nil || s.mapTracker.Index() == nil || s.mapTracker.PendingCount() != 0 {
		return false
	}
	pos := s.mapTracker.Position()
	idx := s.mapTracker.Index()
	return (s.mapRecordingMode == MapRecordingArmed || s.mapRecordingMode == MapRecordingRecording) &&
		pos.Valid && pos.Confidence == mapper.Green && pos.EventDescribesCell && pos.Coord == job.key.coord &&
		idx.MapSetID == job.key.setID && idx.TopologyRevision == job.key.revision &&
		s.mapScanLastOK && s.mapScanLast == job.key && s.mapScanSequence == job.token
}

func (s *Session) writeMapScan(job mapScanJob) {
	// The parser gate closes the only dangerous gap: response bytes cannot be
	// reconciled before the capture is installed for the exact position/epoch.
	s.mapWalkIOMu.Lock()
	defer s.mapWalkIOMu.Unlock()
	s.mapMu.Lock()
	if !s.mapScanJobCurrentLocked(job) || s.conn == nil {
		s.mapMu.Unlock()
		return
	}
	s.clearMapScanCaptureLocked()
	s.mapScanCapture = &mapScanCapture{mapScanJob: job, hints: make(map[string]string, len(job.unknown))}
	s.mapMu.Unlock()

	payload := []byte(job.config.Command + "\n")
	n, err := s.conn.Write(payload)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	s.mapMu.Lock()
	if err != nil {
		if s.mapScanCapture != nil && s.mapScanCapture.token == job.token {
			s.clearMapScanCaptureLocked()
		}
		s.mapMu.Unlock()
		return
	}
	if s.mapScanCapture != nil && s.mapScanCapture.token == job.token {
		token := job.token
		s.mapScanTimer = time.AfterFunc(mapScanTimeout, func() { s.expireMapScan(token) })
	}
	s.mapMu.Unlock()
}

// consumeMapScanLineLocked consumes at most one small map lookup and six prefix
// comparisons while a scan is pending; with no pending capture it is one nil
// check and no allocation. The bool is true for every line inside the bounded
// scan response, including its final boundary, so callers keep those lines out
// of the ordinary room detector. Caller holds mapMu.
func (s *Session) consumeMapScanLineLocked(line string) (*ServerMsg, bool) {
	capture := s.mapScanCapture
	if capture == nil {
		return nil, false
	}
	if !s.mapScanJobCurrentLocked(capture.mapScanJob) {
		s.cancelMapScanCaptureLocked()
	}
	capture.lines++
	if !capture.canceled {
		if dir, hint, ok := capture.config.Match(line); ok && capture.unknown[dir] {
			capture.hints[dir] = boundedMapText(hint, 256)
		}
	}
	trimmed := strings.TrimSpace(line)
	complete := (capture.config.StopBlank && trimmed == "") || capture.config.Boundary(line) || capture.lines >= capture.config.MaxLines
	if !complete {
		return nil, true
	}
	return s.finishMapScanLocked(capture.token), true
}

func (s *Session) finishMapScanLocked(token uint64) *ServerMsg {
	capture := s.mapScanCapture
	if capture == nil || capture.token != token {
		return nil
	}
	if capture.canceled || !s.mapScanJobCurrentLocked(capture.mapScanJob) || len(capture.hints) == 0 {
		s.clearMapScanCaptureLocked()
		return nil
	}
	hints := make([]RoomScanHint, 0, len(capture.hints))
	for _, rule := range capture.config.Directions {
		if hint, ok := capture.hints[rule.Dir]; ok {
			hints = append(hints, RoomScanHint{Dir: rule.Dir, Hint: hint})
		}
	}
	msg := &ServerMsg{Type: "room_scan", MapSetID: capture.key.setID, Zone: capture.key.coord.Zone, RoomX: capture.key.coord.X, RoomY: capture.key.coord.Y, RoomL: capture.key.coord.L, RoomScan: hints, RoomScanRecordingRevision: s.mapRecordingRevision}
	snapshot := *msg
	snapshot.RoomScan = append([]RoomScanHint(nil), hints...)
	s.mapScanSnapshot = &snapshot
	s.clearMapScanCaptureLocked()
	return msg
}

// RoomScanSnapshot replays the last completed ephemeral scan to a reconnected
// browser, but only while it still belongs to the active recording position.
func (s *Session) RoomScanSnapshot() (ServerMsg, bool) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapScanSnapshot == nil || s.mapTracker == nil || (s.mapRecordingMode != MapRecordingArmed && s.mapRecordingMode != MapRecordingRecording) {
		return ServerMsg{}, false
	}
	pos := s.mapTracker.Position()
	msg := *s.mapScanSnapshot
	if !pos.Valid || pos.Confidence != mapper.Green || !pos.EventDescribesCell || pos.Coord != (mapper.Coord{Zone: msg.Zone, X: msg.RoomX, Y: msg.RoomY, L: msg.RoomL}) {
		return ServerMsg{}, false
	}
	msg.RoomScan = append([]RoomScanHint(nil), msg.RoomScan...)
	return msg, true
}

func (s *Session) expireMapScan(token uint64) {
	s.mapMu.Lock()
	msg := s.finishMapScanLocked(token)
	s.mapMu.Unlock()
	if msg != nil {
		s.broadcastMsg(*msg)
	}
}

func (s *Session) clearMapScanCaptureLocked() {
	if s.mapScanTimer != nil {
		s.mapScanTimer.Stop()
		s.mapScanTimer = nil
	}
	s.mapScanCapture = nil
}

// cancelMapScanCaptureLocked turns an in-flight response into a bounded discard
// window. We deliberately keep the capture until blank/prompt/max-lines (or its
// timer): clearing it immediately would feed the old scan response into the
// ordinary room detector after a user command or movement overtook the probe.
func (s *Session) cancelMapScanCaptureLocked() {
	if s.mapScanCapture == nil {
		return
	}
	s.mapScanCapture.canceled = true
	clear(s.mapScanCapture.hints)
}

func (s *Session) cancelMapScanForOutbound() {
	s.mapMu.Lock()
	// Invalidate both an installed capture and a writer goroutine that has been
	// scheduled but has not reached the transport gate yet.
	s.mapScanSequence++
	s.mapScanAfterBoundary = nil
	s.cancelMapScanCaptureLocked()
	s.mapMu.Unlock()
}
