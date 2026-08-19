package session

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

const mapWalkHopTimeout = 12 * time.Second

func (s *Session) walkEpoch() int64 {
	if s.mapWalkEpoch != 0 {
		return s.mapWalkEpoch
	}
	// Unit harnesses construct Session values directly; retain a stable epoch
	// there without weakening production's per-live-transport generation.
	return s.sessionID
}

type mapWalkJob struct {
	id             uint64
	request        uint64
	steps          []mapper.PathStep
	current        int
	planning       bool
	dispatching    bool
	inFlight       bool
	hop            uint64
	expected       mapper.Coord
	timer          *time.Timer
	stopping       bool
	stopReason     string
	paused         bool
	resuming       bool
	pauseReason    string
	target         mapper.Coord
	revision       uint64
	desired        *mapper.Coord
	desiredRequest uint64
}

// MapWalkState is the REST-facing progress snapshot for the single active walk.
type MapWalkState struct {
	Active       bool           `json:"active"`
	Epoch        int64          `json:"epoch"`
	JobID        uint64         `json:"job_id,omitempty"`
	Current      int            `json:"current"`
	Total        int            `json:"total"`
	Reason       string         `json:"reason,omitempty"`
	ReasonCode   string         `json:"reason_code,omitempty"`
	ReasonArgs   map[string]any `json:"reason_args,omitempty"`
	ReasonDetail string         `json:"reason_detail,omitempty"`
	State        string         `json:"state"`
	CanResume    bool           `json:"can_resume"`
	Revision     uint64         `json:"revision"`
}

func (s *Session) mapWalkStateLocked(reason string) MapWalkState {
	if s.mapWalk == nil {
		return s.stoppedMapWalkStateLocked(reason)
	}
	s.mapWalk.revision++
	state := "running"
	if s.mapWalk.stopping {
		state = "stopping"
		reason = s.mapWalk.stopReason
	} else if s.mapWalk.paused {
		state = "paused"
		reason = s.mapWalk.pauseReason
	} else {
		reason = ""
	}
	return MapWalkState{Active: true, Epoch: s.walkEpoch(), JobID: s.mapWalk.id, Current: s.mapWalk.current, Total: len(s.mapWalk.steps), Reason: reason, State: state, CanResume: s.mapWalk.paused && !s.manualMoveReserved && s.mapWalkCanResumeLocked(), Revision: s.mapWalk.revision}
}

// stoppedMapWalkStateLocked keeps REST and WebSocket terminal responses on the
// session's high-water generation. An id-zero stop is only valid for a pristine
// session and must not overwrite a newer active job in the browser.
func (s *Session) stoppedMapWalkStateLocked(reason string) MapWalkState {
	return MapWalkState{Epoch: s.walkEpoch(), JobID: s.nextWalk, Reason: reason, State: "stopped"}
}

func (s *Session) canonicalMapWalkStateLocked(reason string) MapWalkState {
	if s.mapWalk == nil {
		return s.stoppedMapWalkStateLocked(reason)
	}
	return s.mapWalkStateLocked("")
}

// mapWalkCanResumeLocked reports whether replanning can safely use the current
// tracker position. The caller owns mapWalkMu; map-walk dispatch uses this same
// lock order before it inspects the FIFO.
func (s *Session) mapWalkCanResumeLocked() bool {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil || s.mapTracker.PendingCount() != 0 {
		return false
	}
	pos := s.mapTracker.Position()
	return pos.Valid && pos.Confidence == mapper.Green
}

func walkMsg(st MapWalkState) ServerMsg {
	return ServerMsg{Type: "map_walk", WalkActive: st.Active, WalkEpoch: st.Epoch, WalkJobID: st.JobID, WalkCurrent: st.Current, WalkTotal: st.Total, WalkReason: st.Reason, WalkState: st.State, WalkCanResume: st.CanResume, WalkRevision: st.Revision}
}

func (s *Session) broadcastMapWalk(st MapWalkState) {
	st.Epoch = s.walkEpoch()
	s.broadcastMsg(walkMsg(st))
}

// StartMapWalk computes and owns a structured route. It never serializes steps
// through the VM or command queue: aliases may rewrite a direction and queueing
// the whole script would recreate the tail-after-refusal bug this job prevents.
func (s *Session) StartMapWalk(to mapper.Coord) (state MapWalkState) {
	s.mapWalkMu.Lock()
	s.nextWalkRequest++
	request := s.nextWalkRequest
	s.mapWalkMu.Unlock()
	state = s.startMapWalkRequest(to, request)
	state.Epoch = s.walkEpoch()
	return state
}

