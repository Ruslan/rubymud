package web

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const mcpInitializeRequest = `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}`

type failOnReadBody struct {
	read bool
}

func (b *failOnReadBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("request body must not be read")
}

func (*failOnReadBody) Close() error { return nil }

func TestMCPDisabledIsNotExposed(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/mcp"},
		{method: http.MethodGet, path: "/mcp"},
		{method: http.MethodOptions, path: "/mcp"},
		{method: http.MethodPost, path: "/mcp/"},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(mcpInitializeRequest))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read response: %v", readErr)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled MCP status = %d, want 404", resp.StatusCode)
		}
		if string(body) != "404 page not found\n" {
			t.Fatalf("disabled MCP body = %q, want generic 404", body)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != "" {
			t.Fatalf("disabled MCP exposed auth scheme: %q", got)
		}
	}
}

func TestMCPDisabledIsNotExposedBehindBasicAuth(t *testing.T) {
	s, _ := setupTestServer(t)
	disabled, err := New(":0", s.manager, s.store, t.TempDir(), BasicAuth{User: "user", Pass: "pass"}, MCPConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(disabled.httpServer.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(mcpInitializeRequest))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled MCP behind basic auth status = %d, want 404", resp.StatusCode)
	}
}

func TestMCPBearerAuthentication(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	tests := []struct {
		name          string
		authorization []string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: []string{"Basic mcp-secret"}, wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme case", authorization: []string{"bearer mcp-secret"}, wantStatus: http.StatusUnauthorized},
		{name: "wrong token", authorization: []string{"Bearer wrong"}, wantStatus: http.StatusUnauthorized},
		{name: "extra whitespace", authorization: []string{"Bearer  mcp-secret"}, wantStatus: http.StatusUnauthorized},
		{name: "comma joined values", authorization: []string{"Bearer mcp-secret,Bearer mcp-secret"}, wantStatus: http.StatusUnauthorized},
		{name: "multiple headers", authorization: []string{"Bearer mcp-secret", "Bearer mcp-secret"}, wantStatus: http.StatusUnauthorized},
		{name: "correct", authorization: []string{"Bearer mcp-secret"}, wantStatus: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewBufferString(mcpInitializeRequest))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header["Authorization"] = tc.authorization
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST /mcp: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if got := resp.Header.Get("WWW-Authenticate"); got == "" {
					t.Fatal("401 response missing WWW-Authenticate")
				}
				if got := resp.Header.Get("WWW-Authenticate"); strings.Contains(got, "mcp-secret") {
					t.Fatalf("WWW-Authenticate leaked token: %q", got)
				}
			}
		})
	}
}

func TestMCPRejectsMalformedAuthorizationAtHandlerBoundary(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	for _, value := range []string{"Bearer mcp-secret ", "Bearer mcp-secret\r\nX-Test: injected"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitializeRequest))
		req.Header["Authorization"] = []string{value}
		res := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization %q status = %d, want 401", value, res.Code)
		}
	}
}

func TestMCPRejectsBeforeReadingRequestBody(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	body := &failOnReadBody{}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = body
	res := httptest.NewRecorder()

	s.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
	if body.read {
		t.Fatal("unauthorized request body was read")
	}
}

func TestMCPAuthenticationIsIndependentOfWebCredentials(t *testing.T) {
	s, _ := setupTestServer(t)
	server, err := New("127.0.0.1:0", s.manager, s.store, t.TempDir(), BasicAuth{User: "web-user", Pass: "web-pass"}, MCPConfig{Enabled: true, Token: "mcp-secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name       string
		headers    http.Header
		wantStatus int
	}{
		{name: "bearer succeeds without basic", headers: http.Header{"Authorization": []string{"Bearer mcp-secret"}}, wantStatus: http.StatusOK},
		{name: "basic cannot substitute", headers: http.Header{"Authorization": []string{"Basic d2ViLXVzZXI6d2ViLXBhc3M="}}, wantStatus: http.StatusUnauthorized},
		{name: "session token cannot substitute", headers: http.Header{"X-Session-Token": []string{s.apiToken}}, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitializeRequest))
			req.Header = tc.headers
			res := httptest.NewRecorder()
			server.httpServer.Handler.ServeHTTP(res, req)
			if res.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusUnauthorized && res.Header().Get("WWW-Authenticate") != `Bearer realm="mudhost-mcp"` {
				t.Fatalf("WWW-Authenticate = %q, want Bearer challenge", res.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestMCPAuthenticatesBeforeMethodDispatch(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			unauthorized := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(unauthorized, httptest.NewRequest(method, "/mcp", nil))
			if unauthorized.Code != http.StatusUnauthorized {
				t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
			}

			req := httptest.NewRequest(method, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer mcp-secret")
			authorized := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(authorized, req)
			if authorized.Code != http.StatusMethodNotAllowed {
				t.Fatalf("authorized status = %d, want 405", authorized.Code)
			}
			if got := authorized.Header().Get("Allow"); got != http.MethodPost {
				t.Fatalf("Allow = %q, want POST", got)
			}
		})
	}
}

func TestMCPRejectsUnsafeUnauthenticatedBind(t *testing.T) {
	for _, addr := range []string{":0", "0.0.0.0:0", "[::]:0", "192.0.2.1:0", "example.invalid:0", "/tmp/mudhost.sock"} {
		t.Run(addr, func(t *testing.T) {
			s, _ := setupTestServer(t)
			if _, err := New(addr, s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true}); err == nil {
				t.Fatal("New succeeded, want unsafe unauthenticated MCP bind rejected")
			}
		})
	}
}

func TestMCPAllowsLoopbackUnauthenticatedBind(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		t.Run(addr, func(t *testing.T) {
			s, _ := setupTestServer(t)
			server, err := New(addr, s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true})
			if err != nil {
				t.Fatalf("New(%q): %v", addr, err)
			}
			if !server.mcp.Enabled {
				t.Fatal("MCP unexpectedly disabled")
			}
		})
	}
}

