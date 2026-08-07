package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// These cover POST /map-record-room (the «Записать комнату»
// human autorecord) and POST /map-undo (the REST undo wrapper). They drive the
// real HTTP layer against the MCP test harness (temp in-memory DB) exactly like
// mapper_write_path_test.go / mapper_session_test.go.

// setLiveRoom injects the session's retained live-room event. The web-package
// harness builds its Session by reflection (no TCP, no read loop), so the parser
// pipeline that normally fills lastRoom cannot run here — the wiring from a real
// room block to lastRoom is covered in the session package
// (TestObserveIncomingRoomBlockRetainsLastRoom).
func setLiveRoom(t *testing.T, sess *session.Session, hint, desc, exits string) {
	t.Helper()
	setMcpUnexportedField(t, sess, "lastRoom", mapper.RoomEvent{Hint: hint, Desc: desc, Exits: exits})
	setMcpUnexportedField(t, sess, "lastRoomOK", true)
}

// postJSON POSTs body to a session-scoped map endpoint and decodes the JSON,
// asserting the soft-fail contract (always 200).
func postJSON(t *testing.T, ts *httptest.Server, token string, sid int64, path, body string) map[string]any {
	t.Helper()
	req, _ := newAuthenticatedRequest(http.MethodPost,
		fmt.Sprintf("%s/api/sessions/%d/%s", ts.URL, sid, path), strings.NewReader(body), token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want 200 (soft-fail contract)", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

// storeRoomAt loads the FULL room row at a coord (slimRoomAt only exposes the
// map-pane subset, which omits note/image_index/tag).
func storeRoomAt(t *testing.T, s *Server, setID int64, zone string, x, y, l int) storage.Room {
	t.Helper()
	rooms, err := s.store.ListRooms(setID)
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	for _, r := range rooms {
		if r.Zone == zone && r.X == x && r.Y == y && r.L == l {
			return r
		}
	}
	t.Fatalf("no room at {%s %d,%d,%d} in set %d", zone, x, y, l, setID)
	return storage.Room{}
}

func postRecordRoom(t *testing.T, ts *httptest.Server, token string, sid int64, body string) map[string]any {
	t.Helper()
	return postJSON(t, ts, token, sid, "map-record-room", body)
}

func postMapUndo(t *testing.T, ts *httptest.Server, token string, sid int64) map[string]any {
	t.Helper()
	return postJSON(t, ts, token, sid, "map-undo", "{}")
}

// walkIntoUnmappedCell anchors at the seeded (1,0,0) and walks E — a direction
// the map does NOT record — so the tracker lands 🟡 at the dead-reckoned,
// UNMAPPED coord (1,1,0) (tracker branch (c)). That is the exact state the
// "draw a new room" case of the record button exists for. It also sets the live
// room to the room the game reports there, whose exits include W (the way back).
func walkIntoUnmappedCell(t *testing.T, sess *session.Session) mapper.Coord {
	t.Helper()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.PushMove("E")
		tr.Reconcile(mapper.RoomEvent{Hint: "Новая", Desc: "brand new", Exits: "W E"})
	})
	setLiveRoom(t, sess, "Новая", "brand new", "W E")

	var pos mapper.Position
	sess.WithMapTracker(func(tr *mapper.Tracker) { pos = tr.Position() })
	if !pos.Valid || pos.Coord != (mapper.Coord{Zone: "Z", X: 1, Y: 1, L: 0}) {
		t.Fatalf("precondition: expected dead-reckoned 🟡 at Z 1,1,0, got %+v", pos)
	}
	if pos.Confidence != mapper.Yellow {
		t.Fatalf("precondition: expected yellow at an unmapped cell, got %q", pos.Confidence)
	}
	return pos.Coord
}

