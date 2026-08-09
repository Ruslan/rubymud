package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func webAuthAdminRequest(t *testing.T, s *Server, method, target, body, user, password string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("X-Session-Token", s.apiToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)
	return w
}

func TestWebAuthCanBeEnabledAndDisabledWithoutRestart(t *testing.T) {
	s, _ := setupTestServer(t)

	initial := webAuthAdminRequest(t, s, http.MethodGet, "/api/web-auth/settings", "", "", "")
	if initial.Code != http.StatusOK {
		t.Fatalf("initial status = %d, body=%s", initial.Code, initial.Body.String())
	}
	var status webAuthSettingsJSON
	if err := json.Unmarshal(initial.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode initial status: %v", err)
	}
	if status.EffectiveEnabled || status.CredentialConfigured || status.ExternallyManaged {
		t.Fatalf("initial status = %+v", status)
	}

	enabled := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":true,"username":"operator","password":"correct horse battery staple"}`, "", "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body=%s", enabled.Code, enabled.Body.String())
	}
	if bytes.Contains(enabled.Body.Bytes(), []byte("correct horse")) || bytes.Contains(enabled.Body.Bytes(), []byte("$2")) {
		t.Fatalf("enable response disclosed credential material: %s", enabled.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("GET / after enable = %d, want 401", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("operator", "correct horse battery staple")
	s.httpServer.Handler.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusOK {
		t.Fatalf("GET / with stored credentials = %d, want 200", authorized.Code)
	}

	disabled := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false}`, "operator", "correct horse battery staple")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body=%s", disabled.Code, disabled.Body.String())
	}
	open := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(open, httptest.NewRequest(http.MethodGet, "/", nil))
	if open.Code != http.StatusOK {
		t.Fatalf("GET / after disable = %d, want 200", open.Code)
	}
}

func TestWebAuthStoredCredentialsSurviveRestartAndNeverLeak(t *testing.T) {
	s, _ := setupTestServer(t)
	password := "not stored as plaintext"
	enabled := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":true,"username":"operator","password":"`+password+`"}`, "", "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body=%s", enabled.Code, enabled.Body.String())
	}

	raw, err := s.store.GetSetting("web_auth")
	if err != nil {
		t.Fatalf("read stored web auth: %v", err)
	}
	if strings.Contains(raw, password) {
		t.Fatalf("stored setting contains plaintext password: %q", raw)
	}

	restarted, err := New(":0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })

	wrong := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodGet, "/", nil)
	wrongReq.SetBasicAuth("operator", "wrong")
	restarted.httpServer.Handler.ServeHTTP(wrong, wrongReq)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("restart wrong credential status = %d, want 401", wrong.Code)
	}

	ok := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodGet, "/", nil)
	okReq.SetBasicAuth("operator", password)
	restarted.httpServer.Handler.ServeHTTP(ok, okReq)
	if ok.Code != http.StatusOK {
		t.Fatalf("restart correct credential status = %d, want 200", ok.Code)
	}
}

func TestWebAuthUpdateSynchronizesAllServersForStore(t *testing.T) {
	first, _ := setupTestServer(t)
	second, err := New(":0", first.manager, first.store, t.TempDir(), BasicAuth{}, MCPConfig{})
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	enabled := webAuthAdminRequest(t, first, http.MethodPatch, "/api/web-auth/settings", `{"enabled":true,"username":"operator","password":"correct horse battery staple"}`, "", "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body=%s", enabled.Code, enabled.Body.String())
	}

	w := httptest.NewRecorder()
	second.httpServer.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("second server status = %d, want 401", w.Code)
	}
}

