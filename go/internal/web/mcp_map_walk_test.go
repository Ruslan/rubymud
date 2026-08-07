package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
)

type partialMCPMapWalkConn struct{ net.Conn }

func (c *partialMCPMapWalkConn) Write([]byte) (int, error) { return 1, io.ErrUnexpectedEOF }
func (c *partialMCPMapWalkConn) Close() error              { return nil }

func TestMCPMapWalkToolsListed(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	tools := s.mcpListTools().(map[string]any)["tools"].([]any)
	seen := map[string]map[string]any{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		seen[tool["name"].(string)] = tool
	}
	for _, name := range []string{"mud_map_walk", "mud_map_walk_status", "mud_map_walk_stop", "mud_map_walk_resume"} {
		if _, ok := seen[name]; !ok {
			t.Errorf("tools/list missing %s", name)
		}
	}
	start := seen["mud_map_walk"]["inputSchema"].(map[string]any)
	if required := start["required"].([]string); len(required) != 1 || required[0] != "to" {
		t.Errorf("mud_map_walk required = %#v, want [to]", required)
	}
}

// TestMCPMapWalkLifecycle exercises the MCP facade against the same live
// Session map-walk state machine used by the REST handlers.
func TestMCPMapWalkLifecycle(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	started := callTool(t, s, "mud_map_walk", `{"to":{"zone":"Z","x":2,"y":0,"l":0}}`)
	if started["isError"] == true {
		t.Fatalf("start errored: %q", toolText(t, started))
	}
	if got := toolText(t, started); !strings.Contains(got, "active: true") || !strings.Contains(got, "state: running") || !strings.Contains(got, "total: 2") || !strings.Contains(got, "epoch:") || !strings.Contains(got, "job:") || !strings.Contains(got, "revision:") {
		t.Fatalf("start state = %q", got)
	}

	status := callTool(t, s, "mud_map_walk_status", fmt.Sprintf(`{"session_id":%d}`, sid))
	if status["isError"] == true {
		t.Fatalf("status errored: %q", toolText(t, status))
	}
	if got := toolText(t, status); !strings.Contains(got, "active: true") || !strings.Contains(got, "current: 0/2") {
		t.Fatalf("status = %q", got)
	}

	stopped := callTool(t, s, "mud_map_walk_stop", fmt.Sprintf(`{"session_id":%d}`, sid))
	if stopped["isError"] == true {
		t.Fatalf("stop errored: %q", toolText(t, stopped))
	}
	if got := toolText(t, stopped); !strings.Contains(got, "state: stopping") || !strings.Contains(got, "reason: cancelled by MCP") {
		t.Fatalf("stop state = %q", got)
	}
	if again := callTool(t, s, "mud_map_walk_stop", fmt.Sprintf(`{"session_id":%d}`, sid)); again["isError"] == true || !strings.Contains(toolText(t, again), "state: stopping") {
		t.Fatalf("repeated stop should preserve the drain barrier: %#v", again)
	}

	// The in-flight hop is allowed to drain, exactly as the REST cancellation
	// barrier requires. MCP status must then report that no walk is active.
	sess.ObserveMapWalkPosition(mapper.Position{Valid: true, Confidence: mapper.Green, EventDescribesCell: true, Coord: mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}})
	terminal := callTool(t, s, "mud_map_walk_status", fmt.Sprintf(`{"session_id":%d}`, sid))
	if terminal["isError"] == true {
		t.Fatalf("terminal status errored: %q", toolText(t, terminal))
	}
	if got := toolText(t, terminal); !strings.Contains(got, "active: false") || !strings.Contains(got, "state: stopped") {
		t.Fatalf("terminal status = %q", got)
	}
	if again := callTool(t, s, "mud_map_walk_stop", fmt.Sprintf(`{"session_id":%d}`, sid)); again["isError"] == true || !strings.Contains(toolText(t, again), "state: stopped") {
		t.Fatalf("stop after terminal should be idempotent: %#v", again)
	}

	resumed := callTool(t, s, "mud_map_walk_resume", fmt.Sprintf(`{"session_id":%d}`, sid))
	if resumed["isError"] != true || !strings.Contains(toolText(t, resumed), "no paused route") {
		t.Fatalf("resume after terminal should soft-fail: %#v", resumed)
	}
}