// TestRecordRoomCreatesUnmappedCell: the player is standing at a dead-reckoned
// coord with NO room in the map; recording CREATES the room there from the live
// text, reports created=true, forks the frozen set, and re-anchors to 🟢.
func TestRecordRoomCreatesUnmappedCell(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid) // frozen imported set
	sess.LoadActiveMapSet()
	walkIntoUnmappedCell(t, sess)

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}
	if out["created"] != true || out["updated"] != false {
		t.Errorf("want created (cell did not exist), got %#v", out)
	}
	if out["forked"] != true {
		t.Errorf("recording onto a FROZEN set must fork it copy-on-write: %#v", out)
	}
	forkID := int64(out["forked_to"].(float64))

	room := slimRoomAt(t, s, forkID, "Z", 1, 1, 0)
	if room == nil {
		t.Fatal("no room was created at the recorded coord")
	}
	if room.H != "Новая" {
		t.Errorf("hint = %q, want the LIVE room name", room.H)
	}
	if !slimHasExit(room, "W") || !slimHasExit(room, "E") {
		t.Errorf("exits not taken from the live event: %+v", room.E)
	}

	// Recording is an explicit human assertion "this room is here" — the tracker
	// re-anchors onto it, so confidence becomes green (exact).
	if out["exact"] != true {
		t.Errorf("record should re-anchor exactly onto the new cell: %#v", out)
	}
	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	if txt := toolText(t, res); !strings.Contains(txt, "Confidence: green") || !strings.Contains(txt, "x=1 y=1 l=0") {
		t.Errorf("tracker should be green at the recorded cell: %q", txt)
	}
}

// TestRecordRoomRoutesIntoFreshlyRecordedRoom is the load-bearing check on the
// spec's "NO link op needed" simplification: after recording a new room whose
// LIVE exits contain the way back (W), a route from elsewhere must be able to
// walk INTO it — even though the source cell (1,0,0) still does not advertise E.
// IndexRoom.ConnectsTo accepts the edge because the NEW room advertises the
// opposite direction. If this fails, the record needs an explicit TopoLink.
func TestRecordRoomRoutesIntoFreshlyRecordedRoom(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	walkIntoUnmappedCell(t, sess)

	if out := postRecordRoom(t, ts, s.apiToken, sid, "{}"); out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}

	// The source cell must still NOT advertise E — the edge is one-sided in the
	// data, and only ConnectsTo's back-link rule makes it routable.
	forkID, _, _ := s.store.GetActiveMapSetID(sid)
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 1, 0, 0), "E") {
		t.Fatal("precondition broken: the source cell gained an E exit (a link op was emitted?)")
	}

	// Stand somewhere else and route INTO the recorded cell.
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	out := postJSON(t, ts, s.apiToken, sid, "map-path", `{"to":{"zone":"Z","x":1,"y":1,"l":0}}`)
	if out["reachable"] != true {
		t.Fatalf("cannot route INTO the freshly recorded room — the no-link-op "+
			"simplification does NOT hold: %#v", out)
	}
	dirs, _ := out["directions"].([]any)
	got := make([]string, 0, len(dirs))
	for _, d := range dirs {
		got = append(got, d.(string))
	}
	if strings.Join(got, ";") != "s;e" {
		t.Errorf("route into recorded room = %v, want [s e]", got)
	}
}

// TestRecordRoomWithoutBackExitIsNotRoutable is the NEGATIVE control for the test
// above: it proves the route succeeds BECAUSE the recorded room advertises the
// way back, not because of some permissive grid fallback. Record the same cell
// with live exits that do NOT include W and the same route is unreachable — which
// is also the honest limit of the no-link-op simplification (a MUD room that hides
// its back exit would need an explicit link).
func TestRecordRoomWithoutBackExitIsNotRoutable(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	walkIntoUnmappedCell(t, sess)
	setLiveRoom(t, sess, "Новая", "brand new", "E") // no W back to (1,0,0)

	if out := postRecordRoom(t, ts, s.apiToken, sid, "{}"); out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	out := postJSON(t, ts, s.apiToken, sid, "map-path", `{"to":{"zone":"Z","x":1,"y":1,"l":0}}`)
	if out["reachable"] == true {
		t.Errorf("route should NOT exist without a back-exit — the positive test is "+
			"passing for the wrong reason: %#v", out)
	}
}