// startMapWalkRequest applies a user request whose generation was assigned by
// StartMapWalk. Deferred replacements retain that generation and are discarded
// if a later click has superseded them while a stopped event was broadcast.
func (s *Session) startMapWalkRequest(to mapper.Coord, request uint64) (state MapWalkState) {
	defer func() { state.Epoch = s.walkEpoch() }()
	// A previous physical hop may already be on the wire. Do not send a
	// replacement until that hop drains; retain only the target and replan from
	// the position it actually confirms.
	s.mapWalkMu.Lock()
	if request != s.nextWalkRequest {
		st := s.canonicalMapWalkStateLocked("route request superseded")
		s.mapWalkMu.Unlock()
		return st
	}
	if s.manualMoveReserved && s.mapWalk != nil && s.mapWalk.paused {
		st := s.mapWalkStateLocked("manual movement is being sent; finish it before replacing route")
		st.CanResume = false
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return st
	}
	if s.manualMoveReserved {
		st := s.canonicalMapWalkStateLocked("manual movement is being sent")
		s.mapWalkMu.Unlock()
		return st
	}
	if s.mapWalk != nil {
		copy := to
		s.mapWalk.stopping = true
		s.mapWalk.stopReason = "replacement waiting for in-flight confirmation"
		s.mapWalk.desired = &copy
		s.mapWalk.desiredRequest = request
		if !s.mapWalk.inFlight && !s.mapWalk.dispatching && !s.mapWalk.planning {
			old := s.mapWalk
			s.mapWalk = nil
			stopped := MapWalkState{JobID: old.id, Current: old.current, Total: len(old.steps), Reason: "replaced", State: "stopped"}
			s.mapWalkMu.Unlock()
			s.broadcastMapWalk(stopped)
			return s.startMapWalkRequest(to, request)
		}
		st := s.mapWalkStateLocked("")
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return st
	}
	s.nextWalk++
	job := &mapWalkJob{id: s.nextWalk, request: request, planning: true, target: to}
	s.mapWalk = job
	s.mapWalkMu.Unlock()
	return s.planMapWalk(job.id, to)
}

// planMapWalk computes a route after reserving the sole movement slot. The
// reservation is deliberately installed before touching the map: a concurrent
// manual command observes it and drains/rejects instead of slipping a physical
// move into the tracker's FIFO while planning is in progress.
func (s *Session) planMapWalk(id uint64, to mapper.Coord) MapWalkState {
	if s.conn == nil {
		return s.finishMapWalkPlan(id, "session not connected — no game transport")
	}
	s.mapMu.Lock()
	var res mapper.PathResult
	if s.mapTracker == nil || s.mapTracker.Index() == nil {
		s.mapMu.Unlock()
		return s.finishMapWalkPlan(id, "no active map set for this session")
	}
	if s.mapTracker.PendingCount() != 0 {
		s.mapMu.Unlock()
		return s.finishMapWalkPlan(id, "movement confirmation already pending")
	}
	pos := s.mapTracker.Position()
	if !pos.Valid || pos.Confidence == mapper.Red {
		s.mapMu.Unlock()
		return s.finishMapWalkPlan(id, "position lost — use \"I'm here\" to re-anchor")
	}
	if pos.Coord == to {
		s.mapMu.Unlock()
		return s.finishMapWalkPlan(id, "already here")
	}
	res = s.mapTracker.Index().FindPath(pos.Coord, func(room *mapper.IndexRoom) bool { return room.Coord == to })
	s.mapMu.Unlock()
	if !res.Reachable {
		return s.finishMapWalkPlan(id, res.Reason)
	}
	for _, step := range res.Steps {
		if strings.TrimSpace(step.Command) == "" {
			return s.finishMapWalkPlan(id, "route contains an empty traversal command")
		}
		if step.Locked {
			return s.finishMapWalkPlan(id, "route crosses a door marked locked; unlock it before auto-walk")
		}
		if step.OpenAction != "" {
			if _, move := mapper.MoveDir(strings.TrimSpace(step.OpenAction)); move {
				return s.finishMapWalkPlan(id, "door-opening command is a bare movement direction")
			}
		}
	}

	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || !s.mapWalk.planning {
		st := s.canonicalMapWalkStateLocked("route replaced before planning completed")
		s.mapWalkMu.Unlock()
		return st
	}
	if s.mapWalk.stopping {
		job := s.mapWalk
		desired := job.desired
		desiredRequest := job.desiredRequest
		s.mapWalk = nil
		st := MapWalkState{JobID: job.id, Reason: "stopped", State: "stopped"}
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		if desired != nil {
			return s.startMapWalkRequest(*desired, desiredRequest)
		}
		return st
	}
	s.mapWalk.planning = false
	s.mapWalk.steps = append([]mapper.PathStep(nil), res.Steps...)
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	if terminal, failed := s.dispatchMapWalk(st.JobID); failed {
		return terminal
	}
	latest, _ := s.MapWalkSnapshot()
	return latest
}

