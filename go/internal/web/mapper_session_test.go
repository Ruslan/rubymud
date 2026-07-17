package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
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

// postMapPath hits POST /api/sessions/{id}/map-path and returns the decoded body.
func postMapPath(t *testing.T, ts *httptest.Server, token string, sid int64, body string) map[string]any {
	t.Helper()
	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/map-path", ts.URL, sid),
		strings.NewReader(body), token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST map-path: %v", err)
	}
	defer resp.Body.Close()
	// Soft-fail contract: always 200, never 500.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("map-path status = %d, want 200 (soft-fail contract)", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// asDirs coerces the JSON `directions` array to []string.
func asDirs(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// TestRESTMapPathFromCurrentPosition: with a seeded position, clicking a distant
// room returns canonical lowercase English directions joinable with ';'.
func TestRESTMapPathFromCurrentPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	// Anchor at (0,0,0); route to (2,0,0) is S,S => "s;s" (S = x+1).
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	out := postMapPath(t, ts, s.apiToken, sid, `{"to":{"zone":"Z","x":2,"y":0,"l":0}}`)
	if out["reachable"] != true {
		t.Fatalf("expected reachable, got %#v", out)
	}
	dirs := asDirs(out["directions"])
	if strings.Join(dirs, ";") != "s;s" {
		t.Fatalf("directions = %v, want [s s] (canonical lowercase english)", dirs)
	}
}

// TestRESTMapPathLostPosition: no anchor => red/lost => soft-fail reachable:false.
func TestRESTMapPathLostPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet() // no anchor => position lost

	out := postMapPath(t, ts, s.apiToken, sid, `{"to":{"zone":"Z","x":2,"y":0,"l":0}}`)
	if out["reachable"] != false {
		t.Fatalf("expected reachable:false when lost, got %#v", out)
	}
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "lost") {
		t.Fatalf("expected lost reason, got %q", reason)
	}
}

// TestRESTMapPathCurrentRoom: clicking the room you're standing in is a no-op
// (reachable + empty directions + here flag), so the UI does not insert garbage.
func TestRESTMapPathCurrentRoom(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	out := postMapPath(t, ts, s.apiToken, sid, `{"to":{"zone":"Z","x":0,"y":0,"l":0}}`)
	if out["reachable"] != true || out["here"] != true {
		t.Fatalf("expected reachable+here for current room, got %#v", out)
	}
	if dirs := asDirs(out["directions"]); len(dirs) != 0 {
		t.Fatalf("expected empty directions for current room, got %v", dirs)
	}
}

// TestRESTMapPathDTTarget: routing to a death-trap target soft-fails with dt:true.
func TestRESTMapPathDTTarget(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	// A(0,0,0)->S->DT(1,0,0). The DT is the click target.
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "DT", ZoneCount: 1, RoomCount: 2,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "A", Exits: "S", EDirs: `["S"]`, Doors: `[]`, Ch: 2},
			{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "Trap", Exits: "N", EDirs: `["N"]`, Doors: `[]`, Ch: 1, IsDT: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	s.store.SetActiveMapSetID(sid, id)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	out := postMapPath(t, ts, s.apiToken, sid, `{"to":{"zone":"Z","x":1,"y":0,"l":0}}`)
	if out["reachable"] != false || out["dt"] != true {
		t.Fatalf("expected reachable:false + dt:true for DT target, got %#v", out)
	}
}

func TestRESTMapPathNoTracker(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sess.LoadActiveMapSet() // no active set

	out := postMapPath(t, ts, s.apiToken, sess.SessionID(), `{"to":{"zone":"Z","x":0,"y":0,"l":0}}`)
	if out["reachable"] != false {
		t.Fatalf("expected reachable:false with no active set, got %#v", out)
	}
}

// postMapCellPatch hits POST /api/sessions/{id}/map-cell-patch.
func postMapCellPatch(t *testing.T, ts *httptest.Server, token string, sid int64, body string) map[string]any {
	t.Helper()
	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/map-cell-patch", ts.URL, sid),
		strings.NewReader(body), token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST map-cell-patch: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("map-cell-patch status = %d, want 200 (soft-fail contract)", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestRESTMapCellPatchAddsExit: patching +U into the active set's room updates
// its edirs + ch so a subsequent slim-room fetch shows the new exit.
func TestRESTMapCellPatchAddsExit(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	setID := seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// Seeded room (0,0,0) has edirs ["S"], ch=2. Add U.
	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"],"remove_exits":[]}`)
	if out["ok"] != true {
		t.Fatalf("expected ok:true, got %#v", out)
	}
	rooms, err := s.store.ListSlimRooms(setID, "Z")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	var start *storage.SlimRoom
	for i := range rooms {
		if rooms[i].X == 0 && rooms[i].Y == 0 && rooms[i].L == 0 {
			start = &rooms[i]
		}
	}
	if start == nil {
		t.Fatal("start room missing")
	}
	hasU := false
	for _, d := range start.E {
		if d == "U" {
			hasU = true
		}
	}
	if !hasU {
		t.Errorf("expected U added to edirs, got %v", start.E)
	}
	if start.Ch&(1<<4) == 0 {
		t.Errorf("expected ch U-bit set, got ch=%d", start.Ch)
	}
}

// TestRESTMapCellPatchNoActiveSet: no active set => soft-fail ok:false, not 500.
func TestRESTMapCellPatchNoActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sess.LoadActiveMapSet() // no active set

	out := postMapCellPatch(t, ts, s.apiToken, sess.SessionID(), `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)
	if out["ok"] != false {
		t.Fatalf("expected ok:false with no active set, got %#v", out)
	}
}

// TestRESTMapCellPatchRoomNotFound: unknown room => soft-fail ok:false.
func TestRESTMapCellPatchRoomNotFound(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":42,"y":42,"l":0,"add_exits":["U"]}`)
	if out["ok"] != false {
		t.Fatalf("expected ok:false for unknown room, got %#v", out)
	}
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "not found") {
		t.Fatalf("expected not-found reason, got %q", reason)
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
