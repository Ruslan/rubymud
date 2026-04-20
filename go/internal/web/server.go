package web

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"rubymud/go/internal/session"
)

//go:embed static/*
var webFiles embed.FS

type Server struct {
	httpServer *http.Server
	session    *session.Session
	upgrader   websocket.Upgrader
}

func New(listenAddr string, sess *session.Session) *Server {
	log.Printf("creating web server on %s", listenAddr)
	s := &Server{
		session: sess,
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

	s.session.AttachClient(ws)
	defer func() {
		log.Printf("websocket closing: %s", ws.RemoteAddr())
		s.session.DetachClient(ws)
		_ = ws.Close()
	}()

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			log.Printf("websocket read error from %s: %v", ws.RemoteAddr(), err)
			return
		}

		command := strings.TrimSpace(string(payload))
		if command == "" {
			log.Printf("ignored empty command from %s", ws.RemoteAddr())
			continue
		}

		log.Printf("received command from %s: %q", ws.RemoteAddr(), command)
		if err := s.session.SendCommand(command); err != nil {
			log.Printf("send command failed: %v", err)
			return
		}
	}
}
