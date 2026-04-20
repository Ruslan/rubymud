package session

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"unicode/utf8"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

var packetEnd = []byte{0xff, 0xf9}

type ServerMsg struct {
	Type    string           `json:"type"`
	Entries []ClientLogEntry `json:"entries,omitempty"`
	History []string         `json:"history,omitempty"`
	Hotkeys []HotkeyJSON     `json:"hotkeys,omitempty"`
	Status  string           `json:"status,omitempty"`
	Message string           `json:"message,omitempty"`
}

type ClientLogEntry struct {
	Text     string          `json:"text"`
	Commands []string        `json:"commands,omitempty"`
	Buttons  []ButtonOverlay `json:"buttons,omitempty"`
}

type ButtonOverlay struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type HotkeyJSON struct {
	Shortcut string `json:"shortcut"`
	Command  string `json:"command"`
}

type clientSink struct {
	id   int
	name string
	send func(msg ServerMsg) error
}

type Session struct {
	sessionID    int64
	mudAddr      string
	conn         net.Conn
	store        *storage.Store
	vm           *vm.VM
	clients      map[int]clientSink
	nextClientID int
	mu           sync.Mutex
	closed       bool
}

func New(sessionID int64, mudAddr string, store *storage.Store, v *vm.VM) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	return &Session{
		sessionID: sessionID,
		mudAddr:   mudAddr,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   make(map[int]clientSink),
	}, nil
}

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
					entry.Buttons = append(entry.Buttons, storage.ButtonOverlay{
						Label:   b.Label,
						Command: b.Command,
					})
				}

				highlighted := s.vm.ApplyHighlights(line)
				s.broadcastEntryWithText(entry, highlighted)
			}
		}
	}
}

func (s *Session) sendTriggerCommand(cmd string) error {
	log.Printf("trigger sending command: %q", cmd)
	if err := s.store.AppendHistoryEntry(s.sessionID, "trigger", cmd); err != nil {
		log.Printf("append history entry failed: %v", err)
	}
	_, err := s.conn.Write([]byte(cmd + "\n"))
	return err
}

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
	cle := ClientLogEntry{Text: entry.RawText, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}

	msg := ServerMsg{Type: "output", Entries: []ClientLogEntry{cle}}
	s.broadcastMsg(msg)
}

func (s *Session) broadcastEntryWithText(entry storage.LogEntry, text string) {
	cle := ClientLogEntry{Text: text, Commands: entry.Commands}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, ButtonOverlay{Label: b.Label, Command: b.Command})
	}

	msg := ServerMsg{Type: "output", Entries: []ClientLogEntry{cle}}
	s.broadcastMsg(msg)
}

func (s *Session) BroadcastEcho(text string) {
	msg := ServerMsg{Type: "output", Entries: []ClientLogEntry{{Text: text}}}
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

func (s *Session) SendCommand(command string, source string) error {
	if source == "" {
		source = "input"
	}

	results := s.vm.ProcessInput(command)

	echoMessages := make([]string, 0)
	commands := make([]string, 0)

	for _, r := range results {
		if strings.HasPrefix(r, "#") || strings.Contains(r, ": ") {
			echoMessages = append(echoMessages, r)
		} else {
			commands = append(commands, r)
		}
	}

	for _, msg := range echoMessages {
		s.BroadcastEcho(msg)
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
	return nil
}

func (s *Session) RecentLogs(limit int) ([]storage.LogEntry, error) {
	return s.store.RecentLogs(s.sessionID, limit)
}

func (s *Session) RecentInputHistory(limit int) ([]string, error) {
	return s.store.RecentInputHistory(s.sessionID, limit)
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clients = map[int]clientSink{}
	s.mu.Unlock()

	log.Printf("closing session for %s", s.mudAddr)
	if err := s.store.MarkSessionDisconnected(s.sessionID); err != nil {
		log.Printf("mark session disconnected failed: %v", err)
	}
	return s.conn.Close()
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
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
		part = strings.TrimSuffix(part, "\r")
		lines = append(lines, part)
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
