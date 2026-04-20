package session

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"sync"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

var packetEnd = []byte{0xff, 0xf9}

type Session struct {
	mudAddr string
	conn    net.Conn
	clients map[*websocket.Conn]struct{}
	mu      sync.Mutex
	closed  bool
}

func New(mudAddr string) (*Session, error) {
	conn, err := net.Dial("tcp", mudAddr)
	if err != nil {
		return nil, err
	}

	return &Session{
		mudAddr: mudAddr,
		conn:    conn,
		clients: make(map[*websocket.Conn]struct{}),
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

			s.Broadcast(processed)
		}
	}
}

func (s *Session) AttachClient(ws *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[ws] = struct{}{}
	log.Printf("client attached: %s, total clients=%d", ws.RemoteAddr(), len(s.clients))
}

func (s *Session) DetachClient(ws *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, ws)
	log.Printf("client detached: %s, total clients=%d", ws.RemoteAddr(), len(s.clients))
}

func (s *Session) Broadcast(message string) {
	s.mu.Lock()
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for ws := range s.clients {
		clients = append(clients, ws)
	}
	s.mu.Unlock()

	log.Printf("broadcasting %d bytes to %d clients", len(message), len(clients))

	for _, ws := range clients {
		if err := ws.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			log.Printf("websocket write error to %s: %v", ws.RemoteAddr(), err)
			s.DetachClient(ws)
			_ = ws.Close()
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

func (s *Session) SendCommand(command string) error {
	log.Printf("sending command to MUD: %q", command)
	_, err := s.conn.Write([]byte(command + "\n"))
	return err
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for ws := range s.clients {
		clients = append(clients, ws)
	}
	s.clients = map[*websocket.Conn]struct{}{}
	s.mu.Unlock()

	for _, ws := range clients {
		_ = ws.Close()
	}

	log.Printf("closing session for %s", s.mudAddr)
	return s.conn.Close()
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
