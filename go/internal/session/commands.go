package session

import (
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

var errMapWalkManualBarrier = errors.New("map walk is stopping; retry movement after confirmation")

// IsMapWalkManualBarrier distinguishes the expected movement drain response
// from a transport failure. WebSocket callers keep the connection alive so the
// client can receive the subsequent stopped map-walk event and retry.
func IsMapWalkManualBarrier(err error) bool {
	return errors.Is(err, errMapWalkManualBarrier)
}

func (s *Session) sendTriggerCommand(cmd string, entryID int64, buffer string) error {
	log.Printf("trigger sending command: %q (entry=%d, buffer=%s)", cmd, entryID, buffer)
	dir, manualMove := mapper.MoveDir(cmd)
	if manualMove {
		if err := s.reserveManualMapMove(); err != nil {
			return err
		}
	}
	if err := s.store.AppendHistoryEntry(s.sessionID, "trigger", cmd); err != nil {
		log.Printf("append history entry failed: %v", err)
	}
	// Add visual hint to the log entry that triggered this
	if err := s.store.AppendCommandOverlay(entryID, cmd); err != nil {
		log.Printf("append command overlay failed: %v", err)
	}
	// Broadcast hint to clients immediately with correct context
	s.broadcastCommandHint(cmd, entryID, buffer)

	if manualMove {
		return s.commitManualMapMove(cmd, dir, "trigger")
	}
	payload := []byte(cmd + "\n")
	n, err := s.writeCommandPayload(payload)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	if err == nil {
		// Mapper: detect a movement direction only after the command actually
		// reached the MUD, covering trigger/alias/speedwalk-sent moves (already
		// alias/VM-expanded). Queuing before a failed write would leave an unsent
		// direction in the FIFO and mis-reconcile the next room event.
		s.observeOutgoingMove(cmd, "trigger")
	}
	return err
}

func (s *Session) SendCommand(command string, source string) error {
	_, err := s.SendCommandWithTrace(command, source)
	return err
}

func (s *Session) SendCommandWithTrace(command string, source string) ([]string, error) {
	return s.sendCommandWithTrace(command, source, false)
}

// SendMCPCommand is the trusted MCP boundary. A browser-provided source string
// must not opt into public MCP command hints, so the capability is an explicit
// method call from the authenticated MCP handler rather than source == "mcp".
func (s *Session) SendMCPCommand(command string) error {
	_, err := s.sendCommandWithTrace(command, "mcp", true)
	return err
}

func (s *Session) sendCommandWithTrace(command string, source string, publishMCP bool) ([]string, error) {
	if source == "" {
		source = "input"
	}

	originalCommand := strings.TrimSpace(command)

	// Special case: empty enter from user input sends just a newline
	if command == "" && source == "input" {
		_, err := s.writeCommandPayload([]byte("\n"))
		return nil, err
	}

	directManualMove := false
	if _, directManualMove = mapper.MoveDir(originalCommand); source == "input" && originalCommand != "" && !directManualMove {
		if err := s.store.AppendHistoryEntry(s.sessionID, "input", originalCommand); err != nil {
			log.Printf("append history entry failed: %v", err)
		}
	}

	results := s.vm.ProcessInputDetailed(command)
	shouldBroadcastVariables := hasVariableChange(results)
	type echoMsg struct {
		Text         string
		TargetBuffer string
	}
	echoMessages := make([]echoMsg, 0)
	commands := make([]string, 0)
	canonicalCommands := make([]string, 0)
	for _, r := range results {
		if r.IsInternal && (source != "input" || (r.Depth > 0 && !r.ShowOnInput)) {
			continue
		}
		switch r.Kind {
		case vm.ResultEcho:
			echoMessages = append(echoMessages, echoMsg{Text: r.Text, TargetBuffer: r.TargetBuffer})
		case vm.ResultExec, vm.ResultWebFetch:
			for _, line := range s.runLocalResult(r) {
				echoMessages = append(echoMessages, echoMsg{Text: line, TargetBuffer: r.TargetBuffer})
			}
		default:
			commands = append(commands, r.Text)
		}
	}

	for _, msg := range echoMessages {
		buf := msg.TargetBuffer
		if buf == "" {
			buf = "main"
		}
		plain := stripANSI(msg.Text)
		id, err := s.store.AppendLogEntry(s.sessionID, buf, msg.Text, plain)
		if err == nil {
			entry := storage.LogEntry{ID: id, Buffer: buf, RawText: msg.Text, PlainText: plain}
			s.broadcastEntryWithText(entry, s.vm.ApplyHighlights(msg.Text))
		} else {
			log.Printf("failed to save echo: %v", err)
		}
	}

	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}

		dir, manualMove := mapper.MoveDir(cmd)
		if manualMove {
			if err := s.reserveManualMapMove(); err != nil {
				return canonicalCommands, err
			}
		}

		historyKind := source
		if source == "input" {
			historyKind = "expanded"
		}

		log.Printf("sending command to MUD: %q (source=%s)", cmd, source)
		if directManualMove {
			if err := s.store.AppendHistoryEntry(s.sessionID, "input", originalCommand); err != nil {
				log.Printf("append history entry failed: %v", err)
			}
			directManualMove = false
		}
		if err := s.store.AppendHistoryEntry(s.sessionID, historyKind, cmd); err != nil {
			log.Printf("append history entry failed: %v", err)
		}
		var hintEntryID int64
		var hintBuffer string
		// Skip command_hint overlays for connect-sourced (session-init/auto-login)
		// commands so auto-login credentials stay out of command_hint overlays —
		// and therefore out of the colored-HTML log export, which reads those
		// overlays (see docs/dev/planned/html-log-export.md security note).
		if source != "connect" {
			if publishMCP {
				if anchor, err := s.store.LatestCommandHintAnchor(s.sessionID); err != nil {
					log.Printf("capture MCP command hint anchor failed: %v", err)
				} else {
					hintEntryID, hintBuffer = anchor.EntryID, anchor.Buffer
				}
			} else {
				if entryID, buffer, err := s.store.AppendCommandHintToLatestLogEntryWithAnchor(s.sessionID, cmd); err != nil {
					log.Printf("append command hint failed: %v", err)
				} else {
					hintEntryID, hintBuffer = entryID, buffer
				}
			}
		}
		publishMCPHint := func() {
			// Browser input has its origin-only optimistic trace. MCP has no
			// browser origin, so persist and publish its canonical command to
			// every observer only after the transport accepted the frame.
			if !publishMCP || hintEntryID <= 0 {
				return
			}
			anchor := storage.CommandHintAnchor{EntryID: hintEntryID, Buffer: hintBuffer}
			if err := s.store.AppendCommandHintAtAnchor(anchor, cmd); err != nil {
				log.Printf("append successful MCP command hint failed: %v", err)
				return
			}
			s.broadcastCommandHint(cmd, hintEntryID, hintBuffer)
		}
		if manualMove {
			if err := s.commitManualMapMove(cmd, dir, source); err != nil {
				return canonicalCommands, err
			}
			canonicalCommands = append(canonicalCommands, cmd)
			publishMCPHint()
			continue
		}
		if handled, err := s.writeManualMapScan(cmd); handled {
			if err != nil {
				return canonicalCommands, err
			}
			canonicalCommands = append(canonicalCommands, cmd)
			publishMCPHint()
			s.mu.Lock()
			s.lastCommandAt = time.Now()
			s.mu.Unlock()
			continue
		}
		payload := []byte(cmd + "\n")
		n, err := s.writeCommandPayload(payload)
		if err == nil && n != len(payload) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return canonicalCommands, err
		}
		// Mapper: detect a movement direction after a SUCCESSFUL write (after
		// alias/VM expansion). Pushing before the write would leave an unsent
		// direction in the FIFO on a mid-speedwalk write error.
		s.observeOutgoingMove(cmd, source)
		canonicalCommands = append(canonicalCommands, cmd)
		publishMCPHint()
		s.mu.Lock()
		s.lastCommandAt = time.Now()
		s.mu.Unlock()
	}

	if shouldBroadcastVariables {
		s.BroadcastVariables()
	}
	return canonicalCommands, nil
}

