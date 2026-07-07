package web

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

	"rubymud/go/internal/colorreg"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

const restoreChunkSize = 100

//go:embed static/*
var webFiles embed.FS

type Server struct {
	httpServer             *http.Server
	manager                *session.Manager
	store                  *storage.Store
	upgrader               websocket.Upgrader
	apiToken               string
	basicAuth              BasicAuth
	configDir              string
	restoreAfterCursorHook func(*session.Session) error
}

// BasicAuth holds optional HTTP basic auth credentials. When both fields are
// set, every route except the PWA/static assets needed for installation is
// gated behind basic auth (issue #6). It is an outer gate layered on top of
// the existing session-token protection.
type BasicAuth struct {
	User string
	Pass string
}

func (b BasicAuth) enabled() bool {
	return b.User != "" && b.Pass != ""
}

// isPublicAsset reports whether a path must stay reachable without basic auth
// so the PWA can be discovered and installed. A gated manifest or icon breaks
// install because browsers fetch them without credentials.
func isPublicAsset(path string) bool {
	switch path {
	case "/manifest.webmanifest", "/manifest.json", "/service-worker.js", "/app-icon.svg", "/favicon.ico", "/favicon.svg":
		return true
	}
	return strings.HasPrefix(path, "/assets/")
}

type clientMessage struct {
	Method          string `json:"method"`
	Value           string `json:"value"`
	Source          string `json:"source"`
	SessID          int64  `json:"sess_id"`
	ClientCommandID string `json:"client_command_id"`
}