// TestRecordRoomUpdatesExistingAndPreservesMetadata: recording while standing on
// a cell that ALREADY exists UPDATES hint/desc/exits from the live text and
// leaves the room's manual metadata (note, is_dt, image) untouched — the fields
// the op leaves nil are preserved by upsertRoomWithSnapshot on an update.
func TestRecordRoomUpdatesExistingAndPreservesMetadata(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// Give (0,0,0) manual metadata through the SAME write-path (MCP upsert), which
	// also forks the frozen set so both writes land on the editable copy.
	res := callTool(t, s, "mud_room_upsert", fmt.Sprintf(
		`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":"мой склад","is_dt":true,"imageindex":7}`, sid))
	if res["isError"] == true {
		t.Fatalf("seed upsert failed: %q", toolText(t, res))
	}
	setID, _, _ := s.store.GetActiveMapSetID(sid)

	// Stand on it with an OUTDATED map: the game now reports a U exit and a new
	// description the map lacks.
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	setLiveRoom(t, sess, "Первая", "one, rebuilt", "S U")

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}
	if out["updated"] != true || out["created"] != false {
		t.Errorf("want updated (cell existed), got %#v", out)
	}

	room := storeRoomAt(t, s, setID, "Z", 0, 0, 0)
	if room.Desc != "one, rebuilt" || room.Hint != "Первая" {
		t.Errorf("hint/desc not refreshed from live: %q / %q", room.Hint, room.Desc)
	}
	if !strings.Contains(room.EDirs, "U") {
		t.Errorf("live U exit not recorded: edirs=%q", room.EDirs)
	}
	// The nil fields survived the update.
	if room.Note != "мой склад" {
		t.Errorf("manual note was clobbered: %q", room.Note)
	}
	if !room.IsDT {
		t.Error("is_dt was clobbered by the record")
	}
	if room.ImageIndex == nil || *room.ImageIndex != 7 {
		t.Errorf("image_index was clobbered: %v", room.ImageIndex)
	}
	if room.Tag == nil || *room.Tag != 1 {
		t.Errorf("tag was clobbered: %v", room.Tag)
	}
}

// TestRecordRoomOneUndoEntry: a record is exactly ONE TopologyOp, so exactly ONE
// undo reverses it (the spec's "do not emit a second link op" invariant), and the
// created room disappears again.
func TestRecordRoomOneUndoEntry(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	walkIntoUnmappedCell(t, sess)

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	forkID := int64(out["forked_to"].(float64))
	if depth := s.store.WriteJournal().Depth(forkID); depth != 1 {
		t.Fatalf("journal depth after ONE record = %d, want exactly 1 (no extra link op)", depth)
	}

	undo := postMapUndo(t, ts, s.apiToken, sid)
	if undo["applied"] != true {
		t.Fatalf("undo not applied: %#v", undo)
	}
	if undo["label"] != "upsert room" {
		t.Errorf("undo label = %v, want \"upsert room\"", undo["label"])
	}
	if undo["zone"] != "Z" || undo["x"] != float64(1) || undo["y"] != float64(1) {
		t.Errorf("undo did not report the recorded cell: %#v", undo)
	}
	if slimRoomAt(t, s, forkID, "Z", 1, 1, 0) != nil {
		t.Error("ONE undo should have removed the recorded room entirely")
	}
	if depth := s.store.WriteJournal().Depth(forkID); depth != 0 {
		t.Errorf("journal depth after undo = %d, want 0", depth)
	}
}

// TestMapUndoNothingToUndo: the REST undo reports nothing_to_undo on an empty
// per-set journal instead of erroring.
func TestMapUndoNothingToUndo(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	out := postMapUndo(t, ts, s.apiToken, sid)
	if out["applied"] != false || out["nothing_to_undo"] != true {
		t.Errorf("want nothing_to_undo, got %#v", out)
	}
	if out["reason"] == nil || out["reason"] == "" {
		t.Errorf("expected a human reason: %#v", out)
	}
}

// TestRecordRoomSoftFailsWithoutLiveRoom: no room block seen yet => 200 with a
// reason, no write.
func TestRecordRoomSoftFailsWithoutLiveRoom(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != false {
		t.Fatalf("record should refuse without a live room: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["reason"]), "no live room") {
		t.Errorf("reason = %v, want a 'no live room' explanation", out["reason"])
	}
	setID, _, _ := s.store.GetActiveMapSetID(sid)
	if s.store.WriteJournal().Depth(setID) != 0 {
		t.Error("a refused record must not journal a write")
	}
}

// TestRecordRoomSoftFailsWithoutPosition: a tracker with no valid position (no
// anchor yet) refuses with a reason rather than guessing a coord.
func TestRecordRoomSoftFailsWithoutPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	setLiveRoom(t, sess, "Первая", "one", "S")

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != false {
		t.Fatalf("record should refuse without a position: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["reason"]), "position unknown") {
		t.Errorf("reason = %v, want a 'position unknown' explanation", out["reason"])
	}
}

// TestRecordRoomSoftFailsWithoutActiveSet: no active map set => reason, no 500.
func TestRecordRoomSoftFailsWithoutActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	sess.LoadActiveMapSet() // nil index
	setLiveRoom(t, sess, "Первая", "one", "S")

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != false || !strings.Contains(fmt.Sprint(out["reason"]), "no active map set") {
		t.Errorf("want a no-active-set refusal, got %#v", out)
	}
}

