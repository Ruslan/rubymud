package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rubymud/go/internal/storage"
)

type blockingMCPBody struct {
	started chan struct{}
	release chan struct{}
	body    *strings.Reader
}

func (b *blockingMCPBody) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return b.body.Read(p)
}

func (*blockingMCPBody) Close() error { return nil }

func TestMCPAdminEnableUnsafeListenerGeneratesOneTimeToken(t *testing.T) {
	s, _ := setupTestServer(t)
	server, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(ts.Close)

	get := func() map[string]any {
		req, err := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/mcp/settings", nil, server.apiToken)
		if err != nil {
			t.Fatalf("GET request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET settings: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET Cache-Control = %q, want no-store", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GET: %v", err)
		}
		return payload
	}

	initial := get()
	if initial["enabled"] != false || initial["token_configured"] != false {
		t.Fatalf("initial settings = %#v, want disabled and tokenless", initial)
	}

	req, err := newAuthenticatedRequest(http.MethodPatch, ts.URL+"/api/mcp/settings", bytes.NewBufferString(`{"enabled":true}`), server.apiToken)
	if err != nil {
		t.Fatalf("PATCH request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", resp.StatusCode)
	}
	var changed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&changed); err != nil {
		t.Fatalf("decode PATCH: %v", err)
	}
	token, _ := changed["token"].(string)
	if token == "" || !validBearerToken(token) || len(token) < 43 {
		t.Fatalf("generated token = %q, want URL-safe >=256-bit bearer token", token)
	}

	refreshed := get()
	encoded, _ := json.Marshal(refreshed)
	if _, exists := refreshed["token"]; exists {
		t.Fatalf("GET returned one-time token field: %s", encoded)
	}
	if _, exists := refreshed["token_digest"]; exists {
		t.Fatalf("GET returned token digest field: %s", encoded)
	}
	if strings.Contains(string(encoded), token) || refreshed["token_configured"] != true || refreshed["effective_enabled"] != true {
		t.Fatalf("GET after enable = %s, leaked token or wrong effective state", encoded)
	}
}

func TestMCPAdminDisableAndRotateInvalidateSessionsImmediately(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{})
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	patchEnabled := func(enabled bool) map[string]any {
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		req, _ := newAuthenticatedRequest(http.MethodPatch, ts.URL+"/api/mcp/settings", bytes.NewReader(body), s.apiToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH MCP settings: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status = %d, want 200", resp.StatusCode)
		}
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode PATCH: %v", err)
		}
		return result
	}
	call := func(token, sessionID string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(mcpInitializeRequest))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if sessionID != "" {
			req.Header.Set(mcpSessionHeaderName, sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("MCP call: %v", err)
		}
		return resp
	}

	// Loopback may enable without a credential and creates a continuity session.
	if got := patchEnabled(true); got["effective_enabled"] != true {
		t.Fatalf("enabled response = %#v", got)
	}
	init := call("", "")
	sessionID := init.Header.Get(mcpSessionHeaderName)
	init.Body.Close()
	if sessionID == "" {
		t.Fatal("initialize did not create MCP session")
	}
	patchEnabled(false)
	disabled := call("", sessionID)
	defer disabled.Body.Close()
	if disabled.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled MCP = %d, want generic 404", disabled.StatusCode)
	}

	patchEnabled(true)
	rotateReq, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/mcp/settings/token", nil, s.apiToken)
	rotateResp, err := http.DefaultClient.Do(rotateReq)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	defer rotateResp.Body.Close()
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200", rotateResp.StatusCode)
	}
	var rotated map[string]any
	if err := json.NewDecoder(rotateResp.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotate: %v", err)
	}
	token := rotated["token"].(string)
	wrong := call("old-token", "")
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", wrong.StatusCode)
	}
	accepted := call(token, "")
	accepted.Body.Close()
	if accepted.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d, want 200", accepted.StatusCode)
	}
	staleReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":2}`))
	staleReq.Header.Set("Authorization", "Bearer "+token)
	staleReq.Header.Set(mcpSessionHeaderName, sessionID)
	stale, err := http.DefaultClient.Do(staleReq)
	if err != nil {
		t.Fatalf("stale session call: %v", err)
	}
	defer stale.Body.Close()
	if stale.StatusCode != http.StatusNotFound {
		t.Fatalf("old session after rotation = %d, want 404", stale.StatusCode)
	}
}

