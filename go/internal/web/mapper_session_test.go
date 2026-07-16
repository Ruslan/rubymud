package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These cover the session-scoped mapper REST endpoints added for the UI map
// pane controls (§6): GET/POST active-map-set and POST map-anchor. They reuse
// the MCP test harness because it injects the session into the manager (the
// anchor + reload hooks need a live session).

// getActiveMapSetID hits the GET endpoint and returns the decoded value.
func getActiveMapSetID(t *testing.T, ts *httptest.Server, token string, sessionID int64) *int64 {
	t.Helper()
	req, _ := newAuthenticatedRequest(http.MethodGet, fmt.Sprintf("%s/api/sessions/%d/active-map-set", ts.URL, sessionID), nil, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET active-map-set: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET active-map-set status = %d", resp.StatusCode)
	}
	var out struct {
		ActiveMapSetID *int64 `json:"active_map_set_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.ActiveMapSetID
}

func TestRESTActiveMapSetRoundTrip(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	// Initially none.
	if got := getActiveMapSetID(t, ts, s.apiToken, sid); got != nil {
		t.Fatalf("initial active set = %v, want nil", *got)
	}

	// Create a set and activate it via POST.
	setID := seedTrackerSet(t, s, sid)
	// seedTrackerSet already set it in the DB; clear it so the POST does the work.
	if err := s.store.SetActiveMapSetID(sid, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}

	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/active-map-set", ts.URL, sid),
		strings.NewReader(fmt.Sprintf(`{"map_set_id":%d}`, setID)), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST active-map-set status = %d", resp.StatusCode)
	}

	if got := getActiveMapSetID(t, ts, s.apiToken, sid); got == nil || *got != setID {
		t.Fatalf("after POST active set = %v, want %d", got, setID)
	}

	// Clearing with null map_set_id.
	req2, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/active-map-set", ts.URL, sid),
		strings.NewReader(`{"map_set_id":null}`), s.apiToken)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST clear: %v", err)
	}
	resp2.Body.Close()
	if got := getActiveMapSetID(t, ts, s.apiToken, sid); got != nil {
		t.Fatalf("after clear active set = %v, want nil", *got)
	}
}

func TestRESTActiveMapSetRejectsUnknown(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/active-map-set", ts.URL, sess.SessionID()),
		strings.NewReader(`{"map_set_id":9999}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown map set", resp.StatusCode)
	}
}

func TestRESTMapAnchor(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/map-anchor", ts.URL, sid),
		strings.NewReader(`{"zone":"Z","x":0,"y":0,"l":0}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST map-anchor: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("map-anchor status = %d", resp.StatusCode)
	}
	var out struct {
		OK    bool `json:"ok"`
		Exact bool `json:"exact"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK || !out.Exact {
		t.Fatalf("anchor result = %+v, want ok+exact (room (0,0,0) exists in the set)", out)
	}
}

func TestRESTMapAnchorNoTracker(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	// No active set loaded → soft failure JSON, not a hard error.
	sess.LoadActiveMapSet()

	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/map-anchor", ts.URL, sess.SessionID()),
		strings.NewReader(`{"zone":"Z","x":0,"y":0,"l":0}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST map-anchor: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (soft failure)", resp.StatusCode)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.OK {
		t.Fatalf("expected ok=false with no active set")
	}
}