// writeCommandPayload serializes every ordinary game command with mapper-owned
// scan and route writes. The scan capture is canceled before the later command
// reaches the wire, so a sequential MUD's scan response can only be discarded
// through its own boundary and cannot consume the later command's response.
// Telnet negotiation bytes deliberately do not use this command protocol.
func (s *Session) writeCommandPayload(payload []byte) (int, error) {
	s.mapWalkIOMu.Lock()
	defer s.mapWalkIOMu.Unlock()
	s.cancelMapScanForOutbound()
	return s.conn.Write(payload)
}

// reserveManualMapMove claims the physical-hop slot before any history or UI
// side effects. A rejected movement therefore never appears as an optimistic
// command. It does no blocking I/O under mapWalkMu.
func (s *Session) reserveManualMapMove() error {
	s.mapWalkMu.Lock()
	var stopped *MapWalkState
	if s.manualMoveReserved {
		s.mapWalkMu.Unlock()
		return errMapWalkManualBarrier
	}
	if s.mapWalk != nil {
		job := s.mapWalk
		if job.paused {
			s.manualMoveReserved = true
			s.mapWalkMu.Unlock()
			return nil
		}
		job.stopping = true
		job.stopReason = "manual movement requested; waiting for route hop"
		job.desired = nil
		if job.inFlight || job.dispatching || job.planning {
			st := s.mapWalkStateLocked("")
			s.mapWalkMu.Unlock()
			s.broadcastMapWalk(st)
			return errMapWalkManualBarrier
		}
		if job.timer != nil {
			job.timer.Stop()
		}
		s.mapWalk = nil
		st := MapWalkState{JobID: job.id, Current: job.current, Total: len(job.steps), Reason: "manual movement requested", State: "stopped"}
		stopped = &st
	}
	s.manualMoveReserved = true
	s.mapWalkMu.Unlock()
	if stopped != nil {
		s.broadcastMapWalk(*stopped)
	}
	return nil
}