func TestMCPAdminIdempotentUpdateKeepsCurrentSessions(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{})
	request := func(method, path, body, sessionID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if strings.HasPrefix(path, "/api/") {
			req.Header.Set("X-Session-Token", s.apiToken)
			req.Header.Set("Content-Type", "application/json")
		}
		if sessionID != "" {
			req.Header.Set(mcpSessionHeaderName, sessionID)
		}
		res := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(res, req)
		return res
	}
	if res := request(http.MethodPatch, "/api/mcp/settings", `{"enabled":true}`, ""); res.Code != http.StatusOK {
		t.Fatalf("enable status = %d", res.Code)
	}
	initialized := request(http.MethodPost, "/mcp", mcpInitializeRequest, "")
	sessionID := initialized.Header().Get(mcpSessionHeaderName)
	if sessionID == "" {
		t.Fatal("initialize did not return a session")
	}
	if res := request(http.MethodPatch, "/api/mcp/settings", `{"enabled":true}`, ""); res.Code != http.StatusOK {
		t.Fatalf("idempotent update status = %d", res.Code)
	}
	listed := request(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","method":"tools/list","id":2}`, sessionID)
	if listed.Code != http.StatusOK {
		t.Fatalf("session after idempotent update = %d, want 200", listed.Code)
	}
}

func TestMCPAdminDatabaseFailureDoesNotChangeRuntimeGate(t *testing.T) {
	s, _ := setupTestServer(t)
	if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest("stored-token")}); err != nil {
		t.Fatalf("seed MCP settings: %v", err)
	}
	server, err := New("127.0.0.1:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(ts.Close)
	if err := server.store.DB().Exec(`CREATE TRIGGER fail_mcp_settings BEFORE UPDATE ON mcp_settings BEGIN SELECT RAISE(ABORT, 'no write'); END`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	req, _ := newAuthenticatedRequest(http.MethodPatch, ts.URL+"/api/mcp/settings", strings.NewReader(`{"enabled":false}`), server.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("PATCH status = %d, want 500", resp.StatusCode)
	}
	mcpReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(mcpInitializeRequest))
	mcpReq.Header.Set("Authorization", "Bearer stored-token")
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		t.Fatalf("MCP call: %v", err)
	}
	defer mcpResp.Body.Close()
	if mcpResp.StatusCode != http.StatusOK {
		t.Fatalf("runtime changed after failed DB update: %d", mcpResp.StatusCode)
	}
}

func TestMCPAdminExternalOverridesAreVisibleAndLocked(t *testing.T) {
	s, _ := setupTestServer(t)
	if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: false, TokenDigest: mcpDigest("stored-token")}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	server, err := New("127.0.0.1:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{
		Enabled: true, EnabledSet: true, Token: "operator-token", TokenSet: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(ts.Close)

	get, _ := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/mcp/settings", nil, server.apiToken)
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings["requested_enabled"] != true || settings["effective_enabled"] != true || settings["externally_managed"] != true || settings["enabled_externally_managed"] != true || settings["token_externally_managed"] != true {
		t.Fatalf("external status = %#v", settings)
	}
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "operator-token") || strings.Contains(string(encoded), "stored-token") {
		t.Fatalf("settings leaked a credential: %s", encoded)
	}

	update, _ := newAuthenticatedRequest(http.MethodPatch, ts.URL+"/api/mcp/settings", strings.NewReader(`{"enabled":false}`), server.apiToken)
	update.Header.Set("Content-Type", "application/json")
	blocked, err := http.DefaultClient.Do(update)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("external enabled PATCH = %d, want 409", blocked.StatusCode)
	}
	rotate, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/mcp/settings/token", nil, server.apiToken)
	blocked, err = http.DefaultClient.Do(rotate)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("external token rotate = %d, want 409", blocked.StatusCode)
	}
}

func TestMCPExplicitEnabledOverrideCanUsePersistedCredential(t *testing.T) {
	s, _ := setupTestServer(t)
	if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: false, TokenDigest: mcpDigest("stored-token")}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	server, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true, EnabledSet: true})
	if err != nil {
		t.Fatalf("explicit enabled with persisted credential: %v", err)
	}
	gate := server.mcpRuntime.snapshotValue()
	if !gate.enabled || !gate.hasToken {
		t.Fatalf("runtime gate = %+v, want enabled with persisted credential", gate)
	}
}

