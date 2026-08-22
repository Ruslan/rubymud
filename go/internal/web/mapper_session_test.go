package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"

	"golang.org/x/text/unicode/norm"
)

type zeroWriteErrConn struct{ mockConn }

func (c *zeroWriteErrConn) Write(_ []byte) (int, error) { return 0, net.ErrClosed }

func TestMapWalkBarrierTracePreservesPartialAliasPrefix(t *testing.T) {
	trace, nonfatal := mapWalkBarrierTrace("cmd-1", []string{"say hello"}, true)
	if !nonfatal || trace == nil {
		t.Fatal("barrier must keep websocket alive and produce a trace")
	}
	if len(trace.Commands) != 1 || trace.Commands[0] != "say hello" {
		t.Fatalf("trace commands = %#v, want sent prefix", trace.Commands)
	}
	if _, nonfatal := mapWalkBarrierTrace("cmd-1", nil, false); nonfatal {
		t.Fatal("non-barrier error must remain fatal")
	}
}

// These cover the session-scoped mapper REST endpoints added for the UI map
// pane controls: GET/POST active-map-set and POST map-anchor. They reuse
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

func TestRESTActiveMapSetRejectsMissingSession(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "existing"})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/999999/active-map-set", ts.URL),
		strings.NewReader(fmt.Sprintf(`{"map_set_id":%d}`, setID)), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for missing session", resp.StatusCode)
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

func TestRESTMapAnchorRejectsBlankZone(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	for _, body := range []string{
		`{"x":0,"y":0,"l":0}`,
		`{"zone":"   ","x":0,"y":0,"l":0}`,
	} {
		req, _ := newAuthenticatedRequest(http.MethodPost,
			fmt.Sprintf("%s/api/sessions/%d/map-anchor", ts.URL, sess.SessionID()),
			strings.NewReader(body), s.apiToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST map-anchor: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("map-anchor body %s status = %d, want 400", body, resp.StatusCode)
		}
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

func TestRESTMapPathAndWalkCanonicalizeTargetZone(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	nfd := "Cafe\u0301"
	nfc := norm.NFC.String(nfd)
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Rooms: []storage.Room{
		{Zone: nfc, X: 0, Y: 0, L: 0, Hint: "start", EDirs: `["S"]`, Doors: `[]`, Ch: 2},
		{Zone: nfc, X: 1, Y: 0, L: 0, Hint: "target", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatal(err)
	}
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: nfc, X: 0, Y: 0, L: 0})
	body := fmt.Sprintf(`{"to":{"zone":"  %s  ","x":1,"y":0,"l":0}}`, nfd)
	if out := postMapPath(t, ts, s.apiToken, sess.SessionID(), body); out["reachable"] != true {
		t.Fatalf("NFD map-path = %#v", out)
	}
	if state := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-walk", body); state["active"] != true || state["total"] != float64(1) {
		t.Fatalf("NFD map-walk = %#v", state)
	}
}

