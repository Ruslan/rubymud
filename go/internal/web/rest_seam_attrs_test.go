package web

import (
	"net/http/httptest"
	"testing"

	"rubymud/go/internal/storage"
)

func TestRESTSetSeamAttrsForksAndSupportsEmptyCommand(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	originalID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "empty seam", RoomCount: 2, ZoneCount: 2,
		Rooms: []storage.Room{
			{Zone: "A", X: 0, Y: 0, L: 0, Tag: intPtr(1), EDirs: "[]", Doors: "[]"},
			{Zone: "B", X: 1, Y: 0, L: 0, Tag: intPtr(7), EDirs: "[]", Doors: "[]"},
		},
		LegacySeams: []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, "B||7", "B| enter |7")},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), originalID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()

	out := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-edge-attrs",
		`{"zone":"A","x":0,"y":0,"l":0,"seam":{"target_zone":"B","target_tag":7,"command":""},"phrase":"enter portal"}`)
	if out["ok"] != true {
		t.Fatalf("REST seam attrs = %#v", out)
	}
	forkID := int64(out["forked_to"].(float64))
	if forkID == originalID {
		t.Fatal("seam write did not fork frozen set")
	}
	edges, err := s.store.ListMapEdges(forkID)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == storage.EdgeKindSeam && e.Command == "" && e.Source == storage.EdgeSourceManual && e.Phrase == "enter portal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fork %d missing attributed empty-command seam: %+v", forkID, edges)
	}
	spaced := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-edge-attrs",
		`{"zone":"A","x":0,"y":0,"l":0,"seam":{"target_zone":"B","target_tag":7,"command":" enter "},"weight":2}`)
	if spaced["ok"] != true {
		t.Fatalf("REST changed exact whitespace in seam selector: %#v", spaced)
	}
	edges, err = s.store.ListMapEdges(forkID)
	if err != nil {
		t.Fatalf("ListMapEdges after spaced selector: %v", err)
	}
	found = false
	for _, e := range edges {
		if e.Kind == storage.EdgeKindSeam && e.Command == " enter " && e.Source == storage.EdgeSourceManual && e.Weight == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fork %d missing exact spaced-command seam attrs: %+v", forkID, edges)
	}
	original, err := s.store.ListMapEdges(originalID)
	if err != nil || len(original) == 0 {
		t.Fatalf("frozen original changed: edges=%+v err=%v", original, err)
	}
	for _, e := range original {
		if e.Kind == storage.EdgeKindSeam && e.Source != storage.EdgeSourceParsed {
			t.Fatalf("frozen original changed: %+v", original)
		}
	}

	bad := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-edge-attrs",
		`{"zone":"A","x":0,"y":0,"l":0,"seam":{"target_zone":"B","target_tag":7},"phrase":"x"}`)
	if bad["ok"] != false {
		t.Fatalf("missing command field accepted: %#v", bad)
	}
}