// finishMapWalkPlan removes a reservation that never produced a physical move.
func (s *Session) finishMapWalkPlan(id uint64, reason string) MapWalkState {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || !s.mapWalk.planning {
		st := s.canonicalMapWalkStateLocked(reason)
		s.mapWalkMu.Unlock()
		return st
	}
	job := s.mapWalk
	if job.resuming {
		if reason == "already here" {
			s.mapWalk = nil
			st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "completed", State: "stopped"}
			s.mapWalkMu.Unlock()
			s.broadcastMapWalk(st)
			return st
		}
		job.planning = false
		job.paused = true
		job.resuming = false
		job.pauseReason = reason
		st := s.mapWalkStateLocked("")
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return st
	}
	desired := job.desired
	desiredRequest := job.desiredRequest
	s.mapWalk = nil
	st := MapWalkState{JobID: job.id, Reason: reason, State: "stopped"}
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	if desired != nil {
		return s.startMapWalkRequest(*desired, desiredRequest)
	}
	return st
}

// CancelMapWalk cancels only the paced route; it intentionally does not touch
// the tracker FIFO or any user/scheduler commands already in flight.
func (s *Session) CancelMapWalk(reason string) (state MapWalkState) {
	defer func() { state.Epoch = s.walkEpoch() }()
	s.mapWalkMu.Lock()
	if s.mapWalk == nil {
		st := s.stoppedMapWalkStateLocked(reason)
		s.mapWalkMu.Unlock()
		return st
	}
	job := s.mapWalk
	job.stopping = true
	job.stopReason = reason
	job.desired = nil
	if !job.inFlight && !job.dispatching {
		if job.timer != nil {
			job.timer.Stop()
		}
		s.mapWalk = nil
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: reason, State: "stopped"}
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return st
	}
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	return st
}

// ResumeMapWalk replans a paused route from the latest green tracker anchor.
// It always creates a newer job generation so delayed paused snapshots cannot
// overwrite resumed progress in a restored browser.
func (s *Session) ResumeMapWalk() (state MapWalkState) {
	defer func() { state.Epoch = s.walkEpoch() }()
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || !s.mapWalk.paused {
		st := s.canonicalMapWalkStateLocked("no paused route to continue")
		s.mapWalkMu.Unlock()
		return st
	}
	if s.manualMoveReserved {
		st := s.mapWalkStateLocked("")
		st.CanResume = false
		s.mapWalkMu.Unlock()
		return st
	}
	if !s.mapWalkCanResumeLocked() {
		st := s.mapWalkStateLocked("")
		s.mapWalkMu.Unlock()
		return st
	}
	target := s.mapWalk.target
	s.nextWalk++
	s.nextWalkRequest++
	job := &mapWalkJob{id: s.nextWalk, request: s.nextWalkRequest, planning: true, resuming: true, target: target}
	s.mapWalk = job
	s.mapWalkMu.Unlock()
	return s.planMapWalk(job.id, target)
}

// invalidateMapWalk drops a route immediately when the transport or map graph is
// no longer the one it was planned against. Unlike CancelMapWalk it does not
// drain an old hop: its caller has already invalidated or discarded that context.
func (s *Session) invalidateMapWalk(reason string) {
	s.mapWalkMu.Lock()
	var st *MapWalkState
	if s.mapWalk != nil {
		job := s.mapWalk
		if job.timer != nil {
			job.timer.Stop()
		}
		s.mapWalk = nil
		next := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: reason, State: "stopped"}
		st = &next
	}
	s.mapWalkMu.Unlock()

	if st != nil {
		// The old graph and this route's pending movement queue are inseparable.
		// Retaining its FIFO head across a topology/index change can reconcile an
		// old physical hop against a new edge, so drop both together. Unrelated
		// manual pending moves retain the established topology-edit semantics.
		s.broadcastMapPositionInvalidated(reason)
		s.broadcastMapWalk(*st)
	}
}