// commitManualMapMove writes and publishes the reserved manual hop without
// holding lifecycle mutexes. The parser I/O gate gives it the same no-gap FIFO
// publication guarantee as map-walk dispatch.
func (s *Session) commitManualMapMove(cmd, dir, source string) error {
	s.mapWalkIOMu.Lock()
	s.cancelMapScanForOutbound()
	n, err := s.conn.Write([]byte(cmd + "\n"))
	if err == nil && n != len(cmd)+1 {
		err = io.ErrShortWrite
	}
	var recordingState *MapRecordingState
	if err == nil {
		s.mapMu.Lock()
		beforeRevision := s.mapRecordingRevision
		if s.mapTracker != nil {
			s.mapTracker.PushMove(dir)
			s.recordAcceptedMapMoveLocked(cmd, dir, source, nil)
		}
		if s.mapRecordingRevision != beforeRevision {
			state := s.mapRecordingStateLocked()
			recordingState = &state
		}
		s.mapMu.Unlock()
	}
	s.mapWalkMu.Lock()
	s.manualMoveReserved = false
	var positionMsg ServerMsg
	if err != nil && n > 0 {
		s.mapMu.Lock()
		if s.mapTracker != nil {
			s.mapTracker.Invalidate("manual movement command outcome unknown")
			s.stopMapRecordingLocked("manual movement command outcome unknown")
			state := s.mapRecordingStateLocked()
			recordingState = &state
			positionMsg = s.roomPositionMsgLocked()
		}
		s.mapMu.Unlock()
	}
	var paused *MapWalkState
	if s.mapWalk != nil && s.mapWalk.paused {
		if err != nil && n > 0 {
			s.mapWalk.pauseReason = "manual movement command outcome unknown — re-anchor before continuing"
		}
		st := s.mapWalkStateLocked("")
		paused = &st
	}
	s.mapWalkMu.Unlock()
	s.mapWalkIOMu.Unlock()
	if s.mapWalkBeforeManualPausedBroadcast != nil {
		s.mapWalkBeforeManualPausedBroadcast()
	}
	if paused != nil {
		s.broadcastMapWalk(*paused)
	}
	if positionMsg.Type != "" {
		s.broadcastMsg(positionMsg)
	}
	if recordingState != nil {
		s.broadcastMsg(mapRecordingMsg(*recordingState))
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.lastCommandAt = time.Now()
	s.mu.Unlock()
	return nil
}

func hasVariableChange(results []vm.Result) bool {
	for _, result := range results {
		if result.VariablesChanged {
			return true
		}
	}
	return false
}
