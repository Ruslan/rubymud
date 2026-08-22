package web

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func TestMCPSetSeamAttrsUsesSemanticSelector(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "seams", RoomCount: 2, ZoneCount: 2,
		Rooms: []storage.Room{
			{Zone: "A", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "gate", EDirs: "[]", Doors: "[]"},
			{Zone: "B", X: 4, Y: 2, L: 0, Tag: intPtr(7), Hint: "yard", EDirs: "[]", Doors: "[]"},
		},
		LegacySeams: []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, "B|enter|7")},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()

	res := callTool(t, s, "mud_room_set_seam_attrs", fmt.Sprintf(
		`{"session_id":%d,"zone":"A","x":0,"y":0,"l":0,"target_zone":"B","target_tag":7,"command":"enter","weight":0.5,"phrase":"open portal","door":true,"locked":true,"open_cmd":"say friend","open_delay_ms":2000,"no_route":true}`,
		sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("set seam attrs: %q", toolText(t, res))
	}
	if got := toolText(t, res); !strings.Contains(got, "forked_to:") {
		t.Fatalf("frozen seam write did not report its fork: %q", got)
	}
	edges, err := s.store.ListMapEdges(activeSetID(t, s, sess.SessionID()))
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var seam *storage.MapEdge
	for i := range edges {
		e := &edges[i]
		if e.Kind == storage.EdgeKindSeam && e.FromZone == "A" && e.Command == "enter" {
			seam = e
		}
	}
	if seam == nil || seam.Source != storage.EdgeSourceManual || seam.Weight != .5 || seam.Phrase != "open portal" || !seam.Door || !seam.Locked || seam.OpenCmd != "say friend" || seam.OpenDelayMS != 2000 || !seam.NoRoute {
		t.Fatalf("stored seam attrs = %+v", seam)
	}
	original, err := s.store.ListMapEdges(id)
	if err != nil || len(original) == 0 || original[len(original)-1].Source != storage.EdgeSourceParsed {
		t.Fatalf("frozen original changed: edges=%+v err=%v", original, err)
	}
}

func TestMCPSetSeamAttrsSelectsExactDuplicateOccurrence(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "duplicate seams", RoomCount: 2, ZoneCount: 2,
		Rooms: []storage.Room{
			{Zone: "A", EDirs: "[]", Doors: "[]"},
			{Zone: "B", X: 1, Tag: intPtr(7), EDirs: "[]", Doors: "[]"},
		},
		LegacySeams: []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, "B|enter|7", "B|enter|7")},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	res := callTool(t, s, "mud_room_set_seam_attrs", fmt.Sprintf(
		`{"session_id":%d,"zone":"A","x":0,"y":0,"l":0,"target_zone":"B","target_tag":7,"command":"enter","order":1,"phrase":"second only"}`,
		sess.SessionID()))
	if res["isError"] == true {
		t.Fatalf("set duplicate occurrence: %q", toolText(t, res))
	}
	edges, err := s.store.ListMapEdges(activeSetID(t, s, sess.SessionID()))
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind != storage.EdgeKindSeam {
			continue
		}
		if edge.SeamOrder == 0 && edge.Phrase != "" {
			t.Fatalf("first occurrence changed: %+v", edge)
		}
		if edge.SeamOrder == 1 && edge.Phrase != "second only" {
			t.Fatalf("second occurrence unchanged: %+v", edge)
		}
	}
}
