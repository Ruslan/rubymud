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
	manager    *session.Manager
	store      *storage.Store
	hotkeys    []config.Hotkey
	upgrader   websocket.Upgrader
	apiToken   string
}

type clientMessage struct {
	Method string `json:"method"`
	Value  string `json:"value"`
	Source string `json:"source"`
	SessID int64  `json:"sess_id"`
}

func New(listenAddr string, manager *session.Manager, store *storage.Store, hotkeys []config.Hotkey) *Server {
	log.Printf("creating web server on %s", listenAddr)

	apiToken, _ := store.GetSetting("api_token")
	if apiToken == "" {
		tokenBytes := make([]byte, 16)
		rand.Read(tokenBytes)
		apiToken = hex.EncodeToString(tokenBytes)
		_ = store.SetSetting("api_token", apiToken)
	}

	s := &Server{
		manager:  manager,
		store:    store,
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
		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", s.listSessions)
			r.Post("/", s.createSession)
			r.Route("/{sessionID}", func(r chi.Router) {
				r.Put("/", s.updateSession)
				r.Delete("/", s.deleteSession)
				r.Post("/connect", s.connectSession)
				r.Post("/disconnect", s.disconnectSession)

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
				r.Route("/groups", func(r chi.Router) {
					r.Get("/", s.listGroups)
					r.Post("/toggle", s.toggleGroup)
				})
			})
		})
		r.Route("/app", func(r chi.Router) {
			r.Get("/settings", s.getAppSettings)
			r.Post("/settings/rotate-api-token", s.rotateAPIToken)
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

func (s *Server) getSession(r *http.Request) (*session.Session, int64, error) {
	idStr := chi.URLParam(r, "sessionID")
	if idStr == "" {
		return nil, 0, nil
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, 0, err
	}
	sess, _ := s.manager.GetSession(id)
	return sess, id, nil
}

// App Settings

func (s *Server) getAppSettings(w http.ResponseWriter, r *http.Request) {
	apiToken, _ := s.store.GetSetting("api_token")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"api_token": apiToken,
	})
}

func (s *Server) rotateAPIToken(w http.ResponseWriter, r *http.Request) {
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	apiToken := hex.EncodeToString(tokenBytes)
	if err := s.store.SetSetting("api_token", apiToken); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.apiToken = apiToken
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"api_token": apiToken,
	})
}