// TestRecordRoomRefusesForeignCell: an explicit coord that is NOT the cell the
// player stands in is refused — the live text describes where the player IS, so
// writing it elsewhere would record the wrong room.
func TestRecordRoomRefusesForeignCell(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	setLiveRoom(t, sess, "Первая", "one", "S")

	out := postRecordRoom(t, ts, s.apiToken, sid, `{"zone":"Z","x":2,"y":0,"l":0}`)
	if out["applied"] != false {
		t.Fatalf("recording a cell you are not standing in must be refused: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["reason"]), "standing in") {
		t.Errorf("reason = %v", out["reason"])
	}

	// The matching explicit coord is accepted (the UI always sends one).
	ok := postRecordRoom(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0}`)
	if ok["applied"] != true || ok["updated"] != true {
		t.Errorf("explicit CURRENT coord should apply as an update: %#v", ok)
	}
}

// --- guards that protect existing map data (review round 1) -----------------

// TestRecordRefusesWhileRedLost is the CRITICAL regression: Position.Valid stays
// TRUE on a text veto (tracker step 5b holds the LAST-KNOWN coord at 🔴), and
// lastRoom already holds the text of the room the player was teleported INTO. A
// record in that state would overwrite a good, unrelated cell and then cement a
// false 🟢 by re-anchoring. It must refuse, leaving the cell untouched.
func TestRecordRefusesWhileRedLost(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	// Walk S; the MUD teleports us to an unrelated room. The hint contradicts the
	// assumed cell -> text veto -> 🔴, but Valid stays true at the last-known coord.
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.PushMove("S")
		tr.Reconcile(mapper.RoomEvent{Hint: "Тюрьма", Desc: "far away jail", Exits: "N"})
	})
	setLiveRoom(t, sess, "Тюрьма", "far away jail", "N")

	var pos mapper.Position
	sess.WithMapTracker(func(tr *mapper.Tracker) { pos = tr.Position() })
	if pos.Confidence != mapper.Red || !pos.Valid {
		t.Fatalf("precondition: want valid+RED, got %+v", pos)
	}

	out := postRecordRoom(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0}`)
	if out["applied"] != false {
		t.Fatalf("recording while 🔴 lost must be refused: %#v", out)
	}
	setID, _, _ := s.store.GetActiveMapSetID(sid)
	if room := storeRoomAt(t, s, setID, "Z", 0, 0, 0); room.Hint != "Первая" {
		t.Errorf("the good cell was overwritten: hint=%q, want Первая", room.Hint)
	}
	if s.store.WriteJournal().Depth(setID) != 0 {
		t.Error("a refused record must not journal a write")
	}
}

