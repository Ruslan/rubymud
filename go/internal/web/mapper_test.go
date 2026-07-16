package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"rubymud/go/internal/storage"
)

func itoa(v int64) string    { return strconv.FormatInt(v, 10) }
func urlEnc(s string) string { return url.QueryEscape(s) }

func getJSON[T any](t *testing.T, u, token string) T {
	t.Helper()
	req, err := newAuthenticatedRequest(http.MethodGet, u, nil, token)
	if err != nil {
		t.Fatalf("request %s: %v", u, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", u, resp.StatusCode)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", u, err)
	}
	return out
}

// buildMinimalTPF0Room builds a tiny valid TMudRoom2 stream with a Tag and a
// CP1251 Hint, enough for the parser to produce one room. Kept local to the web
// package so the REST import path is exercised end-to-end.
func buildMinimalTPF0Room(t *testing.T, tag int8, hint string) []byte {
	t.Helper()
	var b bytes.Buffer
	shortstr := func(s string) {
		raw, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatalf("cp1251 encode: %v", err)
		}
		b.WriteByte(byte(len(raw)))
		b.Write(raw)
	}
	b.WriteString("TPF0")
	shortstr("TMudRoom2")
	shortstr("") // instance name
	// Tag: Int8 (value type 0x02)
	shortstr("Tag")
	b.WriteByte(0x02)
	b.WriteByte(byte(tag))
	// X, Y as Int8
	shortstr("X")
	b.WriteByte(0x02)
	b.WriteByte(0)
	shortstr("Y")
	b.WriteByte(0x02)
	b.WriteByte(byte(tag))
	// Hint: ShortString (0x06)
	shortstr("Hint")
	b.WriteByte(0x06)
	raw, _ := charmap.Windows1251.NewEncoder().Bytes([]byte(hint))
	b.WriteByte(byte(len(raw)))
	b.Write(raw)
	b.WriteByte(0) // end of properties
	b.WriteByte(0) // end of children
	return b.Bytes()
}

func buildTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		w.Write(body)
	}
	zw.Close()
	return buf.Bytes()
}

func TestRESTImportListZonesRooms(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	zoneA := append(buildMinimalTPF0Room(t, 1, "Комната A1"), buildMinimalTPF0Room(t, 2, "Комната A2")...)
	zoneB := buildMinimalTPF0Room(t, 1, "Комната B1")
	zipData := buildTestZip(t, map[string][]byte{
		"maps/Зона А.mm2": zoneA,
		"maps/Зона Б.mm2": zoneB,
		"readme.txt":      []byte("junk"),
	})

	// --- POST /api/map-sets/import (multipart, field "archive") ---
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("archive", "world.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(zipData)
	mw.Close()

	req, err := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/map-sets/import", &body, s.apiToken)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, want 200", resp.StatusCode)
	}
	var summary struct {
		ID         int64    `json:"id"`
		Name       string   `json:"name"`
		ZoneCount  int      `json:"zone_count"`
		RoomCount  int      `json:"room_count"`
		Unresolved []string `json:"unresolved"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.ID == 0 || summary.ZoneCount != 2 || summary.RoomCount != 3 {
		t.Fatalf("summary = %+v, want id>0 zones=2 rooms=3", summary)
	}
	if summary.Name != "world" {
		t.Errorf("name = %q, want world", summary.Name)
	}

	// --- GET /api/map-sets ---
	sets := getJSON[[]storage.MapSet](t, ts.URL+"/api/map-sets", s.apiToken)
	if len(sets) != 1 || sets[0].ID != summary.ID {
		t.Fatalf("map-sets = %+v", sets)
	}

	// --- GET /api/map-sets/{id}/zones ---
	zones := getJSON[[]storage.ZoneInfo](t, ts.URL+"/api/map-sets/"+itoa(summary.ID)+"/zones", s.apiToken)
	if len(zones) != 2 || zones[0].Zone != "Зона А" || zones[0].RoomCount != 2 {
		t.Fatalf("zones = %+v", zones)
	}

	// --- GET /api/rooms?map_set=&zone= ---
	rooms := getJSON[[]storage.SlimRoom](t, ts.URL+"/api/rooms?map_set="+itoa(summary.ID)+"&zone="+urlEnc("Зона А"), s.apiToken)
	if len(rooms) != 2 {
		t.Fatalf("rooms = %+v, want 2", rooms)
	}
	if rooms[0].H == "" {
		t.Errorf("slim room missing hint: %+v", rooms[0])
	}

	_ = sess

	// A second import creates a NEW set (does not overwrite).
	var body2 bytes.Buffer
	mw2 := multipart.NewWriter(&body2)
	fw2, _ := mw2.CreateFormFile("archive", "world.zip")
	fw2.Write(zipData)
	mw2.Close()
	req2, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/map-sets/import", &body2, s.apiToken)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	sets2 := getJSON[[]storage.MapSet](t, ts.URL+"/api/map-sets", s.apiToken)
	if len(sets2) != 2 {
		t.Errorf("after second import: %d sets, want 2 (new set each time)", len(sets2))
	}
}

func TestRESTRoomsMissingParams(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	req, _ := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/rooms?map_set=1", nil, s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing zone)", resp.StatusCode)
	}
}
