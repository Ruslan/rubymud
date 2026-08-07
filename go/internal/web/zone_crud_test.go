package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"rubymud/go/internal/storage"
)

func TestRESTZoneMovePreviewApplyCOWAndStrictBody(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "zones", Rooms: []storage.Room{{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatal(err)
	}
	sess.LoadActiveMapSet()
	url := fmt.Sprintf("%s/api/sessions/%d/map-zones/preview", ts.URL, sess.SessionID())
	call := func(path, body string) map[string]any {
		req, err := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/%s", ts.URL, sess.SessionID(), path), bytes.NewBufferString(body), s.apiToken)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	preview := call("map-zones/preview", `{"source_zone":"A","target_zone":"B","rooms":[{"x":0,"y":0,"l":0}]}`)
	if preview["ok"] != true {
		t.Fatalf("preview=%#v", preview)
	}
	p := preview["preview"].(map[string]any)
	rev := p["revision"].(float64)
	applied := call("map-zones/apply", fmt.Sprintf(`{"source_zone":"A","target_zone":"B","rooms":[{"x":0,"y":0,"l":0}],"expected_revision":%.0f}`, rev))
	if applied["ok"] != true || applied["forked_to"] == nil {
		t.Fatalf("apply=%#v", applied)
	}
	if room := roomAtWeb(t, s, activeSetID(t, s, sess.SessionID()), "B", 0, 0, 0); room == nil {
		t.Fatal("room not moved")
	}
	badReq, err := newAuthenticatedRequest(http.MethodPost, url, bytes.NewBufferString(`{"source_zone":"A","target_zone":"B","rooms":[],"unknown":1}`), s.apiToken)
	if err != nil {
		t.Fatal(err)
	}
	badReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var bad map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&bad)
	if bad["ok"] != false {
		t.Fatalf("unknown field accepted: %#v", bad)
	}
	nullCoord := call("map-zones/preview", `{"source_zone":"B","target_zone":"C","rooms":[{"x":null,"y":0,"l":0}]}`)
	if nullCoord["ok"] != false {
		t.Fatalf("null coordinate accepted: %#v", nullCoord)
	}
	renamePreview := call("map-zones/rename/preview", `{"source_zone":"B","target_zone":"D"}`)
	if renamePreview["ok"] != true {
		t.Fatalf("rename preview=%#v", renamePreview)
	}
	renameRevision := renamePreview["preview"].(map[string]any)["revision"].(float64)
	renameApply := call("map-zones/rename/apply", fmt.Sprintf(`{"source_zone":"B","target_zone":"D","expected_revision":%.0f}`, renameRevision))
	if renameApply["ok"] != true {
		t.Fatalf("rename apply=%#v", renameApply)
	}
	collisionSet, err := s.store.CreateMapSet(storage.MapSetInput{Name: "refused COW", Rooms: []storage.Room{
		{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: "B", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), collisionSet); err != nil {
		t.Fatal(err)
	}
	sess.LoadActiveMapSet()
	setsBefore, err := s.store.ListMapSets()
	if err != nil {
		t.Fatal(err)
	}
	collisionPreview := call("map-zones/preview", `{"source_zone":"A","target_zone":"B","rooms":[{"x":0,"y":0,"l":0}]}`)
	collisionRevision := collisionPreview["preview"].(map[string]any)["revision"].(float64)
	refused := call("map-zones/apply", fmt.Sprintf(`{"source_zone":"A","target_zone":"B","rooms":[{"x":0,"y":0,"l":0}],"expected_revision":%.0f}`, collisionRevision))
	if refused["ok"] != false {
		t.Fatalf("collision unexpectedly applied: %#v", refused)
	}
	if got := activeSetID(t, s, sess.SessionID()); got != collisionSet {
		t.Fatalf("refused COW switched active set to %d, want source %d", got, collisionSet)
	}
	setsAfter, err := s.store.ListMapSets()
	if err != nil {
		t.Fatal(err)
	}
	if len(setsAfter) != len(setsBefore) {
		t.Fatalf("refused COW leaked a fork: sets before=%d after=%d", len(setsBefore), len(setsAfter))
	}
}

func roomAtWeb(t *testing.T, s *Server, setID int64, zone string, x, y, l int) *storage.Room {
	t.Helper()
	var r storage.Room
	if err := s.store.DB().Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", setID, zone, x, y, l).First(&r).Error; err != nil {
		return nil
	}
	return &r
}
