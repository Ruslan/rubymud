package web

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

func TestMCPInhabitantsListReadWriteAndLookSummary(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	setID := seedTrackerSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	sid := sess.SessionID()

	created := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"create","room":{"zone":"Z","x":0,"y":0,"l":0},
		"name":"Keeper","kind":"npc","look_target":"keeper","look_text":"A silver-eyed guardian.",
		"note":"Knows the passphrase.","strategy":"Ask before fighting.",
		"presence_text":"The keeper watches the gate.","resident":true}`, sid))
	if created["isError"] == true || !strings.Contains(toolText(t, created), "created inhabitant id=") {
		t.Fatalf("create = %#v", created)
	}
	var entity storage.MapInhabitant
	if err := s.store.DB().Where("map_set_id=?", setID).First(&entity).Error; err != nil {
		t.Fatal(err)
	}

	placed := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"place","inhabitant_id":%d,
		"room":{"zone":"Z","x":1,"y":0,"l":0},"presence_text":"The keeper is visiting.","resident":false}`, sid, entity.ID))
	if placed["isError"] == true || !strings.Contains(toolText(t, placed), "placed inhabitant") {
		t.Fatalf("place = %#v", placed)
	}

	list := toolText(t, callTool(t, s, "mud_inhabitants", fmt.Sprintf(`{
		"session_id":%d,"room":{"zone":"Z","x":0,"y":0,"l":0},"limit":20}`, sid)))
	for _, want := range []string{"Keeper", "resident=true", `strategy="Ask before fighting."`} {
		if !strings.Contains(list, want) {
			t.Errorf("list omits %q:\n%s", want, list)
		}
	}
	if strings.Contains(list, "silver-eyed") {
		t.Fatalf("bounded list leaked detailed look text:\n%s", list)
	}

	detail := toolText(t, callTool(t, s, "mud_inhabitant", fmt.Sprintf(`{
		"session_id":%d,"inhabitant_id":%d}`, sid, entity.ID)))
	for _, want := range []string{"A silver-eyed guardian.", "placements: 2", `zone="Z" x=1 y=0 l=0`} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail omits %q:\n%s", want, detail)
		}
	}

	anchor := callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"Z","x":0,"y":0,"l":0}`, sid))
	if anchor["isError"] == true {
		t.Fatalf("anchor = %#v", anchor)
	}
	look := toolText(t, callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sid)))
	for _, want := range []string{"Known inhabitants:", "Keeper [npc, resident]", "strategy=Ask before fighting."} {
		if !strings.Contains(look, want) {
			t.Errorf("mud_look_map omits %q:\n%s", want, look)
		}
	}
	if strings.Contains(look, "silver-eyed") {
		t.Fatalf("mud_look_map leaked detailed look text:\n%s", look)
	}

	var notices []session.ServerMsg
	sess.AttachClient("inhabitant update observer", func(msg session.ServerMsg) error {
		notices = append(notices, msg)
		return nil
	})
	updated := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"update","inhabitant_id":%d,
		"room":{"zone":"Z","x":0,"y":0,"l":0},"strategy":"","resident":false}`, sid, entity.ID))
	if updated["isError"] == true {
		t.Fatalf("partial update = %#v", updated)
	}
	view, found, err := s.store.GetRoomInhabitant(setID, storage.EdgeCell{Zone: "Z", X: 0, Y: 0, L: 0}, entity.ID)
	if err != nil || !found {
		t.Fatalf("updated view: found=%v err=%v", found, err)
	}
	if view.Name != "Keeper" || view.Kind != storage.InhabitantKindNPC || view.LookText != "A silver-eyed guardian." || view.PresenceText != "The keeper watches the gate." {
		t.Fatalf("omitted update fields were not preserved: %+v", view)
	}
	if view.Strategy != "" || view.Resident {
		t.Fatalf("explicit empty/false update fields were not applied: %+v", view)
	}
	if len(notices) != 1 || notices[0].Type != "map_knowledge_changed" || notices[0].MapSetID != setID {
		t.Fatalf("shared update notices = %+v, want one map-set invalidation", notices)
	}

	notices = nil
	placementOnly := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"update","inhabitant_id":%d,
		"room":{"zone":"Z","x":0,"y":0,"l":0},"presence_text":"Standing watch.","resident":true}`, sid, entity.ID))
	if placementOnly["isError"] == true {
		t.Fatalf("placement-only update = %#v", placementOnly)
	}
	if len(notices) != 1 || notices[0].Type != "room_knowledge_changed" || notices[0].MapSetID != setID ||
		notices[0].Zone != "Z" || notices[0].RoomX != 0 || notices[0].RoomY != 0 || notices[0].RoomL != 0 {
		t.Fatalf("placement-only notices = %+v, want one exact-room invalidation", notices)
	}

	removed := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"remove","inhabitant_id":%d,
		"room":{"zone":"Z","x":1,"y":0,"l":0}}`, sid, entity.ID))
	if removed["isError"] == true || !strings.Contains(toolText(t, removed), "removed inhabitant") {
		t.Fatalf("remove = %#v", removed)
	}
	placements, err := s.store.ListInhabitantPlacements(setID, entity.ID)
	if err != nil || len(placements) != 1 {
		t.Fatalf("placements after remove = %+v err=%v", placements, err)
	}
}

