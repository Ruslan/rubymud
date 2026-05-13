package session

import (
	"log"
	"time"

	"rubymud/go/internal/storage"
)

const latencyLogThreshold = 50 * time.Millisecond

func (s *Session) AttachClient(name string, send func(msg ServerMsg) error) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextClientID++
	id := s.nextClientID
	s.clients[id] = clientSink{id: id, name: name, send: send}
	log.Printf("client attached: %s, total clients=%d", name, len(s.clients))
	return id
}

func (s *Session) DetachClient(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := "unknown"
	if client, ok := s.clients[id]; ok {
		name = client.name
	}
	delete(s.clients, id)
	log.Printf("client detached: %s, total clients=%d", name, len(s.clients))
}

func (s *Session) beginOutputBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchActive = true
	s.batchLatency = latencyAggregate{}
}

func (s *Session) flushOutputBatch() {
	s.mu.Lock()
	batch := s.outputBatch
	hints := s.outputBatchHints
	oldestLine := s.batchOldestLine
	latency := s.batchLatency
	s.outputBatch = nil
	s.outputBatchHints = nil
	s.batchOldestLine = time.Time{}
	s.batchLatency = latencyAggregate{}
	s.batchActive = false
	s.mu.Unlock()

	if len(batch) > 0 {
		started := time.Now()
		s.broadcastMsg(ServerMsg{Type: "output", Entries: batch})
		sendDuration := time.Since(started)
		if !oldestLine.IsZero() {
			total := time.Since(oldestLine)
			if total >= latencyLogThreshold || sendDuration >= latencyLogThreshold {
				log.Printf(
					"[latency] ws_flush entries=%d lines=%d bytes=%d oldest_to_ws=%v line_total_sum=%v parse_sum=%v vm_sum=%v vm_gag_sum=%v vm_triggers_sum=%v vm_subs_sum=%v db_main_sum=%v db_extra_sum=%v db_total_sum=%v effects_sum=%v highlight_sum=%v ws_queue_sum=%v max_line=%v max_db=%v ws_send=%v",
					len(batch),
					latency.Lines,
					latency.Bytes,
					total,
					latency.Total,
					latency.Parse,
					latency.VM,
					latency.VMGag,
					latency.VMTriggers,
					latency.VMSubs,
					latency.DBMain,
					latency.DBExtra,
					latency.DBMain+latency.DBExtra,
					latency.Effects,
					latency.Highlight,
					latency.WSQueue,
					latency.MaxLine,
					latency.MaxDB,
					sendDuration,
				)
			}
		}
	}
	for _, hint := range hints {
		s.broadcastMsg(hint)
	}
}

func (s *Session) queueOrBroadcast(cle ClientLogEntry) {
	s.queueOrBroadcastAt(cle, time.Time{})
}

func (s *Session) queueOrBroadcastAt(cle ClientLogEntry, lineStartedAt time.Time) {
	s.mu.Lock()
	if s.batchActive {
		s.outputBatch = append(s.outputBatch, cle)
		if !lineStartedAt.IsZero() && (s.batchOldestLine.IsZero() || lineStartedAt.Before(s.batchOldestLine)) {
			s.batchOldestLine = lineStartedAt
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	started := time.Now()
	s.broadcastMsg(ServerMsg{Type: "output", Entries: []ClientLogEntry{cle}})
	sendDuration := time.Since(started)
	if !lineStartedAt.IsZero() {
		total := time.Since(lineStartedAt)
		if total >= latencyLogThreshold || sendDuration >= latencyLogThreshold {
			log.Printf("[latency] ws_send entries=1 total_to_ws=%v ws_send=%v", total, sendDuration)
		}
	}
}

func (s *Session) broadcastEntry(entry storage.LogEntry) {
	cle := ClientLogEntry{ID: entry.ID, Text: entry.RawText, Buffer: entry.Buffer, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}
	s.queueOrBroadcast(cle)
}

func (s *Session) broadcastEntryWithText(entry storage.LogEntry, text string) {
	s.broadcastEntryWithTextAt(entry, text, time.Time{})
}

func (s *Session) broadcastEntryWithTextAt(entry storage.LogEntry, text string, lineStartedAt time.Time) {
	cle := ClientLogEntry{ID: entry.ID, Text: text, Buffer: entry.Buffer, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}
	s.queueOrBroadcastAt(cle, lineStartedAt)
}

func (s *Session) BroadcastEcho(text string) {
	s.queueOrBroadcast(ClientLogEntry{Text: text})
}

func (s *Session) BroadcastVariables() {
	variables, err := s.CurrentVariables()
	if err != nil {
		log.Printf("load variables failed: %v", err)
		return
	}
	s.broadcastMsg(ServerMsg{Type: "variables", Variables: variables})
}

func (s *Session) BroadcastStatus(status string) {
	s.broadcastMsg(ServerMsg{Type: "status", Status: status})
}

func (s *Session) broadcastCommandHint(cmd string, entryID int64, buffer string) {
	msg := ServerMsg{
		Type:    "command_hint",
		Command: cmd,
		EntryID: entryID,
		Buffer:  buffer,
	}

	s.mu.Lock()
	if s.batchActive {
		s.outputBatchHints = append(s.outputBatchHints, msg)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.broadcastMsg(msg)
}

func (s *Session) broadcastMsg(msg ServerMsg) {
	s.mu.Lock()
	clients := make([]clientSink, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()

	for _, client := range clients {
		if err := client.send(msg); err != nil {
			log.Printf("client send error to %s: %v", client.name, err)
			s.DetachClient(client.id)
		}
	}
}
