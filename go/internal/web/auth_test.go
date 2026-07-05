package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupTestServerWithBasicAuth builds a server exactly like setupTestServer but
// with HTTP basic auth credentials configured.
func setupTestServerWithBasicAuth(t *testing.T, user, pass string) *httptest.Server {
	t.Helper()
	s, _ := setupTestServer(t)
	s.basicAuth = BasicAuth{User: user, Pass: pass}
	// Rebuild the router so the basic-auth middleware wired in New() picks up
	// the credentials injected above.
	rebuilt := New(":0", s.manager, s.store, t.TempDir(), BasicAuth{User: user, Pass: pass})
	ts := httptest.NewServer(rebuilt.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestBasicAuthRejectsMissingCredentials(t *testing.T) {
	ts := setupTestServerWithBasicAuth(t, "rmud", "s3cret")

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET / without credentials = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatalf("missing WWW-Authenticate header on 401")
	}
}

func TestBasicAuthRejectsWrongCredentials(t *testing.T) {
	ts := setupTestServerWithBasicAuth(t, "rmud", "s3cret")

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.SetBasicAuth("rmud", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET / with wrong password = %d, want 401", resp.StatusCode)
	}
}

func TestBasicAuthAcceptsCorrectCredentials(t *testing.T) {
	ts := setupTestServerWithBasicAuth(t, "rmud", "s3cret")

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.SetBasicAuth("rmud", "s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / with correct credentials = %d, want 200", resp.StatusCode)
	}
}

func TestBasicAuthExemptsPWAAssets(t *testing.T) {
	ts := setupTestServerWithBasicAuth(t, "rmud", "s3cret")

	for _, path := range []string{"/manifest.webmanifest", "/app-icon.svg"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("GET %s without credentials = 401, want it exempt from basic auth", path)
		}
	}
}

func TestBasicAuthDisabledWhenUnset(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("GET / with basic auth unset = 401, want it disabled")
	}
}
