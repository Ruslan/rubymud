package session

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"rubymud/go/internal/storage"
)

func (s *Session) RunReadLoop() {
	log.Printf("session read loop started for %s", s.mudAddr)
	buf := make([]byte, 100*1024)
	packet := make([]byte, 0, 100*1024)

	for {
		n, err := s.conn.Read(buf)
		if err != nil {
			if !s.isClosed() {
				log.Printf("mud read error: %v", err)
			}
			return
		}
		if n == 0 {
			continue
		}

		packet = append(packet, buf[:n]...)
		for bytes.Contains(packet, packetEnd) {
			idx := bytes.Index(packet, packetEnd)
			currentPacket := append([]byte(nil), packet[:idx]...)
			packet = packet[idx+len(packetEnd):]

			processed := normalizePacket(currentPacket)
			if processed == "" {
				continue
			}

			for _, line := range splitLinesForLogs(processed) {
				id, err := s.store.AppendLogEntry(s.sessionID, line, line)
				if err != nil {
					log.Printf("append log entry failed: %v", err)
					continue
				}

				plainText := stripANSI(line)
				effects := s.vm.MatchTriggers(plainText, id)
				buttons := s.vm.ApplyEffects(effects, s.sendTriggerCommand)

				entry := storage.LogEntry{ID: id, RawText: line}
				for _, b := range buttons {
					entry.Buttons = append(entry.Buttons, storage.ButtonOverlay{Label: b.Label, Command: b.Command})
				}

				highlighted := s.vm.ApplyHighlights(line)
				s.broadcastEntryWithText(entry, highlighted)
			}
		}
	}
}

func normalizePacket(packet []byte) string {
	packet = bytes.ReplaceAll(packet, packetEnd, nil)
	if len(packet) == 0 {
		return ""
	}

	var out bytes.Buffer
	for len(packet) > 0 {
		r, size := utf8.DecodeRune(packet)
		if r == utf8.RuneError && size == 1 {
			out.WriteString(fmt.Sprintf("[\\x%02x]", packet[0]))
			packet = packet[1:]
			continue
		}
		out.WriteRune(r)
		packet = packet[size:]
	}

	return out.String()
}

func splitLinesForLogs(text string) []string {
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, strings.TrimSuffix(part, "\r"))
	}
	return lines
}

func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}
