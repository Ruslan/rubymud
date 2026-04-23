package session

import (
	"log"

	"rubymud/go/internal/storage"
)

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

func (s *Session) broadcastEntry(entry storage.LogEntry) {
	cle := ClientLogEntry{ID: entry.ID, Text: entry.RawText, Buffer: entry.Buffer, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}
	s.broadcastMsg(ServerMsg{Type: "output", Entries: []ClientLogEntry{cle}})
}

func (s *Session) broadcastEntryWithText(entry storage.LogEntry, text string) {
	cle := ClientLogEntry{ID: entry.ID, Text: text, Buffer: entry.Buffer, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}
	s.broadcastMsg(ServerMsg{Type: "output", Entries: []ClientLogEntry{cle}})
}

func (s *Session) BroadcastEcho(text string) {
	s.broadcastMsg(ServerMsg{Type: "output", Entries: []ClientLogEntry{{Text: text}}})
}

func (s *Session) BroadcastVariables() {
	variables, err := s.CurrentVariables()
	if err != nil {
		log.Printf("load variables failed: %v", err)
		return
	}
	s.broadcastMsg(ServerMsg{Type: "variables", Variables: variables})
}

func (s *Session) broadcastCommandHint(cmd string, entryID int64, buffer string) {
	s.broadcastMsg(ServerMsg{
		Type:    "command_hint",
		Command: cmd,
		EntryID: entryID,
		Buffer:  buffer,
	})
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
