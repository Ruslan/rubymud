package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// These cover the phase-5 slice-3 topology CRUD MCP tools (mud_room_upsert /
// mud_room_link / mud_room_unlink / mud_room_delete) end-to-end through the
// unified WriteTopology path: fork-if-frozen (forked_to), generalized before-state
// undo (mud_map_undo reverses each), and position-preserving refresh.

// activeSetID reads the session's active set id via the store.
func activeSetID(t *testing.T, s *Server, sid int64) int64 {
	t.Helper()
	id, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil || !ok {
		t.Fatalf("no active set: ok=%v err=%v", ok, err)
	}
	return id
}

// TestMCPRoomUpsertForksThenPartialUpdateThenUndo: create a new room (forks the
// frozen set), partial-update it, then undo both via mud_map_undo.
func TestMCPRoomUpsertForksThenPartialUpdateThenUndo(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	frozenID := seedTrackerSet(t, s, sid) // frozen imported set (zone Z)
	sess.LoadActiveMapSet()

	// Create a brand-new room at (9,9,0) — this forks the frozen set.
	res := callTool(t, s, "mud_room_upsert",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":9,"y":9,"l":0,"hint":"Secret","desc":"hidden","exits":"N S","is_dt":true}`, sid))
	if res["isError"] == true {
		t.Fatalf("upsert errored: %q", toolText(t, res))
	}
	txt := toolText(t, res)
	if !strings.Contains(txt, "forked_to:") {
		t.Errorf("first write on a frozen set must fork: %q", txt)
	}
	forkID := activeSetID(t, s, sid)
	if forkID == frozenID {
		t.Fatal("session still on frozen set — no fork happened")
	}
	created := slimRoomAt(t, s, forkID, "Z", 9, 9, 0)
	if created == nil || created.H != "Secret" || !created.S {
		t.Errorf("created room wrong: %#v", created)
	}
	if !slimHasExit(created, "N") || !slimHasExit(created, "S") {
		t.Errorf("created exits wrong: %v", created.E)
	}

	// Partial update: change only the hint (already editable now — no second fork).
	res = callTool(t, s, "mud_room_upsert",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":9,"y":9,"l":0,"hint":"Renamed"}`, sid))
	if res["isError"] == true {
		t.Fatalf("update errored: %q", toolText(t, res))
	}
	if strings.Contains(toolText(t, res), "forked_to:") {
		t.Error("second write must NOT fork again")
	}
	upd := slimRoomAt(t, s, forkID, "Z", 9, 9, 0)
	if upd.H != "Renamed" || !upd.S || !slimHasExit(upd, "N") {
		t.Errorf("partial update clobbered preserved fields: %#v", upd)
	}

	// Undo the update — hint back to "Secret".
	callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if r := slimRoomAt(t, s, forkID, "Z", 9, 9, 0); r == nil || r.H != "Secret" {
		t.Errorf("undo of update did not restore hint: %#v", r)
	}

	// Undo the create — room gone.
	callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if slimRoomAt(t, s, forkID, "Z", 9, 9, 0) != nil {
		t.Error("undo of create did not delete the room")
	}
}

// TestMCPRoomLinkUnlinkBidirectionalUndo: link adds the edge on both cells,
// unlink removes both, and undo of each restores BOTH cells.
func TestMCPRoomLinkUnlinkBidirectionalUndo(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid) // rooms (0,0,0)[S], (1,0,0)[N,S], (2,0,0)[N]
	sess.LoadActiveMapSet()

	// Link U from (1,0,0): its U-neighbor (1,0,1) doesn't exist → one-sided.
	res := callTool(t, s, "mud_room_link",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0,"dir":"U"}`, sid))
	if res["isError"] == true {
		t.Fatalf("link errored: %q", toolText(t, res))
	}
	forkID := activeSetID(t, s, sid)
	if !slimHasExit(slimRoomAt(t, s, forkID, "Z", 1, 0, 0), "U") {
		t.Error("link did not add U")
	}

	// Unlink N from (1,0,0): N steps to x=0 (room (0,0,0) exists) — bidirectional.
	// (1,0,0) has N; (0,0,0) has S (the reverse of N). Unlink removes N here + S there.
	res = callTool(t, s, "mud_room_unlink",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0,"dir":"N"}`, sid))
	if res["isError"] == true {
		t.Fatalf("unlink errored: %q", toolText(t, res))
	}
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 1, 0, 0), "N") {
		t.Error("unlink did not remove N on source")
	}
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "S") {
		t.Error("unlink did not remove reverse S on neighbor")
	}

	// Undo the unlink — both edges restored.
	callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if !slimHasExit(slimRoomAt(t, s, forkID, "Z", 1, 0, 0), "N") ||
		!slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "S") {
		t.Error("undo of unlink did not restore both cells")
	}

	// Bad direction soft-fails.
	res = callTool(t, s, "mud_room_link",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":1,"y":0,"l":0,"dir":"X"}`, sid))
	if res["isError"] != true {
		t.Errorf("bad direction should soft-fail, got %q", toolText(t, res))
	}
}

