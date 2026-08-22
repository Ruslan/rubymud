package fiveroom

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	maxCommandBytes = 256
	readIdleTimeout = 30 * time.Second
	writeTimeout    = 5 * time.Second
	roomBoundaryLag = 20 * time.Millisecond
)

type TraceEntry struct {
	Sequence     uint64 `json:"sequence"`
	ConnectionID uint64 `json:"connection_id"`
	From         int    `json:"from"`
	Command      string `json:"command"`
	To           int    `json:"to"`
	Moved        bool   `json:"moved"`
	AtUnixMilli  int64  `json:"at_unix_milli"`
}

type Server struct {
	listener net.Listener
	trace    *os.File
	options  Options

	traceMu sync.Mutex
	connMu  sync.Mutex
	conns   map[net.Conn]struct{}

	nextConnection atomic.Uint64
	nextSequence   atomic.Uint64
	acceptWG       sync.WaitGroup
	connWG         sync.WaitGroup
	closeOnce      sync.Once
	closeErr       error
}

// Options changes only fixture presentation. DarkRoom makes that room emit the
// exact RMUD visibility-loss block until the same connection sends `torch`.
type Options struct {
	DarkRoom     int
	RoomBoundary bool
	OneWayHillo  bool
	DynamicCrate bool
	HiddenHatch  bool
}

func Listen(addr, tracePath string) (*Server, error) {
	return ListenWithOptions(addr, tracePath, Options{})
}

func ListenWithOptions(addr, tracePath string, options Options) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	trace, err := os.Create(tracePath)
	if err != nil {
		listener.Close()
		return nil, err
	}
	s := &Server{listener: listener, trace: trace, options: options, conns: make(map[net.Conn]struct{})}
	s.acceptWG.Add(1)
	go s.acceptLoop()
	return s, nil
}

func (s *Server) Addr() net.Addr { return s.listener.Addr() }

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		listenerErr := s.listener.Close()
		s.acceptWG.Wait()
		s.connMu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connMu.Unlock()
		s.connWG.Wait()
		traceErr := s.trace.Close()
		if listenerErr != nil && !errors.Is(listenerErr, net.ErrClosed) {
			s.closeErr = listenerErr
		} else {
			s.closeErr = traceErr
		}
	})
	return s.closeErr
}

func (s *Server) acceptLoop() {
	defer s.acceptWG.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connMu.Lock()
		s.conns[conn] = struct{}{}
		s.connMu.Unlock()
		connectionID := s.nextConnection.Add(1)
		s.connWG.Add(1)
		go s.handleConnection(conn, connectionID)
	}
}

func (s *Server) handleConnection(conn net.Conn, connectionID uint64) {
	defer s.connWG.Done()
	defer func() {
		s.connMu.Lock()
		delete(s.conns, conn)
		s.connMu.Unlock()
		_ = conn.Close()
	}()

	world := NewWorldWithOptions(WorldOptions{OneWayHillo: s.options.OneWayHillo, DynamicCrate: s.options.DynamicCrate, HiddenHatch: s.options.HiddenHatch})
	lit := s.options.DarkRoom == 0
	if err := s.writeObservedRoom(conn, world.Current(), lit); err != nil {
		return
	}
	reader := bufio.NewReaderSize(conn, maxCommandBytes+16)
	for {
		command, err := readCommand(conn, reader)
		if err != nil {
			return
		}
		if command == "" {
			continue
		}
		from := world.Current().ID
		if strings.EqualFold(command, "torch") {
			entry := TraceEntry{Sequence: s.nextSequence.Add(1), ConnectionID: connectionID, From: from, Command: command, To: from, Moved: false, AtUnixMilli: time.Now().UnixMilli()}
			if err := s.appendTrace(entry); err != nil {
				return
			}
			lit = true
			if err := writeAcknowledgement(conn, "Вы зажгли факел."); err != nil {
				return
			}
			continue
		}
		if strings.EqualFold(command, "scan") {
			entry := TraceEntry{Sequence: s.nextSequence.Add(1), ConnectionID: connectionID, From: from, Command: command, To: from, Moved: false, AtUnixMilli: time.Now().UnixMilli()}
			if err := s.appendTrace(entry); err != nil {
				return
			}
			if err := writeScanBlock(conn, world.ScanLines()); err != nil {
				return
			}
			continue
		}
		if world.ApplyAction(command) {
			entry := TraceEntry{Sequence: s.nextSequence.Add(1), ConnectionID: connectionID, From: from, Command: command, To: from, Moved: false, AtUnixMilli: time.Now().UnixMilli()}
			if err := s.appendTrace(entry); err != nil {
				return
			}
			message := []string{
				"Стараясь не свалить на себя пирамиду всевозможных коробок, вы аккуратно передвинули ящик.",
				"Взглянув на то место, где только что стоял, Вы обнаружили в полу дыру!",
			}
			if s.options.HiddenHatch {
				message = []string{"Механизм с гулом приходит в движение."}
			}
			if err := writeLinesBlock(conn, message...); err != nil {
				return
			}
			continue
		}
		room, moved := world.Apply(command)
		entry := TraceEntry{
			Sequence: s.nextSequence.Add(1), ConnectionID: connectionID,
			From: from, Command: command, To: room.ID, Moved: moved, AtUnixMilli: time.Now().UnixMilli(),
		}
		if err := s.appendTrace(entry); err != nil {
			return
		}
		if !moved {
			if err := writeLine(conn, "Вы не можете идти в этом направлении."); err != nil {
				return
			}
			continue
		}
		if err := s.writeObservedRoom(conn, room, lit); err != nil {
			return
		}
	}
}

