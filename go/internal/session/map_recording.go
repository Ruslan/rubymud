package session

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

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

const mapRecordingPendingCap = 64

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
}

type MapRecordingConflictSnapshot struct {
	ID           uint64           `json:"id"`
	CreatedAt    time.Time        `json:"created_at"`
	Move         PendingMapMove   `json:"move"`
	Expected     mapper.Coord     `json:"expected"`
	ExpectedHint string           `json:"expected_hint"`
	ExpectedRoom mapper.RoomEvent `json:"expected_room"`
	Observed     mapper.RoomEvent `json:"observed"`
	PendingTail  []PendingMapMove `json:"pending_tail,omitempty"`
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
	out := MapRecordingState{Mode: mode, Reason: s.mapRecordingReason, Pending: len(s.mapRecordingPending), Revision: s.mapRecordingRevision}
	if s.mapRecordingConflict != nil {
		cp := *s.mapRecordingConflict
		cp.PendingTail = append([]PendingMapMove(nil), s.mapRecordingConflict.PendingTail...)
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
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapRecordingMode != "" && s.mapRecordingMode != MapRecordingOff {
		return s.mapRecordingStateLocked(), fmt.Errorf("map recording already active")
	}
	if s.mapTracker == nil || s.mapTracker.Index() == nil {
		return s.mapRecordingStateLocked(), fmt.Errorf("no active map set")
	}
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Confidence != mapper.Green || !pos.EventDescribesCell {
		return s.mapRecordingStateLocked(), fmt.Errorf("green confirmed anchor required")
	}
	if !s.lastRoomOK || s.lastRoom.Hint == "" {
		return s.mapRecordingStateLocked(), fmt.Errorf("complete live room required")
	}
	s.mapRecordingMode = MapRecordingArmed
	s.mapRecordingReason = ""
	s.mapRecordingConflict = nil
	s.mapRecordingPending = s.mapRecordingPending[:0]
	s.mapRecordingEpoch++
	s.mapRecordingRevision++
	return s.mapRecordingStateLocked(), nil
}

func (s *Session) StopMapRecording(reason string) MapRecordingState {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	s.stopMapRecordingLocked(reason)
	return s.mapRecordingStateLocked()
}

// TryStopMapRecording is the user-facing stop gate. A reconciliation holding
// the lifecycle reservation cannot be reported as stopped before its committed
// DB mutation has been published into session state.
func (s *Session) TryStopMapRecording(reason string) (MapRecordingState, bool) {
	if !s.mapReconcileMu.TryLock() {
		return s.MapRecordingState(), false
	}
	defer s.mapReconcileMu.Unlock()
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	s.stopMapRecordingLocked(reason)
	return s.mapRecordingStateLocked(), true
}

func (s *Session) stopMapRecordingLocked(reason string) {
	if s.mapReconcileApplying {
		return
	}
	changed := s.mapRecordingMode != MapRecordingOff || len(s.mapRecordingPending) > 0 || s.mapRecordingConflict != nil || s.mapRecordingReason != reason
	s.mapRecordingMode = MapRecordingOff
	s.mapRecordingReason = reason
	s.mapRecordingConflict = nil
	s.mapRecordingPending = s.mapRecordingPending[:0]
	s.mapReconcilePreview = nil
	if changed {
		s.mapRecordingRevision++
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
	layouts := make([]storage.LayoutMutation, 0, len(choice.Layouts))
	for _, l := range choice.Layouts {
		v := storage.RoomLayout{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L, DisplayX: l.DisplayX, DisplayY: l.DisplayY, DisplayL: l.DisplayL, Pinned: l.Pinned}
		layouts = append(layouts, storage.LayoutMutation{Cell: storage.EdgeCell{Zone: l.Zone, X: l.X, Y: l.Y, L: l.L}, Layout: &v})
	}
	if err := s.store.ValidateReconciliationPreview(setID, storage.EdgeCell{Zone: choice.Target.Zone, X: choice.Target.X, Y: choice.Target.Y, L: choice.Target.L}, layouts); err != nil {
		return MapReconciliationPreview{}, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return MapReconciliationPreview{}, err
	}
	p := MapReconciliationPreview{Token: base64.RawURLEncoding.EncodeToString(b), ConflictID: c.ID, MapSetID: setID, ExpectedRevision: set.TopologyRevision, Source: c.Move.From, Expected: c.Expected, Target: choice.Target, Direction: c.Move.Direction, Command: c.Move.Command, Layouts: append([]MapReconciliationLayout(nil), choice.Layouts...), Epoch: epoch}
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
	s.mapMu.Lock()
	p := s.mapReconcilePreview
	if p == nil || p.Token != token || p.ConflictID != conflictID || s.mapRecordingMode != MapRecordingConflict || s.mapRecordingConflict == nil || s.mapRecordingConflict.ID != conflictID || s.mapRecordingEpoch != p.Epoch {
		s.mapMu.Unlock()
		return MapRecordingState{}, 0, fmt.Errorf("reconciliation preview is stale or already used")
	}
	cp := *p
	cp.Layouts = append([]MapReconciliationLayout(nil), p.Layouts...)
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
	if s.mapTracker == nil || (s.mapRecordingMode != MapRecordingArmed && s.mapRecordingMode != MapRecordingRecording) {
		return
	}
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Confidence == mapper.Red {
		s.stopMapRecordingLocked("position lost")
		return
	}
	if len(s.mapRecordingPending) >= mapRecordingPendingCap {
		s.stopMapRecordingLocked("pending movement limit exceeded")
		return
	}
	from := pos.Coord
	if n := len(s.mapRecordingPending); n > 0 {
		from = predictedMapMoveLanding(s.mapTracker, s.mapRecordingPending[n-1])
	}
	s.mapRecordingSequence++
	if s.mapRecordingMode == MapRecordingArmed {
		s.mapRecordingMode = MapRecordingRecording
	}
	expected := predictedMapMoveLanding(s.mapTracker, PendingMapMove{From: from, Direction: dir})
	if authoritativeExpected != nil {
		expected = *authoritativeExpected
	}
	s.mapRecordingPending = append(s.mapRecordingPending, PendingMapMove{Sequence: s.mapRecordingSequence, Epoch: s.mapRecordingEpoch, Command: command, Direction: dir, Source: source, From: from, Expected: expected, HasExpected: true, ExpectedExact: authoritativeExpected != nil})
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
		s.rebasePendingMapMovesLocked()
		s.mapRecordingRevision++
	}
}

func (s *Session) rebasePendingMapMovesLocked() {
	if s.mapTracker == nil || !s.mapTracker.Position().Valid {
		return
	}
	from := s.mapTracker.Position().Coord
	for i := range s.mapRecordingPending {
		s.mapRecordingPending[i].From = from
		if !s.mapRecordingPending[i].ExpectedExact {
			from = predictedMapMoveLanding(s.mapTracker, s.mapRecordingPending[i])
			s.mapRecordingPending[i].Expected = from
			s.mapRecordingPending[i].HasExpected = true
		} else {
			from = s.mapRecordingPending[i].Expected
		}
	}
}

// observeMapRecordingRoomLocked consumes one accepted movement and freezes the
// first unsafe occupied-target mismatch. It performs no storage access or write.
func (s *Session) observeMapRecordingRoomLocked(ev mapper.RoomEvent) bool {
	if len(s.mapRecordingPending) == 0 || s.mapRecordingMode == MapRecordingConflict {
		return false
	}
	move := s.mapRecordingPending[0]
	s.mapRecordingPending = s.mapRecordingPending[1:]
	s.mapRecordingRevision++
	if s.mapRecordingMode != MapRecordingRecording || s.mapTracker == nil || s.mapTracker.Index() == nil {
		return false
	}
	if !move.HasExpected {
		return false
	}
	if !move.ExpectedExact && ev.Hint != "" {
		if reported, ok := s.mapTracker.PredictReportedLanding(move.From, move.Direction); ok {
			move.Expected = reported
		}
	}
	expected := s.mapTracker.Index().Room(move.Expected)
	observedFingerprint := mapimport.Fingerprint(ev.Hint, ev.Desc, ev.Exits)
	if expected == nil || expected.Fingerprint == observedFingerprint {
		return false
	}
	s.mapRecordingSequence++
	s.mapRecordingMode = MapRecordingConflict
	s.mapRecordingReason = "occupied expected room content mismatch"
	tail := append([]PendingMapMove(nil), s.mapRecordingPending...)
	s.mapRecordingConflict = &MapRecordingConflictSnapshot{ID: s.mapRecordingSequence, CreatedAt: time.Now().UTC(), Move: move, Expected: expected.Coord, ExpectedHint: boundedMapText(expected.Hint, 512), ExpectedRoom: mapper.RoomEvent{Hint: boundedMapText(expected.Hint, 512), Desc: boundedMapText(expected.Desc, 4096), Exits: boundedMapText(expected.Exits, 1024)}, Observed: mapper.RoomEvent{Hint: boundedMapText(ev.Hint, 512), Desc: boundedMapText(ev.Desc, 4096), Exits: boundedMapText(ev.Exits, 1024)}, PendingTail: tail, Reason: s.mapRecordingReason}
	s.mapRecordingPending = s.mapRecordingPending[:0]
	s.mapRecordingRevision++
	return true
}

func boundedMapText(v string, max int) string {
	r := []rune(v)
	if len(r) > max {
		return string(r[:max])
	}
	return v
}
