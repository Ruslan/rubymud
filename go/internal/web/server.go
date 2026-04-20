package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"rubymud/go/internal/config"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

const restoreChunkSize = 100

//go:embed static/*
var webFiles embed.FS

type Server struct {
	httpServer *http.Server
	session    *session.Session
	hotkeys    []config.Hotkey
	upgrader   websocket.Upgrader
}

type serverMessage struct {
	Type    string           `json:"type"`
	Entries []clientLogEntry `json:"entries,omitempty"`
	History []string         `json:"history,omitempty"`
	Hotkeys []config.Hotkey  `json:"hotkeys,omitempty"`
	Status  string           `json:"status,omitempty"`
}

type clientLogEntry struct {
	Text     string   `json:"text"`
	Commands []string `json:"commands,omitempty"`
}

type clientMessage struct {
	Method string `json:"method"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

func New(listenAddr string, sess *session.Session, hotkeys []config.Hotkey) *Server {
	log.Printf("creating web server on %s", listenAddr)
	s := &Server{
		session: sess,
		hotkeys: hotkeys,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/", http.HandlerFunc(s.handleIndex))

	s.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	return s
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	log.Printf("http %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	root, err := fs.Sub(webFiles, "static")
	if err != nil {
		http.Error(w, "failed to load web assets", http.StatusInternalServerError)
		return
	}

	http.FileServer(http.FS(root)).ServeHTTP(w, r)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Printf("websocket upgrade requested from %s", r.RemoteAddr)
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	log.Printf("websocket connected: %s", ws.RemoteAddr())
	if err := ws.WriteJSON(serverMessage{Type: "status", Status: "connected"}); err != nil {
		log.Printf("initial status send failed: %v", err)
		_ = ws.Close()
		return
	}

	if err := s.sendRestoreState(ws); err != nil {
		log.Printf("restore state send failed: %v", err)
		_ = ws.Close()
		return
	}

	clientID := s.session.AttachClient(ws.RemoteAddr().String(), func(entry storage.LogEntry) error {
		return ws.WriteJSON(serverMessage{Type: "output", Entries: []clientLogEntry{toClientLogEntry(entry)}})
	})
	defer func() {
		log.Printf("websocket closing: %s", ws.RemoteAddr())
		s.session.DetachClient(clientID)
		_ = ws.Close()
	}()

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			log.Printf("websocket read error from %s: %v", ws.RemoteAddr(), err)
			return
		}

		command, source := parseClientCommand(payload)
		if command == "" {
			log.Printf("ignored empty command from %s", ws.RemoteAddr())
			continue
		}

		log.Printf("received command from %s: %q (source=%s)", ws.RemoteAddr(), command, source)
		if err := s.session.SendCommand(command, source); err != nil {
			log.Printf("send command failed: %v", err)
			return
		}
	}
}

func parseClientCommand(payload []byte) (string, string) {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "", ""
	}

	var message clientMessage
	if err := json.Unmarshal(payload, &message); err == nil && message.Method == "send" {
		return strings.TrimSpace(message.Value), strings.TrimSpace(message.Source)
	}

	return trimmed, "input"
}

func (s *Server) sendRestoreState(ws *websocket.Conn) error {
	logs, err := s.session.RecentLogs(500)
	if err != nil {
		return err
	}

	history, err := s.session.RecentInputHistory(500)
	if err != nil {
		return err
	}

	begin := serverMessage{
		Type:    "restore_begin",
		History: history,
		Hotkeys: s.hotkeys,
	}
	if err := ws.WriteJSON(begin); err != nil {
		return err
	}

	entries := make([]clientLogEntry, 0, len(logs))
	for _, entry := range logs {
		entries = append(entries, toClientLogEntry(entry))
	}

	for start := 0; start < len(entries); start += restoreChunkSize {
		end := start + restoreChunkSize
		if end > len(entries) {
			end = len(entries)
		}
		if err := ws.WriteJSON(serverMessage{Type: "restore_chunk", Entries: entries[start:end]}); err != nil {
			return err
		}
	}

	return ws.WriteJSON(serverMessage{Type: "restore_end"})
}

func toClientLogEntry(entry storage.LogEntry) clientLogEntry {
	return clientLogEntry{Text: entry.RawText, Commands: entry.Commands}
}