// TestMCPRoomDeleteUndoRecreates: delete a room, undo recreates it with fields
// intact; deleting a nonexistent room soft-fails.
func TestMCPRoomDeleteUndoRecreates(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_room_delete",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":2,"y":0,"l":0}`, sid))
	if res["isError"] == true {
		t.Fatalf("delete errored: %q", toolText(t, res))
	}
	forkID := activeSetID(t, s, sid)
	if slimRoomAt(t, s, forkID, "Z", 2, 0, 0) != nil {
		t.Fatal("room not deleted")
	}

	// Delete of a now-absent room soft-fails.
	res = callTool(t, s, "mud_room_delete",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":2,"y":0,"l":0}`, sid))
	if res["isError"] != true {
		t.Errorf("delete of a nonexistent room should soft-fail, got %q", toolText(t, res))
	}

	// Undo recreates it with exits intact ((2,0,0) had [N]).
	callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	r := slimRoomAt(t, s, forkID, "Z", 2, 0, 0)
	if r == nil {
		t.Fatal("undo of delete did not recreate the room")
	}
	if r.H != "Третья" || !slimHasExit(r, "N") {
		t.Errorf("recreated room fields wrong: %#v", r)
	}
}

// TestMCPRoomDeleteCurrentRoomGoesRed (review case iii): deleting the tracker's
// CURRENT room drops the tracker to red (the cell no longer exists in the rebuilt
// index) without crashing.
func TestMCPRoomDeleteCurrentRoomGoesRed(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// Anchor at (0,0,0) — green.
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	if !strings.Contains(toolText(t, res), "Confidence: green") {
		t.Fatalf("precondition: should be green after anchor: %q", toolText(t, res))
	}

	// Delete the current room.
	res = callTool(t, s, "mud_room_delete", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	if res["isError"] == true {
		t.Fatalf("delete of current room errored: %q", toolText(t, res))
	}

	// Tracker must now be red (current cell gone), no panic.
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	txt := toolText(t, res)
	if !strings.Contains(txt, "Confidence: red") {
		t.Errorf("deleting the current room should drop tracker to red, got %q", txt)
	}
}

// TestMCPRoomCRUDNoActiveSetSoftFails: with no active set, every CRUD tool
// soft-fails (isError) rather than erroring hard.
func TestMCPRoomCRUDNoActiveSetSoftFails(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	sess.LoadActiveMapSet() // no active set

	for _, tc := range []struct{ name, args string }{
		{"mud_room_upsert", `{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"hint":"x"}`},
		{"mud_room_link", `{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"dir":"N"}`},
		{"mud_room_unlink", `{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0,"dir":"N"}`},
		{"mud_room_delete", `{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`},
	} {
		res := callTool(t, s, tc.name, fmt.Sprintf(tc.args, sid))
		if res["isError"] != true {
			t.Errorf("%s with no active set should soft-fail, got %q", tc.name, toolText(t, res))
		}
	}
}
