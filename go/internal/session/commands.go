package session

import (
	"log"
	"strings"

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
	shouldBroadcastVariables := isVariableCommand(command)
	if source == "" {
		source = "input"
	}

	results := s.vm.ProcessInputDetailed(command)
	type echoMsg struct {
		Text         string
		TargetBuffer string
	}
	echoMessages := make([]echoMsg, 0)
	commands := make([]string, 0)
	for _, r := range results {
		switch r.Kind {
		case vm.ResultEcho:
			echoMessages = append(echoMessages, echoMsg{Text: r.Text, TargetBuffer: r.TargetBuffer})
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

		log.Printf("sending command to MUD: %q (source=%s)", cmd, source)
		if err := s.store.AppendHistoryEntry(s.sessionID, source, cmd); err != nil {
			log.Printf("append history entry failed: %v", err)
		}
		if err := s.store.AppendCommandHintToLatestLogEntry(s.sessionID, cmd); err != nil {
			log.Printf("append command hint failed: %v", err)
		}
		if _, err := s.conn.Write([]byte(cmd + "\n")); err != nil {
			return err
		}
	}

	if shouldBroadcastVariables {
		s.BroadcastVariables()
	}
	return nil
}

func isVariableCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}

	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(trimmed, "#")))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "var", "variable", "unvar", "unvariable":
		return true
	default:
		return false
	}
}
