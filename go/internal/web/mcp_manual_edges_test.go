package web

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// End-to-end cover for the hand-drawn edge layer through the UNIFIED write-path:
// mud_room_mark_edge -> Session.WriteTopology (fork-if-frozen, per-set journal,
// position-preserving refresh) -> mud_path / mud_map_undo.
//
// seedTrackerSet gives zone Z as a 3-cell strip: (0,0,0)[S] - (1,0,0)[N,S] -
// (2,0,0)[N], i.e. "Первая" -> "Вторая" -> "Третья", with no other cells.

// markEdge calls mud_room_mark_edge and fails the test on a tool error.
func markEdge(t *testing.T, s *Server, sid int64, x, y, l int, dir, mark string, oneWay bool) string {
	t.Helper()
	res := callTool(t, s, "mud_room_mark_edge", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":%d,"y":%d,"l":%d,"dir":%q,"mark":%q,"one_way":%v}`,
		sid, x, y, l, dir, mark, oneWay))
	if res["isError"] == true {
		t.Fatalf("mud_room_mark_edge(%s,%s) errored: %q", dir, mark, toolText(t, res))
	}
	return toolText(t, res)
}

// TestMCPMarkHiddenExitMakesTheRouteAndSurvivesARoomUpdate is the live 16077
// shape: a room whose exits line says nothing, reachable only because a player
// marked the way in by hand. The route must exist afterwards, and re-recording the
// room from its (still silent) live text must not take it away.
func TestMCPMarkHiddenExitMakesTheRouteAndSurvivesARoomUpdate(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// A secret cell east of "Вторая": no exits at all, and "Вторая" does not
	// advertise E either — so nothing connects it.
	res := callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":1,"y":1,"l":0,"hint":"Ящики","desc":"crates","exits":""}`, sid))
	if res["isError"] == true {
		t.Fatalf("upsert secret room: %q", toolText(t, res))
	}
	setID := activeSetID(t, s, sid) // the upsert forked the frozen set

	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Ящики"}`, sid))
	if res["isError"] != true {
		t.Fatalf("the secret room must be unreachable before the mark: %q", toolText(t, res))
	}

	// Mark the way in, from "Вторая" east. Default = both directions.
	txt := markEdge(t, s, sid, 1, 0, 0, "E", "open", false)
	if !strings.Contains(txt, "HIDDEN EXIT") {
		t.Errorf("mark report does not say what it did: %q", txt)
	}
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Ящики"}`, sid))
	if res["isError"] == true {
		t.Fatalf("route through the hand-drawn edge not found: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "s; e") {
		t.Errorf("route = %q, want it to end with the marked east step", toolText(t, res))
	}

	// The room's own exits are untouched — this layer is not the parsed one.
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); slimHasExit(r, "E") {
		t.Errorf("the mark leaked into the room's exits: %v", r.E)
	}

	// «Обновить комнату»: re-record "Вторая" from live text that (as always) says
	// nothing about the crates. The mark must still be there.
	res = callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0,"hint":"Вторая","desc":"two","exits":"N S"}`, sid))
	if res["isError"] == true {
		t.Fatalf("re-record: %q", toolText(t, res))
	}
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Ящики"}`, sid))
	if res["isError"] == true {
		t.Fatalf("«обновить комнату» ate the hand-drawn edge: %q", toolText(t, res))
	}
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); !containsStr(r.M, "E") {
		t.Errorf("slim room lost the hidden-exit marker the pane draws: m=%v", r.M)
	}
}

// TestMCPMarkEdgeUndo: one mark is one journal entry, and undo takes the markup
// back to nothing (the route disappears again).
func TestMCPMarkEdgeUndo(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":1,"y":1,"l":0,"hint":"Ящики","exits":""}`, sid))
	setID := activeSetID(t, s, sid)
	markEdge(t, s, sid, 1, 0, 0, "E", "open", false)
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo errored: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "hidden exit") {
		t.Errorf("undo report should name the mark: %q", toolText(t, res))
	}
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); len(r.M) != 0 {
		t.Errorf("undo left hand-drawn markup behind: %v", r.M)
	}
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Ящики"}`, sid))
	if res["isError"] != true {
		t.Fatalf("the route survived the undo of the mark: %q", toolText(t, res))
	}
}