// stopMapWalkForZoneMove cancels the route tail without invalidating tracker
// position or its pending FIFO. A zone move translates every incident endpoint,
// so an already-sent physical hop is still meaningful in the rebuilt index even
// though the remaining route must be replanned.
func (s *Session) stopMapWalkForZoneMove(reason string) {
	s.mapWalkMu.Lock()
	var st *MapWalkState
	if s.mapWalk != nil {
		job := s.mapWalk
		if job.timer != nil {
			job.timer.Stop()
		}
		s.mapWalk = nil
		next := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: reason, State: "stopped"}
		st = &next
	}
	s.mapWalkMu.Unlock()
	if st != nil {
		s.broadcastMapWalk(*st)
	}
}

// StopMapWalkForManualMove starts the same drain barrier and reports whether a
// manual movement must be retried. Calling code must not write that direction
// while true: otherwise it becomes a second uncorrelatable in-flight hop.
func (s *Session) StopMapWalkForManualMove() bool {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil {
		s.mapWalkMu.Unlock()
		return false
	}
	if s.mapWalk.paused {
		s.mapWalkMu.Unlock()
		return false
	}
	s.mapWalk.stopping = true
	s.mapWalk.stopReason = "manual movement requested; waiting for route hop"
	s.mapWalk.desired = nil
	if !s.mapWalk.inFlight && !s.mapWalk.dispatching {
		job := s.mapWalk
		if job.timer != nil {
			job.timer.Stop()
		}
		s.mapWalk = nil
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "manual movement requested", State: "stopped"}
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return false
	}
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	return true
}

func (s *Session) MapWalkSnapshot() (MapWalkState, bool) {
	s.mapWalkMu.Lock()
	defer s.mapWalkMu.Unlock()
	if s.mapWalk == nil {
		return MapWalkState{Epoch: s.walkEpoch(), JobID: s.nextWalk, State: "stopped"}, false
	}
	return s.mapWalkStateLocked(""), true
}