func TestMCPMapWalkSoftFailures(t *testing.T) {
	s, sess := setupMcpTestServer(t)

	noTracker := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":1,"y":0,"l":0}}`, sess.SessionID()))
	if noTracker["isError"] != true || !strings.Contains(toolText(t, noTracker), "no active map set") {
		t.Fatalf("start without a tracker should soft-fail: %#v", noTracker)
	}
	// Loading an unset active map installs a tracker with a nil index. It must
	// fail through the same MCP soft-failure contract as an absent tracker.
	sess.LoadActiveMapSet()
	noMap := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":1,"y":0,"l":0}}`, sess.SessionID()))
	if noMap["isError"] != true || !strings.Contains(toolText(t, noMap), "no active map set") {
		t.Fatalf("start without an active map should soft-fail: %#v", noMap)
	}

	missing := callTool(t, s, "mud_map_walk_status", `{"session_id":99999}`)
	if missing["isError"] != true || !strings.Contains(toolText(t, missing), "not connected") {
		t.Fatalf("status for disconnected session should soft-fail: %#v", missing)
	}

	badTarget := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if badTarget["isError"] != true || !strings.Contains(toolText(t, badTarget), "to:{zone,x,y,l} is required") {
		t.Fatalf("missing exact target should soft-fail: %#v", badTarget)
	}
}

func TestMCPMapWalkRequiresEveryExactCoordinateField(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	for _, tc := range []struct {
		name string
		to   string
	}{
		{name: "x", to: `{"zone":"Z","y":0,"l":0}`},
		{name: "y", to: `{"zone":"Z","x":0,"l":0}`},
		{name: "l", to: `{"zone":"Z","x":0,"y":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":%s}`, sid, tc.to))
			if res["isError"] != true || !strings.Contains(toolText(t, res), "to:{zone,x,y,l} is required") {
				t.Fatalf("missing %s should soft-fail: %#v", tc.name, res)
			}
		})
	}
}

func TestMCPMapWalkPartialWriteIsSoftFailure(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}
	setMcpUnexportedField(t, sess, "conn", &partialMCPMapWalkConn{})

	res := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":1,"y":0,"l":0}}`, sid))
	if res["isError"] != true || !strings.Contains(toolText(t, res), "state: paused") || !strings.Contains(toolText(t, res), "outcome unknown") {
		t.Fatalf("partial-write ambiguity should soft-fail: %#v", res)
	}
}

func TestMCPMapWalkAlreadyHereIsSuccessfulNoOp(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	res := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":0,"y":0,"l":0}}`, sid))
	if res["isError"] == true || !strings.Contains(toolText(t, res), "reason: already here") || !strings.Contains(toolText(t, res), "state: stopped") {
		t.Fatalf("already-here walk should be a successful no-op: %#v", res)
	}
}

func TestMCPMapWalkPauseResumeAndReplacement(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":2,"y":0,"l":0}}`, sid))
	// A parser refusal is what pauses a live route; resume must call the
	// Session replan path rather than enqueueing another direction itself.
	sess.ObserveMapWalkRefusal()
	// A real refusal is followed by tracker reconciliation/anchoring, which
	// clears the physical move FIFO before resume is eligible.
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition after refusal = (%v, %v), want (true, true)", ok, exact)
	}
	paused := callTool(t, s, "mud_map_walk_status", fmt.Sprintf(`{"session_id":%d}`, sid))
	if paused["isError"] == true || !strings.Contains(toolText(t, paused), "state: paused") || !strings.Contains(toolText(t, paused), "can_resume: true") {
		t.Fatalf("refusal should expose resumable pause: %#v", paused)
	}
	resumed := callTool(t, s, "mud_map_walk_resume", fmt.Sprintf(`{"session_id":%d}`, sid))
	if resumed["isError"] == true || !strings.Contains(toolText(t, resumed), "state: running") {
		t.Fatalf("resume should replan through Session: %#v", resumed)
	}
	if again := callTool(t, s, "mud_map_walk_resume", fmt.Sprintf(`{"session_id":%d}`, sid)); again["isError"] != true || !strings.Contains(toolText(t, again), "no paused route") {
		t.Fatalf("repeated resume should soft-fail: %#v", again)
	}

	replacement := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":1,"y":0,"l":0}}`, sid))
	if replacement["isError"] == true || !strings.Contains(toolText(t, replacement), "state: stopping") || !strings.Contains(toolText(t, replacement), "replacement waiting") {
		t.Fatalf("replacement must retain the in-flight-hop barrier: %#v", replacement)
	}
}