func TestMCPExplicitOverridesFailClosedAndRetainPersistedState(t *testing.T) {
	t.Run("explicit false locks persisted true off", func(t *testing.T) {
		s, _ := setupTestServer(t)
		if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest("stored-token")}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		server, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{EnabledSet: true})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		status := server.mcpRuntime.status()
		if !status.Enabled || status.RequestedEnabled || status.EffectiveEnabled || !status.EnabledExternallyManaged || !status.TokenConfigured {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("explicit empty token hides persisted digest and fails closed", func(t *testing.T) {
		s, _ := setupTestServer(t)
		if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest("stored-token")}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		server, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true, EnabledSet: true, TokenSet: true})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		status := server.mcpRuntime.status()
		if !status.Enabled || !status.RequestedEnabled || status.EffectiveEnabled || status.TokenConfigured || !status.TokenExternallyManaged || !strings.Contains(status.ExternallyManagedReason, "requires a credential") {
			t.Fatalf("status = %+v", status)
		}
		persisted, err := s.store.GetMCPSettings()
		if err != nil || persisted.TokenDigest != mcpDigest("stored-token") {
			t.Fatalf("persisted settings changed: %+v, err=%v", persisted, err)
		}
	})
}

func TestMCPAdminConcurrentAuthenticationAndRotation(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{})
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	enable, _ := newAuthenticatedRequest(http.MethodPatch, ts.URL+"/api/mcp/settings", strings.NewReader(`{"enabled":true}`), s.apiToken)
	enable.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(enable)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	resp.Body.Close()

	var tokenMu sync.RWMutex
	token := ""
	rotate := func() error {
		req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/mcp/settings/token", nil, s.apiToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("rotate status %d", res.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			return err
		}
		next, _ := payload["token"].(string)
		if next == "" {
			return errors.New("empty rotated token")
		}
		tokenMu.Lock()
		token = next
		tokenMu.Unlock()
		return nil
	}
	if err := rotate(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokenMu.RLock()
			current := token
			tokenMu.RUnlock()
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(mcpInitializeRequest))
			req.Header.Set("Authorization", "Bearer "+current)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("MCP request: %v", err)
				return
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusUnauthorized {
				t.Errorf("concurrent MCP status = %d, want 200 or 401", res.StatusCode)
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rotate(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestMCPAdminRotationUpdatesEveryServerSharingStore(t *testing.T) {
	base, _ := setupTestServer(t)
	if err := base.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest("old-token")}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	first, err := New("127.0.0.1:0", base.manager, base.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New first: %v", err)
	}
	second, err := New("127.0.0.1:0", base.manager, base.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New second: %v", err)
	}

	rotate := httptest.NewRequest(http.MethodPost, "/api/mcp/settings/token", nil)
	rotate.Header.Set("X-Session-Token", first.apiToken)
	rotated := httptest.NewRecorder()
	first.httpServer.Handler.ServeHTTP(rotated, rotate)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate status = %d", rotated.Code)
	}
	var payload mcpSettingsJSON
	if err := json.NewDecoder(rotated.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rotate: %v", err)
	}
	for token, want := range map[string]int{"old-token": http.StatusUnauthorized, payload.Token: http.StatusOK} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitializeRequest))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		second.httpServer.Handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("second server token %q status = %d, want %d", token, res.Code, want)
		}
	}
}

func TestMCPAdminRotationRejectsRequestWaitingOnBodyWithoutCreatingState(t *testing.T) {
	s, _ := setupTestServer(t)
	if err := s.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest("old-token")}); err != nil {
		t.Fatalf("seed MCP settings: %v", err)
	}
	server, err := New("127.0.0.1:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := &blockingMCPBody{
		started: make(chan struct{}),
		release: make(chan struct{}),
		body:    strings.NewReader(mcpInitializeRequest),
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = body
	req.Header.Set("Authorization", "Bearer old-token")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		res := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(res, req)
		result <- res
	}()
	<-body.started

	rotate := httptest.NewRequest(http.MethodPost, "/api/mcp/settings/token", nil)
	rotate.Header.Set("X-Session-Token", server.apiToken)
	rotated := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rotated, rotate)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200", rotated.Code)
	}
	close(body.release)
	res := <-result
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("old request status after rotation = %d, want 401", res.Code)
	}
	if got := res.Header().Get(mcpSessionHeaderName); got != "" {
		t.Fatalf("old request created MCP session %q after rotation", got)
	}
	server.mcpSessions.mu.Lock()
	defer server.mcpSessions.mu.Unlock()
	if len(server.mcpSessions.clients) != 0 {
		t.Fatalf("old request created state after rotation: %#v", server.mcpSessions.clients)
	}
}

