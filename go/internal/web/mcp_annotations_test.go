package web

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// seedAnnotationSet inserts a small connected 3-room chain along +Y (E is +Y in
// the grid convention): Z(0,0,0)->(0,1,0)->(0,2,0), makes it active, and builds
// the tracker index. Returns the set id. No native DTs — DT is introduced purely
// via annotation in the tests.
func seedAnnotationSet(t *testing.T, s *Server, sessionID int64) int64 {
	t.Helper()
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name:      "Anno",
		ZoneCount: 1,
		RoomCount: 3,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "Первая", Desc: "one",
				Exits: "E", EDirs: `["E"]`, Doors: `[]`, Ch: 4},
			{Zone: "Z", X: 0, Y: 1, L: 0, Tag: intPtr(2), Hint: "Вторая", Desc: "two",
				Exits: "E W", EDirs: `["E","W"]`, Doors: `[]`, Ch: 12},
			{Zone: "Z", X: 0, Y: 2, L: 0, Tag: intPtr(3), Hint: "Третья", Desc: "three",
				Exits: "W", EDirs: `["W"]`, Doors: `[]`, Ch: 8},
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

func TestMCPRoomAnnotateNoActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sess.LoadActiveMapSet() // no active set
	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":"x"}`, sess.SessionID()))
	if res["isError"] != true {
		t.Fatalf("expected soft-fail with no active set: %#v", res)
	}
	if !strings.Contains(toolText(t, res), "No active map set") {
		t.Errorf("unexpected message: %q", toolText(t, res))
	}
}

// TestMCPRoomAnnotateWriteAndSurface writes an annotation, confirms partial
// update preserves fields, and that mud_look_map surfaces it at the current room.
func TestMCPRoomAnnotateWriteAndSurface(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Anchor at the first room so mud_look_map has a current room.
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":"secret lever","hazard":"gas","author":"ru"}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("annotate errored: %q", toolText(t, res))
	}
	txt := toolText(t, res)
	if !strings.Contains(txt, "secret lever") || !strings.Contains(txt, "gas") {
		t.Errorf("annotate echo missing fields: %q", txt)
	}

	// Partial update: change only note; hazard must be preserved.
	res = callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":"lever moved"}`, sess.SessionID()))
	txt = toolText(t, res)
	if !strings.Contains(txt, "lever moved") || !strings.Contains(txt, "gas") {
		t.Errorf("partial update lost preserved hazard: %q", txt)
	}

	// mud_look_map surfaces the annotation at the current room.
	res = callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	txt = toolText(t, res)
	if !strings.Contains(txt, "Annotation:") || !strings.Contains(txt, "lever moved") || !strings.Contains(txt, "gas") {
		t.Errorf("mud_look_map did not surface annotation: %q", txt)
	}
}

// TestMCPAnnotateFrozenSetNoFork verifies annotations work on an imported set
// that another session (or none) is not editing — annotations never fork, so
// writing just upserts by coord regardless of set state.
func TestMCPAnnotateFrozenSetNoFork(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":2,"l":0,"note":"frozen ok"}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("annotate on frozen/imported set errored: %q", toolText(t, res))
	}
	// It landed in storage keyed by coord.
	got, ok, err := s.store.GetRoomAnnotation(id, "Z", 0, 2, 0)
	if err != nil || !ok || got.Note != "frozen ok" {
		t.Fatalf("annotation not persisted by coord: ok=%v err=%v %+v", ok, err, got)
	}
}

