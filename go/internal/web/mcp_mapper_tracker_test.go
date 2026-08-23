package web

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"

	"golang.org/x/text/unicode/norm"
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

func TestMCPAnchorRequiresNonblankZone(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	for _, zone := range []string{"", "   "} {
		res := callTool(t, s, "mud_anchor_here",
			fmt.Sprintf(`{"session_id":%d,"zone":%q,"x":0,"y":0,"l":0}`, sess.SessionID(), zone))
		if res["isError"] != true {
			t.Fatalf("anchor with blank zone %q succeeded: %q", zone, toolText(t, res))
		}
		if !strings.Contains(toolText(t, res), "zone is required") {
			t.Errorf("anchor error = %q, want missing-zone explanation", toolText(t, res))
		}
	}
}

func TestMCPAnchorCanonicalizesZoneLookup(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	nfd := "Cafe\u0301"
	nfc := norm.NFC.String(nfd)
	if nfd == nfc {
		t.Fatal("test needs distinct NFD and NFC names")
	}
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Rooms: []storage.Room{{
		Zone: nfc, X: 0, Y: 0, L: 0, Hint: "Canonical room", EDirs: "[]", Doors: "[]",
	}}})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"  %s  ","x":0,"y":0,"l":0}`, sess.SessionID(), nfd))
	if res["isError"] == true || !strings.Contains(toolText(t, res), "green") {
		t.Fatalf("NFD anchor = %q, want exact green anchor", toolText(t, res))
	}
}

func TestMCPPathCanonicalizesToZone(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	nfd := "Cafe\u0301"
	nfc := norm.NFC.String(nfd)
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Rooms: []storage.Room{{
		Zone: nfc, X: 0, Y: 0, L: 0, Hint: "Canonical room", EDirs: "[]", Doors: "[]",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatal(err)
	}
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: nfc, X: 0, Y: 0, L: 0})
	res := callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_zone":"  %s  "}`, sess.SessionID(), nfd))
	if res["isError"] == true || !strings.Contains(toolText(t, res), "0 command(s)") {
		t.Fatalf("NFD to_zone route = %q, want current-zone zero-hop route", toolText(t, res))
	}
}

