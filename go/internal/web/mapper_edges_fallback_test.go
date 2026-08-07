package web

import (
	"fmt"
	"strings"
	"testing"
)

// TestEmptyEdgeTableDoesNotFallbackToLegacyDerivation proves runtime routing reads
// map_edges only; repair belongs to the explicit backfill/rematerialize path.
func TestEmptyEdgeTableDoesNotFallbackToLegacyDerivation(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	setID := seedTrackerSet(t, s, sess.SessionID())

	// Simulate the drift: rooms present, topology missing.
	if err := s.store.DB().Exec("DELETE FROM map_edges WHERE map_set_id = ?", setID).Error; err != nil {
		t.Fatalf("clear edges: %v", err)
	}
	sess.LoadActiveMapSet()

	// Anchor at the first room and route to the third: reachable only if the index
	// has edges at all.
	if res := callTool(t, s, "mud_anchor_here",
		fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sess.SessionID())); res["isError"] == true {
		t.Fatalf("anchor failed: %v", toolText(t, res))
	}
	res := callTool(t, s, "mud_path",
		fmt.Sprintf(`{"session_id":%d,"to_hint":"Третья"}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "no route") {
		t.Fatalf("runtime derived legacy room topology despite empty edges: %q", text)
	}
}