func New(listenAddr string, manager *session.Manager, store *storage.Store, configDir string, basicAuth BasicAuth) *Server {
	log.Printf("creating web server on %s", listenAddr)

	apiToken, _ := store.GetSetting("api_token")
	if apiToken == "" {
		tokenBytes := make([]byte, 16)
		rand.Read(tokenBytes)
		apiToken = hex.EncodeToString(tokenBytes)
		_ = store.SetSetting("api_token", apiToken)
	}

	s := &Server{
		manager:   manager,
		store:     store,
		configDir: configDir,
		apiToken:  apiToken,
		basicAuth: basicAuth,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return sameOriginRequest(r)
			},
		},
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Optional HTTP basic auth: an outer gate in front of the token check.
	// PWA/static assets stay public so the app can be discovered and installed.
	if s.basicAuth.enabled() {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if isPublicAsset(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
				user, pass, ok := r.BasicAuth()
				userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.basicAuth.User)) == 1
				passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.basicAuth.Pass)) == 1
				if !ok || !userOK || !passOK {
					w.Header().Set("WWW-Authenticate", `Basic realm="mudhost", charset="UTF-8"`)
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		})
	}

	// Token protection: check X-Session-Token header
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api") && r.URL.Path != "/ws" {
				next.ServeHTTP(w, r)
				return
			}
			token := r.Header.Get("X-Session-Token")
			if token == "" {
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
	r.Post("/mcp", s.handleMCP)

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
					r.Get("/resolved", s.listResolvedVariables)
					r.Post("/", s.setVariable)
					r.Delete("/{key}", s.deleteVariable)
				})

				r.Route("/history", func(r chi.Router) {
					r.Get("/", s.listHistory)
					r.Delete("/{entryID}", s.deleteHistoryEntry)
				})

				r.Route("/logs", func(r chi.Router) {
					r.Get("/", s.listLogs)
					r.Get("/live", s.listLiveLogs)
					r.Get("/search", s.listSearch)
					r.Get("/{entryID}/context", s.getLogContext)
					r.Get("/download", s.downloadLogs)
				})

				r.Route("/profiles", func(r chi.Router) {
					r.Get("/", s.listSessionProfiles)
					r.Post("/", s.addProfileToSession)
					r.Put("/reorder", s.reorderSessionProfiles)
					r.Delete("/{profileID}", s.removeProfileFromSession)
				})

				r.Route("/groups", func(r chi.Router) {
					r.Get("/", s.listSessionGroups)
					r.Post("/{name}/toggle", s.toggleSessionGroup)
				})

				r.Get("/hotkeys", s.listSessionHotkeys)
			})
		})

		r.Route("/profiles", func(r chi.Router) {
			r.Get("/", s.listProfiles)
			r.Post("/", s.createProfile)
			r.Post("/export/all", s.exportAllProfilesToFiles)
			r.Post("/import/all", s.importAllProfilesFromFiles)
			r.Post("/import", s.importProfileFromFile)
			r.Get("/files", s.listProfileFiles)

			r.Route("/{profileID}", func(r chi.Router) {
				r.Post("/export", s.exportProfileToFile)
				r.Put("/", s.updateProfile)
				r.Delete("/", s.deleteProfile)

				r.Route("/aliases", func(r chi.Router) {
					r.Get("/", s.listProfileAliases)
					r.Post("/", s.createProfileAlias)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileAlias)
						r.Delete("/", s.deleteProfileAlias)
					})
				})
				r.Route("/variables", func(r chi.Router) {
					r.Get("/", s.listProfileVariables)
					r.Post("/", s.createProfileVariable)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileVariable)
						r.Delete("/", s.deleteProfileVariable)
					})
				})
				r.Route("/triggers", func(r chi.Router) {
					r.Get("/", s.listProfileTriggers)
					r.Post("/", s.createProfileTrigger)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileTrigger)
						r.Delete("/", s.deleteProfileTrigger)
					})
				})
				r.Route("/highlights", func(r chi.Router) {
					r.Get("/", s.listProfileHighlights)
					r.Post("/", s.createProfileHighlight)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileHighlight)
						r.Delete("/", s.deleteProfileHighlight)
					})
				})
				r.Route("/subs", func(r chi.Router) {
					r.Get("/", s.listProfileSubs)
					r.Post("/", s.createProfileSub)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileSub)
						r.Delete("/", s.deleteProfileSub)
					})
				})
				r.Route("/hotkeys", func(r chi.Router) {
					r.Get("/", s.listProfileHotkeys)
					r.Post("/", s.createProfileHotkey)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileHotkey)
						r.Delete("/", s.deleteProfileHotkey)
					})
				})
				r.Route("/timers", func(r chi.Router) {
					r.Get("/", s.listProfileTimers)
					r.Post("/", s.createOrUpdateProfileTimer)
					r.Delete("/{name}", s.deleteProfileTimer)
					r.Post("/{name}/subscriptions", s.createOrUpdateProfileTimerSubscription)
					r.Delete("/{name}/subscriptions/{second}/{sortOrder}", s.deleteProfileTimerSubscription)
				})
				r.Route("/groups", func(r chi.Router) {
					r.Get("/", s.listProfileGroups)
					r.Post("/toggle", s.toggleProfileGroup)
				})
			})
		})

		r.Get("/colors", s.listColors)

		r.Route("/app", func(r chi.Router) {
			r.Get("/settings", s.getAppSettings)
			r.Put("/settings", s.updateAppSettings)
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

func (s *Server) getProfileID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "profileID")
	if idStr == "" {
		return 0, nil
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func (s *Server) notifyProfileChanged(profileID int64, domain string) {
	sessionIDs, _ := s.store.GetSessionIDsForProfile(profileID)
	for _, id := range sessionIDs {
		if sess, ok := s.manager.GetSession(id); ok {
			sess.NotifySettingsChanged(domain)
		}
	}
}

// App Settings

type appSettingsJSON struct {
	APIToken             string `json:"api_token"`
	AllowExecCommand     bool   `json:"allow_exec_command"`
	AllowWebFetchCommand bool   `json:"allow_webfetch_command"`
}

func parseSettingBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boolSettingValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (s *Server) appSettingsPayload() appSettingsJSON {
	apiToken, _ := s.store.GetSetting("api_token")
	allowExec, _ := s.store.GetSetting("allow_exec_command")
	allowWebFetch, _ := s.store.GetSetting("allow_webfetch_command")
	return appSettingsJSON{
		APIToken:             apiToken,
		AllowExecCommand:     parseSettingBool(allowExec),
		AllowWebFetchCommand: parseSettingBool(allowWebFetch),
	}
}

func (s *Server) getAppSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.appSettingsPayload())
}

