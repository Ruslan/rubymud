package web

import (
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"rubymud/go/internal/colorreg"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

const restoreChunkSize = 100

//go:embed static/*
var webFiles embed.FS

type Server struct {
	httpServer                    *http.Server
	manager                       *session.Manager
	store                         *storage.Store
	upgrader                      websocket.Upgrader
	apiToken                      string
	webAuthRuntime                *webAuthRuntime
	webListener                   WebListenerConfig
	mcp                           MCPConfig
	mcpRuntime                    *mcpRuntime
	mcpSessions                   *mcpSessionStore
	mcpTokenRandom                io.Reader
	mcpSettingsUnsubscribe        func()
	webAuthSettingsUnsubscribe    func()
	configDir                     string
	roomImagesRoot                string
	mapSetDeleteMu                sync.Mutex
	mapSetDeleteAfterLock         func() // test hook: runs while delete holds both locks
	mapSetDeleteBeforeStoreDelete func() // test hook: runs after image staging
	restoreAfterCursorHook        func(*session.Session) error
	mapRecordingTryStop           func(*session.Session) (session.MapRecordingState, bool) // test seam
}

// MCPConfig holds command-line/environment overrides for MCP. Enabled and Token
// retain their old meanings for callers constructing MCPConfig{Enabled: true};
// the Set fields distinguish explicit false/empty command-line overrides.
type MCPConfig struct {
	Enabled    bool
	Token      string
	EnabledSet bool
	TokenSet   bool
}

// ValidateMCPConfig preserves validation for legacy callers which supply a
// complete MCPConfig directly. Explicit CLI/environment state can depend on a
// persisted digest and is reconciled after storage is loaded by New.
func ValidateMCPConfig(listenAddr string, config MCPConfig) error {
	config = config.normalized()
	if !config.Enabled {
		return nil
	}
	if config.Token != "" {
		if !validBearerToken(config.Token) {
			return errors.New("MCP token is not a valid Bearer token")
		}
		return nil
	}
	// Explicit command-line/environment enabled state is independent of token
	// ownership and may use a persisted digest, which is loaded by New.
	if config.EnabledSet {
		return nil
	}

	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("invalid HTTP listen address for tokenless MCP: %w", err)
	}
	if isLoopbackHost(host) {
		return nil
	}
	return errors.New("tokenless MCP requires an explicit loopback --listen address")
}