func (s *Server) writeObservedRoom(conn net.Conn, room Room, lit bool) error {
	if room.ID == s.options.DarkRoom && !lit {
		if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(conn, "Здесь кромешная тьма.\r\n[ Exits: %s ]\r\n", room.Exits); err != nil {
			return err
		}
	} else if err := writeRoomBlock(conn, room); err != nil {
		return err
	}
	if !s.options.RoomBoundary {
		return nil
	}
	// Give mudhost's detached scan writer the same scheduling window observed on
	// RMUD before the room's trailing blank arrives. Without this deterministic
	// gap, loopback commonly delivers the whole fixture block in one read and
	// hides the premature-capture regression that the live server exposes.
	time.Sleep(roomBoundaryLag)
	_, err := io.WriteString(conn, "\r\n")
	return err
}

func writeScanBlock(conn net.Conn, lines []string) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(conn, "\r\n")
	return err
}

func (s *Server) appendTrace(entry TraceEntry) error {
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	return json.NewEncoder(s.trace).Encode(entry)
}

func writeRoomBlock(conn net.Conn, room Room) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(conn, "%s\r\n%s\r\n[ Exits: %s ]\r\n", room.Hint, room.Desc, room.Exits)
	return err
}

func writeLine(conn net.Conn, line string) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err := io.WriteString(conn, line+"\r\n")
	return err
}

func writeAcknowledgement(conn net.Conn, line string) error {
	return writeLinesBlock(conn, line)
}

func writeLinesBlock(conn net.Conn, lines ...string) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	_, err := io.WriteString(conn, strings.Join(lines, "\r\n")+"\r\n\r\n")
	return err
}

func readCommand(conn net.Conn, reader *bufio.Reader) (string, error) {
	var command strings.Builder
	for {
		if err := conn.SetReadDeadline(time.Now().Add(readIdleTimeout)); err != nil {
			return "", err
		}
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 255 {
			if err := discardTelnetSequence(reader); err != nil {
				return "", err
			}
			continue
		}
		switch b {
		case '\r':
			continue
		case '\n':
			if !utf8.ValidString(command.String()) {
				return "", fmt.Errorf("command is not valid UTF-8")
			}
			return strings.TrimSpace(command.String()), nil
		}
		if b < 32 {
			continue
		}
		if command.Len() >= maxCommandBytes {
			return "", fmt.Errorf("command exceeds %d bytes", maxCommandBytes)
		}
		command.WriteByte(b)
	}
}

func discardTelnetSequence(reader *bufio.Reader) error {
	command, err := reader.ReadByte()
	if err != nil {
		return err
	}
	switch command {
	case 251, 252, 253, 254: // WILL, WONT, DO, DONT
		_, err = reader.ReadByte()
		return err
	case 250: // SB ... IAC SE
		previousIAC := false
		for {
			b, readErr := reader.ReadByte()
			if readErr != nil {
				return readErr
			}
			if previousIAC && b == 240 {
				return nil
			}
			previousIAC = b == 255
		}
	default:
		return nil
	}
}