func (s *Session) dispatchMapWalk(id uint64) (MapWalkState, bool) {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || s.mapWalk.stopping || s.mapWalk.paused || s.mapWalk.inFlight || s.mapWalk.dispatching || s.mapWalk.current >= len(s.mapWalk.steps) {
		s.mapWalkMu.Unlock()
		return MapWalkState{}, false
	}
	job := s.mapWalk
	step := job.steps[job.current]
	job.dispatching = true
	s.mapWalkMu.Unlock()
	if s.mapWalkBeforeDispatchWrite != nil {
		s.mapWalkBeforeDispatchWrite()
	}
	if s.abortMapWalkDispatch(id) {
		return MapWalkState{}, false
	}

	openCommands, openVars, err := s.expandMapWalkScript(step.OpenAction)
	if openVars {
		s.BroadcastVariables()
	}
	if err != nil {
		return s.finishMapWalkDispatch(id, "invalid door-opening script: "+err.Error(), false)
	}
	if step.OpenAction != "" && len(openCommands) == 0 {
		return s.finishMapWalkDispatch(id, "door-opening script produced no MUD commands", false)
	}
	for _, command := range openCommands {
		if _, move := mapper.MoveDir(command); move {
			return s.finishMapWalkDispatch(id, "door-opening script expands to a movement direction", false)
		}
	}

	// Directions retain their historical raw bypass: aliases must not rewrite a
	// canonical planned step. Phrases and a non-directional seam command are user
	// scripts, so they intentionally receive the ordinary input VM semantics.
	moveCommands := []string{strings.TrimSpace(step.Command)}
	moveVars := false
	moveScript := step.Command
	scriptedTraversal := step.Phrase != "" || step.SeamUnparsed
	if step.Phrase == "" && step.Seam && vm.HasMultipleStatements(step.SeamCommand) {
		// seamDir intentionally recognizes the final direction word, but a raw
		// multi-statement seam is still a script. Preserve its prefix before the
		// mapper's canonical command can discard it.
		moveScript = step.SeamCommand
		scriptedTraversal = true
	}
	if scriptedTraversal {
		moveCommands, moveVars, err = s.expandMapWalkScript(moveScript)
		if moveVars {
			s.BroadcastVariables()
		}
		if err != nil {
			return s.finishMapWalkDispatch(id, "invalid traversal script: "+err.Error(), false)
		}
		if len(moveCommands) == 0 {
			return s.finishMapWalkDispatch(id, "traversal script produced no MUD commands", false)
		}
	}
	// A cancel/replacement can arrive while the optional open command is on the
	// wire. It must prevent the movement command from becoming a new tail.
	//
	// The I/O gate blocks parser reconciliation, not lifecycle operations. Never
	// hold mapWalkMu/mapMu across Write or SQLite: Close must be able to remove
	// this job and close a stuck transport immediately.
	s.mapWalkIOMu.Lock()
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || s.mapWalk.stopping || s.mapWalk.paused || !s.mapWalk.dispatching {
		if s.mapWalk != nil && s.mapWalk.id == id {
			job := s.mapWalk
			job.dispatching = false
			if job.stopping {
				desired := job.desired
				desiredRequest := job.desiredRequest
				s.mapWalk = nil
				st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "stopped", State: "stopped"}
				s.mapWalkMu.Unlock()
				s.mapWalkIOMu.Unlock()
				s.broadcastMapWalk(st)
				if desired != nil {
					s.startMapWalkRequest(*desired, desiredRequest)
				}
				return MapWalkState{}, false
			}
		}
		s.mapWalkMu.Unlock()
		s.mapWalkIOMu.Unlock()
		return MapWalkState{}, false
	}
	s.mapWalkMu.Unlock()

	sent, err := s.writeMapWalkCommands(id, openCommands)
	if err != nil {
		s.mapWalkIOMu.Unlock()
		return s.finishMapWalkDispatch(id, "failed to open door: "+err.Error(), sent)
	}
	sentMove, err := s.writeMapWalkCommands(id, moveCommands)
	sent = sent || sentMove
	if err != nil {
		s.mapWalkIOMu.Unlock()
		if sent {
			state := s.StopMapRecording("map walk command outcome unknown")
			s.broadcastMsg(mapRecordingMsg(state))
		}
		return s.finishMapWalkDispatch(id, "failed to send step: "+err.Error(), sent)
	}

	// Publish FIFO and expected landing together before allowing the parser to
	// process the response that Write may already have made readable.
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || !s.mapWalk.dispatching {
		s.mapWalkMu.Unlock()
		msg := s.invalidateMapPositionLocked("walk command completed after route was invalidated")
		recordingState := s.StopMapRecording("map walk command outcome unknown")
		s.mapWalkIOMu.Unlock()
		if msg.Type != "" {
			s.broadcastMsg(msg)
		}
		s.broadcastMsg(mapRecordingMsg(recordingState))
		return MapWalkState{}, false
	}
	s.mapMu.Lock()
	if s.mapTracker == nil || s.mapTracker.PendingCount() != 0 {
		s.mapMu.Unlock()
		s.mapWalkMu.Unlock()
		msg := s.invalidateMapPositionLocked("walk command conflicted with another pending move")
		recordingState := s.StopMapRecording("map walk command outcome unknown")
		s.mapWalkIOMu.Unlock()
		st, failed := s.finishMapWalkDispatch(id, "movement confirmation already pending", false)
		if msg.Type != "" {
			s.broadcastMsg(msg)
		}
		s.broadcastMsg(mapRecordingMsg(recordingState))
		return st, failed
	}
	if !step.Seam {
		s.mapTracker.PushMove(strings.ToUpper(step.Dir))
	}
	if len(moveCommands) > 0 {
		beforeRecordingRevision := s.mapRecordingRevision
		s.recordAcceptedMapMoveLocked(moveCommands[len(moveCommands)-1], strings.ToUpper(step.Dir), "map_walk", &step.To)
		if s.mapRecordingRevision != beforeRecordingRevision {
			state := s.mapRecordingStateLocked()
			defer s.broadcastMsg(mapRecordingMsg(state))
		}
	}
	s.mapWalk.dispatching = false
	s.mapWalk.inFlight = true
	s.mapWalk.expected = step.To
	s.mapWalk.hop++
	hop := s.mapWalk.hop
	s.mapWalk.timer = time.AfterFunc(mapWalkHopTimeout, func() { s.expireMapWalk(id, hop) })
	s.mapMu.Unlock()
	s.mapWalkMu.Unlock()
	s.mapWalkIOMu.Unlock()
	s.mu.Lock()
	s.lastCommandAt = time.Now()
	s.mu.Unlock()
	return MapWalkState{}, false
}

