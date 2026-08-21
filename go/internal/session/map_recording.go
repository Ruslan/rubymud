package session

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

const mapReconcileLayoutCap = 256

type MapReconciliationLayout struct {
	Zone     string `json:"zone"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	L        int    `json:"l"`
	DisplayX int    `json:"display_x"`
	DisplayY int    `json:"display_y"`
	DisplayL int    `json:"display_l"`
	Pinned   bool   `json:"pinned"`
}
type MapReconciliationChoice struct {
	ConflictID uint64                    `json:"conflict_id"`
	Target     mapper.Coord              `json:"target"`
	Layouts    []MapReconciliationLayout `json:"layouts"`
}
type MapReconciliationPreview struct {
	Token            string                    `json:"token"`
	ConflictID       uint64                    `json:"conflict_id"`
	MapSetID         int64                     `json:"map_set_id"`
	ExpectedRevision int64                     `json:"expected_revision"`
	Source           mapper.Coord              `json:"source"`
	Expected         mapper.Coord              `json:"expected"`
	Target           mapper.Coord              `json:"target"`
	Direction        string                    `json:"direction"`
	Command          string                    `json:"command"`
	Layouts          []MapReconciliationLayout `json:"layouts"`
	Epoch            int64                     `json:"-"`
}

func normalizeReconciliationCoord(coord mapper.Coord) (mapper.Coord, error) {
	zone, err := storage.NormalizeZoneName(coord.Zone)
	if err != nil {
		return coord, err
	}
	coord.Zone = zone
	return coord, nil
}

func normalizeReconciliationLayouts(layouts []MapReconciliationLayout) ([]MapReconciliationLayout, error) {
	out := make([]MapReconciliationLayout, len(layouts))
	copy(out, layouts)
	for i := range out {
		zone, err := storage.NormalizeZoneName(out[i].Zone)
		if err != nil {
			return nil, err
		}
		out[i].Zone = zone
	}
	return out, nil
}

func normalizeReconciliationPreviewZones(preview *MapReconciliationPreview) error {
	var err error
	if preview.Source, err = normalizeReconciliationCoord(preview.Source); err != nil {
		return err
	}
	if preview.Expected, err = normalizeReconciliationCoord(preview.Expected); err != nil {
		return err
	}
	if preview.Target, err = normalizeReconciliationCoord(preview.Target); err != nil {
		return err
	}
	preview.Layouts, err = normalizeReconciliationLayouts(preview.Layouts)
	return err
}

const (
	// OBS is intentionally single-step. This host serves one careful explorer,
	// so a second move while the previous room is still being identified or
	// saved stops recording instead of maintaining a speculative movement FIFO.
	mapRecordingPendingCap = 1
	// Keep the parser and storage contracts aligned. Byte caps are deliberately
	// explicit as well as rune caps: a raw event must be bounded before any
	// fingerprint, pipe classification, or DB lookup happens.
	mapRecordingMaxHint       = 512
	mapRecordingMaxDesc       = 4096
	mapRecordingMaxExits      = 1024
	mapRecordingMaxHintBytes  = mapRecordingMaxHint * 4
	mapRecordingMaxDescBytes  = mapRecordingMaxDesc * 4
	mapRecordingMaxExitsBytes = mapRecordingMaxExits * 4
	// A movement accepted by OBS is evidence with a finite lifetime.  Without a
	// deadline, an unrelated look hours later could consume the old FIFO head and
	// create a room at a coordinate that no longer describes the player's move.
	mapRecordingMoveTimeout = 12 * time.Second
)

type MapRecordingMode string

const (
	MapRecordingOff       MapRecordingMode = "off"
	MapRecordingArmed     MapRecordingMode = "armed"
	MapRecordingRecording MapRecordingMode = "recording"
	MapRecordingConflict  MapRecordingMode = "conflict"
)

// PendingMapMove is transport evidence, not a predicted topology write. It is
// appended only after a complete successful socket write.
type PendingMapMove struct {
	Sequence      uint64       `json:"sequence"`
	Epoch         int64        `json:"epoch"`
	Command       string       `json:"command"`
	Direction     string       `json:"direction"`
	Source        string       `json:"source"`
	From          mapper.Coord `json:"from"`
	Expected      mapper.Coord `json:"expected"`
	HasExpected   bool         `json:"has_expected"`
	ExpectedExact bool         `json:"expected_exact"`
	acceptedAt    time.Time
	expiresAt     time.Time
}

// mapAutorecordJob is a bounded, immutable handoff from the room-event path to
// the topology writer. It contains no live pointers into the tracker/index, so
// the worker can wait for the per-session lifecycle lock without holding the
// parser's mapMu.
type mapAutorecordJob struct {
	Epoch            int64
	MapSetID         int64
	TopologyRevision int64
	Move             PendingMapMove
	Target           mapper.Coord
	Event            mapper.RoomEvent
	BatchID          string
}

type MapRecordingConflictSnapshot struct {
	ID           uint64           `json:"id"`
	CreatedAt    time.Time        `json:"created_at"`
	Move         PendingMapMove   `json:"move"`
	Expected     mapper.Coord     `json:"expected"`
	ExpectedHint string           `json:"expected_hint"`
	ExpectedRoom mapper.RoomEvent `json:"expected_room"`
	Observed     mapper.RoomEvent `json:"observed"`
	Reason       string           `json:"reason"`
	ReasonCode   string           `json:"reason_code,omitempty"`
	ReasonArgs   map[string]any   `json:"reason_args,omitempty"`
	ReasonDetail string           `json:"reason_detail,omitempty"`
}

type MapRecordingState struct {
	Mode         MapRecordingMode              `json:"mode"`
	Reason       string                        `json:"reason,omitempty"`
	ReasonCode   string                        `json:"reason_code,omitempty"`
	ReasonArgs   map[string]any                `json:"reason_args,omitempty"`
	ReasonDetail string                        `json:"reason_detail,omitempty"`
	Pending      int                           `json:"pending"`
	Conflict     *MapRecordingConflictSnapshot `json:"conflict,omitempty"`
	Revision     uint64                        `json:"revision"`
}

func (s *Session) MapRecordingState() MapRecordingState {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return s.mapRecordingStateLocked()
}

func (s *Session) mapRecordingStateLocked() MapRecordingState {
	mode := s.mapRecordingMode
	if mode == "" {
		mode = MapRecordingOff
	}
	out := MapRecordingState{Mode: mode, Reason: s.mapRecordingReason, Pending: len(s.mapRecordingPending) + s.mapRecordingWrites, Revision: s.mapRecordingRevision}
	if s.mapRecordingConflict != nil {
		cp := *s.mapRecordingConflict
		out.Conflict = &cp
	}
	return out
}

func mapRecordingMsg(state MapRecordingState) ServerMsg {
	return ServerMsg{Type: "map_recording_state", MapRecording: &state}
}

func (s *Session) BroadcastMapRecordingState() {
	s.broadcastMsg(mapRecordingMsg(s.MapRecordingState()))
}

func (s *Session) StartMapRecording() (MapRecordingState, error) {
	var activeSetID int64
	if s.store != nil {
		id, ok, err := s.store.GetActiveMapSetID(s.sessionID)
		// A few mapper-only unit harnesses intentionally provide a minimal
		// settings schema and inject an in-memory tracker. Production databases
		// always carry this column; preserve those pure tracker tests while still
		// enforcing editability whenever the active-set table is present.
		if err != nil && !strings.Contains(err.Error(), "no such column: active_map_set_id") {
			return s.MapRecordingState(), err
		}
		if err != nil {
			id, ok = 0, false
		}
		if ok && id > 0 {
			set, err := s.store.GetMapSet(id)
			if err != nil {
				return s.MapRecordingState(), err
			}
			if !set.Editable {
				return s.MapRecordingState(), fmt.Errorf("room autorecord requires an editable map set")
			}
			activeSetID = id
		}
	}
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapRecordingMode != "" && s.mapRecordingMode != MapRecordingOff {
		return s.mapRecordingStateLocked(), fmt.Errorf("map recording already active")
	}
	if s.mapTracker == nil || s.mapTracker.Index() == nil {
		return s.mapRecordingStateLocked(), fmt.Errorf("no active map set")
	}
	if activeSetID > 0 && s.mapTracker.Index().MapSetID != activeSetID {
		return s.mapRecordingStateLocked(), fmt.Errorf("active map set changed")
	}
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Confidence != mapper.Green || !pos.EventDescribesCell {
		return s.mapRecordingStateLocked(), fmt.Errorf("green confirmed anchor required")
	}
	if !s.lastRoomOK || !mapRecordingRoomComplete(s.lastRoom) {
		return s.mapRecordingStateLocked(), fmt.Errorf("complete live room required")
	}
	s.mapRecordingMode = MapRecordingArmed
	s.mapRecordingReason = ""
	s.mapRecordingConflict = nil
	s.disarmMapRecordingTimerLocked()
	s.mapRecordingPending = s.mapRecordingPending[:0]
	s.mapRecordingEpoch++
	s.mapRecordingRevision++
	go s.scheduleMapScan()
	return s.mapRecordingStateLocked(), nil
}

func (s *Session) StopMapRecording(reason string) MapRecordingState {
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	s.stopMapRecordingLocked(reason)
	return s.mapRecordingStateLocked()
}

// StopMapRecordingAndWait is the shutdown/user-stop gate for callers that need
// a hard guarantee that no detached autorecord can commit after the return. It
// waits on mapReconcileMu and the short commit reservation OUTSIDE mapMu;
// parser state is never held behind SQLite.
func (s *Session) StopMapRecordingAndWait(reason string) MapRecordingState {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	s.stopMapRecordingLocked(reason)
	return s.mapRecordingStateLocked()
}

// TryStopMapRecording is the user-facing stop gate. The autorecord write may
// hold the lifecycle reservation while it validates/commits, so a user stop
// waits for that short off-mapMu section. Reconciliation apply is different: it
// deliberately keeps its conflict reservation through post-commit publication,
// and the existing REST contract reports a conflict instead of blocking on it.
func (s *Session) TryStopMapRecording(reason string) (MapRecordingState, bool) {
	if !s.mapReconcileMu.TryLock() {
		// Do not wait behind the one operation whose state must remain a conflict
		// until its post-commit publication has completed. A worker/profile/map
		// switch does not set this flag and is safe to await below.
		s.mapMu.Lock()
		applying := s.mapReconcileApplying
		state := s.mapRecordingStateLocked()
		s.mapMu.Unlock()
		if applying {
			return state, false
		}
		s.mapReconcileMu.Lock()
	}
	defer s.mapReconcileMu.Unlock()
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	s.mapMu.Lock()
	if s.mapReconcileApplying {
		state := s.mapRecordingStateLocked()
		s.mapMu.Unlock()
		return state, false
	}
	defer s.mapMu.Unlock()
	s.stopMapRecordingLocked(reason)
	return s.mapRecordingStateLocked(), true
}

func (s *Session) stopMapRecordingLocked(reason string) {
	if s.mapReconcileApplying {
		return
	}
	s.disarmMapRecordingTimerLocked()
	changed := s.mapRecordingMode != MapRecordingOff || len(s.mapRecordingPending) > 0 || s.mapRecordingWrites > 0 || s.mapRecordingConflict != nil || s.mapRecordingReason != reason
	s.mapRecordingMode = MapRecordingOff
	s.mapRecordingReason = reason
	s.mapRecordingConflict = nil
	s.mapRecordingPending = s.mapRecordingPending[:0]
	s.mapRecordingWrites = 0
	s.mapReconcilePreview = nil
	s.cancelMapScanCaptureLocked()
	s.mapScanSnapshot = nil
	if changed {
		// Invalidate the write already handed to its goroutine. It may finish a
		// transaction that won the race before this stop, but it must never publish
		// stale work after a user stop/map switch.
		s.mapRecordingEpoch++
		s.mapRecordingRevision++
	}
}

func (s *Session) mapRecordingNow() time.Time {
	if s.mapRecordingClock != nil {
		return s.mapRecordingClock()
	}
	return time.Now()
}

// disarmMapRecordingTimerLocked is idempotent and must be called with mapMu.
func (s *Session) disarmMapRecordingTimerLocked() {
	if s.mapRecordingTimer != nil {
		s.mapRecordingTimer.Stop()
		s.mapRecordingTimer = nil
	}
}

// armMapRecordingTimerLocked keeps exactly one timer for the oldest accepted
// move.  The timer is not part of the parser critical path beyond a cheap
// AfterFunc registration; its callback only takes mapMu briefly and broadcasts
// after releasing it.
func (s *Session) armMapRecordingTimerLocked() {
	s.disarmMapRecordingTimerLocked()
	if s.mapRecordingTimerDisabled || len(s.mapRecordingPending) == 0 {
		return
	}
	expiresAt := s.mapRecordingPending[0].expiresAt
	if expiresAt.IsZero() {
		return
	}
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	epoch := s.mapRecordingEpoch
	sequence := s.mapRecordingPending[0].Sequence
	s.mapRecordingTimer = time.AfterFunc(delay, func() {
		s.expireMapRecordingMove(epoch, sequence)
	})
}

func (s *Session) expireMapRecordingMove(epoch int64, sequence uint64) {
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	s.mapMu.Lock()
	if len(s.mapRecordingPending) == 0 || s.mapRecordingEpoch != epoch || s.mapRecordingPending[0].Sequence != sequence {
		s.mapMu.Unlock()
		return
	}
	if s.mapRecordingNow().Before(s.mapRecordingPending[0].expiresAt) {
		s.armMapRecordingTimerLocked()
		s.mapMu.Unlock()
		return
	}
	if s.mapTracker != nil {
		s.mapTracker.Invalidate("movement confirmation timed out")
	}
	before := s.mapRecordingRevision
	s.stopMapRecordingLocked("movement confirmation timed out")
	changed := s.mapRecordingRevision != before
	position := s.roomPositionMsgLocked()
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if position.Type != "" {
		s.broadcastMsg(position)
	}
	if changed {
		s.broadcastMsg(mapRecordingMsg(state))
	}
}

func (s *Session) PreviewMapReconciliation(choice MapReconciliationChoice) (MapReconciliationPreview, error) {
	if len(choice.Layouts) > mapReconcileLayoutCap {
		return MapReconciliationPreview{}, fmt.Errorf("too many layout changes")
	}
	s.mapMu.Lock()
	if s.mapRecordingMode != MapRecordingConflict || s.mapRecordingConflict == nil || s.mapRecordingConflict.ID != choice.ConflictID {
		s.mapMu.Unlock()
		return MapReconciliationPreview{}, fmt.Errorf("map recording conflict is no longer current")
	}
	c := *s.mapRecordingConflict
	epoch := s.mapRecordingEpoch
	s.mapMu.Unlock()
	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	if !ok {
		return MapReconciliationPreview{}, ErrNoActiveMapSet
	}
	set, err := s.store.GetMapSet(setID)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	if !set.Editable {
		return MapReconciliationPreview{}, fmt.Errorf("reconciliation of frozen map sets is not supported; create an editable copy first")
	}
	source, err := normalizeReconciliationCoord(c.Move.From)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	expected, err := normalizeReconciliationCoord(c.Expected)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	target, err := normalizeReconciliationCoord(choice.Target)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	normalizedLayouts, err := normalizeReconciliationLayouts(choice.Layouts)
	if err != nil {
		return MapReconciliationPreview{}, err
	}
	layouts := make([]storage.LayoutMutation, 0, len(normalizedLayouts))
	for _, l := range normalizedLayouts {
		v := storage.RoomLayout{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L, DisplayX: l.DisplayX, DisplayY: l.DisplayY, DisplayL: l.DisplayL, Pinned: l.Pinned}
		layouts = append(layouts, storage.LayoutMutation{Cell: storage.EdgeCell{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L}, Layout: &v})
	}
	if err := s.store.ValidateReconciliationPreview(setID, storage.EdgeCell{Zone: target.Zone, X: target.X, Y: target.Y, L: target.L}, layouts); err != nil {
		return MapReconciliationPreview{}, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return MapReconciliationPreview{}, err
	}
	p := MapReconciliationPreview{Token: base64.RawURLEncoding.EncodeToString(b), ConflictID: c.ID, MapSetID: setID, ExpectedRevision: set.TopologyRevision, Source: source, Expected: expected, Target: target, Direction: c.Move.Direction, Command: c.Move.Command, Layouts: normalizedLayouts, Epoch: epoch}
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapRecordingMode != MapRecordingConflict || s.mapRecordingConflict == nil || s.mapRecordingConflict.ID != c.ID || s.mapRecordingEpoch != epoch {
		return MapReconciliationPreview{}, fmt.Errorf("map recording conflict changed during preview")
	}
	stored := p
	stored.Layouts = append([]MapReconciliationLayout(nil), p.Layouts...)
	s.mapReconcilePreview = &stored
	out := stored
	out.Layouts = append([]MapReconciliationLayout(nil), stored.Layouts...)
	return out, nil
}

func (s *Session) ApplyMapReconciliation(conflictID uint64, token string) (MapRecordingState, int64, error) {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	return s.applyMapReconciliationLocked(conflictID, token)
}

func (s *Session) applyMapReconciliationLocked(conflictID uint64, token string) (MapRecordingState, int64, error) {
	s.mapMu.Lock()
	p := s.mapReconcilePreview
	if p == nil || p.Token != token || p.ConflictID != conflictID || s.mapRecordingMode != MapRecordingConflict || s.mapRecordingConflict == nil || s.mapRecordingConflict.ID != conflictID || s.mapRecordingEpoch != p.Epoch {
		s.mapMu.Unlock()
		return MapRecordingState{}, 0, fmt.Errorf("reconciliation preview is stale or already used")
	}
	cp := *p
	cp.Layouts = append([]MapReconciliationLayout(nil), p.Layouts...)
	if err := normalizeReconciliationPreviewZones(&cp); err != nil {
		s.mapMu.Unlock()
		return MapRecordingState{}, 0, err
	}
	s.mapReconcilePreview = nil
	s.mapReconcileApplying = true
	s.mapMu.Unlock()
	committed := false
	defer func() {
		if !committed {
			s.mapMu.Lock()
			s.mapReconcileApplying = false
			s.mapMu.Unlock()
		}
	}()
	active, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil || !ok || active != cp.MapSetID {
		if err == nil {
			err = fmt.Errorf("active map set changed")
		}
		return s.MapRecordingState(), 0, err
	}
	lock := s.store.TopologyLock(cp.MapSetID)
	lock.Lock()
	defer lock.Unlock()
	layouts := make([]storage.LayoutMutation, 0, len(cp.Layouts))
	for _, l := range cp.Layouts {
		v := storage.RoomLayout{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L, DisplayX: l.DisplayX, DisplayY: l.DisplayY, DisplayL: l.DisplayL, Pinned: l.Pinned}
		layouts = append(layouts, storage.LayoutMutation{Cell: storage.EdgeCell{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L}, Layout: &v})
	}
	before, err := s.store.ApplyReconciliation(cp.MapSetID, storage.ReconciliationInput{Source: storage.EdgeCell{Zone: cp.Source.Zone, X: cp.Source.X, Y: cp.Source.Y, L: cp.Source.L}, Dir: cp.Direction, Target: storage.EdgeCell{Zone: cp.Target.Zone, X: cp.Target.X, Y: cp.Target.Y, L: cp.Target.L}, Command: cp.Command, SourceProvenance: storage.EdgeSourceReconciled, Layouts: layouts, ExpectedRevision: cp.ExpectedRevision})
	if err != nil {
		return s.MapRecordingState(), 0, err
	}
	if s.mapReconcileAfterCommit != nil {
		s.mapReconcileAfterCommit()
	}
	s.store.WriteJournal().Push(cp.MapSetID, storage.UndoEntry{Label: "reconcile map conflict", Before: before, ExpectedRevision: before.Revision + 1})
	idx := s.buildActiveIndex()
	s.mapMu.Lock()
	if s.mapRecordingMode != MapRecordingConflict || s.mapRecordingConflict == nil || s.mapRecordingConflict.ID != conflictID || s.mapRecordingEpoch != cp.Epoch {
		s.mapMu.Unlock()
		return s.MapRecordingState(), cp.MapSetID, fmt.Errorf("conflict changed while reconciliation committed")
	}
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndexPreservingPosition(idx)
	}
	s.mapTracker.Anchor(cp.Target)
	s.syncDoorOpenLocked()
	s.mapRecordingMode = MapRecordingRecording
	s.mapRecordingReason = ""
	s.mapRecordingConflict = nil
	s.mapReconcileApplying = false
	s.mapRecordingRevision++
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	committed = true
	s.BroadcastMapPosition()
	s.broadcastRoomsChanged(cp.MapSetID)
	s.broadcastMsg(mapRecordingMsg(state))
	return state, cp.MapSetID, nil
}

func (s *Session) recordAcceptedMapMoveLocked(command, dir, source string, authoritativeExpected *mapper.Coord) {
	s.cancelMapScanCaptureLocked()
	s.mapScanSnapshot = nil
	if s.mapTracker == nil || (s.mapRecordingMode != MapRecordingArmed && s.mapRecordingMode != MapRecordingRecording) {
		return
	}
	// Dedupe scan per VISIT, not per coordinate for the whole OBS run. A loop may
	// return to a frontier after visiting only fully mapped rooms; in that case
	// mapScanLast would otherwise still name this cell and suppress the new scan.
	s.mapScanLastOK = false
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Confidence == mapper.Red {
		s.stopMapRecordingLocked("position lost")
		return
	}
	if len(s.mapRecordingPending) >= mapRecordingPendingCap || s.mapRecordingWrites > 0 {
		s.stopMapRecordingLocked("previous autorecord step is still pending; wait for the new cell to turn green")
		return
	}
	from := pos.Coord
	s.mapRecordingSequence++
	if s.mapRecordingMode == MapRecordingArmed {
		s.mapRecordingMode = MapRecordingRecording
	}
	expected := predictedMapMoveLanding(s.mapTracker, PendingMapMove{From: from, Direction: dir})
	if authoritativeExpected != nil {
		expected = *authoritativeExpected
	}
	acceptedAt := s.mapRecordingNow()
	s.mapRecordingPending = append(s.mapRecordingPending, PendingMapMove{Sequence: s.mapRecordingSequence, Epoch: s.mapRecordingEpoch, Command: command, Direction: dir, Source: source, From: from, Expected: expected, HasExpected: true, ExpectedExact: authoritativeExpected != nil, acceptedAt: acceptedAt, expiresAt: acceptedAt.Add(mapRecordingMoveTimeout)})
	s.armMapRecordingTimerLocked()
	s.mapRecordingRevision++
}

func predictedMapMoveLanding(tr *mapper.Tracker, move PendingMapMove) mapper.Coord {
	if tr != nil && tr.Index() != nil {
		if room := tr.Index().Room(move.From); room != nil {
			if edge := room.GridEdge(move.Direction); edge != nil && edge.To != nil {
				return *edge.To
			}
		}
	}
	out := move.From
	switch move.Direction {
	case "N":
		out.X--
	case "S":
		out.X++
	case "E":
		out.Y++
	case "W":
		out.Y--
	case "U":
		out.L++
	case "D":
		out.L--
	}
	return out
}

func (s *Session) cancelAcceptedMapMoveLocked() {
	if len(s.mapRecordingPending) > 0 {
		s.mapRecordingPending = s.mapRecordingPending[1:]
		s.armMapRecordingTimerLocked()
		s.mapRecordingRevision++
	}
}

// observeMapRecordingRoomLocked consumes one accepted movement and freezes the
// first unsafe occupied-target mismatch. The legacy test/helper entry point also
// hands an eligible empty-target event to its detached writer; production
// parsing uses the split helper below so the goroutine starts after mapMu is
// released.
func (s *Session) observeMapRecordingRoomLocked(ev mapper.RoomEvent) bool {
	conflict, job := s.observeMapRecordingRoomLockedForAutorecord(ev)
	if job != nil {
		if s.store == nil {
			s.failMapAutorecordLocked(job, "autorecord storage unavailable")
		} else {
			s.startMapAutorecord(*job)
		}
	}
	return conflict
}

// observeMapRecordingRoomLockedForAutorecord is the parser-side decision arm.
// It only consumes in-memory evidence and reserves a bounded async write; it
// never touches SQLite or the active map-set lock.
func (s *Session) observeMapRecordingRoomLockedForAutorecord(ev mapper.RoomEvent) (bool, *mapAutorecordJob) {
	if len(s.mapRecordingPending) == 0 || s.mapRecordingMode == MapRecordingConflict {
		return false, nil
	}
	// Validate the raw parser payload while mapMu is held and before the
	// predicted-target lookup, fingerprint, or pipe branch. In particular, a
	// huge hintless event must stop safely rather than become a conflict snapshot
	// or a storage job.
	if !mapRecordingRoomWithinBounds(ev) {
		s.stopMapRecordingLocked("room event too large")
		return false, nil
	}
	if ev.Obscured {
		// The physical step is still reconciled by the tracker below, but there is
		// no trustworthy room identity to compare or persist. Stop this deliberately
		// single-step recorder and let the player return with visibility.
		s.stopMapRecordingLocked("room obscured; restore visibility and restart OBS")
		return false, nil
	}
	complete := mapRecordingRoomComplete(ev)
	pipe := strings.TrimSpace(ev.Hint) == "" && strings.TrimSpace(ev.Exits) != ""
	if !complete && !pipe {
		s.stopMapRecordingLocked("incomplete room event")
		return false, nil
	}
	move := s.mapRecordingPending[0]
	s.mapRecordingPending = s.mapRecordingPending[1:]
	s.armMapRecordingTimerLocked()
	s.mapRecordingRevision++
	if s.mapRecordingMode != MapRecordingRecording || s.mapTracker == nil || s.mapTracker.Index() == nil {
		return false, nil
	}
	if !move.HasExpected {
		return false, nil
	}
	if !move.ExpectedExact && ev.Hint != "" {
		if reported, ok := s.mapTracker.PredictReportedLanding(move.From, move.Direction); ok {
			move.Expected = reported
		}
	}
	if pipe {
		// A hint-less event with exits is corridor/pipe output. It has already
		// passed the raw bounds fence and must be consumed without resolving a
		// known target or comparing a fingerprint.
		return false, nil
	}
	expected := s.mapTracker.Index().Room(move.Expected)
	if expected != nil {
		observedFingerprint := mapimport.Fingerprint(ev.Hint, ev.Desc, ev.Exits)
		if expected.Fingerprint == observedFingerprint {
			return false, nil
		}
		// Any reserved write for the previous recording step is now stale. Drop its
		// reservation and advance the epoch so a goroutine that has not started
		// its transaction cannot publish a room after reconciliation takes over.
		if s.mapRecordingWrites > 0 {
			s.mapRecordingWrites = 0
			s.mapRecordingEpoch++
		}
		s.disarmMapRecordingTimerLocked()
		s.mapRecordingSequence++
		s.mapRecordingMode = MapRecordingConflict
		s.mapRecordingReason = "occupied expected room content mismatch"
		s.mapRecordingConflict = &MapRecordingConflictSnapshot{ID: s.mapRecordingSequence, CreatedAt: time.Now().UTC(), Move: move, Expected: expected.Coord, ExpectedHint: boundedMapText(expected.Hint, 512), ExpectedRoom: mapper.RoomEvent{Hint: boundedMapText(expected.Hint, 512), Desc: boundedMapText(expected.Desc, 4096), Exits: boundedMapText(expected.Exits, 1024)}, Observed: mapper.RoomEvent{Hint: boundedMapText(ev.Hint, 512), Desc: boundedMapText(ev.Desc, 4096), Exits: boundedMapText(ev.Exits, 1024)}, Reason: s.mapRecordingReason}
		s.mapRecordingPending = s.mapRecordingPending[:0]
		s.mapRecordingRevision++
		return true, nil
	}

	// An unknown target is safe to create only from a complete room block. A
	// hint-less pipe is consumed as tracker evidence but is never a topology
	// write; a truly empty partial block stops recording rather than guessing a
	// teleport.
	event := mapper.RoomEvent{Hint: boundedMapText(ev.Hint, mapRecordingMaxHint), Desc: boundedMapText(ev.Desc, mapRecordingMaxDesc), Exits: boundedMapText(ev.Exits, mapRecordingMaxExits)}
	job := &mapAutorecordJob{
		Epoch:            s.mapRecordingEpoch,
		MapSetID:         s.mapTracker.Index().MapSetID,
		TopologyRevision: s.mapTracker.Index().TopologyRevision,
		Move:             move,
		Target:           move.Expected,
		Event:            event,
		BatchID:          fmt.Sprintf("session-%d-epoch-%d-move-%d", s.sessionID, s.mapRecordingEpoch, move.Sequence),
	}
	s.mapRecordingWrites++
	s.mapRecordingRevision++
	return false, job
}

// mapRecordingRoomWithinBounds validates raw input without allocating a rune
// slice. Both byte and rune limits matter: byte length bounds malformed or
// multi-byte input, while rune length matches the storage/UI contract.
func mapRecordingRoomWithinBounds(ev mapper.RoomEvent) bool {
	return mapRecordingTextWithinBounds(ev.Hint, mapRecordingMaxHint, mapRecordingMaxHintBytes) &&
		mapRecordingTextWithinBounds(ev.Desc, mapRecordingMaxDesc, mapRecordingMaxDescBytes) &&
		mapRecordingTextWithinBounds(ev.Exits, mapRecordingMaxExits, mapRecordingMaxExitsBytes)
}

func mapRecordingRoomComplete(ev mapper.RoomEvent) bool {
	return mapRecordingRoomWithinBounds(ev) && strings.TrimSpace(ev.Hint) != "" && strings.TrimSpace(ev.Desc) != "" && strings.TrimSpace(ev.Exits) != ""
}

func mapRecordingTextWithinBounds(v string, maxRunes, maxBytes int) bool {
	return len(v) <= maxBytes && utf8.RuneCountInString(v) <= maxRunes
}

// startMapAutorecord detaches the only outstanding OBS write from the parser.
// The single-step reservation prevents a second goroutine from being launched
// until this one finishes or recording is stopped.
func (s *Session) startMapAutorecord(job mapAutorecordJob) {
	go func() {
		defer s.recoverGoroutine("autorecord writer")
		s.applyMapAutorecord(job)
	}()
}

func (s *Session) mapAutorecordJobCurrent(job mapAutorecordJob) bool {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return s.mapRecordingMode == MapRecordingRecording && s.mapRecordingEpoch == job.Epoch && s.mapRecordingWrites > 0 && s.mapTracker != nil && s.mapTracker.Index() != nil && s.mapTracker.Index().MapSetID == job.MapSetID
}

func (s *Session) applyMapAutorecord(job mapAutorecordJob) {
	// Establish the current-job fence first, then expose the deterministic
	// interleaving point. Stop/Close/profile/map-set changes invoked from the hook
	// can therefore win before the lifecycle reservation and before SQLite.
	if !s.mapAutorecordJobCurrent(job) {
		return
	}
	if s.mapAutorecordBeforeCommit != nil {
		s.mapAutorecordBeforeCommit()
	}
	// This is the same lifecycle reservation used by reconciliation and active
	// map switching. It keeps the target map set and recording epoch stable while
	// the storage transaction is in flight, without ever entering the parser
	// path's mapMu/SQLite critical section.
	s.mapReconcileMu.Lock()
	s.mapAutorecordCommitMu.Lock()
	commitHeld := true
	reconcileHeld := true
	defer func() {
		if commitHeld {
			s.mapAutorecordCommitMu.Unlock()
		}
		if reconcileHeld {
			s.mapReconcileMu.Unlock()
		}
	}()
	if !s.mapAutorecordJobCurrent(job) {
		return
	}
	hint, desc, exits := job.Event.Hint, job.Event.Desc, job.Event.Exits
	op := storage.TopologyOp{Kind: storage.TopoAutorecordRoom, Autorecord: &storage.AutorecordInput{
		Source: storage.EdgeCell{Zone: job.Move.From.Zone, X: job.Move.From.X, Y: job.Move.From.Y, L: job.Move.From.L},
		Target: storage.EdgeCell{Zone: job.Target.Zone, X: job.Target.X, Y: job.Target.Y, L: job.Target.L},
		Dir:    job.Move.Direction, Command: boundedMapText(job.Move.Command, 512),
		Fields:        storage.RoomFields{Hint: &hint, Desc: &desc, Exits: &exits},
		RecordBatchID: job.BatchID, RecordOrigin: fmt.Sprintf("session:%d", s.sessionID),
		VerificationState: "provisional", RecordSource: storage.EdgeSourceAutorecord,
	}}
	// Validate the active set and captured revision while holding TopologyLock,
	// immediately before ApplyTopologyOp. An admin edit after the room event must
	// make this prediction stale rather than allowing a room/edge write.
	res, err := s.applyAutorecordTopologyLocked(job, op)
	if err != nil {
		s.mapAutorecordCommitMu.Unlock()
		commitHeld = false
		s.mapReconcileMu.Unlock()
		reconcileHeld = false
		s.failMapAutorecord(job, err.Error())
		return
	}
	if !res.Applied {
		s.mapAutorecordCommitMu.Unlock()
		commitHeld = false
		s.mapReconcileMu.Unlock()
		reconcileHeld = false
		s.failMapAutorecord(job, "autorecord write was not applied")
		return
	}
	if s.mapAutorecordAfterCommit != nil {
		s.mapAutorecordAfterCommit()
	}
	s.completeMapAutorecord(job, res.SetID)
	s.mapAutorecordCommitMu.Unlock()
	commitHeld = false
	s.mapReconcileMu.Unlock()
	reconcileHeld = false
}

func (s *Session) applyAutorecordTopologyLocked(job mapAutorecordJob, op storage.TopologyOp) (TopologyWriteResult, error) {
	lock := s.store.TopologyLock(job.MapSetID)
	lock.Lock()
	defer lock.Unlock()
	active, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return TopologyWriteResult{}, err
	}
	if !ok || active != job.MapSetID {
		return TopologyWriteResult{}, fmt.Errorf("active map set changed")
	}
	set, err := s.store.GetMapSet(active)
	if err != nil {
		return TopologyWriteResult{}, err
	}
	if !set.Editable {
		return TopologyWriteResult{}, fmt.Errorf("room autorecord requires an editable map set")
	}
	if set.TopologyRevision != job.TopologyRevision {
		return TopologyWriteResult{}, fmt.Errorf("map topology changed while autorecord was pending")
	}
	// Stop/map/profile paths that do not wait on the lifecycle mutex still bump
	// the recording epoch under mapMu. Recheck after taking the topology lock so a
	// deterministic internal stop cannot leave a write reservation alive.
	if !s.mapAutorecordJobCurrent(job) {
		return TopologyWriteResult{}, fmt.Errorf("autorecord lifecycle changed")
	}
	if s.mapAutorecordAfterCurrentCheck != nil {
		s.mapAutorecordAfterCurrentCheck()
	}
	return s.applyTopologyToSet(job.MapSetID, op, TopologyWriteResult{SetID: job.MapSetID}, true)
}

func (s *Session) failMapAutorecordLocked(job *mapAutorecordJob, reason string) {
	if job == nil || s.mapRecordingEpoch != job.Epoch || s.mapRecordingWrites == 0 {
		return
	}
	s.mapRecordingWrites--
	s.stopMapRecordingLocked(reason)
}

func (s *Session) failMapAutorecord(job mapAutorecordJob, reason string) {
	s.mapMu.Lock()
	before := s.mapRecordingRevision
	s.failMapAutorecordLocked(&job, reason)
	state := s.mapRecordingStateLocked()
	changed := s.mapRecordingRevision != before
	s.mapMu.Unlock()
	if changed {
		s.broadcastMsg(mapRecordingMsg(state))
	}
}

func (s *Session) completeMapAutorecord(job mapAutorecordJob, setID int64) {
	s.mapMu.Lock()
	current := s.mapRecordingEpoch == job.Epoch && s.mapRecordingWrites > 0
	if current {
		s.mapRecordingWrites--
		if s.mapTracker != nil {
			s.mapTracker.Anchor(job.Target)
			s.syncDoorOpenLocked()
		}
		s.mapRecordingRevision++
	}
	// A parser/internal stop is intentionally allowed to avoid waiting on SQLite.
	// Once the final current-job check has passed, however, the transaction owns
	// the commit boundary. Even when that stop advances the epoch, publish the
	// already-committed graph and current (possibly red/off/conflict) position so
	// the browser and writer index cannot remain behind the database.
	position := s.roomPositionMsgLocked()
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if position.Type != "" {
		s.broadcastMsg(position)
	}
	s.broadcastRoomsChanged(setID)
	s.broadcastMsg(mapRecordingMsg(state))
	s.scheduleMapScan()
}

func boundedMapText(v string, max int) string {
	r := []rune(v)
	if len(r) > max {
		return string(r[:max])
	}
	return v
}
