package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// These cover the phase-5 slice-2 unified topology write-path (plan §8):
// fork-if-frozen (copy-on-write), the in-memory per-map-set undo journal, and
// position preservation on the index refresh a topology write triggers.

// TestWritePathPreservesPositionOnPatch: patching an exit into the CURRENT room
// must NOT lose tracker position — even though the patch forks the frozen set and
// rebuilds the index on the fork, the current coord exists in the fork so the
// tracker stays green with its coord + pending queue intact. Same footgun class as
// the annotation-DT reset.
func TestWritePathPreservesPositionOnPatch(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid) // frozen imported set
	sess.LoadActiveMapSet()

	// Anchor at the current room (0,0,0) and enqueue a pending move.
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	sess.WithMapTracker(func(tr *mapper.Tracker) { tr.PushMove("S") })

	res := callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	if txt := toolText(t, res); !strings.Contains(txt, "Confidence: green") || !strings.Contains(txt, "pending_moves: 1") {
		t.Fatalf("precondition failed: %q", txt)
	}

	// Patch +U into the CURRENT room — this forks the set and refreshes the index.
	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)
	if out["ok"] != true {
		t.Fatalf("patch not ok: %#v", out)
	}

	// Position MUST be preserved: still green, still at (0,0,0), pending intact.
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	txt := toolText(t, res)
	if !strings.Contains(txt, "Confidence: green") {
		t.Errorf("patch reset confidence (want green): %q", txt)
	}
	if !strings.Contains(txt, "x=0 y=0 l=0") {
		t.Errorf("position not preserved after patch: %q", txt)
	}
	if !strings.Contains(txt, "pending_moves: 1") {
		t.Errorf("pending queue was flushed by patch: %q", txt)
	}
}

// TestMCPMapUndoReversesLastWrite: mud_map_undo removes the exit that a prior
// map-cell-patch added (exit gone again on the editable set), and preserves
// position.
func TestMCPMapUndoReversesLastWrite(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)
	forkID := int64(out["forked_to"].(float64))
	if !slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "U") {
		t.Fatal("precondition: U should be present after patch")
	}

	// Undo reverses it.
	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo errored: %q", toolText(t, res))
	}
	if txt := toolText(t, res); !strings.Contains(txt, "Undid") || !strings.Contains(txt, "restored cell {Z 0,0,0}") {
		t.Errorf("undo report unexpected: %q", txt)
	}
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "U") {
		t.Error("U should be gone again after undo")
	}

	// Position preserved through undo.
	res = callTool(t, s, "mud_where", fmt.Sprintf(`{"session_id":%d}`, sid))
	if txt := toolText(t, res); !strings.Contains(txt, "Confidence: green") || !strings.Contains(txt, "x=0 y=0 l=0") {
		t.Errorf("undo did not preserve position: %q", txt)
	}
}

// TestMCPMapUndoNothingToUndo: an empty journal reports "nothing to undo".
func TestMCPMapUndoNothingToUndo(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo on empty journal should not error: %q", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "Nothing to undo") {
		t.Errorf("expected nothing-to-undo, got %q", toolText(t, res))
	}
}

// TestMCPMapUndoPerSetJournal: the undo journal is keyed by map_set_id, so a write
// on set A is NOT undoable while set B is active — undo on B finds an empty stack.
func TestMCPMapUndoPerSetJournal(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	// Set A: seed + patch (forks to an editable A').
	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)

	// Switch the session to a DIFFERENT set B and try to undo — the journal is
	// per-set, so B has nothing to undo (A's write is not visible from B).
	setB := seedTrackerSet(t, s, sid) // a second, separate frozen set
	callTool(t, s, "mud_set_active_map_set", fmt.Sprintf(`{"session_id":%d,"map_set":%d}`, sid, setB))

	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if !strings.Contains(toolText(t, res), "Nothing to undo") {
		t.Errorf("undo on set B should find an empty per-set journal, got %q", toolText(t, res))
	}
}

// TestMCPMapUndoBoundedCap: the per-set undo stack is bounded — beyond the cap the
// oldest entry drops, so exactly `cap` undos succeed and then it is empty.
func TestMCPMapUndoBoundedCap(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()
	callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))

	// First patch forks; toggle U on the same cell many times past the cap.
	total := 130 // > undoJournalCap (100)
	for i := 0; i < total; i++ {
		if i%2 == 0 {
			postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["U"]}`)
		} else {
			postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"remove_exits":["U"]}`)
		}
	}

	// The active set is now the fork; its journal is capped at 100. Undo until
	// empty and count how many succeeded.
	setID := *getActiveMapSetID(t, ts, s.apiToken, sid)
	if got := s.store.WriteJournal().Depth(setID); got != 100 {
		t.Fatalf("journal depth = %d, want capped at 100", got)
	}
	undone := 0
	for {
		res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
		if strings.Contains(toolText(t, res), "Nothing to undo") {
			break
		}
		undone++
		if undone > 200 {
			t.Fatal("undo did not drain (possible re-journaling loop)")
		}
	}
	if undone != 100 {
		t.Errorf("undos before empty = %d, want 100 (bounded cap)", undone)
	}
}