func TestMCPAdminSettingsRejectsLooseJSONAndUnsafeOrigin(t *testing.T) {
	s, _ := setupTestServer(t)
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		origin      string
		want        int
	}{
		{name: "missing content type", body: `{"enabled":true}`, want: http.StatusUnsupportedMediaType},
		{name: "wrong content type", contentType: "text/plain", body: `{"enabled":true}`, want: http.StatusUnsupportedMediaType},
		{name: "null enabled", contentType: "application/json", body: `{"enabled":null}`, want: http.StatusBadRequest},
		{name: "wrong enabled type", contentType: "application/json", body: `{"enabled":"true"}`, want: http.StatusBadRequest},
		{name: "unknown field", contentType: "application/json", body: `{"enabled":true,"token":"leak"}`, want: http.StatusBadRequest},
		{name: "trailing value", contentType: "application/json", body: `{"enabled":true}{}`, want: http.StatusBadRequest},
		{name: "oversized", contentType: "application/json", body: `{"enabled":true,"padding":"` + strings.Repeat("x", 5000) + `"}`, want: http.StatusRequestEntityTooLarge},
		{name: "foreign origin", contentType: "application/json", body: `{"enabled":true}`, origin: "https://evil.example", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/mcp/settings", strings.NewReader(tc.body))
			req.Header.Set("X-Session-Token", s.apiToken)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			res := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%q", res.Code, tc.want, res.Body.String())
			}
		})
	}
}

func TestMCPAdminRoutesEnforceAuthenticationMethodsAndEmptyRotateBody(t *testing.T) {
	s, _ := setupTestServer(t)
	unauthorized := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/mcp/settings", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", unauthorized.Code)
	}

	wrongMethod := httptest.NewRequest(http.MethodPut, "/api/mcp/settings", nil)
	wrongMethod.Header.Set("X-Session-Token", s.apiToken)
	methodResponse := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(methodResponse, wrongMethod)
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT settings = %d, want 405", methodResponse.Code)
	}

	rotate := httptest.NewRequest(http.MethodPost, "/api/mcp/settings/token", strings.NewReader(`{"token":"must-not-be-accepted"}`))
	rotate.Header.Set("X-Session-Token", s.apiToken)
	rotateResponse := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rotateResponse, rotate)
	if rotateResponse.Code != http.StatusBadRequest {
		t.Fatalf("rotate with body = %d, want 400", rotateResponse.Code)
	}
}

func TestMCPAdminOneTimeResponsesAreNotCacheable(t *testing.T) {
	s, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/settings/token", nil)
	req.Header.Set("X-Session-Token", s.apiToken)
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("rotate status = %d", res.Code)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestMCPTokenGenerationStrengthGrammarAndRandomFailure(t *testing.T) {
	token, err := newMCPToken(bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32)))
	if err != nil {
		t.Fatalf("newMCPToken: %v", err)
	}
	if len(token) != 43 || !validBearerToken(token) {
		t.Fatalf("generated token = %q, want 256-bit unpadded URL-safe bearer", token)
	}
	if _, err := newMCPToken(strings.NewReader("short")); err == nil {
		t.Fatal("short random read succeeded")
	}
}

func TestMCPAdminRotationRejectsRandomCollisionAndKeepsCredential(t *testing.T) {
	base, _ := setupTestServer(t)
	randomBytes := bytes.Repeat([]byte{0x2a}, 32)
	oldToken, err := newMCPToken(bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := base.store.SaveMCPSettings(storage.MCPSettings{Enabled: true, TokenDigest: mcpDigest(oldToken)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	server, err := New("127.0.0.1:0", base.manager, base.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server.mcpTokenRandom = bytes.NewReader(bytes.Repeat(randomBytes, 4))
	rotate := httptest.NewRequest(http.MethodPost, "/api/mcp/settings/token", nil)
	rotate.Header.Set("X-Session-Token", server.apiToken)
	rotated := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rotated, rotate)
	if rotated.Code != http.StatusInternalServerError {
		t.Fatalf("collision rotation status = %d, want 500", rotated.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitializeRequest))
	request.Header.Set("Authorization", "Bearer "+oldToken)
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("old credential after failed rotation = %d, want 200", response.Code)
	}
}
