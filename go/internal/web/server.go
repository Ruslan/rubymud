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
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	if err := ws.WriteJSON(session.ServerMsg{Type: "status", Status: "connected"}); err != nil {
		_ = ws.Close()
		return
	}

	if err := s.sendRestoreState(ws); err != nil {
		_ = ws.Close()
		return
	}

	clientID := s.session.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
		return ws.WriteJSON(msg)
	})
	defer func() {
		s.session.DetachClient(clientID)
		_ = ws.Close()
	}()

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			return
		}

		message := parseClientMessage(payload)
		switch message.Method {
		case "variables":
			variables, err := s.session.CurrentVariables()
			if err != nil {
				log.Printf("load variables failed: %v", err)
				return
			}
			if err := ws.WriteJSON(session.ServerMsg{Type: "variables", Variables: variables}); err != nil {
				return
			}
		case "send":
			command := strings.TrimSpace(message.Value)
			if command == "" {
				continue
			}

			if err := s.session.SendCommand(command, strings.TrimSpace(message.Source)); err != nil {
				log.Printf("send command failed: %v", err)
				return
			}
		}
	}
}

func parseClientMessage(payload []byte) clientMessage {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return clientMessage{}
	}

	var message clientMessage
	if err := json.Unmarshal(payload, &message); err == nil && message.Method != "" {
		return message
	}

	return clientMessage{Method: "send", Value: trimmed, Source: "input"}
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

	begin := session.ServerMsg{
		Type:    "restore_begin",
		History: history,
	}
	variables, err := s.session.CurrentVariables()
	if err != nil {
		return err
	}
	begin.Variables = variables
	for _, hk := range s.hotkeys {
		begin.Hotkeys = append(begin.Hotkeys, session.HotkeyJSON{Shortcut: hk.Shortcut, Command: hk.Command})
	}
	if err := ws.WriteJSON(begin); err != nil {
		return err
	}

	entries := make([]session.ClientLogEntry, 0, len(logs))
	for _, entry := range logs {
		cle := session.ClientLogEntry{Text: entry.RawText, Commands: entry.Commands}
		for _, b := range entry.Buttons {
			cle.Buttons = append(cle.Buttons, session.ButtonOverlay{Label: b.Label, Command: b.Command})
		}
		entries = append(entries, cle)
	}

	for start := 0; start < len(entries); start += restoreChunkSize {
		end := start + restoreChunkSize
		if end > len(entries) {
			end = len(entries)
		}
		if err := ws.WriteJSON(session.ServerMsg{Type: "restore_chunk", Entries: entries[start:end]}); err != nil {
			return err
		}
	}

	return ws.WriteJSON(session.ServerMsg{Type: "restore_end"})
}