func TestMCPAnchorRequiresEveryCoordinateField(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	for _, coordinates := range []string{
		`"y":0,"l":0`,
		`"x":0,"l":0`,
		`"x":0,"y":0`,
	} {
		res := callTool(t, s, "mud_anchor_here",
			fmt.Sprintf(`{"session_id":%d,"zone":"Z",%s}`, sess.SessionID(), coordinates))
		if res["isError"] != true || !strings.Contains(toolText(t, res), "zone,x,y,l") {
			t.Fatalf("anchor with incomplete coordinates %s = %#v", coordinates, res)
		}
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
	if !strings.Contains(text, "2 command(s)") || !strings.Contains(text, "s; s") {
		t.Errorf("mud_path route wrong (expected english s; s): %q", text)
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

func TestMCPPathWarnsThatDelayedOpeningsNeedServerWalk(t *testing.T) {
	text, isError := formatPath("Тупик", mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{{
		Command: "d", Dir: "d", Door: true, DoorKind: mapper.DoorConfirmed,
		OpenAction: "повернуть рычаг", OpenDelayMS: 2000, Cells: 1,
	}}})
	if isError || !strings.Contains(text, "do NOT send as one script") ||
		!strings.Contains(text, "Use mud_map_walk") ||
		strings.Contains(text, "mud_map_walk_start") ||
		!strings.Contains(text, "WAIT 2000 ms after opening") {
		t.Fatalf("delayed MCP path is unsafe or incomplete: %q", text)
	}
	if strings.Contains(text, "Commands: повернуть рычаг; d") {
		t.Fatalf("delayed route exposed an apparently sendable script: %q", text)
	}
}

// TestMCPPathFlagsPresumedDoor is the live field case surfaced through mud_path:
// hop с (north) into a cell whose SOUTH face records the door while the source
// has no door on that face -> the route must flag it as a PRESUMED door, distinct
// from a confirmed one, and count it separately in the summary.
func TestMCPPathFlagsPresumedDoor(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	// (7,-4) A exits N, no door; (6,-4) B exits (S) -> door on its south face.
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "Presumed", ZoneCount: 1, RoomCount: 2,
		Rooms: []storage.Room{
			{Zone: "Хилло", X: 7, Y: -4, L: 0, Tag: intPtr(1), Hint: "A", Exits: "N",
				EDirs: `["N"]`, Doors: `[]`, Ch: 1},
			{Zone: "Хилло", X: 6, Y: -4, L: 0, Tag: intPtr(2), Hint: "B", Exits: "(S)",
				EDirs: `["S"]`, Doors: `["S"]`, Ch: 2},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	s.store.SetActiveMapSetID(sess.SessionID(), id)
	sess.LoadActiveMapSet()
	// The door-opening command belongs to the session's ROOM-FORMAT PROFILE, not to
	// shared code, so a route only carries one once the profile is resolved — which
	// on the connect path happens right after LoadActiveMapSet. Doing both here makes
	// this the end-to-end check of that wiring: profile -> index -> route.
	sess.LoadMapperProfile()
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Хилло","x":7,"y":-4,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"B"}`, sess.SessionID()))
	text := toolText(t, res)
	if res["isError"] == true {
		t.Fatalf("mud_path errored: %q", text)
	}
	if !strings.Contains(text, "presumed door(s)") {
		t.Errorf("summary should count presumed doors: %q", text)
	}
	// Since door attributes are materialized on directed edges the route no longer stops at a door: the opening command
	// is emitted as a step of the walk, immediately BEFORE the move it unlocks. The
	// door is recorded only on B's south face, so it is presumed — and a presumed
	// door gets its opening action too (harmless if there is none).
	if !strings.Contains(text, "presumed") || !strings.Contains(text, "Commands: откр две север; n") {
		t.Errorf("presumed door hop not flagged/opened on the english n step: %q", text)
	}
	// Must NOT report a confirmed "door(s) to open" (there is none on the source).
	if strings.Contains(text, "door(s) to open") {
		t.Errorf("should not report a confirmed door for a presumed-only hop: %q", text)
	}
}

// TestMCPPathSeamEmitsEnglish is the seam golden: a cross-zone route's seam hop
// must emit the canonical english letter (e), NOT the raw ".mm2" command "на
// восток" (which the client mis-parses as "надеть" and derails on). The
// [SEAM →Zone] annotation must still be present, and the whole joined route must
// be english.
func TestMCPPathSeamEmitsEnglish(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	// Zone A "Берег" seams east ("на восток") into zone B "Море" (tag 50).
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "Seam", ZoneCount: 2, RoomCount: 2, SeamCount: 1,
		Rooms: []storage.Room{
			{Zone: "A", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "Берег", Exits: "E",
				EDirs: `["E"]`, Doors: `[]`, Ch: 4},
			{Zone: "B", X: 9, Y: 9, L: 0, Tag: intPtr(50), Hint: "Море", Exits: "N",
				EDirs: `["N"]`, Doors: `[]`, Ch: 1},
		},
		LegacySeams: []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, "B|на восток|50")},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	s.store.SetActiveMapSetID(sess.SessionID(), id)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"A","x":0,"y":0,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_zone":"B"}`, sess.SessionID()))
	text := toolText(t, res)
	if res["isError"] == true {
		t.Fatalf("mud_path errored: %q", text)
	}
	// The emitted command must be english "e", NOT the raw "на восток".
	if !strings.Contains(text, "Commands: e") {
		t.Errorf("seam route should emit english 'e', got: %q", text)
	}
	if strings.Contains(text, "Commands: на восток") || strings.Contains(text, "; на восток") {
		t.Errorf("seam route must NOT emit the raw 'на восток' command: %q", text)
	}
	// The [SEAM →B] annotation (with the raw map command) is still present.
	if !strings.Contains(text, "[SEAM →B") {
		t.Errorf("seam annotation missing: %q", text)
	}
	if !strings.Contains(text, "на восток") {
		t.Errorf("raw seam command should still appear as an annotation: %q", text)
	}
}
