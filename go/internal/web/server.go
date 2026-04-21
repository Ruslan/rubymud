package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
	apiToken   string
}

type clientMessage struct {
	Method string `json:"method"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

func New(listenAddr string, sess *session.Session, hotkeys []config.Hotkey) *Server {
	log.Printf("creating web server on %s", listenAddr)

	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	apiToken := hex.EncodeToString(tokenBytes)

	s := &Server{
		session:  sess,
		hotkeys:  hotkeys,
		apiToken: apiToken,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return sameOriginRequest(r)
			},
		},
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Token protection: check X-Session-Token header
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip check for non-API routes (static files, index)
			if !strings.HasPrefix(r.URL.Path, "/api") && r.URL.Path != "/ws" {
				next.ServeHTTP(w, r)
				return
			}

			// Validate token
			token := r.Header.Get("X-Session-Token")
			if token == "" {
				// Also check query param for WebSockets
				token = r.URL.Query().Get("token")
			}

			if token != s.apiToken {
				http.Error(w, "Unauthorized: Invalid or missing session token", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	r.HandleFunc("/ws", s.handleWebSocket)

	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.WriteHeader(http.StatusNoContent)
	})

	r.Get("/settings", s.handleSettingsIndex)
	r.Get("/settings/*", s.handleSettingsIndex)

	r.Route("/api", func(r chi.Router) {
		r.Route("/variables", func(r chi.Router) {
			r.Get("/", s.listVariables)
			r.Post("/", s.setVariable)
			r.Delete("/{key}", s.deleteVariable)
		})
		r.Route("/aliases", func(r chi.Router) {
			r.Get("/", s.listAliases)
			r.Post("/", s.createAlias)
			r.Route("/{id}", func(r chi.Router) {
				r.Put("/", s.updateAlias)
				r.Delete("/", s.deleteAlias)
			})
		})
		r.Route("/triggers", func(r chi.Router) {
			r.Get("/", s.listTriggers)
			r.Post("/", s.createTrigger)
			r.Route("/{id}", func(r chi.Router) {
				r.Put("/", s.updateTrigger)
				r.Delete("/", s.deleteTrigger)
			})
		})
		r.Route("/highlights", func(r chi.Router) {
			r.Get("/", s.listHighlights)
			r.Post("/", s.createHighlight)
			r.Route("/{id}", func(r chi.Router) {
				r.Put("/", s.updateHighlight)
				r.Delete("/", s.deleteHighlight)
			})
		})
		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", s.listSessions)
		})
	})

	r.Handle("/*", http.HandlerFunc(s.handleIndex))

	s.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: r,
	}

	return s
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "index.html"
	} else {
		path = strings.TrimPrefix(path, "/")
	}

	content, err := webFiles.ReadFile("static/" + path)
	if err != nil {
		// If not found, serve index.html
		content, err = webFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "failed to load web assets", http.StatusInternalServerError)
			return
		}
		path = "index.html"
	}

	if strings.HasSuffix(path, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tokenTag := "<script>window.API_TOKEN=\"" + s.apiToken + "\"</script>"
		html := strings.Replace(string(content), "<!-- %TOKEN% -->", tokenTag, 1)
		w.Write([]byte(html))
		return
	}

	// For other files, serve normally
	http.ServeContent(w, r, path, time.Now(), strings.NewReader(string(content)))
}

func (s *Server) handleSettingsIndex(w http.ResponseWriter, r *http.Request) {
	content, err := webFiles.ReadFile("static/settings.html")
	if err != nil {
		http.Error(w, "failed to load web assets", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tokenTag := "<script>window.API_TOKEN=\"" + s.apiToken + "\"</script>"
	html := strings.Replace(string(content), "<!-- %TOKEN% -->", tokenTag, 1)
	w.Write([]byte(html))
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if !strings.EqualFold(originURL.Host, r.Host) {
		return false
	}

	requestScheme := forwardedScheme(r)
	if requestScheme == "" {
		return true
	}

	return strings.EqualFold(originURL.Scheme, requestScheme)
}

func forwardedScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// Variables

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	vars, err := s.session.CurrentVariables()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vars)
}

func (s *Server) setVariable(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.session.SetVariable(v.Key, v.Value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("variables")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteVariable(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if err := s.session.DeleteVariable(key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("variables")
	w.WriteHeader(http.StatusNoContent)
}

// Aliases

func (s *Server) listAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := s.session.ListAliases()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(aliases)
}

func (s *Server) createAlias(w http.ResponseWriter, r *http.Request) {
	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.session.SaveAlias(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("aliases")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateAlias(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.ID = id
	if err := s.session.UpdateAlias(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("aliases")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.session.DeleteAlias(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("aliases")
	w.WriteHeader(http.StatusNoContent)
}

// Triggers

func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request) {
	triggers, err := s.session.ListTriggers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(triggers)
}

func (s *Server) createTrigger(w http.ResponseWriter, r *http.Request) {
	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.session.CreateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("triggers")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateTrigger(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.ID = id
	if err := s.session.UpdateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("triggers")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.session.DeleteTrigger(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("triggers")
	w.WriteHeader(http.StatusNoContent)
}

// Highlights

func (s *Server) listHighlights(w http.ResponseWriter, r *http.Request) {
	highlights, err := s.session.ListHighlights()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(highlights)
}

func (s *Server) createHighlight(w http.ResponseWriter, r *http.Request) {
	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.session.CreateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("highlights")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateHighlight(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.ID = id
	if err := s.session.UpdateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("highlights")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteHighlight(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.session.DeleteHighlight(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.session.NotifySettingsChanged("highlights")
	w.WriteHeader(http.StatusNoContent)
}

// Sessions

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.session.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.session != nil {
		for i := range sessions {
			if sessions[i].ID == s.session.SessionID() {
				if s.session.IsClosed() {
					sessions[i].Status = "disconnected"
				} else {
					sessions[i].Status = "connected"
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Printf("websocket connect requested: remote=%s path=%s", r.RemoteAddr, r.URL.Path)
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	log.Printf("websocket upgraded: remote=%s", r.RemoteAddr)

	var writeMu sync.Mutex
	writeJSON := func(msg session.ServerMsg) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteJSON(msg)
	}

	if err := writeJSON(session.ServerMsg{Type: "status", Status: "connected"}); err != nil {
		log.Printf("websocket initial status send failed: %v", err)
		_ = ws.Close()
		return
	}
	log.Printf("websocket initial status sent: remote=%s", r.RemoteAddr)

	if err := s.sendRestoreState(writeJSON); err != nil {
		log.Printf("websocket restore state failed: %v", err)
		_ = ws.Close()
		return
	}

	clientID := s.session.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
		return writeJSON(msg)
	})
	defer func() {
		s.session.DetachClient(clientID)
		_ = ws.Close()
	}()

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			log.Printf("websocket read failed: %v", err)
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
			if err := writeJSON(session.ServerMsg{Type: "variables", Variables: variables}); err != nil {
				log.Printf("websocket variables send failed: %v", err)
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

func (s *Server) sendRestoreState(writeJSON func(session.ServerMsg) error) error {
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
	if err := writeJSON(begin); err != nil {
		return err
	}

	entries := make([]session.ClientLogEntry, 0, len(logs))
	for _, entry := range logs {
		cle := session.ClientLogEntry{Text: s.session.HighlightText(entry.RawText), Commands: entry.Commands}
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
		if err := writeJSON(session.ServerMsg{Type: "restore_chunk", Entries: entries[start:end]}); err != nil {
			return err
		}
	}

	return writeJSON(session.ServerMsg{Type: "restore_end"})
}