// TestMCPBlockEdgeStopsRoutingAndUndoBringsItBack is the negation scenario on an
// edge that IS derived from the room text today: deny it, the route stops using
// it, undo restores the route. This is the shape of a displaced room whose
// visual-grid neighbour is not where its exit actually leads.
func TestMCPBlockEdgeStopsRoutingAndUndoBringsItBack(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	res := callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sid))
	if res["isError"] == true {
		t.Fatalf("baseline route missing: %q", toolText(t, res))
	}

	// Deny the middle hop, one way (out of "Первая" south).
	txt := markEdge(t, s, sid, 0, 0, 0, "S", "blocked", true)
	if !strings.Contains(txt, "NO PASSAGE") {
		t.Errorf("block report does not say what it did: %q", txt)
	}
	setID := activeSetID(t, s, sid)
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sid))
	if res["isError"] != true {
		t.Fatalf("mud_path still routes through a denied edge: %q", toolText(t, res))
	}
	// The room still PRINTS the exit — the negation is about the graph, not the text.
	if r := slimRoomAt(t, s, setID, "Z", 0, 0, 0); !slimHasExit(r, "S") {
		t.Errorf("the negation edited the room's exits, which it must not: %v", r.E)
	}
	if r := slimRoomAt(t, s, setID, "Z", 0, 0, 0); !containsStr(r.MB, "S") {
		t.Errorf("slim room lost the negation marker the pane draws: mb=%v", r.MB)
	}
	// One way only: the way back is still walkable, so the route from the far end
	// still exists.
	res = callTool(t, s, "mud_path", fmt.Sprintf(
		`{"session_id":%d,"from":{"zone":"Z","x":2,"y":0,"l":0},"to_hint":"Первая"}`, sid))
	if res["isError"] == true {
		t.Fatalf("a one-way negation also closed the way back: %q", toolText(t, res))
	}

	// Undo: route restored.
	if res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid)); res["isError"] == true {
		t.Fatalf("undo errored: %q", toolText(t, res))
	}
	res = callTool(t, s, "mud_path", fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo of the negation did not restore the route: %q", toolText(t, res))
	}
}

