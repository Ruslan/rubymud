package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

func doInhabitantRequest(t *testing.T, s *Server, method, rawURL string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := newAuthenticatedRequest(method, rawURL, reader, s.apiToken)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func doRawInhabitantRequest(t *testing.T, s *Server, method, rawURL, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := newAuthenticatedRequest(method, rawURL, strings.NewReader(body), s.apiToken)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func TestRESTRoomInhabitantsLazyCRUDOnFrozenMapSet(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	setID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "frozen", Rooms: []storage.Room{{Zone: "Alpha", X: 1, Y: 2, L: 0, Hint: "Forge"}},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	before, _ := s.store.GetMapSet(setID)
	var notices []session.ServerMsg
	sess.AttachClient("knowledge observer", func(msg session.ServerMsg) error {
		notices = append(notices, msg)
		return nil
	})

	endpoint := fmt.Sprintf("%s/api/map-sets/%d/inhabitants?zone=%s&x=1&y=2&l=0", ts.URL, setID, url.QueryEscape("Alpha"))
	resp, data := doInhabitantRequest(t, s, http.MethodGet, endpoint, nil)
	if resp.StatusCode != http.StatusOK || string(data) != "[]\n" {
		t.Fatalf("empty list: status=%d body=%s", resp.StatusCode, data)
	}

	create := func(name, kind string, resident bool) storage.RoomInhabitantView {
		resp, data := doInhabitantRequest(t, s, http.MethodPost,
			fmt.Sprintf("%s/api/map-sets/%d/inhabitants", ts.URL, setID), map[string]any{
				"zone": "Alpha", "x": 1, "y": 2, "l": 0,
				"name": name, "kind": kind, "look_target": strings.ToLower(name),
				"look_text": name + " inspected", "note": "note", "strategy": "strategy",
				"presence_text": name + " is here", "resident": resident,
			})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status=%d body=%s", name, resp.StatusCode, data)
		}
		var row storage.RoomInhabitantView
		if err := json.Unmarshal(data, &row); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return row
	}
	smith := create("Smith", storage.InhabitantKindNPC, true)
	rat := create("Rat", storage.InhabitantKindMonster, false)
	if smith.ID == 0 || rat.ID == 0 || smith.ID == rat.ID {
		t.Fatalf("created ids: smith=%d rat=%d", smith.ID, rat.ID)
	}
	resp, data = doInhabitantRequest(t, s, http.MethodPost,
		fmt.Sprintf("%s/api/map-sets/%d/inhabitants", ts.URL, setID), map[string]any{
			"inhabitant_id": smith.ID, "zone": "Beta", "x": 9, "y": 8, "l": 1,
			"presence_text": "Smith visits the market.", "resident": false,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("second placement: status=%d body=%s", resp.StatusCode, data)
	}

	catalogURL := fmt.Sprintf("%s/api/map-sets/%d/inhabitants/catalog?query=smith&limit=20&exclude_zone=%s&exclude_x=7&exclude_y=7&exclude_l=0",
		ts.URL, setID, url.QueryEscape("Alpha"))
	resp, data = doInhabitantRequest(t, s, http.MethodGet, catalogURL, nil)
	var catalog inhabitantCatalogResponse
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &catalog) != nil || catalog.Truncated || len(catalog.Items) != 1 || catalog.Items[0].ID != smith.ID || catalog.Items[0].PlacementCount != 2 {
		t.Fatalf("catalog: status=%d response=%+v body=%s", resp.StatusCode, catalog, data)
	}
	excludedURL := fmt.Sprintf("%s/api/map-sets/%d/inhabitants/catalog?limit=20&exclude_zone=%s&exclude_x=1&exclude_y=2&exclude_l=0",
		ts.URL, setID, url.QueryEscape("Alpha"))
	resp, data = doInhabitantRequest(t, s, http.MethodGet, excludedURL, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &catalog) != nil || len(catalog.Items) != 0 {
		t.Fatalf("room-excluded catalog: status=%d response=%+v body=%s", resp.StatusCode, catalog, data)
	}

	resp, data = doInhabitantRequest(t, s, http.MethodGet, endpoint, nil)
	var rows []storage.RoomInhabitantView
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &rows) != nil || len(rows) != 2 {
		t.Fatalf("list: status=%d rows=%+v body=%s", resp.StatusCode, rows, data)
	}

	resp, data = doInhabitantRequest(t, s, http.MethodPatch,
		fmt.Sprintf("%s/api/map-sets/%d/inhabitants/%d", ts.URL, setID, smith.ID), map[string]any{
			"zone": "Alpha", "x": 1, "y": 2, "l": 0,
			"name": "Master Smith", "kind": "other", "look_target": "smith",
			"look_text": "Sooty apron", "note": "quest giver", "strategy": "bring ore",
			"presence_text": "Hammering steel", "resident": false,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", resp.StatusCode, data)
	}
	var patched storage.RoomInhabitantView
	if err := json.Unmarshal(data, &patched); err != nil || patched.Name != "Master Smith" || patched.Resident {
		t.Fatalf("patched=%+v err=%v", patched, err)
	}

	resp, data = doInhabitantRequest(t, s, http.MethodDelete,
		fmt.Sprintf("%s/api/map-sets/%d/inhabitants/%d?zone=%s&x=1&y=2&l=0", ts.URL, setID, rat.ID, url.QueryEscape("Alpha")), nil)
	if resp.StatusCode != http.StatusNoContent || len(data) != 0 {
		t.Fatalf("delete: status=%d body=%s", resp.StatusCode, data)
	}
	_, data = doInhabitantRequest(t, s, http.MethodGet, endpoint, nil)
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) != 1 || rows[0].Name != "Master Smith" {
		t.Fatalf("after delete rows=%+v err=%v", rows, err)
	}

	// Knowledge writes use their own lightweight invalidation and never take the
	// topology COW/reload path.
	after, _ := s.store.GetMapSet(setID)
	sets, _ := s.store.ListMapSets()
	active, ok, _ := s.store.GetActiveMapSetID(sess.SessionID())
	if len(sets) != 1 || after.Editable || after.TopologyRevision != before.TopologyRevision || !ok || active != setID {
		t.Fatalf("knowledge changed topology: before=%+v after=%+v sets=%+v active=%d/%v", before, after, sets, active, ok)
	}
	if len(notices) != 6 { // two creates, attach, two cells for shared-entity patch, delete
		t.Fatalf("notices=%+v, want six room invalidations", notices)
	}
	betaNotice := false
	for _, notice := range notices {
		if notice.Type != "room_knowledge_changed" || notice.MapSetID != setID {
			t.Fatalf("unexpected notice: %+v", notice)
		}
		if notice.Zone == "Beta" && notice.RoomX == 9 && notice.RoomY == 8 && notice.RoomL == 1 {
			betaNotice = true
		} else if notice.Zone != "Alpha" || notice.RoomX != 1 || notice.RoomY != 2 || notice.RoomL != 0 {
			t.Fatalf("unexpected notice coordinate: %+v", notice)
		}
	}
	if !betaNotice {
		t.Fatal("shared entity patch did not invalidate its second room")
	}

	// The zone feed stays slim. Full knowledge is only on the room-scoped endpoint.
	roomsURL := fmt.Sprintf("%s/api/rooms?map_set=%d&zone=%s", ts.URL, setID, url.QueryEscape("Alpha"))
	_, data = doInhabitantRequest(t, s, http.MethodGet, roomsURL, nil)
	var slim []map[string]any
	if err := json.Unmarshal(data, &slim); err != nil || len(slim) != 1 {
		t.Fatalf("decode slim rooms: %v %s", err, data)
	}
	if _, included := slim[0]["inhabitants"]; included {
		t.Fatalf("slim room unexpectedly contains inhabitants: %s", data)
	}
}