// TestMCPAnnotationDTReachesPath is the key cross-cut: an annotation with dt=true
// on a cell must make mud_path REFUSE routing INTO it (same as a native is_dt),
// and mud_look_map must show it as a DEATH TRAP.
func TestMCPAnnotationDTReachesPath(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Baseline: routing to the far cell succeeds (no DT anywhere yet).
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))
	res := callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":0,"y":2,"l":0}}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("baseline path should succeed pre-annotation: %q", toolText(t, res))
	}

	// Annotate the target cell as a death trap. This is an in-place index update
	// (no rebuild), so the tracker's anchored position at (0,0,0) survives — no
	// re-anchor needed here.
	res = callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":2,"l":0,"dt":true,"author":"ru"}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("dt annotate errored: %q", toolText(t, res))
	}

	// mud_path must now REFUSE routing into the annotated-DT cell (from the still-
	// anchored start position — not re-anchored).
	res = callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":0,"y":2,"l":0}}`, sess.SessionID()))
	if res["isError"] != true {
		t.Fatalf("expected mud_path to refuse annotation-DT target, got: %q", toolText(t, res))
	}
	if !strings.Contains(strings.ToUpper(toolText(t, res)), "DEATH TRAP") {
		t.Errorf("refusal should cite death trap: %q", toolText(t, res))
	}

	// mud_look_map at the annotated-DT cell shows it DT.
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":2,"l":0}`, sess.SessionID()))
	res = callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	lookTxt := toolText(t, res)
	if !strings.Contains(lookTxt, "DEATH TRAP") {
		t.Errorf("mud_look_map should show annotation-DT cell as DEATH TRAP: %q", lookTxt)
	}
	// And exactly once — no double DEATH TRAP (annotation dt line is suppressed in
	// look-map since the room-level line already covers it).
	if n := strings.Count(lookTxt, "DEATH TRAP"); n != 1 {
		t.Errorf("mud_look_map should show DEATH TRAP exactly once, got %d: %q", n, lookTxt)
	}

	// Clearing the DT annotation (dt=false) recomputes the cell's effective is_dt
	// to native (false) in place — routing succeeds again, still no position reset.
	res = callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":2,"l":0,"dt":false}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("clear dt errored: %q", toolText(t, res))
	}
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))
	res = callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to":{"zone":"Z","x":0,"y":2,"l":0}}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("path should succeed after clearing DT annotation: %q", toolText(t, res))
	}
}

// TestMCPAnnotateCurrentRoomDTPreservesPosition is the HIGH-priority fix: an agent
// annotating ITS CURRENT ROOM dt:true must NOT lose tracker position/confidence —
// the DT overlay is applied in place, not via a full index reload. mud_path must
// then refuse that cell, and a later dt:false must drop the DT in place.
func TestMCPAnnotateCurrentRoomDTPreservesPosition(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	// Anchor at the current room and enqueue a pending move so we can prove the
	// queue is not flushed.
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":1,"l":0}`, sess.SessionID()))
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.PushMove("E") })

	// Confidence is green and there is 1 pending move before annotating.
	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !strings.Contains(toolText(t, res), "green") {
		t.Fatalf("precondition: expected green: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "pending_moves: 1") {
		t.Fatalf("precondition: expected pending_moves: 1: %q", toolText(t, res))
	}

	// Annotate the CURRENT room as a death trap ("I just found a DT here").
	res = callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":1,"l":0,"dt":true}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("annotate current room dt errored: %q", toolText(t, res))
	}

	// Position and confidence MUST be preserved — still green, still at (0,1,0),
	// pending queue intact — NOT reset to red.
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	whereTxt := toolText(t, res)
	// Match the confidence line specifically ("green (anchored)" — note "anchored"
	// contains the substring "red", so a naive Contains(whereTxt,"red") false-fires).
	if !strings.Contains(whereTxt, "Confidence: green") {
		t.Errorf("annotating current room dt:true reset confidence (want green): %q", whereTxt)
	}
	if !strings.Contains(whereTxt, "x=0 y=1 l=0") {
		t.Errorf("position not preserved after dt annotate: %q", whereTxt)
	}
	if !strings.Contains(whereTxt, "pending_moves: 1") {
		t.Errorf("pending queue was flushed by dt annotate: %q", whereTxt)
	}
	// And the DT reached routing: refuse a route INTO the now-DT current cell.
	res = callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"from":{"zone":"Z","x":0,"y":0,"l":0},"to":{"zone":"Z","x":0,"y":1,"l":0}}`, sess.SessionID()))
	if res["isError"] != true || !strings.Contains(strings.ToUpper(toolText(t, res)), "DEATH TRAP") {
		t.Errorf("mud_path should refuse the annotation-DT current cell: %q", toolText(t, res))
	}

	// dt:false drops the DT in place; position still preserved.
	callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":1,"l":0,"dt":false}`, sess.SessionID()))
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !strings.Contains(toolText(t, res), "green") || !strings.Contains(toolText(t, res), "x=0 y=1 l=0") {
		t.Errorf("dt:false should preserve position/confidence: %q", toolText(t, res))
	}
	res = callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"from":{"zone":"Z","x":0,"y":0,"l":0},"to":{"zone":"Z","x":0,"y":1,"l":0}}`, sess.SessionID()))
	if res["isError"] == true {
		t.Errorf("mud_path should succeed after dt:false in place: %q", toolText(t, res))
	}
}

// TestMCPAnnotateDanglingCell writes an annotation on a cell that is NOT a room in
// the active set — it is kept (dangling annotations are intentional) and the
// response carries a soft note.
func TestMCPAnnotateDanglingCell(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":9,"y":9,"l":0,"note":"not mapped yet"}`, sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("dangling annotate should still succeed: %q", toolText(t, res))
	}
	txt := toolText(t, res)
	if !strings.Contains(txt, "dangling") {
		t.Errorf("expected a dangling soft-note: %q", txt)
	}
	// It is persisted.
	if _, ok, err := s.store.GetRoomAnnotation(id, "Z", 9, 9, 0); err != nil || !ok {
		t.Fatalf("dangling annotation not persisted: ok=%v err=%v", ok, err)
	}
}