// Variables

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vars, err := s.store.ListVariables(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result := make([]session.VariableJSON, 0, len(vars))
	for _, v := range vars {
		result = append(result, session.VariableJSON{Key: v.Key, Value: v.Value})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) setVariable(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var v struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SetVariable(id, v.Key, v.Value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("variables")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteVariable(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := chi.URLParam(r, "key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteVariable(id, key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("variables")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Aliases

func (s *Server) listAliases(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	aliases, err := s.store.ListAliases(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(aliases)
}

func (s *Server) createAlias(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveAlias(id, a.Name, a.Template, a.Enabled, a.GroupName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("aliases")
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateAlias(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.ID = id
	a.SessionID = sessionID
	if err := s.store.UpdateAlias(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("aliases")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteAliasByID(id, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("aliases")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Triggers

func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	triggers, err := s.store.ListTriggers(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(triggers)
}

func (s *Server) createTrigger(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.SessionID = id
	if err := s.store.CreateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("triggers")
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateTrigger(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.ID = id
	t.SessionID = sessionID
	if err := s.store.UpdateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("triggers")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteTriggerByID(id, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("triggers")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Highlights

func (s *Server) listHighlights(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	highlights, err := s.store.ListHighlights(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(highlights)
}

func (s *Server) createHighlight(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.SessionID = id
	if err := s.store.CreateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("highlights")
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateHighlight(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.ID = id
	h.SessionID = sessionID
	if err := s.store.UpdateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("highlights")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteHighlight(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteHighlightByID(id, sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("highlights")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Groups

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	groups, err := s.store.ListRuleGroups(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

func (s *Server) toggleGroup(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Domain    string `json:"domain"`
		GroupName string `json:"group_name"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Domain) == "" {
		http.Error(w, "missing domain", http.StatusBadRequest)
		return
	}
	if err := s.store.SetGroupEnabled(id, strings.TrimSpace(payload.Domain), strings.TrimSpace(payload.GroupName), payload.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged(strings.TrimSpace(payload.Domain))
	}
	w.WriteHeader(http.StatusNoContent)
}

// Sessions

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.manager.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name string `json:"name"`
		Host string `json:"mud_host"`
		Port int    `json:"mud_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	record, err := s.manager.CreateSession(payload.Name, payload.Host, payload.Port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(record)
}

func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var record storage.SessionRecord
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	record.ID = id
	if err := s.manager.UpdateSession(record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.manager.DeleteSession(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) connectSession(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.manager.Connect(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) disconnectSession(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.manager.Disconnect(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

	var currentSess *session.Session
	var clientID int

	detach := func() {
		if currentSess != nil {
			currentSess.DetachClient(clientID)
			currentSess = nil
		}
	}
	defer func() {
		detach()
		_ = ws.Close()
	}()

	// Try to attach to session if ID provided in query
	sessIDStr := r.URL.Query().Get("session_id")
	if sessIDStr != "" {
		if id, err := strconv.ParseInt(sessIDStr, 10, 64); err == nil {
			if sess, ok := s.manager.GetSession(id); ok {
				currentSess = sess
			}
		}
	}

	// Send status reflecting actual attach state
	var initialStatus string
	switch {
	case currentSess != nil:
		initialStatus = "connected"
	case sessIDStr != "":
		initialStatus = "disconnected"
	default:
		initialStatus = "no session"
	}
	if err := writeJSON(session.ServerMsg{Type: "status", Status: initialStatus}); err != nil {
		log.Printf("websocket initial status send failed: %v", err)
		return
	}

	// Send restore state and attach client
	if currentSess != nil {
		if err := s.sendRestoreState(currentSess, writeJSON); err != nil {
			log.Printf("websocket restore state failed: %v", err)
			return
		}
		clientID = currentSess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
			return writeJSON(msg)
		})
	}

	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			log.Printf("websocket read failed: %v", err)
			return
		}

		message := parseClientMessage(payload)
		switch message.Method {
		case "attach":
			detach()
			if id := message.SessID; id != 0 {
				if sess, ok := s.manager.GetSession(id); ok {
					currentSess = sess
					if err := s.sendRestoreState(sess, writeJSON); err != nil {
						log.Printf("websocket restore state failed: %v", err)
						return
					}
					clientID = sess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
						return writeJSON(msg)
					})
				}
			}
		case "variables":
			if currentSess == nil {
				continue
			}
			variables, err := currentSess.CurrentVariables()
			if err != nil {
				log.Printf("load variables failed: %v", err)
				return
			}
			if err := writeJSON(session.ServerMsg{Type: "variables", Variables: variables}); err != nil {
				log.Printf("websocket variables send failed: %v", err)
				return
			}
		case "send":
			if currentSess == nil {
				continue
			}
			command := strings.TrimSpace(message.Value)
			if command == "" {
				continue
			}

			if err := currentSess.SendCommand(command, strings.TrimSpace(message.Source)); err != nil {
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

func (s *Server) sendRestoreState(sess *session.Session, writeJSON func(session.ServerMsg) error) error {
	logs, err := sess.RecentLogs(500)
	if err != nil {
		return err
	}

	history, err := sess.RecentInputHistory(500)
	if err != nil {
		return err
	}

	begin := session.ServerMsg{
		Type:    "restore_begin",
		History: history,
	}
	variables, err := sess.CurrentVariables()
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
		cle := session.ClientLogEntry{Text: sess.HighlightText(entry.RawText), Commands: entry.Commands}
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