func TestRESTRoomInhabitantValidation(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	setID, _ := s.store.CreateMapSet(storage.MapSetInput{Name: "map"})
	base := fmt.Sprintf("%s/api/map-sets/%d/inhabitants", ts.URL, setID)

	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "missing coord", body: map[string]any{"name": "x", "kind": "npc"}},
		{name: "invalid kind", body: map[string]any{"zone": "Z", "x": 0, "y": 0, "l": 0, "name": "x", "kind": "boss"}},
		{name: "blank name", body: map[string]any{"zone": "Z", "x": 0, "y": 0, "l": 0, "name": " ", "kind": "npc"}},
		{name: "oversize text", body: map[string]any{"zone": "Z", "x": 0, "y": 0, "l": 0, "name": "x", "kind": "other", "note": strings.Repeat("я", storage.InhabitantTextLimit+1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := doInhabitantRequest(t, s, http.MethodPost, base, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", resp.StatusCode, data)
			}
		})
	}

	trailing := `{"zone":"Z","x":0,"y":0,"l":0,"name":"x","kind":"npc"}{}`
	resp, data := doRawInhabitantRequest(t, s, http.MethodPost, base, trailing)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing JSON: status=%d body=%s", resp.StatusCode, data)
	}
	oversized := map[string]any{
		"zone": "Z", "x": 0, "y": 0, "l": 0, "name": "x", "kind": "npc",
		"note": strings.Repeat("x", int(roomInhabitantBodyLimit)+1),
	}
	resp, data = doInhabitantRequest(t, s, http.MethodPost, base, oversized)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status=%d body=%s", resp.StatusCode, data)
	}
}