func TestRESTTopologyRejectsInvalidZoneWith4xx(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	req, _ := newAuthenticatedRequest(http.MethodPost,
		fmt.Sprintf("%s/api/sessions/%d/map-cell-patch", ts.URL, sess.SessionID()),
		strings.NewReader(`{"zone":"   ","x":0,"y":0,"l":0}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid topology zone status=%d, want 400", resp.StatusCode)
	}
}

func TestRESTMapWalkStartsAndCancelsSessionJob(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	started := postJSON(t, ts, s.apiToken, sid, "map-walk", `{"to":{"zone":"Z","x":2,"y":0,"l":0}}`)
	if active, _ := started["active"].(bool); !active {
		t.Fatalf("map-walk start = %+v, want active", started)
	}
	if total, _ := started["total"].(float64); total != 2 {
		t.Fatalf("map-walk total = %+v, want two hops", started["total"])
	}

	cancelled := postJSON(t, ts, s.apiToken, sid, "map-walk/cancel", `{}`)
	if state, _ := cancelled["state"].(string); state != "stopping" {
		t.Fatalf("map-walk cancel = %+v, want drain barrier", cancelled)
	}
}

func TestRESTMapWalkStartWriteFailureReturnsBroadcastTerminalState(t *testing.T) {
	for _, door := range []bool{false, true} {
		name := "move"
		if door {
			name = "door-open"
		}
		t.Run(name, func(t *testing.T) {
			s, sess := setupMcpTestServer(t)
			ts := httptest.NewServer(s.httpServer.Handler)
			defer ts.Close()
			sid := sess.SessionID()
			setMcpUnexportedField(t, sess, "conn", &zeroWriteErrConn{})
			if door {
				rooms := []storage.Room{
					{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "A", Desc: "a", Exits: "S", EDirs: `["S"]`, Doors: `["S"]`},
					{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "B", Desc: "b", Exits: "N", EDirs: `["N"]`, Doors: `[]`},
				}
				idx := buildTestMapIndex(1, rooms)
				idx.SetDoorOpenTemplate("open door {dir_en}")
				tracker := mapper.NewTracker(idx)
				tracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
				setMcpUnexportedField(t, sess, "mapTracker", tracker)
			} else {
				seedTrackerSet(t, s, sid)
				sess.LoadActiveMapSet()
				sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
			}

			var broadcasts []session.ServerMsg
			sess.AttachClient("walk-failure", func(msg session.ServerMsg) error {
				if msg.Type == "map_walk" {
					broadcasts = append(broadcasts, msg)
				}
				return nil
			})
			state := postJSON(t, ts, s.apiToken, sid, "map-walk", `{"to":{"zone":"Z","x":1,"y":0,"l":0}}`)
			if state["active"] != false || state["state"] != "failed" {
				t.Fatalf("REST write failure = %#v, want terminal failed", state)
			}
			reason, _ := state["reason"].(string)
			wantPrefix := "failed to send step:"
			if door {
				wantPrefix = "failed to open door:"
			}
			if !strings.HasPrefix(reason, wantPrefix) || state["epoch"] == nil || state["job_id"] == nil || state["revision"] == nil {
				t.Fatalf("REST terminal lacks authoritative metadata: %#v", state)
			}
			if len(broadcasts) == 0 {
				t.Fatal("write failure did not broadcast terminal walk state")
			}
			got := broadcasts[len(broadcasts)-1]
			if got.WalkState != state["state"] || got.WalkReason != reason || float64(got.WalkJobID) != state["job_id"] || float64(got.WalkRevision) != state["revision"] {
				t.Fatalf("REST/WS terminal mismatch: rest=%#v ws=%+v", state, got)
			}
		})
	}
}

func TestRESTMapWalkResumesPausedJob(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	started := sess.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.CancelHead() })
	sess.ObserveMapWalkRefusal()

	resumed := postJSON(t, ts, s.apiToken, sid, "map-walk/resume", `{}`)
	if active, _ := resumed["active"].(bool); !active || resumed["state"] != "running" {
		t.Fatalf("resume = %#v, want running state", resumed)
	}
	if id, _ := resumed["job_id"].(float64); uint64(id) <= started.JobID {
		t.Fatalf("resume job id = %#v, want newer than %d", resumed["job_id"], started.JobID)
	}
}

func TestRESTMapWalkStartDuringPausedManualReservationKeepsActiveState(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	started := sess.StartMapWalk(mapper.Coord{Zone: "Z", X: 2, Y: 0, L: 0})
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.CancelHead() })
	sess.ObserveMapWalkRefusal()
	setMcpUnexportedField(t, sess, "manualMoveReserved", true)

	state := postJSON(t, ts, s.apiToken, sid, "map-walk", `{"to":{"zone":"Z","x":1,"y":0,"l":0}}`)
	if active, _ := state["active"].(bool); !active || state["state"] != "paused" {
		t.Fatalf("start while manual reservation = %#v, want active paused", state)
	}
	if jobID, _ := state["job_id"].(float64); uint64(jobID) != started.JobID {
		t.Fatalf("start response job_id=%v, want original paused job %d", state["job_id"], started.JobID)
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
	if out["total_weight"] != float64(0) {
		t.Fatalf("current-room total_weight = %#v, want 0", out["total_weight"])
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

// slimRoomAt finds the slim room at a coord in a set (test helper).
func slimRoomAt(t *testing.T, s *Server, setID int64, zone string, x, y, l int) *storage.SlimRoom {
	t.Helper()
	rooms, err := s.store.ListSlimRooms(setID, zone)
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	for i := range rooms {
		if rooms[i].X == x && rooms[i].Y == y && rooms[i].L == l {
			return &rooms[i]
		}
	}
	return nil
}

func slimHasExit(r *storage.SlimRoom, dir string) bool {
	if r == nil {
		return false
	}
	for _, d := range r.E {
		if d == dir {
			return true
		}
	}
	return false
}

// TestRESTMapCellPatchForksAndAddsExit: patching a FROZEN imported set forks it
// copy-on-write into a new editable set, the exit lands on the FORK (not the
// frozen original), the response reports forked_to, and the session's active set
// switches to the fork. This is the unified write-path end to end.
func TestRESTMapCellPatchForksAndAddsExit(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	setID := seedTrackerSet(t, s, sid) // imported => frozen (editable=0)
	sess.LoadActiveMapSet()

	// Seeded room (0,0,0) has edirs ["S"], ch=2. Add U.
	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"],"remove_exits":[]}`)
	if out["ok"] != true {
		t.Fatalf("expected ok:true, got %#v", out)
	}
	// A frozen set forks — the response must carry forked_to.
	forkedTo, ok := out["forked_to"].(float64)
	if !ok || int64(forkedTo) == 0 {
		t.Fatalf("expected forked_to with a new set id, got %#v", out)
	}
	forkID := int64(forkedTo)
	if forkID == setID {
		t.Fatalf("fork id %d must differ from the frozen original %d", forkID, setID)
	}

	// The session's active set switched to the fork.
	if got := getActiveMapSetID(t, ts, s.apiToken, sid); got == nil || *got != forkID {
		t.Fatalf("active set = %v, want fork %d", got, forkID)
	}

	// The exit landed on the FORK.
	forkStart := slimRoomAt(t, s, forkID, "Z", 0, 0, 0)
	if !slimHasExit(forkStart, "U") {
		t.Errorf("fork room missing added U exit, edirs=%v", forkStart.E)
	}
	if forkStart.Ch&(1<<4) == 0 {
		t.Errorf("fork room ch U-bit unset, ch=%d", forkStart.Ch)
	}

	// The FROZEN original is untouched.
	origStart := slimRoomAt(t, s, setID, "Z", 0, 0, 0)
	if slimHasExit(origStart, "U") {
		t.Errorf("frozen original must NOT have the added U exit, edirs=%v", origStart.E)
	}
}

// TestRESTMapCellPatchSecondWriteNoRefork: once the active set is editable (a
// prior write forked it), a second write applies IN PLACE and does NOT fork again.
func TestRESTMapCellPatchSecondWriteNoRefork(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	out1 := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)
	forkID := int64(out1["forked_to"].(float64))

	// Second write on the now-editable set: no fork, applies in place.
	out2 := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["D"]}`)
	if out2["ok"] != true {
		t.Fatalf("second write not ok: %#v", out2)
	}
	if _, present := out2["forked_to"]; present {
		t.Fatalf("second write must NOT fork again, got %#v", out2)
	}
	if got := getActiveMapSetID(t, ts, s.apiToken, sid); got == nil || *got != forkID {
		t.Fatalf("active set changed on second write: %v, want %d", got, forkID)
	}
	start := slimRoomAt(t, s, forkID, "Z", 0, 0, 0)
	if !slimHasExit(start, "U") || !slimHasExit(start, "D") {
		t.Errorf("both U and D should be present after two writes, got %v", start.E)
	}
}

func TestRESTMapCellPatchSetsMutableDirectionsWithoutChangingExits(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"mutable_exits":["d","D","u"]}`)
	if out["ok"] != true {
		t.Fatalf("mutable patch=%#v", out)
	}
	setID, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil || !ok {
		t.Fatalf("active set after mutable patch=%d ok=%v err=%v", setID, ok, err)
	}
	room := slimRoomAt(t, s, setID, "Z", 0, 0, 0)
	if strings.Join(room.ME, ",") != "U,D" {
		t.Fatalf("mutable directions=%v", room.ME)
	}
	if strings.Join(room.E, ",") != "S" {
		t.Fatalf("ordinary exits changed=%v", room.E)
	}
	if depth := s.store.WriteJournal().Depth(setID); depth != 1 {
		t.Fatalf("mutable metadata write journal depth=%d, want 1", depth)
	}
	undo := postMapUndo(t, ts, s.apiToken, sid)
	if undo["applied"] != true || undo["nothing_to_undo"] != false {
		t.Fatalf("undo mutable metadata=%#v", undo)
	}
	if undo["label"] != "set mutable exits U D" {
		t.Fatalf("mutable undo label=%#v", undo["label"])
	}
	room = slimRoomAt(t, s, setID, "Z", 0, 0, 0)
	if len(room.ME) != 0 {
		t.Fatalf("undo left mutable directions=%v", room.ME)
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
