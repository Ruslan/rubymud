package session

import (
	"log"
	"strings"
	"time"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func (s *Session) sendTriggerCommand(cmd string, entryID int64, buffer string) error {
	log.Printf("trigger sending command: %q (entry=%d, buffer=%s)", cmd, entryID, buffer)
	if err := s.store.AppendHistoryEntry(s.sessionID, "trigger", cmd); err != nil {
		log.Printf("append history entry failed: %v", err)
	}
	// Add visual hint to the log entry that triggered this
	if err := s.store.AppendCommandOverlay(entryID, cmd); err != nil {
		log.Printf("append command overlay failed: %v", err)
	}
	// Broadcast hint to clients immediately with correct context
	s.broadcastCommandHint(cmd, entryID, buffer)

	_, err := s.conn.Write([]byte(cmd + "\n"))
	return err
}

func (s *Session) SendCommand(command string, source string) error {
	_, err := s.SendCommandWithTrace(command, source)
	return err
}

func (s *Session) SendCommandWithTrace(command string, source string) ([]string, error) {
	if source == "" {
		source = "input"
	}

	originalCommand := strings.TrimSpace(command)

	// Special case: empty enter from user input sends just a newline
	if command == "" && source == "input" {
		_, err := s.conn.Write([]byte("\n"))
		return nil, err
	}

	if source == "input" && originalCommand != "" {
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

		historyKind := source
		if source == "input" {
			historyKind = "expanded"
		}

		log.Printf("sending command to MUD: %q (source=%s)", cmd, source)
		if err := s.store.AppendHistoryEntry(s.sessionID, historyKind, cmd); err != nil {
			log.Printf("append history entry failed: %v", err)
		}
		if source != "connect" {
			if err := s.store.AppendCommandHintToLatestLogEntry(s.sessionID, cmd); err != nil {
				log.Printf("append command hint failed: %v", err)
			}
		}
		if _, err := s.conn.Write([]byte(cmd + "\n")); err != nil {
			return canonicalCommands, err
		}
		canonicalCommands = append(canonicalCommands, cmd)
		s.mu.Lock()
		s.lastCommandAt = time.Now()
		s.mu.Unlock()
	}

	if shouldBroadcastVariables {
		s.BroadcastVariables()
	}
	return canonicalCommands, nil
}

func hasVariableChange(results []vm.Result) bool {
	for _, result := range results {
		if result.VariablesChanged {
			return true
		}
	}
	return false
}