func TestMCPInhabitantsExactRoomUsesBoundedSingleRead(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	setID := seedTrackerSet(t, s, sess.SessionID())
	cell := storage.EdgeCell{Zone: "Z", X: 0, Y: 0, L: 0}
	for _, name := range []string{"Keeper", "Rat"} {
		if _, err := s.store.CreateRoomInhabitant(setID, cell, storage.RoomInhabitantFields{
			InhabitantFields: storage.InhabitantFields{Name: name, Kind: storage.InhabitantKindNPC},
		}); err != nil {
			t.Fatal(err)
		}
	}

	var queryCount atomic.Int64
	const callbackName = "test:count_mcp_exact_inhabitant_queries"
	if err := s.store.DB().Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.store.DB().Callback().Query().Remove(callbackName) })

	result := callTool(t, s, "mud_inhabitants", fmt.Sprintf(`{
		"session_id":%d,"room":{"zone":"Z","x":0,"y":0,"l":0},"query":"rat","limit":20}`, sess.SessionID()))
	if result["isError"] == true {
		t.Fatalf("exact-room list = %#v", result)
	}
	text := toolText(t, result)
	if !strings.Contains(text, "Rat") || strings.Contains(text, "Keeper") {
		t.Fatalf("exact-room query was not applied:\n%s", text)
	}
	if got := queryCount.Load(); got > 2 {
		t.Fatalf("exact-room list executed %d reads, want active-set lookup plus one bounded room query", got)
	}
}

func TestMCPInhabitantWriteSchemaHasOperationSpecificRequirements(t *testing.T) {
	tools := (&Server{}).mcpListTools().(map[string]any)["tools"].([]any)
	var schema map[string]any
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] == "mud_inhabitant_write" {
			schema = tool["inputSchema"].(map[string]any)
			break
		}
	}
	if schema == nil {
		t.Fatal("mud_inhabitant_write schema missing")
	}
	branches, ok := schema["oneOf"].([]any)
	if !ok || len(branches) != 4 {
		t.Fatalf("operation-specific oneOf = %#v", schema["oneOf"])
	}
	wantRequired := map[string][]string{
		"create": {"name", "kind"},
		"place":  {"inhabitant_id"},
		"update": {"inhabitant_id"},
		"remove": {"inhabitant_id"},
	}
	seen := map[string]bool{}
	for _, raw := range branches {
		branch := raw.(map[string]any)
		operation := branch["properties"].(map[string]any)["operation"].(map[string]any)["const"].(string)
		required := branch["required"].([]string)
		for _, want := range wantRequired[operation] {
			found := false
			for _, got := range required {
				found = found || got == want
			}
			if !found {
				t.Errorf("%s required=%v, missing %s", operation, required, want)
			}
		}
		seen[operation] = true
	}
	if len(seen) != 4 {
		t.Fatalf("schema operations = %v", seen)
	}
}

func TestMCPInhabitantsEnforcesListLimitAndWriteValidation(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedTrackerSet(t, s, sess.SessionID())
	sid := sess.SessionID()

	for _, args := range []string{
		fmt.Sprintf(`{"session_id":%d,"limit":101}`, sid),
		fmt.Sprintf(`{"session_id":%d,"room":{"zone":"Z","x":0,"y":0}}`, sid),
	} {
		res := callTool(t, s, "mud_inhabitants", args)
		if res["isError"] != true {
			t.Fatalf("invalid list succeeded: args=%s result=%#v", args, res)
		}
	}
	res := callTool(t, s, "mud_inhabitant_write", fmt.Sprintf(`{
		"session_id":%d,"operation":"place","inhabitant_id":999,
		"room":{"zone":"Z","x":0,"y":0,"l":0}}`, sid))
	if res["isError"] != true {
		t.Fatalf("missing entity placement succeeded: %#v", res)
	}
}