func TestMCPTokenlessListenerMustActuallyBeLoopback(t *testing.T) {
	config := MCPConfig{Enabled: true}
	for _, tc := range []struct {
		name    string
		addr    net.Addr
		wantErr bool
	}{
		{name: "IPv4 loopback", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}},
		{name: "IPv6 loopback", addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}},
		{name: "IPv4 wildcard", addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, wantErr: true},
		{name: "IPv6 wildcard", addr: &net.TCPAddr{IP: net.IPv6zero, Port: 8080}, wantErr: true},
		{name: "non-loopback", addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 8080}, wantErr: true},
		{name: "unix", addr: &net.UnixAddr{Name: "/tmp/mudhost.sock", Net: "unix"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMCPListenerAddr(tc.addr, config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateMCPListenerAddr(%v) error = %v, wantErr=%v", tc.addr, err, tc.wantErr)
			}
		})
	}
}

func TestMCPConfigTrimsTokenAndRejectsWhitespaceWithinIt(t *testing.T) {
	s, _ := setupTestServer(t)
	server, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true, Token: "  mcp-secret  "})
	if err != nil {
		t.Fatalf("New with trimmed token: %v", err)
	}
	if server.mcp.Token != "mcp-secret" {
		t.Fatalf("normalized token = %q, want mcp-secret", server.mcp.Token)
	}
	if _, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true, Token: "two words"}); err == nil {
		t.Fatal("New accepted MCP token containing whitespace")
	}
	for _, token := range []string{"comma,token", "colon:token", "control\x00token", "padding=middle", "="} {
		if _, err := New("0.0.0.0:0", s.manager, s.store, t.TempDir(), BasicAuth{}, MCPConfig{Enabled: true, Token: token}); err == nil {
			t.Fatalf("New accepted invalid Bearer token %q", token)
		}
	}
}

func TestMCPAuthorizationLogsNeverContainToken(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpInitializeRequest))
	req.Header.Set("Authorization", "Bearer mcp-secret")
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if strings.Contains(logs.String(), "mcp-secret") {
		t.Fatalf("request log exposed MCP token: %s", logs.String())
	}
}

func TestMCPRejectsQueryBeforeRequestLogging(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	req := httptest.NewRequest(http.MethodPost, "/mcp?access_token=mcp-secret", strings.NewReader(mcpInitializeRequest))
	req.Header.Set("Authorization", "Bearer mcp-secret")
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("query-bearing MCP status = %d, want 400", res.Code)
	}
	if strings.Contains(logs.String(), "mcp-secret") {
		t.Fatalf("request log exposed query credential: %s", logs.String())
	}
}

func TestMCPTokenIsNotIncludedInAppSettings(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	req, err := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/app/settings", nil, s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET app settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET app settings status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read app settings: %v", err)
	}
	if strings.Contains(string(body), "mcp-secret") {
		t.Fatalf("app settings exposed MCP token: %s", body)
	}
	if token, err := s.store.GetSetting("mcp_token"); err != nil || token != "" {
		t.Fatalf("stored MCP token = %q, err=%v; MCP tokens must remain runtime-only", token, err)
	}
}

func TestMCPOptionsDoesNotEnableCORS(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://untrusted.example")
	res := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(res, req)

	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS origin = %q, want unset", got)
	}
}

func TestMCPBearerAuthenticationConcurrentRequests(t *testing.T) {
	s, _ := setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true, Token: "mcp-secret"})
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(mcpInitializeRequest))
			if err != nil {
				t.Errorf("NewRequest: %v", err)
				return
			}
			req.Header.Set("Authorization", "Bearer mcp-secret")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("POST /mcp: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}
