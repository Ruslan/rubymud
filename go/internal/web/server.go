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
	upgrader   websocket.Upgrader
	apiToken   string
	configDir  string
}

type clientMessage struct {
	Method string `json:"method"`
	Value  string `json:"value"`
	Source string `json:"source"`
	SessID int64  `json:"sess_id"`
}

func New(listenAddr string, manager *session.Manager, store *storage.Store, configDir string) *Server {
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
				r.Route("/hotkeys", func(r chi.Router) {
					r.Get("/", s.listProfileHotkeys)
					r.Post("/", s.createProfileHotkey)
					r.Route("/{id}", func(r chi.Router) {
						r.Put("/", s.updateProfileHotkey)
						r.Delete("/", s.deleteProfileHotkey)
					})
				})
				r.Route("/groups", func(r chi.Router) {
					r.Get("/", s.listProfileGroups)
					r.Post("/toggle", s.toggleProfileGroup)
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

// Session History

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	history, err := s.store.ListHistory(id, 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteAliasByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "aliases")
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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteHighlightByID(id, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notifyProfileChanged(pid, "highlights")
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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
	id, _ := strconv.ParseInt(idStr, 10, 64)

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
			
			command := message.Value
			source := strings.TrimSpace(message.Source)
			
			// Only trim if it's not a direct empty input intent
			if command != "" || source != "input" {
				command = strings.TrimSpace(command)
			}
			
			if command == "" && source != "input" {
				continue
			}

			if err := currentSess.SendCommand(command, source); err != nil {
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
			cle := session.ClientLogEntry{ID: entry.ID, Text: sess.HighlightText(entry.RawText), Buffer: bufName, Commands: entry.Commands}
			for _, b := range entry.Buttons {
				cle.Buttons = append(cle.Buttons, session.ButtonOverlay{Label: b.Label, Command: b.Command})
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
		Type:      "restore_begin",
		History:   history,
		Variables: variables,
		Buffers:   buffers,
		Timers:    timers,
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
	res, err := s.store.CreateProfileVariable(pid, v.Name, v.DefaultValue, v.Description)
	if err != nil {
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
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var v storage.ProfileVariable
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v.ID = id
	v.ProfileID = pid
	if err := s.store.UpdateProfileVariable(v); err != nil {
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
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := s.store.DeleteProfileVariable(id, pid); err != nil {
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