// TestMCPAnnotateBattleLogSurface covers battle_log write + read + surface.
func TestMCPAnnotateBattleLogSurface(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID()))

	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"battle_log":"orc pack wiped me twice"}`, sess.SessionID()))
	if !strings.Contains(toolText(t, res), "orc pack wiped me twice") {
		t.Errorf("annotate echo missing battle_log: %q", toolText(t, res))
	}
	// Surfaced in look_map.
	res = callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !strings.Contains(toolText(t, res), "orc pack wiped me twice") {
		t.Errorf("look_map missing battle_log: %q", toolText(t, res))
	}
	// Read back via the reader.
	res = callTool(t, s, "mud_room_annotations", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !strings.Contains(toolText(t, res), "orc pack wiped me twice") {
		t.Errorf("reader missing battle_log: %q", toolText(t, res))
	}
}

// TestMCPAnnotateClearAuthorAndHazard covers clearing author/hazard back to "".
func TestMCPAnnotateClearAuthorAndHazard(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"hazard":"gas","author":"ru","note":"keep"}`, sess.SessionID()))
	// Clear hazard and author with explicit "".
	callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"hazard":"","author":""}`, sess.SessionID()))

	got, ok, err := s.store.GetRoomAnnotation(seedActiveSetID(t, s, sess.SessionID()), "Z", 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Hazard != "" || got.Author != "" {
		t.Errorf("hazard/author should be cleared: %+v", got)
	}
	if got.Note != "keep" {
		t.Errorf("note should be preserved through the clear: %+v", got)
	}
}

// TestMCPAnnotateCapsFreeText verifies over-long free-text fields are truncated
// (not persisted unbounded) and the response soft-notes the truncation.
func TestMCPAnnotateCapsFreeText(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	big := strings.Repeat("x", annotationFieldCap+500)
	res := callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":%q}`, sess.SessionID(), big))
	if res["isError"] == true {
		t.Fatalf("over-long annotate should truncate, not error: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "truncated") {
		t.Errorf("expected a truncation soft-note: %q", toolText(t, res))
	}
	got, ok, err := s.store.GetRoomAnnotation(seedActiveSetID(t, s, sess.SessionID()), "Z", 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if len([]rune(got.Note)) != annotationFieldCap {
		t.Errorf("note should be capped to %d runes, got %d", annotationFieldCap, len([]rune(got.Note)))
	}
}

// seedActiveSetID returns the session's active map set id (test helper).
func seedActiveSetID(t *testing.T, s *Server, sessionID int64) int64 {
	t.Helper()
	id, ok, err := s.store.GetActiveMapSetID(sessionID)
	if err != nil || !ok {
		t.Fatalf("GetActiveMapSetID: ok=%v err=%v", ok, err)
	}
	return id
}

// TestMCPRoomAnnotationsReader checks the zone reader lists what was written.
func TestMCPRoomAnnotationsReader(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedAnnotationSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()

	callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"note":"n1"}`, sess.SessionID()))
	callTool(t, s, "mud_room_annotate",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":1,"l":0,"hazard":"h2"}`, sess.SessionID()))

	res := callTool(t, s, "mud_room_annotations", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	txt := toolText(t, res)
	if !strings.Contains(txt, "n1") || !strings.Contains(txt, "h2") {
		t.Errorf("annotations reader missing entries: %q", txt)
	}
	if !strings.Contains(txt, "annotations (all zones): 2") {
		t.Errorf("reader count/header off: %q", txt)
	}
}
