package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

// TestDownloadLogsIncludesExplicitOffset verifies exported log lines carry an
// explicit UTC offset so the calendar day is unambiguous, and that the offset
// reflects the request's tz param.
func TestDownloadLogsIncludesExplicitOffset(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	if _, err := s.store.AppendLogEntry(sess.SessionID(), "main", "hello world", "hello world"); err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}

	get := func(tz string) string {
		url := fmt.Sprintf("%s/api/sessions/%d/logs/download", ts.URL, sess.SessionID())
		if tz != "" {
			url += "?tz=" + tz
		}
		req, err := newAuthenticatedRequest(http.MethodGet, url, nil, s.apiToken)
		if err != nil {
			t.Fatalf("newAuthenticatedRequest: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET download: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	// tz=Europe/Kyiv => +0300 or +0200 depending on DST, but must have an offset.
	kyiv := get("Europe/Kyiv")
	if !strings.Contains(kyiv, "hello world") {
		t.Fatalf("download missing log text: %q", kyiv)
	}
	if !strings.Contains(kyiv, "+0300") && !strings.Contains(kyiv, "+0200") {
		t.Fatalf("Kyiv export missing expected offset, got: %q", kyiv)
	}

	// Invalid tz falls back to session zone (UTC default) => +0000.
	fallback := get("Not/AZone")
	if !strings.Contains(fallback, "+0000") {
		t.Fatalf("invalid tz should fall back to UTC (+0000), got: %q", fallback)
	}
}

// TestResolveRequestLocationFallback verifies the tz -> session -> UTC fallback
// chain.
func TestResolveRequestLocationFallback(t *testing.T) {
	s, sess := setupTestServer(t)

	rec, err := s.store.GetSession(sess.SessionID())
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	rec.Timezone = "America/New_York"
	rec.TZFollow = 0
	if err := s.store.UpdateSession(rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	// Valid request tz wins.
	if loc := s.resolveRequestLocation(sess.SessionID(), "Europe/Kyiv"); loc.String() != "Europe/Kyiv" {
		t.Fatalf("request tz not honored: %v", loc)
	}
	// Invalid request tz falls back to session zone.
	if loc := s.resolveRequestLocation(sess.SessionID(), "Not/AZone"); loc.String() != "America/New_York" {
		t.Fatalf("fallback to session zone failed: %v", loc)
	}
	// Unknown session falls back to UTC.
	if loc := s.resolveRequestLocation(999999, ""); loc != storage.LoadLocationOrUTC("") {
		t.Fatalf("unknown session should be UTC, got %v", loc)
	}
}
