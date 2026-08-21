package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
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
		Name: "frozen", Rooms: []storage.Room{{Zone: "Alpha", X: 1, Y: 2, L: 0, Hint: "Forge", Desc: "A soot-darkened forge.", Exits: "E W"}},
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
	var emptyPayload roomInhabitantListResponse
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &emptyPayload) != nil || len(emptyPayload.Items) != 0 || emptyPayload.Truncated {
		t.Fatalf("empty list: status=%d body=%s", resp.StatusCode, data)
	}
	if emptyPayload.Room.Hint != "Forge" || emptyPayload.Room.Desc != "A soot-darkened forge." || emptyPayload.Room.Exits != "E W" {
		t.Fatalf("knowledge room text=%+v body=%s", emptyPayload.Room, data)
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
	var listPayload roomInhabitantListResponse
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &listPayload) != nil || listPayload.Truncated {
		t.Fatalf("list: status=%d rows=%+v body=%s", resp.StatusCode, rows, data)
	}
	rows = listPayload.Items
	if len(rows) != 2 {
		t.Fatalf("list: rows=%+v body=%s", rows, data)
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
	if err := json.Unmarshal(data, &listPayload); err != nil || listPayload.Truncated || len(listPayload.Items) != 1 || listPayload.Items[0].Name != "Master Smith" {
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
	if len(notices) != 5 { // two creates, attach, one coalesced shared-entity patch, delete
		t.Fatalf("notices=%+v, want four room invalidations and one map-set invalidation", notices)
	}
	betaNotice := false
	mapNotice := false
	for _, notice := range notices {
		if notice.MapSetID != setID {
			t.Fatalf("unexpected notice: %+v", notice)
		}
		if notice.Type == "map_knowledge_changed" {
			if mapNotice || notice.Zone != "" {
				t.Fatalf("unexpected map-set notice: %+v", notice)
			}
			mapNotice = true
			continue
		}
		if notice.Type != "room_knowledge_changed" {
			t.Fatalf("unexpected notice: %+v", notice)
		}
		if notice.Zone == "Beta" && notice.RoomX == 9 && notice.RoomY == 8 && notice.RoomL == 1 {
			betaNotice = true
		} else if notice.Zone != "Alpha" || notice.RoomX != 1 || notice.RoomY != 2 || notice.RoomL != 0 {
			t.Fatalf("unexpected notice coordinate: %+v", notice)
		}
	}
	if !betaNotice {
		t.Fatal("second-room placement did not emit its exact invalidation")
	}
	if !mapNotice {
		t.Fatal("shared entity patch did not emit a map-set invalidation")
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

func TestRESTRoomInhabitantsReturnsBoundedEnvelope(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	setID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "bounded", Rooms: []storage.Room{{Zone: "Alpha", X: 0, Y: 0, L: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cell := storage.EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	for i := 0; i < storage.InhabitantListDefaultLimit+1; i++ {
		if _, err := s.store.CreateRoomInhabitant(setID, cell, storage.RoomInhabitantFields{
			InhabitantFields: storage.InhabitantFields{
				Name: fmt.Sprintf("NPC %02d", i), Kind: storage.InhabitantKindNPC,
			},
		}); err != nil {
			t.Fatalf("create inhabitant %d: %v", i, err)
		}
	}

	endpoint := fmt.Sprintf("%s/api/map-sets/%d/inhabitants?zone=Alpha&x=0&y=0&l=0", ts.URL, setID)
	resp, data := doInhabitantRequest(t, s, http.MethodGet, endpoint, nil)
	var payload struct {
		Items      []storage.RoomInhabitantView `json:"items"`
		Truncated  bool                         `json:"truncated"`
		NextCursor string                       `json:"next_cursor"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &payload) != nil {
		t.Fatalf("bounded room list: status=%d body=%s", resp.StatusCode, data)
	}
	if len(payload.Items) != storage.InhabitantListDefaultLimit || !payload.Truncated {
		t.Fatalf("bounded room payload = %d rows truncated=%v, want %d/true", len(payload.Items), payload.Truncated, storage.InhabitantListDefaultLimit)
	}
	if payload.Items[0].Name != "NPC 00" || payload.Items[len(payload.Items)-1].Name != "NPC 19" {
		t.Fatalf("bounded room payload order = %q..%q", payload.Items[0].Name, payload.Items[len(payload.Items)-1].Name)
	}
	if payload.NextCursor == "" {
		t.Fatal("bounded room payload did not provide a continuation cursor")
	}
	resp, data = doInhabitantRequest(t, s, http.MethodGet, endpoint+"&cursor="+url.QueryEscape(payload.NextCursor), nil)
	var continuation struct {
		Items     []storage.RoomInhabitantView `json:"items"`
		Truncated bool                         `json:"truncated"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &continuation) != nil || continuation.Truncated || len(continuation.Items) != 1 || continuation.Items[0].Name != "NPC 20" {
		t.Fatalf("bounded room continuation: status=%d body=%s", resp.StatusCode, data)
	}
	legacyPayload, _ := json.Marshal(map[string]any{"name": payload.Items[len(payload.Items)-1].Name, "id": payload.Items[len(payload.Items)-1].ID})
	legacyCursor := base64.RawURLEncoding.EncodeToString(legacyPayload)
	resp, data = doInhabitantRequest(t, s, http.MethodGet, endpoint+"&cursor="+url.QueryEscape(legacyCursor), nil)
	if resp.StatusCode != http.StatusBadRequest || string(data) != "legacy_room_inhabitant_cursor\n" {
		t.Fatalf("legacy room cursor: status=%d body=%q", resp.StatusCode, data)
	}
	resp, _ = doInhabitantRequest(t, s, http.MethodGet, endpoint+"&cursor=not-a-cursor", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid room cursor status=%d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestRESTRoomInhabitantsRejectsUnversionedLegacyCursorWithoutSkippingNames(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	setID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "cursor-version"})
	if err != nil {
		t.Fatal(err)
	}
	cell := storage.EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	for _, name := range []string{"Zed", "Alpha", "Beta"} {
		if _, err := s.store.CreateRoomInhabitant(setID, cell, storage.RoomInhabitantFields{
			InhabitantFields: storage.InhabitantFields{Name: name, Kind: storage.InhabitantKindNPC},
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	endpoint := fmt.Sprintf("%s/api/map-sets/%d/inhabitants?zone=Alpha&x=0&y=0&l=0&limit=1", ts.URL, setID)
	resp, data := doInhabitantRequest(t, s, http.MethodGet, endpoint, nil)
	var first struct {
		Items      []storage.RoomInhabitantView `json:"items"`
		NextCursor string                       `json:"next_cursor"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(data, &first) != nil || len(first.Items) != 1 || first.Items[0].Name != "Zed" {
		t.Fatalf("versioned cursor first page: status=%d body=%s", resp.StatusCode, data)
	}
	if first.NextCursor == "" {
		t.Fatal("versioned cursor first page did not provide a cursor")
	}
	legacyPayload, _ := json.Marshal(map[string]any{"name": "Alpha", "id": first.Items[0].ID})
	legacyCursor := base64.RawURLEncoding.EncodeToString(legacyPayload)
	resp, data = doInhabitantRequest(t, s, http.MethodGet, endpoint+"&cursor="+url.QueryEscape(legacyCursor), nil)
	if resp.StatusCode != http.StatusBadRequest || string(data) != "legacy_room_inhabitant_cursor\n" {
		t.Fatalf("legacy cursor response: status=%d body=%q, want stable 400 code", resp.StatusCode, data)
	}
	idOnlyCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"id":20}`))
	resp, data = doInhabitantRequest(t, s, http.MethodGet, endpoint+"&cursor="+url.QueryEscape(idOnlyCursor), nil)
	if resp.StatusCode != http.StatusBadRequest || string(data) != "legacy_room_inhabitant_cursor\n" {
		t.Fatalf("id-only legacy cursor response: status=%d body=%q, want stable 400 code", resp.StatusCode, data)
	}
}

func TestDecodeRoomInhabitantCursorKeepsZeroAndNegativeIDsInvalid(t *testing.T) {
	for _, raw := range []string{`{"id":0}`, `{"id":-1}`} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, err := decodeRoomInhabitantCursor(cursor); err == nil || errors.Is(err, errLegacyRoomInhabitantCursor) {
			t.Fatalf("cursor %s classified as %v, want ordinary invalid cursor", raw, err)
		}
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
