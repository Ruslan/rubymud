package web

import (
	"fmt"
	"strings"
	"testing"
)

func TestMCPPortalCRUDReturnsAuthoritativeOccurrence(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	original := portalTestSet(t, s, sess.SessionID())
	create := callTool(t, s, "mud_room_portal", fmt.Sprintf(`{"session_id":%d,"action":"create","zone":"A","x":0,"y":0,"l":0,"target_zone":"B","target_x":0,"target_y":0,"target_l":0,"command":"prepare;enter","weight":0.5,"phrase":"say friend"}`, sess.SessionID()))
	if create["isError"] == true {
		t.Fatalf("create: %s", toolText(t, create))
	}
	text := toolText(t, create)
	if !strings.Contains(text, "selector={order=0}") || !strings.Contains(text, `target={zone="B" x=0 y=0 l=0}`) || !strings.Contains(text, "forked_to:") {
		t.Fatalf("create result=%q", text)
	}
	if activeSetID(t, s, sess.SessionID()) == original {
		t.Fatal("MCP create did not COW")
	}
	update := callTool(t, s, "mud_room_portal", fmt.Sprintf(`{"action":"update","zone":"A","x":0,"y":0,"l":0,"order":0,"command":"enter revised","no_route":true}`))
	if update["isError"] == true || !strings.Contains(toolText(t, update), `command="enter revised"`) {
		t.Fatalf("update=%q", toolText(t, update))
	}
	remove := callTool(t, s, "mud_room_portal", fmt.Sprintf(`{"action":"delete","zone":"A","x":0,"y":0,"l":0,"order":0}`))
	if remove["isError"] == true || !strings.Contains(toolText(t, remove), "deleted=true") {
		t.Fatalf("delete=%q", toolText(t, remove))
	}
	undo := callTool(t, s, "mud_map_undo", `{}`)
	if undo["isError"] == true {
		t.Fatalf("undo=%q", toolText(t, undo))
	}
}

func TestMCPPortalRejectsMissingTargetAndSupportsZeroCoordinates(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	portalTestSet(t, s, sess.SessionID())
	bad := callTool(t, s, "mud_room_portal", `{"action":"create","zone":"A","x":0,"y":0,"l":0,"target_zone":"B","target_x":0,"target_y":0,"command":"go"}`)
	if bad["isError"] != true {
		t.Fatalf("partial target accepted: %q", toolText(t, bad))
	}
	good := callTool(t, s, "mud_room_portal", `{"action":"create","zone":"A","x":0,"y":0,"l":0,"target_zone":"B","target_x":0,"target_y":0,"target_l":0,"command":"go"}`)
	if good["isError"] == true || !strings.Contains(toolText(t, good), "selector={order=0}") {
		t.Fatalf("zero target rejected: %q", toolText(t, good))
	}
	ignored := callTool(t, s, "mud_room_portal", `{"action":"delete","zone":"A","x":0,"y":0,"l":0,"order":0,"command":"ignored"}`)
	if ignored["isError"] != true {
		t.Fatalf("delete accepted ignored update fields: %q", toolText(t, ignored))
	}
	edges, err := s.store.ListMapEdges(activeSetID(t, s, sess.SessionID()))
	if err != nil || len(edges) != 1 {
		t.Fatalf("refused delete mutated portal: edges=%+v err=%v", edges, err)
	}
}