// expandMapWalkScript is the route-only equivalent of the VM half of normal
// input processing. It intentionally does not call SendCommandWithTrace: that
// public path reserves a *manual* map move for every expanded direction, which
// would stop the route that owns this logical hop. Local echo and variable side
// effects still happen through the same VM execution; async local operations are
// explicitly rejected before any route command reaches the transport.
func (s *Session) expandMapWalkScript(script string) (commands []string, variablesChanged bool, err error) {
	if strings.TrimSpace(script) == "" {
		return nil, false, nil
	}
	results, asyncEffects := s.vm.ProcessInputDetailedNoAsync(script)
	type echoMsg struct {
		text   string
		buffer string
	}
	echoes := make([]echoMsg, 0)
	for _, result := range results {
		variablesChanged = variablesChanged || result.VariablesChanged
		if result.IsInternal {
			continue
		}
		switch result.Kind {
		case vm.ResultCommand:
			if command := strings.TrimSpace(result.Text); command != "" {
				commands = append(commands, command)
			}
		case vm.ResultEcho:
			echoes = append(echoes, echoMsg{text: result.Text, buffer: result.TargetBuffer})
		case vm.ResultExec, vm.ResultWebFetch:
			err = fmt.Errorf("%s is not allowed during map walk", result.Kind)
		default:
			err = fmt.Errorf("unsupported VM result %q during map walk", result.Kind)
		}
	}
	if len(asyncEffects) > 0 {
		err = fmt.Errorf("async local effect %q is not allowed during map walk", asyncEffects[0])
	}
	for _, echo := range echoes {
		buffer := echo.buffer
		if buffer == "" {
			buffer = "main"
		}
		plain := stripANSI(echo.text)
		entryID, appendErr := s.store.AppendLogEntry(s.sessionID, buffer, echo.text, plain)
		if appendErr != nil {
			log.Printf("map-walk script echo failed: %v", appendErr)
			continue
		}
		entry := storage.LogEntry{ID: entryID, Buffer: buffer, RawText: echo.text, PlainText: plain}
		s.broadcastEntryWithText(entry, s.vm.ApplyHighlights(echo.text))
	}
	return commands, variablesChanged, err
}

// writeMapWalkCommands sends one already-expanded script as a single logical
// hop. It checks lifecycle state between physical commands, never observes them
// as manual movement, and records history only after an entire command write
// succeeds so rejected suffixes cannot pollute history.
func (s *Session) writeMapWalkCommands(id uint64, commands []string) (sent bool, err error) {
	for _, command := range commands {
		s.mapWalkMu.Lock()
		active := s.mapWalk != nil && s.mapWalk.id == id && s.mapWalk.dispatching && !s.mapWalk.stopping && !s.mapWalk.paused
		s.mapWalkMu.Unlock()
		if !active {
			return sent, fmt.Errorf("walk cancelled while dispatching script")
		}
		payload := []byte(command + "\n")
		n, writeErr := s.conn.Write(payload)
		if n > 0 {
			sent = true
		}
		if writeErr != nil {
			return sent, writeErr
		}
		if n != len(payload) {
			return sent, io.ErrShortWrite
		}
		if err := s.store.AppendHistoryEntry(s.sessionID, "map_walk", command); err != nil {
			// The command is already on the wire. Treat history as best-effort here:
			// failing the hop would falsely imply an unknown transport outcome.
			log.Printf("append map-walk history failed: %v", err)
		}
		s.mu.Lock()
		s.lastCommandAt = time.Now()
		s.mu.Unlock()
	}
	return sent, nil
}

// abortMapWalkDispatch resolves a lifecycle change that happened after a
// dispatch claimed the job but before its next physical write. It never calls a
// replacement under mapWalkMu, so replanning cannot recurse into a held lock.
func (s *Session) abortMapWalkDispatch(id uint64) bool {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || !s.mapWalk.dispatching {
		s.mapWalkMu.Unlock()
		return true
	}
	job := s.mapWalk
	if !job.stopping && !job.paused {
		s.mapWalkMu.Unlock()
		return false
	}
	job.dispatching = false
	if !job.stopping {
		s.mapWalkMu.Unlock()
		return true
	}
	desired := job.desired
	desiredRequest := job.desiredRequest
	s.mapWalk = nil
	st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "stopped", State: "stopped"}
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	if desired != nil {
		s.startMapWalkRequest(*desired, desiredRequest)
	}
	return true
}