// TestRecordRefusesEmptyHint: the exits-only accumulator path yields an
// empty-hint/empty-desc event (a pipe corridor with no header block). Writing it
// would wipe the mapped room's name AND description and rewrite its fingerprint,
// after which nothing could ever fingerprint-match that cell again. The endpoint
// — not just the UI — must refuse it.
func TestRecordRefusesEmptyHint(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	setLiveRoom(t, sess, "", "", "S") // hint-less event

	out := postRecordRoom(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0}`)
	if out["applied"] != false {
		t.Fatalf("an empty-hint event must be refused: %#v", out)
	}
	setID, _, _ := s.store.GetActiveMapSetID(sid)
	room := storeRoomAt(t, s, setID, "Z", 0, 0, 0)
	if room.Hint != "Первая" || room.Desc != "one" {
		t.Errorf("the room's name/desc were wiped: hint=%q desc=%q", room.Hint, room.Desc)
	}
}

// TestRecordRefusesAmbiguousHeldPosition: tracker step 2-ambiguous holds the OLD
// coord at 🟡 after the player has actually moved, so the live text describes a
// DIFFERENT cell. Neither the red guard nor the empty-hint guard catches it —
// Position.EventDescribesCell does.
func TestRecordRefusesAmbiguousHeldPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	// Two neighbours of (0,0,0) are IDENTICAL, so a following-step event matches
	// both and the tracker cannot pick one.
	setID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "Twins", ZoneCount: 1, RoomCount: 3,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "Центр", Desc: "hub",
				Exits: "N S", EDirs: `["N","S"]`, Doors: `[]`, Ch: 3},
			// IDENTICAL text AND exits, so one event matches both equally.
			{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "Близнец", Desc: "twin",
				Exits: "N", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
			{Zone: "Z", X: -1, Y: 0, L: 0, Tag: intPtr(3), Hint: "Близнец", Desc: "twin",
				Exits: "N", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sid, setID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	// No pending move (a "following" step): the neighbour search matches BOTH
	// twins -> ambiguous -> hold the OLD coord at 🟡.
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.Reconcile(mapper.RoomEvent{Hint: "Близнец", Desc: "twin", Exits: "N"})
	})
	setLiveRoom(t, sess, "Близнец", "twin", "N")

	var pos mapper.Position
	sess.WithMapTracker(func(tr *mapper.Tracker) { pos = tr.Position() })
	if pos.Confidence != mapper.Yellow || !pos.Valid || pos.Coord.X != 0 {
		t.Fatalf("precondition: want 🟡 held at the OLD coord, got %+v", pos)
	}
	if pos.EventDescribesCell {
		t.Fatal("precondition: an ambiguous hold must not claim the event describes the cell")
	}

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != false {
		t.Fatalf("recording an ambiguously-held position must be refused: %#v", out)
	}
	if room := storeRoomAt(t, s, setID, "Z", 0, 0, 0); room.Hint != "Центр" {
		t.Errorf("the hub cell was overwritten with a twin's text: hint=%q", room.Hint)
	}
}

// TestRecordRefusesWithPendingMoves: the post-write re-anchor flushes the pending
// queue, so a record mid-speedwalk would silently drop the in-flight moves.
func TestRecordRefusesWithPendingMoves(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	setLiveRoom(t, sess, "Первая", "one", "S")
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.PushMove("S")
		tr.PushMove("S")
	})

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != false {
		t.Fatalf("recording mid-speedwalk must be refused: %#v", out)
	}
	var pend int
	sess.WithMapTracker(func(tr *mapper.Tracker) { pend = tr.PendingCount() })
	if pend != 2 {
		t.Errorf("the refused record still flushed the in-flight queue: pending=%d, want 2", pend)
	}
}

// TestUndoOfRecordCreateKeepsPosition: undo of a record-CREATE deletes the cell
// the record anchored to, so the position cannot be preserved by the index
// rebuild. The endpoint re-anchors to the same coord, restoring the pre-record
// 🟡 "anchored to unmapped cell" state instead of losing the position.
func TestUndoOfRecordCreateKeepsPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	target := walkIntoUnmappedCell(t, sess)

	if out := postRecordRoom(t, ts, s.apiToken, sid, "{}"); out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}
	undo := postMapUndo(t, ts, s.apiToken, sid)
	if undo["applied"] != true {
		t.Fatalf("undo not applied: %#v", undo)
	}
	if undo["reanchored"] != true {
		t.Errorf("undo of a create should report the re-anchor: %#v", undo)
	}

	var after mapper.Position
	sess.WithMapTracker(func(tr *mapper.Tracker) { after = tr.Position() })
	if !after.Valid {
		t.Fatal("undoing a record wiped the tracker position")
	}
	if after.Coord != target {
		t.Errorf("position moved to %v, want the pre-record coord %v", after.Coord, target)
	}
	if after.Confidence != mapper.Yellow {
		t.Errorf("confidence = %q, want yellow (unmapped cell again)", after.Confidence)
	}
}

// TestRecordReportsTouchedNeighbours documents the ACCEPTED cost of the
// no-link-op design and the mitigation for it. Writing the live exits makes the
// cell HasExplicitExits(), so ConnectsTo's first clause fires for every live exit
// — including one whose real destination is not the room the map happens to draw
// at that grid coord. We deliberately do NOT trim the exit set and do NOT touch
// ConnectsTo; instead the response must REPORT the touched neighbours (with
// back_link=false for the one-sided ones) so the operator can see the edge and
// undo it.
func TestRecordReportsTouchedNeighbours(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	// A(0,0,0) -S-> B(1,0,0). X(2,1,0) is unrelated: its only exit is E, so it
	// explicitly DENIES a back-link northward, and nothing connects to it.
	setID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "Touches", ZoneCount: 1, RoomCount: 3,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "A", Desc: "a",
				Exits: "S", EDirs: `["S"]`, Doors: `[]`, Ch: 2},
			{Zone: "Z", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "B", Desc: "b",
				Exits: "N", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
			{Zone: "Z", X: 2, Y: 1, L: 0, Tag: intPtr(3), Hint: "X-далеко", Desc: "unrelated",
				Exits: "E", EDirs: `["E"]`, Doors: `[]`, Ch: 4},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sid, setID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()

	// Stand on B, walk E into the unmapped (1,1,0); the live room has a S exit
	// whose real destination is NOT the X the map draws at (2,1,0).
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 1, Y: 0, L: 0})
	sess.WithMapTracker(func(tr *mapper.Tracker) {
		tr.PushMove("E")
		tr.Reconcile(mapper.RoomEvent{Hint: "Новая", Desc: "new", Exits: "W S"})
	})
	setLiveRoom(t, sess, "Новая", "new", "W S")

	out := postRecordRoom(t, ts, s.apiToken, sid, "{}")
	if out["applied"] != true {
		t.Fatalf("record not applied: %#v", out)
	}

	touches, _ := out["touches"].([]any)
	if len(touches) == 0 {
		t.Fatal("the response must report which neighbouring cells the written exits touch")
	}
	var sawX bool
	for _, raw := range touches {
		m, _ := raw.(map[string]any)
		if m["hint"] == "X-далеко" {
			sawX = true
			if m["dir"] != "S" {
				t.Errorf("touch dir = %v, want S", m["dir"])
			}
			if m["back_link"] != false {
				t.Errorf("X denies the back-link, so back_link must be false: %#v", m)
			}
		}
	}
	if !sawX {
		t.Errorf("the one-sided edge into the unrelated cell X was not reported: %#v", touches)
	}
	// The fabricated edge really IS live — that is the accepted cost, and the
	// touches report above is the mitigation. If this ever becomes unreachable,
	// the surplus-exit cost was addressed elsewhere: update this test AND the
	// mapRecordRoom doc comment rather than deleting the assertion.
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	after := postJSON(t, ts, s.apiToken, sid, "map-path", `{"to":{"zone":"Z","x":2,"y":1,"l":0}}`)
	if after["reachable"] != true {
		t.Errorf("expected the documented surplus-exit edge to exist: %#v", after)
	}
}

// TestRecordCoordEchoChecksPartialBodies: the coord echo must be validated
// whenever ANY coord field is present. Gating only on a non-empty zone let a body
// like {"x":5,"y":9} through unchecked, silently recording the current cell while
// the caller believed it had named a different one.
func TestRecordCoordEchoChecksPartialBodies(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	sess.AnchorMapPosition(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	setLiveRoom(t, sess, "Первая", "one", "S")

	// No zone, but coords that are NOT where we stand — must be refused.
	out := postRecordRoom(t, ts, s.apiToken, sid, `{"x":5,"y":9}`)
	if out["applied"] != false {
		t.Fatalf("a zone-less mismatching coord must still be checked: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["reason"]), "standing in") {
		t.Errorf("reason = %v", out["reason"])
	}

	// A partial body that AGREES with the current cell is fine.
	ok := postRecordRoom(t, ts, s.apiToken, sid, `{"x":0,"y":0}`)
	if ok["applied"] != true {
		t.Errorf("a partial coord matching the current cell should apply: %#v", ok)
	}
}

// TestMapLiveRoomEndpoint: a browser that connects (or reloads the page) after
// the last room block has no live room of its own, while the backend still holds
// one. GET map-live-room is the catch-up read that keeps the UI's record gate in
// agreement with what the backend would actually write.
func TestMapLiveRoomEndpoint(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	get := func() map[string]any {
		t.Helper()
		req, _ := newAuthenticatedRequest(http.MethodGet,
			fmt.Sprintf("%s/api/sessions/%d/map-live-room", ts.URL, sid), nil, s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET map-live-room: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	// Nothing seen yet: seen=false, and the UI therefore offers no record.
	if out := get(); out["seen"] != false {
		t.Fatalf("want seen=false before any room block, got %#v", out)
	}

	setLiveRoom(t, sess, "Первая", "one", "S U")
	out := get()
	if out["seen"] != true {
		t.Fatalf("want seen=true once a room block was parsed: %#v", out)
	}
	if out["hint"] != "Первая" || out["desc"] != "one" || out["exits"] != "S U" {
		t.Errorf("live room not reported verbatim: %#v", out)
	}
}
