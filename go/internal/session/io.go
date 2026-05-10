package session

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func (s *Session) RunReadLoop() {
	log.Printf("session read loop started for %s", s.mudAddr)
	decoder := newTelnetDecoder()
	buf := make([]byte, 100*1024)
	var lineBuf bytes.Buffer

	for {
		n, err := s.readSrc.Read(buf)
		if err != nil {
			if !s.IsClosed() {
				if err == io.EOF {
					log.Printf("mud connection closed by remote host")
				} else {
					log.Printf("mud read error: %v", err)
				}
				s.Close()
			}
			return
		}
		if n == 0 {
			continue
		}

		if s.mccpActive.Load() {
			s.mccpDecompressedBytes.Add(uint64(n))
		}

		s.beginOutputBatch()
		events := decoder.Feed(buf[:n])

		for _, ev := range events {
			switch ev.typ {
			case telEventText:
				lineBuf.Write(ev.data)
			case telEventFlush:
				flushLine(&lineBuf, s)
			case telEventMCCP2Start:
				if !s.mccpAccepted {
					log.Printf("MCCP2 start received but not accepted/enabled, closing session")
					s.Close()
					s.flushOutputBatch()
					return
				}
				if lineBuf.Len() > 0 {
					flushLine(&lineBuf, s)
				}
				s.activateMCCP2(decoder)
				decoder.ResetForDecompressed()
			case telEventSB:
				if ev.opt == mccp2 {
					log.Printf("telnet: received duplicate MCCP2 start while active, ignoring")
					continue
				}
				s.handleTelnetEvent(ev)
			default:
				s.handleTelnetEvent(ev)
			}
		}

		processBufferedLines(&lineBuf, s)
		s.flushOutputBatch()

		if decoder.IsCompressed() {
			decoder.ResetForDecompressed()
		}
	}
}

type lineHandler interface {
	processLine(line string)
}

func flushLine(buf *bytes.Buffer, h lineHandler) {
	processBufferedLines(buf, h)
	if buf.Len() == 0 {
		return
	}
	line := buf.String()
	buf.Reset()
	line = strings.TrimRight(line, "\r\n\x00")
	h.processLine(line)
}

func processBufferedLines(buf *bytes.Buffer, h lineHandler) {
	for {
		data := buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		buf.Next(idx + 1)
		line = strings.TrimRight(line, "\r\x00")
		h.processLine(line)
	}
}

func (s *Session) processLine(line string) {
	processed := normalizeLine(line)

	plainText := stripANSI(processed)
	var effects []vm.Effect
	var routing vm.RoutingInfo

	// Only match triggers if the line is not blank
	if processed != "" {
		effects, routing = s.vm.MatchTriggers(plainText)
	} else {
		routing.TargetBuffer = "main" // Default to main for blank lines
	}

	id, err := s.store.AppendLogEntry(s.sessionID, routing.TargetBuffer, processed, plainText)
	if err != nil {
		log.Printf("append log entry failed: %v", err)
		return
	}

	for i := range effects {
		if effects[i].Type == "button" {
			effects[i].LogEntryID = id
		}
	}

	buttons := s.vm.ApplyEffects(effects, id, routing.TargetBuffer, s.sendTriggerCommand, s.BroadcastResult)

	entry := storage.LogEntry{ID: id, Buffer: routing.TargetBuffer, RawText: processed, PlainText: plainText}
	for _, b := range buttons {
		entry.Buttons = append(entry.Buttons, storage.ButtonOverlay{Label: b.Label, Command: b.Command})
	}

	highlighted := s.vm.ApplyHighlights(processed)
	s.broadcastEntryWithText(entry, highlighted)

	for _, copyBuffer := range routing.CopyBuffers {
		copyID, err := s.store.AppendLogEntry(s.sessionID, copyBuffer, processed, plainText)
		if err == nil {
			for _, b := range entry.Buttons {
				_ = s.store.AppendButtonOverlay(copyID, b.Label, b.Command)
			}
			copyEntry := entry
			copyEntry.ID = copyID
			copyEntry.Buffer = copyBuffer
			s.broadcastEntryWithText(copyEntry, highlighted)
		}
	}

	for _, echo := range routing.Echoes {
		echoPlain := stripANSI(echo.Text)
		echoID, err := s.store.AppendLogEntry(s.sessionID, echo.TargetBuffer, echo.Text, echoPlain)
		if err == nil {
			echoEntry := storage.LogEntry{ID: echoID, Buffer: echo.TargetBuffer, RawText: echo.Text, PlainText: echoPlain}
			s.broadcastEntryWithText(echoEntry, s.vm.ApplyHighlights(echo.Text))
		}
	}
}

func normalizeLine(line string) string {
	var out bytes.Buffer
	data := []byte(line)
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < 32 && c != '\t' && c != '\n' && c != '\r' && c != '\x1b' {
			out.WriteString(fmt.Sprintf("[\\x%02x]", c))
			continue
		}
		out.WriteByte(c)
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
	data := []byte(s)
	inEscape := false
	for i := 0; i < len(data); i++ {
		c := data[i]
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
		if c < 32 && c != '\t' && c != '\n' && c != '\r' {
			continue
		}
		result.WriteByte(c)
	}
	return result.String()
}

// compile-time interface check
var _ lineHandler = (*Session)(nil)
