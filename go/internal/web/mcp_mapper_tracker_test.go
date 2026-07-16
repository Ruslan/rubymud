package web

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// seedTrackerSet inserts a small connected set and makes it active for the
// session, then builds the session's tracker index. Returns the set id.
func seedTrackerSet(t *testing.T, s *Server, sessionID int64) int64 {
	t.Helper()
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name:      "T",
		ZoneCount: 1,
		RoomCount: 3,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "Первая", Desc: "one",
				Exits: "S", EDirs: `["S"]`, Doors: `[]`, Ch: 2},
			{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "Вторая", Desc: "two",
				Exits: "N S", EDirs: `["N","S"]`, Doors: `[]`, Ch: 3},
			{Zone: "Z", X: 2, Y: 0, L: 0, Tag: intPtr(3), Hint: "Третья", Desc: "three",
				Exits: "N", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sessionID, id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	return id
}

func TestMCPWhereNoActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sess.LoadActiveMapSet() // no active set => nil index tracker
	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "(none)") {
		t.Errorf("expected no active set: %q", text)
	}
	if !strings.Contains(text, "red") {
		t.Errorf("expected red confidence with no set: %q", text)
	}
}

func TestMCPAnchorThenWhere(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Anchor at the first room.
	res := callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))
	text := toolText(t, res)
	if res["isError"] == true {
		t.Fatalf("anchor errored: %q", text)
	}
	if !strings.Contains(text, "green") {
		t.Errorf("anchor should be green: %q", text)
	}

	// mud_where reflects it.
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text = toolText(t, res)
	if !strings.Contains(text, "green") || !strings.Contains(text, "Первая") {
		t.Errorf("mud_where after anchor: %q", text)
	}
	if !strings.Contains(text, "pending_moves: 0") {
		t.Errorf("expected pending_moves inline: %q", text)
	}
}

// TestMCPWhereSurfacesExitDiff drives a superset mismatch (the graceful-
// degradation case) and confirms mud_where reports yellow + the +live exit diff.
func TestMCPWhereSurfacesExitDiff(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Anchor at (0,0) "Первая", walk S; the live event for "Вторая" reports a
	// superset of exits (N S E) — the map has N S, so +E is live-only.
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
		tr.PushMove("S")
		tr.Reconcile(mapper.RoomEvent{Hint: "Вторая", Desc: "two", Exits: "N S E"})
	})

	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "yellow") {
		t.Errorf("superset mismatch should be yellow in mud_where: %q", text)
	}
	if !strings.Contains(text, "Exit diff") || !strings.Contains(text, "+E") {
		t.Errorf("mud_where should surface the +E exit diff: %q", text)
	}
	// It must NOT be red (graceful degradation, not loss).
	if strings.Contains(text, "Confidence: red") {
		t.Errorf("must not be red on a superset mismatch: %q", text)
	}
}

func TestMCPLookMap(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "Вторая") {
		t.Errorf("look_map missing hint: %q", text)
	}
	if !strings.Contains(text, "green") {
		t.Errorf("look_map missing confidence: %q", text)
	}
	// Exits N and S both mapped (ch=3).
	if !strings.Contains(text, "N") || !strings.Contains(text, "S") || !strings.Contains(text, "mapped") {
		t.Errorf("look_map missing exit connectivity: %q", text)
	}
}

func TestMCPPathAndRedRefusal(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Without anchoring, position is unknown/red => mud_path must refuse.
	res := callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sess.SessionID()))
	if res["isError"] != true {
		t.Errorf("mud_path on red should isError: %#v", res)
	}
	if !strings.Contains(toolText(t, res), "position lost") {
		t.Errorf("expected position-lost message: %q", toolText(t, res))
	}

	// Anchor, then path succeeds.
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))
	res = callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("mud_path after anchor should succeed: %q", toolText(t, res))
	}
	text := toolText(t, res)
	if !strings.Contains(text, "2 command(s)") || !strings.Contains(text, "ю; ю") {
		t.Errorf("mud_path route wrong: %q", text)
	}
}

func TestMCPSetActiveMapSetRebuildsIndex(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedTrackerSet(t, s, sess.SessionID())
	// Clear active + tracker so the tool must set + rebuild.
	s.store.SetActiveMapSetID(sess.SessionID(), 0)
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_set_active_map_set",
		fmt.Sprintf(`{"session_id":%d,"map_set":%d}`, sess.SessionID(), id))
	text := toolText(t, res)
	if res["isError"] == true {
		t.Fatalf("set_active errored: %q", text)
	}
	if !strings.Contains(text, "index rebuilt") {
		t.Errorf("expected rebuild confirmation: %q", text)
	}

	// The tracker index now resolves, so anchoring works.
	res = callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":2,"y":0,"l":0}`, sess.SessionID()))
	if res["isError"] == true {
		t.Errorf("anchor after set_active should work: %q", toolText(t, res))
	}
}

// TestMCPPathFlagsDoorAndPipe seeds a route with a closed door and a pipe run and
// confirms mud_path surfaces both: the door is flagged (not silently routed
// through) and the pipe run is collapsed to one command with a cell note.
func TestMCPPathFlagsDoorAndPipe(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	// A(0) --ю(DOOR)--> B(1) --ю--> P(2,pipe) --ю--> C(3).
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "DoorPipe", ZoneCount: 1, RoomCount: 4,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "A", Exits: "(S)",
				EDirs: `["S"]`, Doors: `["S"]`, Ch: 2},
			{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "B", Exits: "N S",
				EDirs: `["N","S"]`, Doors: `[]`, Ch: 3},
			{Zone: "Z", X: 2, Y: 0, L: 0, Tag: intPtr(3), Hint: "", Exits: "N S",
				EDirs: `["N","S"]`, Doors: `[]`, Ch: 3, Pipe: true},
			{Zone: "Z", X: 3, Y: 0, L: 0, Tag: intPtr(4), Hint: "C", Exits: "N",
				EDirs: `["N"]`, Doors: `[]`, Ch: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	s.store.SetActiveMapSetID(sess.SessionID(), id)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"C"}`, sess.SessionID()))
	text := toolText(t, res)
	if res["isError"] == true {
		t.Fatalf("mud_path errored: %q", text)
	}
	// Two commands: A->B (door) and B->P->C (pipe collapsed).
	if !strings.Contains(text, "2 command(s)") {
		t.Errorf("expected 2 collapsed commands: %q", text)
	}
	if !strings.Contains(text, "door(s) to open") || !strings.Contains(text, "[DOOR") {
		t.Errorf("door not flagged on route: %q", text)
	}
	if !strings.Contains(text, "pipe run") {
		t.Errorf("pipe run not noted on route: %q", text)
	}
}