// TestMCPMarkEdgeNeedsBothCells: the tool refuses (and says why) when the face
// leads into empty space, instead of writing a row nothing can walk or clean up.
func TestMCPMarkEdgeNeedsBothCells(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_room_mark_edge", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"dir":"U","mark":"open"}`, sid))
	if res["isError"] != true {
		t.Fatalf("mark into empty space must soft-fail: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "BOTH sides") {
		t.Errorf("refusal should explain the constraint: %q", toolText(t, res))
	}
}

// TestMCPLookMapShowsTheHandDrawnLayer: an agent reading a room must be able to
// SEE a hidden exit (which is in no exits line) and a denied one (which is in the
// exits line but is not walkable). Without this the layer is invisible to the
// reader that most needs it.
func TestMCPLookMapShowsTheHandDrawnLayer(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":1,"y":1,"l":0,"hint":"Ящики","exits":""}`, sid))
	markEdge(t, s, sid, 1, 0, 0, "E", "open", false)
	markEdge(t, s, sid, 1, 0, 0, "N", "blocked", true)
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0}`, sid))

	text := toolText(t, callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sid)))
	if !strings.Contains(text, "HIDDEN EXIT") {
		t.Errorf("mud_look_map hides the hand-drawn exit: %q", text)
	}
	if !strings.Contains(text, "NO PASSAGE") {
		t.Errorf("mud_look_map does not flag the denied exit: %q", text)
	}
	// And the mapped/unmapped column must agree with the graph rather than
	// contradicting the flag next to it.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "HIDDEN EXIT") && strings.Contains(line, "unmapped") {
			t.Errorf("a hidden exit routes are using is reported as unmapped: %q", line)
		}
		if strings.Contains(line, "NO PASSAGE") && strings.Contains(line, " mapped") {
			t.Errorf("a denied exit no route can use is reported as mapped: %q", line)
		}
	}
}

// TestMarkEdgeRESTIsOneDirection: the pane's edge editor shows the two halves of a
// boundary separately and edits them independently, so its endpoint is always
// one-way — otherwise checking "there is a way" on one side would silently rewrite
// the other side's checkbox.
func TestMarkEdgeRESTIsOneDirection(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	res, err := sess.WriteTopology(storage.TopologyOp{
		Kind: storage.TopoBlockManualEdge, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	})
	if err != nil || !res.Applied {
		t.Fatalf("block: err=%v applied=%v", err, res.Applied)
	}
	setID := activeSetID(t, s, sid)
	if r := slimRoomAt(t, s, setID, "Z", 0, 0, 0); !containsStr(r.MB, "S") {
		t.Errorf("near side not denied: mb=%v", r.MB)
	}
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); len(r.MB) != 0 {
		t.Errorf("the far side was denied too — face ownership is not per direction: mb=%v", r.MB)
	}
}

// --- review round 2 ----------------------------------------------------------

// TestFailedUndoKeepsItsJournalEntry: an undo whose restore rolls back must leave
// the journal exactly as it found it. Consuming the entry would make the NEXT
// «Отменить» reverse the write BEFORE the failed one — undoing something the user
// never pointed at, and reporting success while doing it.
//
// The failure is injected with an entry whose hand-drawn snapshot claims one FACE
// twice: restoring it necessarily violates the one-row-per-face UNIQUE index, which
// is the same class of failure the derived-row collision was.
func TestFailedUndoKeepsItsJournalEntry(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// A real write first, so there is something underneath to be wrongly undone.
	markEdge(t, s, sid, 1, 0, 0, "N", "blocked", true)
	setID := activeSetID(t, s, sid)
	j := s.store.WriteJournal()
	if j.Depth(setID) != 1 {
		t.Fatalf("journal depth after one write = %d, want 1", j.Depth(setID))
	}

	dup := func() storage.MapEdge {
		e := storage.MapEdge{
			MapSetID: setID, Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceManual,
			Passable: true, FromZone: "Z", FromX: 0, FromY: 0, FromL: 0, Dir: "S",
		}
		to := storage.EdgeCell{Zone: "Z", X: 1, Y: 0, L: 0}
		e.SetTo(&to)
		return e
	}
	j.Push(setID, storage.UndoEntry{Label: "poison", Before: storage.TopologyBefore{
		Manual: storage.ManualEdgeSnapshot{
			Cells: []storage.EdgeCell{{Zone: "Z", X: 0, Y: 0, L: 0}},
			Edges: []storage.MapEdge{dup(), dup()},
		},
	}})
	if j.Depth(setID) != 2 {
		t.Fatalf("journal depth = %d, want 2", j.Depth(setID))
	}

	if _, _, err := sess.UndoTopology(); err == nil {
		t.Fatal("the poisoned undo was supposed to fail")
	}
	if got := j.Depth(setID); got != 2 {
		t.Fatalf("a failed undo consumed its journal entry: depth = %d, want 2", got)
	}
	// And the entry still on top is the one that failed, not the write below it.
	entry, ok := j.Pop(setID)
	if !ok || entry.Label != "poison" {
		t.Fatalf("top of stack after the failed undo = %q (ok=%v), want the failed entry", entry.Label, ok)
	}
	// The write underneath is untouched and still undoable.
	if got := j.Depth(setID); got != 1 {
		t.Fatalf("the write underneath went missing: depth = %d, want 1", got)
	}
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); !containsStr(r.MB, "N") {
		t.Errorf("the failed undo also mangled the write underneath: mb=%v", r.MB)
	}
}

// TestUndoAfterClearOverRESTDoesNotError is the three-click UI path end to end:
// deny a face, take the denial back, undo. It went through the REST/MCP surface as
// a 500 / isError before the face-clearing fix.
func TestUndoAfterClearOverRESTDoesNotError(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	markEdge(t, s, sid, 0, 0, 0, "S", "blocked", true)
	setID := activeSetID(t, s, sid)
	markEdge(t, s, sid, 0, 0, 0, "S", "auto", true)

	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo of a cleared mark errored: %q", toolText(t, res))
	}
	if r := slimRoomAt(t, s, setID, "Z", 0, 0, 0); !containsStr(r.MB, "S") {
		t.Errorf("undo did not put the negation back: mb=%v g=%v", r.MB, r.G)
	}
}

// TestRecordTouchesRespectsTheHandDrawnLayer: the record report exists to make a
// write's blast radius visible, so a FALSE entry in it is worse than none. Walking
// the exits line announced "the record connected you to X" across a face the
// player had marked NO PASSAGE — the exits line still names the direction, and only
// the graph knows the passage was denied.
func TestRecordTouchesRespectsTheHandDrawnLayer(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// (1,0,0) "Вторая" prints N and S and really connects both ways.
	target := mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0}
	before := recordTouches(sess, target)
	if len(before) != 2 {
		t.Fatalf("baseline touches = %v, want both neighbours", before)
	}

	// Deny the north face by hand. The room still PRINTS N.
	markEdge(t, s, sid, 1, 0, 0, "N", "blocked", true)
	setID := activeSetID(t, s, sid)
	if r := slimRoomAt(t, s, setID, "Z", 1, 0, 0); !slimHasExit(r, "N") {
		t.Fatal("setup: the exits line should still name N")
	}

	after := recordTouches(sess, target)
	for _, tch := range after {
		if tch["dir"] == "N" {
			t.Fatalf("the record report claims a connection across a denied face: %v", after)
		}
	}
	if len(after) != 1 {
		t.Fatalf("touches after the negation = %v, want only the surviving south link", after)
	}

	// A hidden exit is reported too, and flagged as the player's own, not the
	// record's doing.
	callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":1,"y":1,"l":0,"hint":"Ящики","exits":""}`, sid))
	markEdge(t, s, sid, 1, 0, 0, "E", "open", false)
	found := false
	for _, tch := range recordTouches(sess, target) {
		if tch["dir"] == "E" {
			found = true
			if tch["manual"] != true {
				t.Errorf("a hand-drawn passage is not flagged as hand-drawn: %v", tch)
			}
		}
	}
	if !found {
		t.Error("the record report hides a hand-drawn passage out of the recorded cell")
	}
}
