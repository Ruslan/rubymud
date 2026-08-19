package web

import (
	"fmt"
	"strings"
	"testing"

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
