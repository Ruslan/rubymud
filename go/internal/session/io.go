package session

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

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
	lineStartedAt := time.Now()
	var mudRTT time.Duration
	var parseDuration time.Duration
	var vmGagDuration time.Duration
	var vmTriggersDuration time.Duration
	var vmSubsDuration time.Duration
	var dbMainDuration time.Duration
	var dbExtraDuration time.Duration
	var effectsDuration time.Duration
	var highlightDuration time.Duration
	var wsQueueDuration time.Duration

	s.mu.Lock()
	if !s.lastCommandAt.IsZero() {
		mudRTT = time.Since(s.lastCommandAt)
		log.Printf("[ping] %v", mudRTT)
		s.lastCommandAt = time.Time{} // Reset
	}
	s.mu.Unlock()

	phaseStartedAt := time.Now()
	processed := normalizeLine(line)

	plainText := stripANSI(processed)
	parseDuration = time.Since(phaseStartedAt)
	var effects []vm.Effect
	var routing vm.RoutingInfo

	phaseStartedAt = time.Now()
	if gagOverlay, gagged := s.vm.CheckGag(plainText); gagged {
		vmGagDuration += time.Since(phaseStartedAt)
		dbStartedAt := time.Now()
		if _, err := s.store.AppendLogEntryWithOverlays(s.sessionID, "main", processed, plainText, []storage.LogOverlay{gagOverlay}); err != nil {
			dbMainDuration += time.Since(dbStartedAt)
			log.Printf("append gag log entry failed: %v", err)
			s.finishLineLatency("gag_error", len(line), mudRTT, parseDuration, vmGagDuration, vmTriggersDuration, vmSubsDuration, dbMainDuration, dbExtraDuration, effectsDuration, highlightDuration, wsQueueDuration, time.Since(lineStartedAt))
			return
		}
		dbMainDuration += time.Since(dbStartedAt)
		s.finishLineLatency("gag", len(line), mudRTT, parseDuration, vmGagDuration, vmTriggersDuration, vmSubsDuration, dbMainDuration, dbExtraDuration, effectsDuration, highlightDuration, wsQueueDuration, time.Since(lineStartedAt))
		return
	}
	vmGagDuration += time.Since(phaseStartedAt)

	// Only match triggers if the line is not blank
	phaseStartedAt = time.Now()
	if processed != "" {
		effects, routing = s.vm.MatchTriggers(plainText)
	} else {
		routing.TargetBuffer = "main" // Default to main for blank lines
	}
	vmTriggersDuration += time.Since(phaseStartedAt)

	phaseStartedAt = time.Now()
	displayRaw, displayPlain, subOverlays := s.vm.ApplySubsAndCollectOverlays(processed, plainText)
	vmSubsDuration += time.Since(phaseStartedAt)

	dbStartedAt := time.Now()
	id, err := s.store.AppendLogEntryWithOverlays(s.sessionID, routing.TargetBuffer, processed, plainText, subOverlays)
	dbMainDuration += time.Since(dbStartedAt)
	if err != nil {
		log.Printf("append log entry failed: %v", err)
		s.finishLineLatency("append_error", len(line), mudRTT, parseDuration, vmGagDuration, vmTriggersDuration, vmSubsDuration, dbMainDuration, dbExtraDuration, effectsDuration, highlightDuration, wsQueueDuration, time.Since(lineStartedAt))
		return
	}

	for i := range effects {
		if effects[i].Type == "button" {
			effects[i].LogEntryID = id
		}
	}

	phaseStartedAt = time.Now()
	buttons := s.vm.ApplyEffects(effects, id, routing.TargetBuffer, s.sendTriggerCommand, s.BroadcastResult)
	effectsDuration += time.Since(phaseStartedAt)

	entry := storage.LogEntry{ID: id, Buffer: routing.TargetBuffer, RawText: processed, PlainText: plainText, DisplayRaw: displayRaw, DisplayPlain: displayPlain, Overlays: subOverlays}
	for _, b := range buttons {
		entry.Buttons = append(entry.Buttons, storage.ButtonOverlay{Label: b.Label, Command: b.Command})
	}

	phaseStartedAt = time.Now()
	highlighted := s.vm.ApplyHighlights(displayRaw)
	highlightDuration += time.Since(phaseStartedAt)

	phaseStartedAt = time.Now()
	s.broadcastEntryWithTextAt(entry, highlighted, lineStartedAt)
	wsQueueDuration += time.Since(phaseStartedAt)

	for _, copyBuffer := range routing.CopyBuffers {
		dbStartedAt := time.Now()
		copyID, err := s.store.AppendLogEntryWithOverlays(s.sessionID, copyBuffer, processed, plainText, cloneLogOverlays(subOverlays))
		dbExtraDuration += time.Since(dbStartedAt)
		if err == nil {
			for _, b := range entry.Buttons {
				dbStartedAt = time.Now()
				_ = s.store.AppendButtonOverlay(copyID, b.Label, b.Command)
				dbExtraDuration += time.Since(dbStartedAt)
			}
			copyEntry := entry
			copyEntry.ID = copyID
			copyEntry.Buffer = copyBuffer
			phaseStartedAt = time.Now()
			s.broadcastEntryWithTextAt(copyEntry, highlighted, lineStartedAt)
			wsQueueDuration += time.Since(phaseStartedAt)
		}
	}

	for _, echo := range routing.Echoes {
		phaseStartedAt = time.Now()
		echoPlain := stripANSI(echo.Text)
		parseDuration += time.Since(phaseStartedAt)
		dbStartedAt := time.Now()
		echoID, err := s.store.AppendLogEntry(s.sessionID, echo.TargetBuffer, echo.Text, echoPlain)
		dbExtraDuration += time.Since(dbStartedAt)
		if err == nil {
			echoEntry := storage.LogEntry{ID: echoID, Buffer: echo.TargetBuffer, RawText: echo.Text, PlainText: echoPlain}
			phaseStartedAt = time.Now()
			echoHighlighted := s.vm.ApplyHighlights(echo.Text)
			highlightDuration += time.Since(phaseStartedAt)
			phaseStartedAt = time.Now()
			s.broadcastEntryWithTextAt(echoEntry, echoHighlighted, lineStartedAt)
			wsQueueDuration += time.Since(phaseStartedAt)
		}
	}

	s.finishLineLatency("line", len(line), mudRTT, parseDuration, vmGagDuration, vmTriggersDuration, vmSubsDuration, dbMainDuration, dbExtraDuration, effectsDuration, highlightDuration, wsQueueDuration, time.Since(lineStartedAt))
}

