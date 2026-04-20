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
)

var packetEnd = []byte{0xff, 0xf9}

type clientSink struct {
	id   int
	name string
	send func(storage.LogEntry) error
}

type Session struct {
	sessionID    int64
	mudAddr      string
	conn         net.Conn
	store        *storage.Store
	clients      map[int]clientSink
	nextClientID int
	mu           sync.Mutex
	closed       bool
}

func New(sessionID int64, mudAddr string, store *storage.Store) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	return &Session{
		sessionID: sessionID,
		mudAddr:   mudAddr,
		conn:      conn,
		store:     store,
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
			log.Printf("mud read returned 0 bytes")
			continue
		}

		packet = append(packet, buf[:n]...)
		log.Printf("mud read %d bytes, packet buffer is now %d bytes", n, len(packet))

		for bytes.Contains(packet, packetEnd) {
			idx := bytes.Index(packet, packetEnd)
			currentPacket := append([]byte(nil), packet[:idx]...)
			packet = packet[idx+len(packetEnd):]

			processed := normalizePacket(currentPacket)
			log.Printf("processed packet: raw=%d bytes, text=%d bytes, remaining buffer=%d", len(currentPacket), len(processed), len(packet))
			if processed == "" {
				continue
			}

			for _, line := range splitLinesForLogs(processed) {
				id, err := s.store.AppendLogEntry(s.sessionID, line, line)
				if err != nil {
					log.Printf("append log entry failed: %v", err)
					continue
				}

				s.Broadcast(storage.LogEntry{ID: id, RawText: line})
			}
		}
	}
}

func (s *Session) AttachClient(name string, send func(storage.LogEntry) error) int {
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

func (s *Session) Broadcast(entry storage.LogEntry) {
	s.mu.Lock()
	clients := make([]clientSink, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()

	log.Printf("broadcasting log entry %d to %d clients", entry.ID, len(clients))

	for _, client := range clients {
		if err := client.send(entry); err != nil {
			log.Printf("client send error to %s: %v", client.name, err)
			s.DetachClient(client.id)
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

func (s *Session) SendCommand(command string, source string) error {
	if source == "" {
		source = "input"
	}

	log.Printf("sending command to MUD: %q (source=%s)", command, source)
	if err := s.store.AppendHistoryEntry(s.sessionID, source, command); err != nil {
		log.Printf("append history entry failed: %v", err)
	}
	if err := s.store.AppendCommandHintToLatestLogEntry(s.sessionID, command); err != nil {
		log.Printf("append command hint failed: %v", err)
	}
	_, err := s.conn.Write([]byte(command + "\n"))
	return err
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
	clients := make([]clientSink, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
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

func splitLinesForLogs(text string) []string {
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSuffix(part, "\r")
		lines = append(lines, part)
	}
	return lines
}