// TestWritePathUndoOfAddExistingKeepsIt is the CRITICAL-fix regression at the
// full write-path level: patching in an exit the room ALREADY has, then undoing,
// must NOT delete the pre-existing exit (the old symmetric-inverse journaled a
// bogus "remove" that deleted it). The seeded room (0,0,0) already has S.
func TestWritePathUndoOfAddExistingKeepsIt(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	// Add S (already present) — a no-op patch that still forks the frozen set.
	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"add_exits":["S"]}`)
	if out["ok"] != true {
		t.Fatalf("patch not ok: %#v", out)
	}
	forkID := int64(out["forked_to"].(float64))
	if !slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "S") {
		t.Fatal("precondition: S should be present after add-existing")
	}

	// Undo must LEAVE S intact (must not delete the pre-existing exit).
	res := callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if res["isError"] == true {
		t.Fatalf("undo errored: %q", toolText(t, res))
	}
	if !slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "S") {
		t.Error("undo of add-existing DELETED the pre-existing S exit (data corruption)")
	}
}

// TestWritePathUndoOfRemoveAbsentDoesNotFabricate: removing an ABSENT exit then
// undoing must not fabricate a phantom exit. The seeded room (0,0,0) has no U.
func TestWritePathUndoOfRemoveAbsentDoesNotFabricate(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	seedTrackerSet(t, s, sid)
	sess.LoadActiveMapSet()

	out := postMapCellPatch(t, ts, s.apiToken, sid, `{"zone":"Z","x":0,"y":0,"l":0,"remove_exits":["U"]}`)
	forkID := int64(out["forked_to"].(float64))
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "U") {
		t.Fatal("precondition: U should be absent")
	}

	callTool(t, s, "mud_map_undo", fmt.Sprintf(`{"session_id":%d}`, sid))
	if slimHasExit(slimRoomAt(t, s, forkID, "Z", 0, 0, 0), "U") {
		t.Error("undo of remove-absent FABRICATED a phantom U exit")
	}
}

// TestWritePathConcurrentNoDoubleFork is the HIGH-fix regression: two concurrent
// writers to the same FROZEN set must fork it exactly ONCE (the per-map-set lock
// serializes the fork decision), not diverge into two editable copies. Run with
// -race.
func TestWritePathConcurrentNoDoubleFork(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()

	setID := seedTrackerSet(t, s, sid) // frozen imported set
	sess.LoadActiveMapSet()

	var setsBefore int64
	s.store.DB().Model(&storage.MapSet{}).Count(&setsBefore)

	// Two goroutines issue their first topology write to the same frozen set.
	var wg sync.WaitGroup
	wg.Add(2)
	op := func(dir string) storage.TopologyOp {
		return storage.TopologyOp{Kind: storage.TopoPatchExits, Zone: "Z", X: 0, Y: 0, L: 0, AddExits: []string{dir}}
	}
	for _, dir := range []string{"U", "D"} {
		go func(d string) {
			defer wg.Done()
			if _, err := sess.WriteTopology(op(d)); err != nil {
				t.Errorf("WriteTopology(%s): %v", d, err)
			}
		}(dir)
	}
	wg.Wait()

	// Exactly ONE new set (the fork) was created — not two.
	var setsAfter int64
	s.store.DB().Model(&storage.MapSet{}).Count(&setsAfter)
	if setsAfter != setsBefore+1 {
		t.Fatalf("map_sets count = %d, want %d (exactly one fork)", setsAfter, setsBefore+1)
	}

	// The session is on the single editable fork, distinct from the frozen original.
	activeID, _, _ := s.store.GetActiveMapSetID(sid)
	if activeID == setID {
		t.Fatal("active set is still the frozen original — no fork happened")
	}
	var fork storage.MapSet
	if err := s.store.DB().First(&fork, activeID).Error; err != nil {
		t.Fatalf("load fork: %v", err)
	}
	if !fork.Editable || fork.ForkedFromID == nil || *fork.ForkedFromID != setID {
		t.Errorf("fork metadata wrong: %+v", fork)
	}
}