type lineLatencySample struct {
	Kind       string
	Bytes      int
	MudRTT     time.Duration
	Parse      time.Duration
	VM         time.Duration
	VMGag      time.Duration
	VMTriggers time.Duration
	VMSubs     time.Duration
	DBMain     time.Duration
	DBExtra    time.Duration
	Effects    time.Duration
	Highlight  time.Duration
	WSQueue    time.Duration
	Total      time.Duration
}

func (s lineLatencySample) dbTotal() time.Duration {
	return s.DBMain + s.DBExtra
}

type latencyAggregate struct {
	Lines      int
	Bytes      int
	Parse      time.Duration
	VM         time.Duration
	VMGag      time.Duration
	VMTriggers time.Duration
	VMSubs     time.Duration
	DBMain     time.Duration
	DBExtra    time.Duration
	Effects    time.Duration
	Highlight  time.Duration
	WSQueue    time.Duration
	Total      time.Duration
	MaxLine    time.Duration
	MaxDB      time.Duration
}

func (a *latencyAggregate) add(s lineLatencySample) {
	a.Lines++
	a.Bytes += s.Bytes
	a.Parse += s.Parse
	a.VM += s.VM
	a.VMGag += s.VMGag
	a.VMTriggers += s.VMTriggers
	a.VMSubs += s.VMSubs
	a.DBMain += s.DBMain
	a.DBExtra += s.DBExtra
	a.Effects += s.Effects
	a.Highlight += s.Highlight
	a.WSQueue += s.WSQueue
	a.Total += s.Total
	if s.Total > a.MaxLine {
		a.MaxLine = s.Total
	}
	if dbTotal := s.dbTotal(); dbTotal > a.MaxDB {
		a.MaxDB = dbTotal
	}
}

func (s *Session) finishLineLatency(kind string, bytes int, mudRTT, parseDuration, vmGagDuration, vmTriggersDuration, vmSubsDuration, dbMainDuration, dbExtraDuration, effectsDuration, highlightDuration, wsQueueDuration, total time.Duration) {
	sample := lineLatencySample{
		Kind:       kind,
		Bytes:      bytes,
		MudRTT:     mudRTT,
		Parse:      parseDuration,
		VM:         vmGagDuration + vmTriggersDuration + vmSubsDuration,
		VMGag:      vmGagDuration,
		VMTriggers: vmTriggersDuration,
		VMSubs:     vmSubsDuration,
		DBMain:     dbMainDuration,
		DBExtra:    dbExtraDuration,
		Effects:    effectsDuration,
		Highlight:  highlightDuration,
		WSQueue:    wsQueueDuration,
		Total:      total,
	}
	s.recordLineLatency(sample)
	logLineLatency(sample)
}

func (s *Session) recordLineLatency(sample lineLatencySample) {
	s.mu.Lock()
	if s.batchActive {
		s.batchLatency.add(sample)
	}
	s.mu.Unlock()
}

func logLineLatency(sample lineLatencySample) {
	if sample.Total < latencyLogThreshold && sample.dbTotal() < latencyLogThreshold {
		return
	}
	log.Printf(
		"[latency] line kind=%s bytes=%d mud_rtt=%v total=%v parse=%v vm=%v vm_gag=%v vm_triggers=%v vm_subs=%v db_main=%v db_extra=%v db_total=%v effects=%v highlight=%v ws_queue=%v",
		sample.Kind,
		sample.Bytes,
		sample.MudRTT,
		sample.Total,
		sample.Parse,
		sample.VM,
		sample.VMGag,
		sample.VMTriggers,
		sample.VMSubs,
		sample.DBMain,
		sample.DBExtra,
		sample.dbTotal(),
		sample.Effects,
		sample.Highlight,
		sample.WSQueue,
	)
}

func cloneLogOverlays(overlays []storage.LogOverlay) []storage.LogOverlay {
	cloned := make([]storage.LogOverlay, len(overlays))
	for i, overlay := range overlays {
		cloned[i] = overlay
		cloned[i].ID = 0
		cloned[i].LogEntryID = 0
		if overlay.StartOffset != nil {
			v := *overlay.StartOffset
			cloned[i].StartOffset = &v
		}
		if overlay.EndOffset != nil {
			v := *overlay.EndOffset
			cloned[i].EndOffset = &v
		}
	}
	return cloned
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