func validBearerToken(token string) bool {
	padding := false
	payload := false
	for i := 0; i < len(token); i++ {
		c := token[i]
		if c == '=' {
			if !payload {
				return false
			}
			padding = true
			continue
		}
		if padding {
			return false
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~+/", rune(c))) {
			return false
		}
		payload = true
	}
	return payload
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	if !strings.EqualFold(host, "localhost") {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

// validateMCPListenerAddr closes the DNS/TOCTOU gap left by validating a
// configured hostname before net.Listen resolves it.
func validateMCPListenerAddr(addr net.Addr, config MCPConfig) error {
	config = config.normalized()
	if !config.Enabled || config.Token != "" {
		return nil
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || tcpAddr.IP == nil || !tcpAddr.IP.IsLoopback() {
		return errors.New("tokenless MCP listener did not bind to a loopback address")
	}
	return nil
}

// BasicAuth holds the optional operator override for database-managed web
// access credentials. MCP has its own Bearer credential because both schemes
// cannot share one Authorization header.
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

// New preserves the package's historical constructor for in-process callers.
// The executable uses NewWithWebListenerConfig so startup can distinguish a
// saved listener from an explicit --listen operator override.
func New(listenAddr string, manager *session.Manager, store *storage.Store, configDir string, basicAuth BasicAuth, mcp MCPConfig) (*Server, error) {
	return newServer(WebListenerConfig{CurrentAddr: listenAddr, ConfiguredAddr: listenAddr}, manager, store, configDir, basicAuth, mcp)
}

// NewWithWebListenerConfig creates the production server with resolved
// current and next-start listener ownership. The final exposure check happens
// after net.Listen resolves the actual bound address in ListenAndServe.
func NewWithWebListenerConfig(listener WebListenerConfig, manager *session.Manager, store *storage.Store, configDir string, basicAuth BasicAuth, mcp MCPConfig) (*Server, error) {
	if err := ValidateWebListenAddr(listener.CurrentAddr); err != nil {
		return nil, fmt.Errorf("current web listener: %w", err)
	}
	if err := ValidateWebListenAddr(listener.ConfiguredAddr); err != nil {
		return nil, fmt.Errorf("configured web listener: %w", err)
	}
	return newServer(listener, manager, store, configDir, basicAuth, mcp)
}

func newServer(listener WebListenerConfig, manager *session.Manager, store *storage.Store, configDir string, basicAuth BasicAuth, mcp MCPConfig) (*Server, error) {
	listenAddr := listener.CurrentAddr
	persistedWebAuth, err := store.GetWebAuthSettings()
	if err != nil {
		return nil, fmt.Errorf("load web access settings: %w", err)
	}
	webAuthRuntime, err := newWebAuthRuntime(persistedWebAuth, basicAuth)
	if err != nil {
		return nil, fmt.Errorf("web access configuration: %w", err)
	}
	mcp = mcp.normalized()
	// Preserve the established operator-facing startup failure for the legacy
	// explicit Enabled:true form. Database-owned state is instead reconciled
	// fail-closed so the Settings UI remains available to repair it.
	if mcp.Enabled && !mcp.EnabledSet && !mcp.TokenSet && mcp.Token == "" {
		if err := ValidateMCPConfig(listenAddr, mcp); err != nil {
			return nil, err
		}
	}
	persistedMCP, err := store.GetMCPSettings()
	if err != nil {
		return nil, fmt.Errorf("load MCP settings: %w", err)
	}
	runtime, err := newMCPRuntime(listenAddr, persistedMCP, mcp)
	if err != nil {
		return nil, err
	}
	log.Printf("creating web server on %s", listenAddr)

	apiToken, _ := store.GetSetting("api_token")
	if apiToken == "" {
		tokenBytes := make([]byte, 16)
		rand.Read(tokenBytes)
		apiToken = hex.EncodeToString(tokenBytes)
		_ = store.SetSetting("api_token", apiToken)
	}

	s := &Server{
		manager:        manager,
		store:          store,
		configDir:      configDir,
		roomImagesRoot: store.RoomImagesRoot(),
		apiToken:       apiToken,
		webAuthRuntime: webAuthRuntime,
		webListener:    listener,
		mcp:            mcp,
		mcpRuntime:     runtime,
		mcpSessions:    newMCPSessionStore(),
		mcpTokenRandom: rand.Reader,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return sameOriginRequest(r)
			},
		},
	}
	s.mcpSettingsUnsubscribe = store.SubscribeMCPSettings(func(settings storage.MCPSettings) {
		_ = s.applyPersistedMCPSettings(settings)
	})
	s.webAuthSettingsUnsubscribe = store.SubscribeWebAuthSettings(func(settings storage.WebAuthSettings) {
		_ = s.webAuthRuntime.replacePersisted(settings)
	})
	// Close the read/subscribe race: a write missed before registration is seen
	// here, while revision ordering makes a concurrent callback win over stale data.
	currentMCP, err := store.GetMCPSettings()
	if err != nil {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		return nil, fmt.Errorf("reload MCP settings after subscribe: %w", err)
	}
	if err := s.applyPersistedMCPSettings(currentMCP); err != nil {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		return nil, err
	}
	currentWebAuth, err := store.GetWebAuthSettings()
	if err != nil {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		return nil, fmt.Errorf("reload web access settings after subscribe: %w", err)
	}
	if err := s.webAuthRuntime.replacePersisted(currentWebAuth); err != nil {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		return nil, err
	}
	if err := s.recoverMapSetImageTombstones(); err != nil {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		return nil, fmt.Errorf("recover map-set image cleanup: %w", err)
	}

	r := chi.NewRouter()
	// MCP credentials belong only in Authorization. Reject any query before the
	// request logger can record a client accidentally putting a secret in the URL.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/mcp" && r.URL.RawQuery != "" {
				if !s.mcpRuntime.snapshotValue().enabled {
					http.NotFound(w, r)
					return
				}
				http.Error(w, "MCP endpoint does not accept query parameters", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Keep a disabled /mcp indistinguishable from any other missing route, even
	// when HTTP basic auth is enabled. The catch-all index handler must not turn
	// this into a successful HTML response.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.URL.Path == "/mcp" && !s.mcpRuntime.snapshotValue().enabled) || strings.HasPrefix(r.URL.Path, "/mcp/") {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// HTTP basic auth is a runtime gate so Settings changes take effect without
	// rebuilding the router. PWA assets remain public; MCP uses its own Bearer
	// credential because both schemes cannot share Authorization.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/mcp" || isPublicAsset(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !s.webAuthRuntime.authenticate(user, pass, ok) {
				w.Header().Set("WWW-Authenticate", `Basic realm="mudhost", charset="UTF-8"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

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
	r.Handle("/mcp", http.HandlerFunc(s.handleMCP))

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
					r.Get("/export-html", s.exportLogHTML)
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

				// Mapper: which map set this session uses, and manual re-anchor.
				r.Get("/active-map-set", s.getActiveMapSet)
				r.Post("/active-map-set", s.setActiveMapSet)
				r.Post("/map-anchor", s.anchorMapPosition)
				r.Post("/map-path", s.mapPath)
				r.Post("/map-walk", s.mapWalk)
				r.Post("/map-walk/cancel", s.cancelMapWalk)
				r.Post("/map-walk/resume", s.resumeMapWalk)
				r.Post("/map-cell-patch", s.mapCellPatch)
				r.Get("/map-live-room", s.mapLiveRoom)
				r.Post("/map-record-room", s.mapRecordRoom)
				r.Get("/map-recording-state", s.mapRecordingState)
				r.Post("/map-recording-start", s.mapRecordingStart)
				r.Post("/map-recording-stop", s.mapRecordingStop)
				r.Post("/map-reconciliation/preview", s.mapReconciliationPreview)
				r.Post("/map-reconciliation/apply", s.mapReconciliationApply)
				r.Post("/map-mark-edge", s.mapMarkEdge)
				r.Post("/map-edge-attrs", s.mapEdgeAttrs)
				r.Post("/map-portals", s.mapPortal)
				r.Patch("/map-portals", s.mapPortal)
				r.Delete("/map-portals", s.mapPortal)
				r.Post("/map-zones/preview", s.mapZoneMove)
				r.Post("/map-zones/apply", s.mapZoneMove)
				r.Post("/map-zones/rename", s.mapZoneMove)
				r.Post("/map-zones/rename/preview", s.mapZoneMove)
				r.Post("/map-zones/rename/apply", s.mapZoneMove)
				r.Post("/map-undo", s.mapUndo)

				// Which ROOM FORMAT (mapper profile) this session parses with.
				// Unrelated to /sessions/{id}/profiles above (alias/trigger layering).
				r.Get("/mapper-profile", s.getSessionMapperProfile)
				r.Post("/mapper-profile", s.setSessionMapperProfile)
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

		r.Route("/map-sets", func(r chi.Router) {
			r.Get("/", s.listMapSets)
			r.Post("/import", s.importMapSet)
			r.Get("/{id}/zones", s.listMapSetZones)
			r.Get("/{id}/rooms/search", s.searchMapRooms)
			r.Get("/{id}/inhabitants", s.listRoomInhabitants)
			r.Post("/{id}/inhabitants", s.createRoomInhabitant)
			r.Patch("/{id}/inhabitants/{inhabitantID}", s.updateRoomInhabitant)
			r.Delete("/{id}/inhabitants/{inhabitantID}", s.deleteRoomInhabitant)
			r.Delete("/{id}", s.deleteMapSet)
		})
		r.Get("/rooms", s.listRooms)

		// Room-format profiles. NOTE the name: these describe how to PARSE a room
		// block, and have nothing to do with /api/profiles (alias/trigger layering).
		r.Route("/mapper-profiles", func(r chi.Router) {
			r.Get("/", s.listMapperProfiles)
			r.Post("/", s.createMapperProfile)
			// Literal actions are registered before /{id} so chi cannot route them as ids.
			r.Post("/validate", s.validateMapperProfileSpec)
			r.Post("/replay", s.replayMapperProfileSpec)
			r.Put("/{id}", s.updateMapperProfile)
			r.Delete("/{id}", s.deleteMapperProfile)
		})

		r.Route("/app", func(r chi.Router) {
			r.Get("/settings", s.getAppSettings)
			r.Put("/settings", s.updateAppSettings)
			r.Post("/settings/rotate-api-token", s.rotateAPIToken)
		})
		r.Route("/mcp", func(r chi.Router) {
			r.Get("/settings", s.getMCPSettings)
			r.Post("/settings", s.updateMCPSettings)
			r.Patch("/settings", s.updateMCPSettings)
			r.Post("/settings/token", s.rotateMCPToken)
		})
		r.Route("/web-auth", func(r chi.Router) {
			r.Get("/settings", s.getWebAuthSettings)
			r.Patch("/settings", s.updateWebAuthSettings)
		})
	})

	r.Handle("/*", http.HandlerFunc(s.handleIndex))

	s.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: r,
	}
	s.httpServer.RegisterOnShutdown(func() {
		s.mcpSettingsUnsubscribe()
		s.webAuthSettingsUnsubscribe()
		s.mcpSessions.clear()
	})

	return s, nil
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	if err := validateWebAuthListenerAddr(listener.Addr(), s.webAuthRuntime.status().EffectiveEnabled); err != nil {
		_ = listener.Close()
		return err
	}
	gate := s.mcpRuntime.snapshotValue()
	listenerConfig := MCPConfig{Enabled: gate.enabled}
	if gate.hasToken {
		listenerConfig.Token = "configured"
	}
	if err := validateMCPListenerAddr(listener.Addr(), listenerConfig); err != nil {
		_ = listener.Close()
		return err
	}
	err = s.httpServer.Serve(listener)
	s.mcpSettingsUnsubscribe()
	s.webAuthSettingsUnsubscribe()
	s.mcpSessions.clear()
	return err
}

// Close releases ephemeral MCP continuity state along with the HTTP listener.
func (s *Server) Close() error {
	s.mcpSettingsUnsubscribe()
	s.webAuthSettingsUnsubscribe()
	s.mcpSessions.clear()
	return s.httpServer.Close()
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

type mcpSettingsJSON struct {
	Enabled                  bool   `json:"enabled"`
	RequestedEnabled         bool   `json:"requested_enabled"`
	EffectiveEnabled         bool   `json:"effective_enabled"`
	TokenConfigured          bool   `json:"token_configured"`
	ExternallyManaged        bool   `json:"externally_managed"`
	ExternallyManagedReason  string `json:"externally_managed_reason,omitempty"`
	EnabledExternallyManaged bool   `json:"enabled_externally_managed"`
	TokenExternallyManaged   bool   `json:"token_externally_managed"`
	ListenerExposure         string `json:"listener_exposure"`
	Token                    string `json:"token,omitempty"`
}

type mcpSettingsUpdateJSON struct {
	Enabled *bool `json:"enabled"`
}

type webAuthSettingsJSON struct {
	Enabled                   bool   `json:"enabled"`
	RequestedEnabled          bool   `json:"requested_enabled"`
	EffectiveEnabled          bool   `json:"effective_enabled"`
	Username                  string `json:"username"`
	CredentialConfigured      bool   `json:"credential_configured"`
	ExternallyManaged         bool   `json:"externally_managed"`
	ExternallyManagedReason   string `json:"externally_managed_reason,omitempty"`
	CurrentListener           string `json:"current_listener"`
	CurrentExposure           string `json:"current_exposure"`
	ConfiguredListener        string `json:"configured_listener"`
	ConfiguredExposure        string `json:"configured_exposure"`
	ListenerExternallyManaged bool   `json:"listener_externally_managed"`
	RestartRequired           bool   `json:"restart_required"`
}

type webAuthSettingsUpdateJSON struct {
	Enabled    *bool   `json:"enabled"`
	Username   *string `json:"username"`
	Password   *string `json:"password"`
	ListenAddr *string `json:"listen_addr"`
}

const webAuthAdminBodyLimit = 4096

func (s *Server) getWebAuthSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.webAuthSettingsStatus())
}

func (s *Server) webAuthSettingsStatus() webAuthSettingsJSON {
	status := s.webAuthRuntime.status()
	persisted := s.webAuthRuntime.persistedValue()
	configured := persisted.ListenAddr
	if configured == "" {
		configured = DefaultWebListenAddr
	}
	status.CurrentListener = s.webListener.CurrentAddr
	status.CurrentExposure = WebListenerExposure(s.webListener.CurrentAddr)
	status.ConfiguredListener = configured
	status.ConfiguredExposure = WebListenerExposure(configured)
	status.ListenerExternallyManaged = s.webListener.ExternallyManaged
	status.RestartRequired = status.CurrentListener != status.ConfiguredListener
	return status
}

func decodeWebAuthSettingsUpdate(w http.ResponseWriter, r *http.Request, update *webAuthSettingsUpdateJSON) (int, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, webAuthAdminBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(update); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, errors.New("request body is too large")
		}
		return http.StatusBadRequest, errors.New("invalid web access settings payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, errors.New("request body is too large")
		}
		return http.StatusBadRequest, errors.New("request body must contain one JSON object")
	}
	if update.Enabled == nil {
		return http.StatusBadRequest, errors.New("enabled is required")
	}
	if (update.Username == nil) != (update.Password == nil) {
		return http.StatusBadRequest, errors.New("username and password must be provided together")
	}
	return 0, nil
}

func validateWebAuthCredentials(username, password string) error {
	if username == "" || username != strings.TrimSpace(username) {
		return errors.New("username must not be empty or have surrounding whitespace")
	}
	if len(username) > 128 {
		return errors.New("username must be at most 128 bytes")
	}
	if strings.ContainsRune(username, ':') || strings.ContainsAny(username, "\r\n\x00") {
		return errors.New("username contains unsupported characters")
	}
	if len(password) < 12 {
		return errors.New("password must be at least 12 bytes")
	}
	if len(password) > 72 {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func (s *Server) updateWebAuthSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateMCPAdminOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var update webAuthSettingsUpdateJSON
	if status, err := decodeWebAuthSettingsUpdate(w, r, &update); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	next := s.webAuthRuntime.persistedValue()
	credentialsExternallyManaged := s.webAuthRuntime.status().ExternallyManaged
	// Environment credentials own only the browser gate. They deliberately do
	// not prevent an operator from repairing the persisted next-start listener.
	if credentialsExternallyManaged && (update.ListenAddr == nil || update.Username != nil || *update.Enabled != next.Enabled) {
		http.Error(w, "web access credentials are managed by startup configuration", http.StatusConflict)
		return
	}
	if update.ListenAddr != nil {
		next.ListenAddr = strings.TrimSpace(*update.ListenAddr)
		if err := ValidateWebListenAddr(next.ListenAddr); err != nil {
			http.Error(w, "invalid web listener: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if update.Username != nil {
		if err := validateWebAuthCredentials(*update.Username, *update.Password); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*update.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "could not secure web access password", http.StatusInternalServerError)
			return
		}
		next.Username = *update.Username
		next.PasswordHash = string(hash)
	}
	if *update.Enabled && (next.Username == "" || next.PasswordHash == "") {
		http.Error(w, "username and password are required before enabling web access protection", http.StatusBadRequest)
		return
	}
	if *update.Enabled && !validWebAuthPasswordHash(next.PasswordHash) {
		http.Error(w, "saved password is invalid; provide a new username and password", http.StatusBadRequest)
		return
	}
	// Disabling must not leave either the currently bound address or the
	// saved next-start address open to the network. Change the listener to
	// local-only, restart, then remove credentials.
	configuredAddr := next.ListenAddr
	if configuredAddr == "" {
		configuredAddr = DefaultWebListenAddr
	}
	effectiveWebAccess := *update.Enabled || credentialsExternallyManaged
	if !effectiveWebAccess && (!webListenerIsLoopback(s.webListener.CurrentAddr) || !webListenerIsLoopback(configuredAddr)) {
		http.Error(w, "move both the current and next-start listener to loopback before disabling web access protection", http.StatusConflict)
		return
	}
	next.Enabled = *update.Enabled
	if _, err := s.store.SaveWebAuthSettings(next); err != nil {
		http.Error(w, "could not save web access settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.webAuthSettingsStatus())
}

func (s *Server) getMCPSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.mcpRuntime.status())
}

func newMCPToken(random io.Reader) (string, error) {
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (s *Server) generateMCPToken(excludedDigest string) (string, error) {
	for range 4 {
		token, err := newMCPToken(s.mcpTokenRandom)
		if err != nil {
			return "", err
		}
		if mcpDigest(token) != excludedDigest {
			return token, nil
		}
	}
	return "", errors.New("could not generate a distinct MCP credential")
}

func mcpDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Server) applyPersistedMCPSettings(settings storage.MCPSettings) error {
	changed, err := s.mcpRuntime.replacePersisted(settings)
	if err != nil {
		return err
	}
	if changed {
		s.mcpSessions.invalidate()
	}
	return nil
}

const mcpAdminBodyLimit = 4096

func validateMCPAdminOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	return len(values) == 0 || (len(values) == 1 && sameOriginRequest(r))
}

func decodeMCPSettingsUpdate(w http.ResponseWriter, r *http.Request, update *mcpSettingsUpdateJSON) (int, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpAdminBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(update); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, errors.New("request body is too large")
		}
		return http.StatusBadRequest, errors.New("invalid MCP settings payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, errors.New("request body is too large")
		}
		return http.StatusBadRequest, errors.New("request body must contain one JSON object")
	}
	if update.Enabled == nil {
		return http.StatusBadRequest, errors.New("enabled is required")
	}
	return 0, nil
}

func (s *Server) updateMCPSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateMCPAdminOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var update mcpSettingsUpdateJSON
	if status, err := decodeMCPSettingsUpdate(w, r, &update); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if s.mcpRuntime.enabledExternallyManaged() {
		http.Error(w, "MCP enabled state is managed by startup configuration", http.StatusConflict)
		return
	}
	status := s.mcpRuntime.status()
	if *update.Enabled && !status.TokenConfigured && status.ListenerExposure != "loopback" && s.mcpRuntime.tokenExternallyManaged() {
		http.Error(w, "MCP credential is managed by startup configuration and is required for this listener", http.StatusConflict)
		return
	}

	oneTimeToken := ""
	candidateDigest := ""
	if *update.Enabled && !status.TokenConfigured && status.ListenerExposure != "loopback" {
		var err error
		oneTimeToken, err = s.generateMCPToken("")
		if err != nil {
			http.Error(w, "could not generate MCP credential", http.StatusInternalServerError)
			return
		}
		candidateDigest = mcpDigest(oneTimeToken)
	}
	_, candidateUsed, err := s.store.UpdateMCPEnabled(*update.Enabled, candidateDigest)
	if err != nil {
		http.Error(w, "could not save MCP settings", http.StatusInternalServerError)
		return
	}
	if !candidateUsed {
		oneTimeToken = ""
	}
	response := s.mcpRuntime.status()
	response.Token = oneTimeToken
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) rotateMCPToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validateMCPAdminOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	if body, err := io.ReadAll(r.Body); err != nil || len(body) != 0 {
		http.Error(w, "request body must be empty", http.StatusBadRequest)
		return
	}
	if s.mcpRuntime.tokenExternallyManaged() {
		http.Error(w, "MCP credential is managed by startup configuration", http.StatusConflict)
		return
	}
	persistedBefore := s.mcpRuntime.persistedValue()
	token, err := s.generateMCPToken(persistedBefore.TokenDigest)
	if err != nil {
		http.Error(w, "could not generate MCP credential", http.StatusInternalServerError)
		return
	}
	_, err = s.store.RotateMCPTokenDigest(mcpDigest(token))
	if err != nil {
		http.Error(w, "could not save MCP settings", http.StatusInternalServerError)
		return
	}
	response := s.mcpRuntime.status()
	response.Token = token
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
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
		// Attach before taking restore snapshots. A route completion that races
		// restore is then delivered to this client; UI generation checks discard
		// any older active snapshot rather than leaving a phantom Stop button.
		clientID = currentSess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
			if err := writeJSON(msg); err != nil {
				_ = ws.Close()
				return err
			}
			return nil
		})
		if err := s.sendRestoreState(currentSess, writeJSON); err != nil {
			log.Printf("websocket restore state failed: %v", err)
			return
		}
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
					clientID = sess.AttachClient(ws.RemoteAddr().String(), func(msg session.ServerMsg) error {
						if err := writeJSON(msg); err != nil {
							_ = ws.Close()
							return err
						}
						return nil
					})
					if err := s.sendRestoreState(sess, writeJSON); err != nil {
						log.Printf("websocket restore state failed: %v", err)
						return
					}
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
				if trace, nonfatal := mapWalkBarrierTrace(message.ClientCommandID, commands, session.IsMapWalkManualBarrier(err)); nonfatal {
					// This is an expected pacing response, not a broken MUD
					// transport. Resolve the client command explicitly with any
					// already-sent alias prefix, then retain the socket for the
					// stopping map-walk event.
					if trace != nil {
						if err := writeJSON(*trace); err != nil {
							log.Printf("websocket command trace rejection failed: %v", err)
							return
						}
					}
					continue
				}
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

// mapWalkBarrierTrace preserves the already-sent prefix of a multi-command
// input while telling the websocket loop that the movement barrier is nonfatal.
func mapWalkBarrierTrace(clientCommandID string, commands []string, barrier bool) (*session.ServerMsg, bool) {
	if !barrier {
		return nil, false
	}
	if clientCommandID == "" {
		return nil, true
	}
	return &session.ServerMsg{Type: "command_trace", ClientCommandID: clientCommandID, Commands: commands}, true
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
	restoreStarted := time.Now()
	var cursorDuration, logsDuration, historyDuration, variablesDuration, hotkeysDuration, writeDuration time.Duration

	cursorStarted := restoreStarted
	restoreCursor, err := s.store.LatestVisibleLogID(sess.SessionID())
	if err != nil {
		return err
	}
	if s.restoreAfterCursorHook != nil {
		if err := s.restoreAfterCursorHook(sess); err != nil {
			return err
		}
	}
	cursorDuration = time.Since(cursorStarted)

	logsStarted := time.Now()
	logsPerBuffer, err := sess.RecentLogsPerBuffer(500)
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
	logsDuration = time.Since(logsStarted)

	historyStarted := time.Now()
	history, err := sess.RecentInputHistory(500)
	if err != nil {
		return err
	}
	historyDuration = time.Since(historyStarted)

	variablesStarted := time.Now()
	variables, err := sess.CurrentVariables()
	if err != nil {
		return err
	}
	variablesDuration = time.Since(variablesStarted)

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

	hotkeysStarted := time.Now()
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
	hotkeysDuration = time.Since(hotkeysStarted)

	// Measure only the direct writeJSON calls. Snapshot construction and the
	// in-memory profile/room lookups remain outside this stage.
	writeJSONMeasured := func(message session.ServerMsg) error {
		started := time.Now()
		err := writeJSON(message)
		writeDuration += time.Since(started)
		return err
	}
	if err := writeJSONMeasured(begin); err != nil {
		return err
	}

	// Room-format status, sent on EVERY restore (connect and reload).
	//
	// It has to be here, not only broadcast at resolve time, because the resolve
	// almost never has an audience: `make run` passes --mud, so Manager.Connect —
	// and therefore LoadMapperProfile — runs BEFORE the HTTP server listens, with
	// zero clients attached. broadcastMsg and BroadcastEcho both drop on an empty
	// client map, and the echo is not persisted to the log store, so on the
	// owner's actual startup path the only surviving notice of a broken profile
	// was the server log. The same loss happens on any page reload. The session
	// keeps the status in memory, so replaying it at restore covers both.
	if ref, name, warn := sess.MapperProfileStatus(); name != "" || warn != "" {
		if err := writeJSONMeasured(session.ServerMsg{
			Type:               "mapper_profile",
			MapperFormat:       ref,
			MapperProfileID:    sess.MapperProfileRowID(),
			MapperProfileName:  name,
			MapperProfileError: warn,
			MapperDoorOpen:     sess.MapperDoorOpenTemplate(),
		}); err != nil {
			return err
		}
	}

	// The tracker itself is in-memory, so a page reload needs its current
	// position replayed just like the mapper profile above. Build the snapshot
	// under the session lock, then write it after that lock has been released.
	if position, ok := sess.RoomPositionSnapshot(); ok {
		if err := writeJSONMeasured(position); err != nil {
			return err
		}
	}
	recording := sess.MapRecordingState()
	if err := writeJSONMeasured(session.ServerMsg{Type: "map_recording_state", MapRecording: &recording}); err != nil {
		return err
	}
	walk, _ := sess.MapWalkSnapshot()
	// Always replay an explicit stopped snapshot. A transient browser reconnect
	// otherwise retains a cached Stop button when a route completed offline.
	if err := writeJSONMeasured(session.ServerMsg{
		Type:          "map_walk",
		WalkActive:    walk.Active,
		WalkEpoch:     walk.Epoch,
		WalkJobID:     walk.JobID,
		WalkCurrent:   walk.Current,
		WalkTotal:     walk.Total,
		WalkReason:    walk.Reason,
		WalkState:     walk.State,
		WalkCanResume: walk.CanResume,
		WalkRevision:  walk.Revision,
	}); err != nil {
		return err
	}

	if err := writeJSONMeasured(session.ServerMsg{Type: "restore_end"}); err != nil {
		return err
	}

	if total := time.Since(restoreStarted); total >= 50*time.Millisecond {
		log.Printf("[latency] ws_restore session=%d total=%s cursor=%s logs=%s history=%s variables=%s hotkeys=%s write=%s", sess.SessionID(), total, cursorDuration, logsDuration, historyDuration, variablesDuration, hotkeysDuration, writeDuration)
	}

	return nil
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