// finishMapWalkDispatch handles a write failure. A zero-byte failure is safe to
// replace; a partial or full-length write with an error is ambiguous and must
// invalidate the tracker before a route can be resumed.
func (s *Session) finishMapWalkDispatch(id uint64, reason string, ambiguous bool) (MapWalkState, bool) {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id {
		s.mapWalkMu.Unlock()
		return MapWalkState{}, false
	}
	job := s.mapWalk
	desired := job.desired
	desiredRequest := job.desiredRequest
	if ambiguous {
		if job.timer != nil {
			job.timer.Stop()
		}
		job.timer = nil
		job.dispatching = false
		job.inFlight = false
		var positionMsg ServerMsg
		var recordingMsg ServerMsg
		s.mapMu.Lock()
		if s.mapTracker != nil {
			s.mapTracker.Invalidate("map walk command outcome unknown")
			before := s.mapRecordingRevision
			s.stopMapRecordingLocked("map walk command outcome unknown")
			if s.mapRecordingRevision != before {
				recordingMsg = mapRecordingMsg(s.mapRecordingStateLocked())
			}
			positionMsg = s.roomPositionMsgLocked()
		}
		s.mapMu.Unlock()
		var st MapWalkState
		if job.stopping && desired == nil {
			s.mapWalk = nil
			st = s.terminalMapWalkStateLocked(job, "stopped", job.stopReason)
		} else {
			job.stopping = false
			job.paused = true
			job.pauseReason = reason + "; command outcome unknown — re-anchor before continuing"
			if desired != nil {
				job.target = *desired
			}
			job.desired = nil
			st = s.mapWalkStateLocked("")
		}
		s.mapWalkMu.Unlock()
		if positionMsg.Type != "" {
			s.broadcastMsg(positionMsg)
		}
		if recordingMsg.Type != "" {
			s.broadcastMsg(recordingMsg)
		}
		s.broadcastMapWalk(st)
		return st, true
	}
	s.mapWalk = nil
	st := s.terminalMapWalkStateLocked(job, "failed", reason)
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	if desired != nil {
		s.startMapWalkRequest(*desired, desiredRequest)
	}
	return st, true
}

// terminalMapWalkStateLocked derives a terminal response from the just-finished
// job. It preserves the job generation and a monotonic revision for the REST
// caller and the WebSocket event that describe the same transition.
func (s *Session) terminalMapWalkStateLocked(job *mapWalkJob, state, reason string) MapWalkState {
	job.revision++
	return MapWalkState{Epoch: s.walkEpoch(), JobID: job.id, Current: job.current, Total: len(job.steps), Reason: reason, State: state, Revision: job.revision}
}

// ObserveMapWalkPosition is fed after every parsed room event. Only a green
// reconciliation at the exact expected final coordinate advances the job.
func (s *Session) ObserveMapWalkPosition(pos mapper.Position) {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil {
		s.mapWalkMu.Unlock()
		return
	}
	if s.mapWalk.paused && !s.mapWalk.inFlight {
		st := s.mapWalkStateLocked("")
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return
	}
	if !s.mapWalk.inFlight {
		s.mapWalkMu.Unlock()
		return
	}
	job := s.mapWalk
	if !pos.Valid || pos.Confidence != mapper.Green || !pos.EventDescribesCell || pos.Coord != job.expected {
		// A random room event cannot drain an in-flight hop: only the exact
		// expected landing, an explicit refusal, or timeout may release it. In
		// particular, retain a queued replacement through stale old-room events.
		s.mapWalkMu.Unlock()
		return
	}
	if job.timer != nil {
		job.timer.Stop()
	}
	job.timer = nil
	job.inFlight = false
	if job.stopping {
		desired := job.desired
		desiredRequest := job.desiredRequest
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "stopped", State: "stopped"}
		s.mapWalk = nil
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		if desired != nil {
			s.startMapWalkRequest(*desired, desiredRequest)
		}
		return
	}
	job.current++
	if job.current == len(job.steps) {
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "completed"}
		s.mapWalk = nil
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return
	}
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
	s.dispatchMapWalk(job.id)
}