func TestMCPMapWalkInteroperatesWithRESTLifecycle(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
	}

	started := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":2,"y":0,"l":0}}`, sid))
	if started["isError"] == true {
		t.Fatalf("MCP start errored: %#v", started)
	}

	callREST := func(path string) session.MapWalkState {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("X-Session-Token", s.apiToken)
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s status=%d body=%q", path, w.Code, w.Body.String())
		}
		var st session.MapWalkState
		if err := json.NewDecoder(w.Body).Decode(&st); err != nil {
			t.Fatalf("decode POST %s: %v", path, err)
		}
		return st
	}

	cancelled := callREST(fmt.Sprintf("/api/sessions/%d/map-walk/cancel", sid))
	if !cancelled.Active || cancelled.State != "stopping" || cancelled.Reason != "cancelled by user" {
		t.Fatalf("REST cancel did not observe MCP job: %+v", cancelled)
	}
	status := callTool(t, s, "mud_map_walk_status", fmt.Sprintf(`{"session_id":%d}`, sid))
	if got := toolText(t, status); status["isError"] == true || !strings.Contains(got, "state: stopping") || !strings.Contains(got, "cancelled by user") {
		t.Fatalf("MCP status did not observe REST cancel: %#v", status)
	}

	sess.ObserveMapWalkPosition(mapper.Position{Valid: true, Confidence: mapper.Green, EventDescribesCell: true, Coord: mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}})
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition after cancel = (%v, %v), want (true, true)", ok, exact)
	}
	restarted := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":2,"y":0,"l":0}}`, sid))
	if restarted["isError"] == true {
		t.Fatalf("MCP restart errored: %#v", restarted)
	}
	sess.ObserveMapWalkRefusal()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}); !ok || !exact {
		t.Fatalf("AnchorMapPosition after refusal = (%v, %v), want (true, true)", ok, exact)
	}
	resumed := callREST(fmt.Sprintf("/api/sessions/%d/map-walk/resume", sid))
	if !resumed.Active || resumed.State != "running" {
		t.Fatalf("REST resume did not continue MCP job: %+v", resumed)
	}
	status = callTool(t, s, "mud_map_walk_status", fmt.Sprintf(`{"session_id":%d}`, sid))
	if got := toolText(t, status); status["isError"] == true || !strings.Contains(got, "state: running") {
		t.Fatalf("MCP status did not observe REST resume: %#v", status)
	}
}

func TestMCPMapWalkRejectsLostAndPendingPositions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*session.Session)
		want   string
		manual bool
	}{
		{
			name:  "lost",
			setup: func(sess *session.Session) {},
			want:  "position lost",
		},
		{
			name:   "pending",
			setup:  func(sess *session.Session) {},
			want:   "movement confirmation already pending",
			manual: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sess := setupMcpTestServer(t)
			sid := sess.SessionID()
			seedTrackerSet(t, s, sid)
			sess.LoadActiveMapSet()
			if tc.name == "pending" {
				if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0}); !ok || !exact {
					t.Fatalf("AnchorMapPosition = (%v, %v), want (true, true)", ok, exact)
				}
			}
			tc.setup(sess)
			if tc.manual {
				if err := sess.SendCommand("s", "input"); err != nil {
					t.Fatalf("manual move setup: %v", err)
				}
			}
			res := callTool(t, s, "mud_map_walk", fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":2,"y":0,"l":0}}`, sid))
			if res["isError"] != true || !strings.Contains(toolText(t, res), tc.want) {
				t.Fatalf("%s start should soft-fail: %#v", tc.name, res)
			}
		})
	}
}