func (s *Server) updateAppSettings(w http.ResponseWriter, r *http.Request) {
	var payload appSettingsJSON
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SetSetting("allow_exec_command", boolSettingValue(payload.AllowExecCommand)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.store.SetSetting("allow_webfetch_command", boolSettingValue(payload.AllowWebFetchCommand)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.appSettingsPayload())
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
	json.NewEncoder(w).Encode(s.appSettingsPayload())
}

// Colors

func (s *Server) listColors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(colorreg.All())
}

// Session Variables

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
	v.Key = strings.TrimSpace(v.Key)
	if err := s.store.SetVariable(id, v.Key, v.Value); err != nil {
		if errors.Is(err, storage.ErrInvalidVariableName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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
	key, err := url.PathUnescape(chi.URLParam(r, "key"))
	if err != nil {
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	}
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteVariable(id, key); err != nil {
		if errors.Is(err, storage.ErrVariableNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("variables")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Session History

type historyListResponse struct {
	Entries      []storage.HistoryEntry `json:"entries"`
	HasMore      bool                   `json:"has_more"`
	NextBeforeID *int64                 `json:"next_before_id,omitempty"`
}

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	query := r.URL.Query()
	limit := 1000
	if limitStr := query.Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var beforeID int64
	if beforeIDStr := query.Get("before_id"); beforeIDStr != "" {
		beforeID, err = strconv.ParseInt(beforeIDStr, 10, 64)
		if err != nil || beforeID < 1 {
			http.Error(w, "invalid before_id", http.StatusBadRequest)
			return
		}
	}

	history, hasMore, err := s.store.ListHistoryPage(id, storage.HistoryListOptions{
		Kind:     strings.TrimSpace(query.Get("kind")),
		Query:    strings.TrimSpace(query.Get("q")),
		BeforeID: beforeID,
		Limit:    limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var nextBeforeID *int64
	if hasMore && len(history) > 0 {
		next := history[len(history)-1].ID
		nextBeforeID = &next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(historyListResponse{
		Entries:      history,
		HasMore:      hasMore,
		NextBeforeID: nextBeforeID,
	})
}

func (s *Server) deleteHistoryEntry(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entryIDStr := chi.URLParam(r, "entryID")
	entryID, err := strconv.ParseInt(entryIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid entry ID", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteHistoryEntry(id, entryID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 1000 {
		limit = 100
	}

	var from, to storage.SQLiteTime
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			http.Error(w, "invalid 'from' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		from.Time = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			http.Error(w, "invalid 'to' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		to.Time = t
	}

	entries, total, err := s.store.LogRangeByDate(id, from, to, limit, (page-1)*limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (s *Server) listLiveLogs(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	if afterID < 0 {
		afterID = 0
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 500
	}

	logs, err := s.store.LogsSinceID(id, afterID, limit+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}

	entries := make([]session.ClientLogEntry, 0, len(logs))
	var latestID int64
	for _, entry := range logs {
		entries = append(entries, clientLogEntryForSession(sess, entry))
		if entry.ID > latestID {
			latestID = entry.ID
		}
	}
	if latestID == 0 {
		latestID = afterID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries":   entries,
		"has_more":  hasMore,
		"latest_id": latestID,
	})
}

func (s *Server) listSearch(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	beforeID, _ := strconv.ParseInt(r.URL.Query().Get("before_id"), 10, 64)
	if beforeID == 0 {
		beforeID = 9223372036854775807
	}

	groups, cursor, err := s.store.SearchLogsDetailed(id, query, 5, beforeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	loc := s.resolveRequestLocation(id, r.URL.Query().Get("tz"))
	localGroups := make([][]logEntryWithTZ, len(groups))
	for i, group := range groups {
		localGroups[i] = logEntriesInLocation(group, loc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"groups": localGroups,
		"cursor": cursor,
	})
}

func (s *Server) getLogContext(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	entryID, _ := strconv.ParseInt(chi.URLParam(r, "entryID"), 10, 64)
	before, _ := strconv.Atoi(r.URL.Query().Get("before"))
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))

	if before == 0 && after == 0 {
		before = 20
		after = 20
	}

	var allEntries []storage.LogEntry

	if before > 0 {
		entries, err := s.store.LogRangeDetailed(id, entryID, before)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		allEntries = append(allEntries, entries...)
	}

	// Fetch anchor entry with session_id check and overlays
	anchor, err := s.store.LogEntryByID(id, entryID)
	if err != nil {
		http.Error(w, "entry not found", http.StatusNotFound)
		return
	}
	allEntries = append(allEntries, anchor)

	if after > 0 {
		entries, err := s.store.LogsSinceID(id, entryID, after)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		allEntries = append(allEntries, entries...)
	}

	loc := s.resolveRequestLocation(id, r.URL.Query().Get("tz"))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logEntriesInLocation(allEntries, loc))
}

func (s *Server) downloadLogs(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	loc := s.resolveRequestLocation(id, r.URL.Query().Get("tz"))

	var from, to storage.SQLiteTime
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			http.Error(w, "invalid 'from' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		from.Time = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			http.Error(w, "invalid 'to' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		to.Time = t
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=logs.txt")

	db := s.store.DB().Model(&storage.LogRecord{}).
		Where("session_id = ?", id).
		Where("NOT EXISTS (SELECT 1 FROM log_overlays gag WHERE gag.log_entry_id = log_entries.id AND gag.overlay_type = 'gag')")

	if !from.Time.IsZero() {
		db = db.Where("created_at >= ?", from)
	}
	if !to.Time.IsZero() {
		db = db.Where("created_at <= ?", to)
	}

	rows, err := db.Order("created_at ASC, id ASC").Rows()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var record storage.LogRecord
		if err := s.store.DB().ScanRows(rows, &record); err != nil {
			log.Printf("error scanning row: %v", err)
			continue
		}
		fmt.Fprintf(w, "[%s] %s\n", record.CreatedAt.Time.In(loc).Format("2006-01-02 15:04:05 -0700"), record.PlainText)
	}
}

// resolveRequestLocation picks the timezone used to present historical
// timestamps: the request's `tz` param when it parses, otherwise the session's
// stored timezone, otherwise UTC.
func (s *Server) resolveRequestLocation(sessionID int64, tz string) *time.Location {
	if tz = strings.TrimSpace(tz); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	if record, err := s.store.GetSession(sessionID); err == nil {
		return storage.LoadLocationOrUTC(record.Timezone)
	}
	return time.UTC
}

// logEntryWithTZ overrides the UTC-only created_at serialization of
// storage.LogEntry with a string rendered in the requested location (RFC3339,
// explicit offset) so the calendar day is unambiguous.
type logEntryWithTZ struct {
	storage.LogEntry
	CreatedAt string `json:"created_at"`
}

func logEntriesInLocation(entries []storage.LogEntry, loc *time.Location) []logEntryWithTZ {
	out := make([]logEntryWithTZ, len(entries))
	for i, e := range entries {
		created := ""
		if !e.CreatedAt.Time.IsZero() {
			created = e.CreatedAt.Time.In(loc).Format(time.RFC3339Nano)
		}
		out[i] = logEntryWithTZ{LogEntry: e, CreatedAt: created}
	}
	return out
}

// Session Profiles

func (s *Server) listSessionProfiles(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profiles, err := s.store.GetSessionProfiles(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(profiles)
}

func (s *Server) listSessionHotkeys(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profileIDs, err := s.store.GetOrderedProfileIDs(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hotkeys, err := s.store.LoadHotkeysForProfiles(profileIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result := make([]session.HotkeyJSON, 0, len(hotkeys))
	for _, hk := range hotkeys {
		result = append(result, session.HotkeyJSON{
			Shortcut:    hk.Shortcut,
			Command:     hk.Command,
			MobileRow:   hk.MobileRow,
			MobileOrder: hk.MobileOrder,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) listSessionGroups(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profileIDs, err := s.store.GetOrderedProfileIDs(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(profileIDs) == 0 {
		profiles, _ := s.store.ListProfiles()
		for _, p := range profiles {
			profileIDs = append(profileIDs, p.ID)
		}
	}

	var allGroups []storage.UnifiedGroupSummary
	seen := make(map[string]int) // name -> index in allGroups

	for _, pid := range profileIDs {
		groups, err := s.store.ListUnifiedGroups(pid)
		if err != nil {
			continue
		}
		for _, g := range groups {
			if idx, ok := seen[g.GroupName]; !ok {
				seen[g.GroupName] = len(allGroups)
				allGroups = append(allGroups, g)
			} else {
				allGroups[idx].TotalCount += g.TotalCount
				allGroups[idx].EnabledCount += g.EnabledCount
				allGroups[idx].DisabledCount += g.DisabledCount
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allGroups)
}

func (s *Server) toggleSessionGroup(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := chi.URLParam(r, "name")

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	profileIDs, err := s.store.GetOrderedProfileIDs(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Same fallback as listSessionGroups: fresh session with no profiles
	// should be able to toggle groups from all available profiles.
	if len(profileIDs) == 0 {
		profiles, _ := s.store.ListProfiles()
		for _, p := range profiles {
			profileIDs = append(profileIDs, p.ID)
		}
	}

	for _, pid := range profileIDs {
		_ = s.store.SetUnifiedGroupEnabled(pid, name, req.Enabled)
	}

	if sess != nil {
		sess.NotifySettingsChanged("aliases")
		sess.NotifySettingsChanged("triggers")
		sess.NotifySettingsChanged("highlights")
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) addProfileToSession(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		ProfileID  int64 `json:"profile_id"`
		OrderIndex int   `json:"order_index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.AddProfileToSession(id, req.ProfileID, req.OrderIndex); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("profiles")
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) reorderSessionProfiles(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req []storage.ProfileOrder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.ReorderSessionProfiles(id, req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("profiles")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeProfileFromSession(w http.ResponseWriter, r *http.Request) {
	sess, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profileID, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.RemoveProfileFromSession(id, profileID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess != nil {
		sess.NotifySettingsChanged("profiles")
	}
	w.WriteHeader(http.StatusNoContent)
}

// Profiles CRUD

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(profiles)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var req storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := s.store.CreateProfile(req.Name, req.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.ID = pid
	if err := s.store.UpdateProfile(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "profiles")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteProfile(pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "profiles")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Aliases

func (s *Server) listProfileAliases(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	aliases, err := s.store.ListAliases(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(aliases)
}

func (s *Server) createProfileAlias(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveAlias(pid, a.Name, a.Template, a.Enabled, a.GroupName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "aliases")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProfileAlias(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid alias id", http.StatusBadRequest)
		return
	}

	var a storage.AliasRule
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.ID = id
	a.ProfileID = pid
	if err := s.store.UpdateAlias(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "aliases")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileAlias(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid alias id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteAliasByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "aliases")
	w.WriteHeader(http.StatusNoContent)
}

type profileTimerResponse struct {
	ProfileID     int64                              `json:"profile_id"`
	Name          string                             `json:"name"`
	Icon          string                             `json:"icon"`
	CycleMS       int                                `json:"cycle_ms"`
	RepeatMode    string                             `json:"repeat_mode"`
	Subscriptions []storage.ProfileTimerSubscription `json:"subscriptions"`
}

func (s *Server) listProfileTimers(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	timers, err := s.store.GetProfileTimers(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]profileTimerResponse, 0, len(timers))
	for _, timer := range timers {
		subs, err := s.store.GetProfileTimerSubscriptions(pid, timer.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if subs == nil {
			subs = []storage.ProfileTimerSubscription{}
		}
		result = append(result, profileTimerResponse{
			ProfileID:     timer.ProfileID,
			Name:          timer.Name,
			Icon:          timer.Icon,
			CycleMS:       timer.CycleMS,
			RepeatMode:    timer.RepeatMode,
			Subscriptions: subs,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) createOrUpdateProfileTimer(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var timer storage.ProfileTimer
	if err := json.NewDecoder(r.Body).Decode(&timer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	timer.ProfileID = pid

	if timer.Name == "" {
		http.Error(w, "timer name is required", http.StatusBadRequest)
		return
	}
	if timer.CycleMS <= 0 {
		http.Error(w, "cycle_ms must be > 0", http.StatusBadRequest)
		return
	}
	if timer.RepeatMode == "" {
		timer.RepeatMode = "repeating"
	}
	if timer.RepeatMode != "repeating" && timer.RepeatMode != "one_shot" {
		http.Error(w, "repeat_mode must be 'repeating' or 'one_shot'", http.StatusBadRequest)
		return
	}

	if err := s.store.SaveProfileTimer(timer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.notifyProfileChanged(pid, "timers")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteProfileTimer(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := chi.URLParam(r, "name")

	if err := s.store.DeleteProfileTimer(pid, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.store.ClearProfileTimerSubscriptions(pid, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.notifyProfileChanged(pid, "timers")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createOrUpdateProfileTimerSubscription(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := chi.URLParam(r, "name")

	var sub storage.ProfileTimerSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sub.ProfileID = pid
	sub.TimerName = name

	if sub.Second < 0 {
		http.Error(w, "second must be >= 0", http.StatusBadRequest)
		return
	}
	if sub.SortOrder < 0 {
		http.Error(w, "sort_order must be >= 0", http.StatusBadRequest)
		return
	}
	if sub.IsBulk {
		sub.IsRemoval = true
		sub.Command = ""
	}
	if sub.IsRemoval {
		if !sub.IsBulk && sub.Command == "" {
			http.Error(w, "command is required for exact removal", http.StatusBadRequest)
			return
		}
	} else {
		if sub.Command == "" {
			http.Error(w, "command is required for subscription", http.StatusBadRequest)
			return
		}
		sub.IsBulk = false
	}

	if err := s.store.SaveProfileTimerSubscription(sub); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.notifyProfileChanged(pid, "timers")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteProfileTimerSubscription(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := chi.URLParam(r, "name")
	secondStr := chi.URLParam(r, "second")
	sortOrderStr := chi.URLParam(r, "sortOrder")

	second, err := strconv.Atoi(secondStr)
	if err != nil {
		http.Error(w, "invalid second", http.StatusBadRequest)
		return
	}
	sortOrder, err := strconv.Atoi(sortOrderStr)
	if err != nil {
		http.Error(w, "invalid sortOrder", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteProfileTimerSubscription(pid, name, second, sortOrder); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.notifyProfileChanged(pid, "timers")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Triggers

func (s *Server) listProfileTriggers(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	triggers, err := s.store.ListTriggers(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(triggers)
}

func (s *Server) createProfileTrigger(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.ProfileID = pid
	if err := s.store.CreateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "triggers")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProfileTrigger(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid trigger id", http.StatusBadRequest)
		return
	}

	var t storage.TriggerRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.ID = id
	t.ProfileID = pid
	if err := s.store.UpdateTrigger(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "triggers")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileTrigger(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid trigger id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteTriggerByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "triggers")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Highlights

func (s *Server) listProfileHighlights(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	highlights, err := s.store.ListHighlights(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(highlights)
}

func (s *Server) createProfileHighlight(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.ProfileID = pid
	if err := s.store.CreateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "highlights")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProfileHighlight(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid highlight id", http.StatusBadRequest)
		return
	}

	var h storage.HighlightRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.ID = id
	h.ProfileID = pid
	if err := s.store.UpdateHighlight(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "highlights")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileHighlight(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid highlight id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteHighlightByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "highlights")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Substitutions

func (s *Server) listProfileSubs(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	subs, err := s.store.ListSubstitutes(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(subs)
}

func (s *Server) createProfileSub(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var sub storage.SubstituteRule
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sub.ProfileID = pid
	if err := s.store.CreateSubstitute(sub); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "subs")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProfileSub(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid sub id", http.StatusBadRequest)
		return
	}

	var sub storage.SubstituteRule
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sub.ID = id
	sub.ProfileID = pid
	if err := s.store.UpdateSubstitute(sub); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "subs")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileSub(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid sub id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteSubstituteByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "subs")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Hotkeys

func (s *Server) listProfileHotkeys(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hotkeys, err := s.store.ListHotkeys(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hotkeys)
}

func (s *Server) createProfileHotkey(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var h storage.HotkeyRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = s.store.CreateHotkey(pid, h.Shortcut, h.Command, h.MobileRow, h.MobileOrder)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "hotkeys")
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProfileHotkey(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid hotkey id", http.StatusBadRequest)
		return
	}

	var h storage.HotkeyRule
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.ID = id
	h.ProfileID = pid
	if err := s.store.UpdateHotkey(h); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "hotkeys")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileHotkey(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid hotkey id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteHotkeyByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "hotkeys")
	w.WriteHeader(http.StatusNoContent)
}

// Profile Groups

func (s *Server) listProfileGroups(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	groups, err := s.store.ListUnifiedGroups(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

func (s *Server) toggleProfileGroup(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		GroupName string `json:"group_name"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SetUnifiedGroupEnabled(pid, strings.TrimSpace(payload.GroupName), payload.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.notifyProfileChanged(pid, "aliases")
	s.notifyProfileChanged(pid, "triggers")
	s.notifyProfileChanged(pid, "highlights")
	s.notifyProfileChanged(pid, "subs")
	w.WriteHeader(http.StatusNoContent)
}

// Sessions Base Methods

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
		Name      string `json:"name"`
		Host      string `json:"mud_host"`
		Port      int    `json:"mud_port"`
		AnsiTheme string `json:"ansi_theme"`
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
	if payload.AnsiTheme != "" {
		record.AnsiTheme = storage.NormalizeAnsiTheme(payload.AnsiTheme)
		if err := s.manager.UpdateSession(record); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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

// Websockets

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
		ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
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

	sessIDStr := r.URL.Query().Get("session_id")
	clientTZ := r.URL.Query().Get("tz")
	if sessIDStr != "" {
		if id, err := strconv.ParseInt(sessIDStr, 10, 64); err == nil {
			if sess, ok := s.manager.GetSession(id); ok {
				currentSess = sess
			}
		}
	}

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

	if currentSess != nil {
		currentSess.ApplyClientTimezone(clientTZ)
		if err := s.sendRestoreState(currentSess, writeJSON); err != nil {
			log.Printf("websocket restore state failed: %v", err)
			return
		}
		clientID = currentSess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
			if err := writeJSON(msg); err != nil {
				_ = ws.Close()
				return err
			}
			return nil
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
					currentSess.ApplyClientTimezone(clientTZ)
					if err := writeJSON(session.ServerMsg{Type: "status", Status: "connected"}); err != nil {
						log.Printf("websocket status send failed: %v", err)
						return
					}
					if err := s.sendRestoreState(sess, writeJSON); err != nil {
						log.Printf("websocket restore state failed: %v", err)
						return
					}
					clientID = sess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
						if err := writeJSON(msg); err != nil {
							_ = ws.Close()
							return err
						}
						return nil
					})
				} else if err := writeJSON(session.ServerMsg{Type: "status", Status: "disconnected"}); err != nil {
					log.Printf("websocket status send failed: %v", err)
					return
				}
			} else if err := writeJSON(session.ServerMsg{Type: "status", Status: "no session"}); err != nil {
				log.Printf("websocket status send failed: %v", err)
				return
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

			command := message.Value
			source := strings.TrimSpace(message.Source)

			// Only trim if it's not a direct empty input intent
			if command != "" || source != "input" {
				command = strings.TrimSpace(command)
			}

			if command == "" && source != "input" {
				continue
			}

			commands, err := currentSess.SendCommandWithTrace(command, source)
			if err != nil {
				log.Printf("send command failed: %v", err)
				return
			}
			if message.ClientCommandID != "" {
				if err := writeJSON(session.ServerMsg{Type: "command_trace", ClientCommandID: message.ClientCommandID, Commands: commands}); err != nil {
					log.Printf("websocket command trace send failed: %v", err)
					return
				}
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
	restoreCursor, err := s.store.LatestVisibleLogID(sess.SessionID())
	if err != nil {
		return err
	}
	if s.restoreAfterCursorHook != nil {
		if err := s.restoreAfterCursorHook(sess); err != nil {
			return err
		}
	}

	logsPerBuffer, err := sess.RecentLogsPerBuffer(500)
	if err != nil {
		return err
	}

	history, err := sess.RecentInputHistory(500)
	if err != nil {
		return err
	}

	buffers := make(map[string][]session.ClientLogEntry, len(logsPerBuffer))
	for bufName, logs := range logsPerBuffer {
		entries := make([]session.ClientLogEntry, 0, len(logs))
		for _, entry := range logs {
			cle := clientLogEntryForSession(sess, entry)
			if cle.Buffer == "" {
				cle.Buffer = bufName
			}
			entries = append(entries, cle)
		}
		buffers[bufName] = entries
	}

	variables, err := sess.CurrentVariables()
	if err != nil {
		return err
	}

	// Include current timers
	timers := sess.TimerSnapshots()

	begin := session.ServerMsg{
		Type:          "restore_begin",
		History:       history,
		Variables:     variables,
		Buffers:       buffers,
		Timers:        timers,
		RestoreCursor: &restoreCursor,
	}

	profileIDs, _ := s.store.GetOrderedProfileIDs(sess.SessionID())
	hotkeys, _ := s.store.LoadHotkeysForProfiles(profileIDs)
	for _, hk := range hotkeys {
		begin.Hotkeys = append(begin.Hotkeys, session.HotkeyJSON{
			Shortcut:    hk.Shortcut,
			Command:     hk.Command,
			MobileRow:   hk.MobileRow,
			MobileOrder: hk.MobileOrder,
		})
	}

	if err := writeJSON(begin); err != nil {
		return err
	}

	return writeJSON(session.ServerMsg{Type: "restore_end"})
}

func clientLogEntryForSession(sess *session.Session, entry storage.LogEntry) session.ClientLogEntry {
	text := entry.DisplayRawText()
	if sess != nil {
		text = sess.RenderLogEntry(entry)
	}
	cle := session.ClientLogEntry{
		ID:            entry.ID,
		Text:          text,
		Buffer:        entry.Buffer,
		Commands:      entry.Commands,
		BellPositions: session.BellPositionsFromOverlays(entry.Overlays),
	}
	for _, b := range entry.Buttons {
		cle.Buttons = append(cle.Buttons, session.ButtonOverlay{Label: b.Label, Command: b.Command})
	}
	return cle
}

// Profile Variables

func (s *Server) listProfileVariables(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vars, err := s.store.ListProfileVariables(pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vars)
}

func (s *Server) createProfileVariable(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var v storage.ProfileVariable
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	res, err := s.store.CreateProfileVariable(pid, v.Name, v.DefaultValue, v.Description)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidVariableName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "variables")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(res)
}

func (s *Server) updateProfileVariable(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid variable id", http.StatusBadRequest)
		return
	}

	var v storage.ProfileVariable
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v.ID = id
	v.ProfileID = pid
	v.Name = strings.TrimSpace(v.Name)
	if err := s.store.UpdateProfileVariable(v); err != nil {
		if errors.Is(err, storage.ErrInvalidVariableName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, storage.ErrProfileVariableNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "variables")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfileVariable(w http.ResponseWriter, r *http.Request) {
	pid, err := s.getProfileID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid variable id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteProfileVariable(id, pid); err != nil {
		if errors.Is(err, storage.ErrProfileVariableNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "variables")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listResolvedVariables(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vars, err := s.store.ListResolvedVariablesForSession(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vars)
}