func (s *Session) expireMapWalk(id, hop uint64) {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || s.mapWalk.id != id || s.mapWalk.hop != hop || !s.mapWalk.inFlight {
		s.mapWalkMu.Unlock()
		return
	}
	job := s.mapWalk
	if job.timer != nil {
		job.timer.Stop()
	}
	if job.stopping && job.desired == nil {
		job.timer = nil
		s.mapWalk = nil
		var positionMsg, recordingMsg ServerMsg
		s.mapMu.Lock()
		if s.mapTracker != nil {
			s.mapTracker.Invalidate("walk confirmation timeout")
			before := s.mapRecordingRevision
			s.stopMapRecordingLocked("walk confirmation timeout")
			if s.mapRecordingRevision != before {
				recordingMsg = mapRecordingMsg(s.mapRecordingStateLocked())
			}
			positionMsg = s.roomPositionMsgLocked()
		}
		s.mapMu.Unlock()
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "stopped", State: "stopped"}
		s.mapWalkMu.Unlock()
		if positionMsg.Type != "" {
			s.broadcastMsg(positionMsg)
		}
		if recordingMsg.Type != "" {
			s.broadcastMsg(recordingMsg)
		}
		s.broadcastMapWalk(st)
		return
	}
	job.timer = nil
	job.inFlight = false
	job.stopping = false
	job.paused = true
	job.pauseReason = "route confirmation timed out; re-anchor before continuing"
	if job.desired != nil {
		job.target = *job.desired
	}
	job.desired = nil
	if s.mapWalkBeforeTimeoutInvalidate != nil {
		s.mapWalkBeforeTimeoutInvalidate()
	}
	// Keep the route reservation while the old FIFO is invalidated. Otherwise a
	// manual write can publish between the timeout state change and Invalidate,
	// leaving a physical move with no tracker head.
	var positionMsg, recordingMsg ServerMsg
	s.mapMu.Lock()
	if s.mapTracker != nil {
		s.mapTracker.Invalidate("walk confirmation timeout")
		before := s.mapRecordingRevision
		s.stopMapRecordingLocked("walk confirmation timeout")
		if s.mapRecordingRevision != before {
			recordingMsg = mapRecordingMsg(s.mapRecordingStateLocked())
		}
		positionMsg = s.roomPositionMsgLocked()
	}
	s.mapMu.Unlock()
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	if positionMsg.Type != "" {
		s.broadcastMsg(positionMsg)
	}
	if recordingMsg.Type != "" {
		s.broadcastMsg(recordingMsg)
	}
	s.broadcastMapWalk(st)
}

// ObserveMapWalkRefusal drains an in-flight hop without accepting arbitrary
// room text as confirmation. The destination stays paused until an explicit
// resume; a refusal must never turn a trigger/open command into a route tail.
func (s *Session) ObserveMapWalkRefusal() {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil {
		s.mapWalkMu.Unlock()
		return
	}
	if s.mapWalk.paused && !s.mapWalk.inFlight {
		st := s.mapWalkStateLocked("")
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return
	}
	if !s.mapWalk.inFlight {
		s.mapWalkMu.Unlock()
		return
	}
	job := s.mapWalk
	if job.timer != nil {
		job.timer.Stop()
	}
	if job.stopping && job.desired == nil {
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "stopped", State: "stopped"}
		s.mapWalk = nil
		s.mapWalkMu.Unlock()
		s.broadcastMapWalk(st)
		return
	}
	job.timer = nil
	job.inFlight = false
	job.stopping = false
	job.paused = true
	job.pauseReason = "movement refused; resolve the obstacle and continue"
	if job.desired != nil {
		job.target = *job.desired
	}
	job.desired = nil
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
}

// broadcastPausedMapWalkEligibility refreshes a paused route after an external
// tracker transition such as re-anchoring. It deliberately does not dispatch:
// only the explicit ResumeMapWalk operation can leave paused state.
func (s *Session) broadcastPausedMapWalkEligibility() {
	s.mapWalkMu.Lock()
	if s.mapWalk == nil || !s.mapWalk.paused {
		s.mapWalkMu.Unlock()
		return
	}
	st := s.mapWalkStateLocked("")
	s.mapWalkMu.Unlock()
	s.broadcastMapWalk(st)
}

func (s *Session) broadcastMapPositionInvalidated(reason string) {
	msg := s.invalidateMapPositionLocked(reason)
	if msg.Type != "" {
		s.broadcastMsg(msg)
	}
}

// invalidateMapPositionLocked acquires mapMu and returns the resulting snapshot.
// Callers that already own mapWalkIOMu use it before opening the parser gate.
func (s *Session) invalidateMapPositionLocked(reason string) ServerMsg {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return ServerMsg{}
	}
	s.mapTracker.Invalidate(reason)
	return s.roomPositionMsgLocked()
}