func TestWebAuthEnvironmentOverrideIsVisibleAndReadOnly(t *testing.T) {
	base, _ := setupTestServer(t)
	s, err := New(":0", base.manager, base.store, t.TempDir(), BasicAuth{User: "env-user", Pass: "environment password"}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	statusResponse := webAuthAdminRequest(t, s, http.MethodGet, "/api/web-auth/settings", "", "env-user", "environment password")
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	var status webAuthSettingsJSON
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.EffectiveEnabled || !status.ExternallyManaged || status.Username != "env-user" || !status.CredentialConfigured {
		t.Fatalf("external status = %+v", status)
	}

	update := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false}`, "env-user", "environment password")
	if update.Code != http.StatusConflict {
		t.Fatalf("external update status = %d, want 409; body=%s", update.Code, update.Body.String())
	}
}

func TestWebAuthRejectsPartialEnvironmentCredentials(t *testing.T) {
	base, _ := setupTestServer(t)
	for _, auth := range []BasicAuth{{User: "only-user"}, {Pass: "only-password"}} {
		if _, err := New(":0", base.manager, base.store, t.TempDir(), auth, MCPConfig{}); err == nil {
			t.Fatalf("New with partial credentials %+v succeeded", auth)
		}
	}
}

func TestWebAuthAdminRejectsUnsafeUpdates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		contentType string
		origin      string
		wantStatus  int
	}{
		{name: "enable without credentials", body: `{"enabled":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "short password", body: `{"enabled":true,"username":"operator","password":"too-short"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "password without username", body: `{"enabled":true,"password":"correct horse battery staple"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", body: `{"enabled":false,"password_hash":"must-not-be-accepted"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "wrong content type", body: `{"enabled":false}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "cross origin", body: `{"enabled":false}`, contentType: "application/json", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setupTestServer(t)
			req := httptest.NewRequest(http.MethodPatch, "/api/web-auth/settings", strings.NewReader(tc.body))
			req.Header.Set("X-Session-Token", s.apiToken)
			req.Header.Set("Content-Type", tc.contentType)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestWebAuthListenerRequiresProtectionAndRestartOrdering(t *testing.T) {
	s, _ := setupTestServer(t)

	unsafe := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false,"listen_addr":"0.0.0.0:8080"}`, "", "")
	if unsafe.Code != http.StatusConflict {
		t.Fatalf("unprotected network listener status = %d, want 409; body=%s", unsafe.Code, unsafe.Body.String())
	}

	enabled := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":true,"username":"operator","password":"correct horse battery staple","listen_addr":"0.0.0.0:8080"}`, "", "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("protected network listener status = %d, body=%s", enabled.Code, enabled.Body.String())
	}
	var status webAuthSettingsJSON
	if err := json.Unmarshal(enabled.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.CurrentExposure != "loopback" || status.ConfiguredExposure != "network" || !status.RestartRequired {
		t.Fatalf("listener status = %+v", status)
	}

	cannotDisable := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false}`, "operator", "correct horse battery staple")
	if cannotDisable.Code != http.StatusConflict {
		t.Fatalf("disable before local restart ordering status = %d, want 409; body=%s", cannotDisable.Code, cannotDisable.Body.String())
	}

	local := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":true,"listen_addr":"127.0.0.1:8080"}`, "operator", "correct horse battery staple")
	if local.Code != http.StatusOK {
		t.Fatalf("restore local listener status = %d, body=%s", local.Code, local.Body.String())
	}
	disabled := webAuthAdminRequest(t, s, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false}`, "operator", "correct horse battery staple")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable local listener status = %d, body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestWebAuthEnvironmentCredentialsDoNotLockSavedListenerRepair(t *testing.T) {
	base, _ := setupTestServer(t)
	server, err := NewWithWebListenerConfig(WebListenerConfig{
		CurrentAddr:       "127.0.0.1:9000",
		ConfiguredAddr:    "0.0.0.0:8080",
		ExternallyManaged: true,
	}, base.manager, base.store, t.TempDir(), BasicAuth{User: "env-user", Pass: "environment password"}, MCPConfig{})
	if err != nil {
		t.Fatalf("NewWithWebListenerConfig: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	repair := webAuthAdminRequest(t, server, http.MethodPatch, "/api/web-auth/settings", `{"enabled":false,"listen_addr":"127.0.0.1:8080"}`, "env-user", "environment password")
	if repair.Code != http.StatusOK {
		t.Fatalf("repair saved listener status = %d, body=%s", repair.Code, repair.Body.String())
	}
	var status webAuthSettingsJSON
	if err := json.Unmarshal(repair.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode repaired status: %v", err)
	}
	if !status.ListenerExternallyManaged || status.ConfiguredListener != "127.0.0.1:8080" || !status.RestartRequired {
		t.Fatalf("repaired status = %+v", status)
	}
}
