package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

// seedMapSet inserts a small map set with two zones and returns its id.
func seedMapSet(t *testing.T, s *Server) int64 {
	t.Helper()
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name:      "Krynn",
		ZoneCount: 2,
		RoomCount: 3,
		SeamCount: 1,
		Rooms: []storage.Room{
			{Zone: "Хилло", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "Бухта Хилло",
				EDirs: `["E","W"]`, Doors: `[]`, Ch: 12, Automaps: `["Море Сирриона|на восток|163"]`},
			{Zone: "Хилло", X: 1, Y: 0, L: 0, Tag: intPtr(2), Hint: "Портовые ворота",
				EDirs: `["E"]`, Doors: `["S"]`, Ch: 4, BColor: strPtr("clRed"), IsDT: true},
			{Zone: "Море Сирриона", X: 5, Y: 5, L: 0, Tag: intPtr(163), Hint: "У берегов",
				EDirs: `[]`, Doors: `[]`, Ch: 0},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	return id
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func callTool(t *testing.T, s *Server, name string, args string) map[string]any {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":1,"params":{"name":%q,"arguments":%s}}`, name, args)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", name, err)
	}
	defer resp.Body.Close()
	var res jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("no result map, got %#v (error=%v)", res.Result, res.Error)
	}
	return result
}

func toolText(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in %#v", result)
	}
	return content[0].(map[string]any)["text"].(string)
}

func TestMCPMapSets(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedMapSet(t, s)

	// No active set yet.
	res := callTool(t, s, "mud_map_sets", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "Krynn") || !strings.Contains(text, "2 zones") || !strings.Contains(text, "3 rooms") {
		t.Errorf("mud_map_sets missing metadata: %q", text)
	}
	if !strings.Contains(text, "(no active set)") {
		t.Errorf("expected no-active-set marker: %q", text)
	}

	// Mark active and confirm the marker moves.
	if err := s.store.SetActiveMapSetID(sess.SessionID(), id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	res = callTool(t, s, "mud_map_sets", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	text = toolText(t, res)
	if !strings.Contains(text, "ACTIVE") {
		t.Errorf("expected ACTIVE marker after setting: %q", text)
	}
	if strings.Contains(text, "(no active set)") {
		t.Errorf("should not report no-active-set once set: %q", text)
	}
}

func TestMCPMapZone(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedMapSet(t, s)

	// Explicit map_set + zone.
	res := callTool(t, s, "mud_map_zone",
		fmt.Sprintf(`{"session_id":%d,"map_set":%d,"zone":"Хилло"}`, sess.SessionID(), id))
	text := toolText(t, res)
	if !strings.Contains(text, "2 room(s)") {
		t.Errorf("expected 2 rooms in Хилло: %q", text)
	}
	if !strings.Contains(text, "Бухта Хилло") || !strings.Contains(text, "Портовые ворота") {
		t.Errorf("missing room hints: %q", text)
	}
	if !strings.Contains(text, "DT") {
		t.Errorf("expected DT flag on death-trap room: %q", text)
	}
	if !strings.Contains(text, "seams=Море Сирриона|на восток|163") {
		t.Errorf("expected seam listing: %q", text)
	}
}

func TestMCPMapZoneDefaultsToActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	id := seedMapSet(t, s)
	s.store.SetActiveMapSetID(sess.SessionID(), id)

	res := callTool(t, s, "mud_map_zone",
		fmt.Sprintf(`{"session_id":%d,"zone":"Море Сирриона"}`, sess.SessionID()))
	text := toolText(t, res)
	if !strings.Contains(text, "1 room(s)") || !strings.Contains(text, "У берегов") {
		t.Errorf("expected active-set default to resolve zone: %q", text)
	}
}

func TestMCPMapZoneNoActiveSet(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	seedMapSet(t, s) // exists but not active

	res := callTool(t, s, "mud_map_zone",
		fmt.Sprintf(`{"session_id":%d,"zone":"Хилло"}`, sess.SessionID()))
	if res["isError"] != true {
		t.Errorf("expected isError when no active set and no map_set given: %#v", res)
	}
}

func TestMCPMapZoneTruncation(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	// Build a zone with more rooms than the limit.
	var rooms []storage.Room
	for i := 0; i < 5; i++ {
		rooms = append(rooms, storage.Room{Zone: "Big", X: i, Y: 0, L: 0,
			Tag: intPtr(i), Hint: fmt.Sprintf("Room %d", i), EDirs: `[]`, Doors: `[]`})
	}
	id, err := s.store.CreateMapSet(storage.MapSetInput{Name: "Big", ZoneCount: 1, RoomCount: 5, Rooms: rooms})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	res := callTool(t, s, "mud_map_zone",
		fmt.Sprintf(`{"session_id":%d,"map_set":%d,"zone":"Big","limit":2}`, sess.SessionID(), id))
	text := toolText(t, res)
	if !strings.Contains(text, "showing first 2") || !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation note with limit=2: %q", text)
	}
}
