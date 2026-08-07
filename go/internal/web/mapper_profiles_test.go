package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// ---------------------------------------------------------------------------
// /api/mapper-profiles — the ROOM FORMAT endpoints. Not to be confused with
// /api/profiles (TinTin alias/trigger layering), which these tests never touch.
// ---------------------------------------------------------------------------

func doJSON(t *testing.T, method, u, token string, body any) (*http.Response, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(blob)
	}
	req, err := newAuthenticatedRequest(method, u, rdr, token)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

func englishSpecMap() map[string]any {
	return map[string]any{
		"name":       "ignored — the server forces this to the row name",
		"exits":      map[string]any{"guard": "Obvious exits:", "pattern": `^Obvious exits:\s*(.*?)\s*\.?$`, "none_token": "None"},
		"block":      map[string]any{"stops": []string{"blank", "max_lines"}, "max_lines": 16},
		"strip":      map[string]any{"mode": "fixed_width", "width": 12},
		"header":     map[string]any{"rule": "first"},
		"junk_field": "dropped on normalization",
	}
}

// TestMapperProfilesListIncludesSyntheticBuiltin: the builtin is not a row, so
// the list endpoint is the ONLY place the UI can learn about it. It must come
// first, be flagged read-only, and carry a spec the "make a copy" action can
// pre-fill a form from.
func TestMapperProfilesListIncludesSyntheticBuiltin(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	type entry struct {
		Ref         string          `json:"ref"`
		ID          int64           `json:"id"`
		Name        string          `json:"name"`
		Builtin     bool            `json:"builtin"`
		Version     int             `json:"version"`
		Spec        json.RawMessage `json:"spec"`
		Title       string          `json:"title"`
		Provenance  string          `json:"provenance"`
		Description string          `json:"description"`
		Editable    bool            `json:"editable"`
		GoDetector  bool            `json:"go_detector"`
	}
	list := getJSON[[]entry](t, ts.URL+"/api/mapper-profiles", s.apiToken)
	// One entry per REGISTERED detector, computed rather than hardcoded so that
	// landing a contributed detector does not break an unrelated test.
	if len(list) != builtinDetectorCount() {
		t.Fatalf("fresh list = %d entries, want the %d registered builtins", len(list), builtinDetectorCount())
	}
	for _, e := range list {
		// Every entry carries the DetectorInfo the settings UI renders verbatim.
		if e.Ref == "" || e.Title == "" || e.Provenance == "" || e.Description == "" {
			t.Errorf("builtin entry is missing UI fields: %+v", e)
		}
	}
	if !list[0].Builtin || list[0].ID != 0 {
		t.Errorf("builtin entry = %+v, want id 0 flagged builtin", list[0])
	}
	if list[0].Ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("first builtin ref = %q, want the default's", list[0].Ref)
	}
	if list[0].Provenance != string(mapper.ProvenanceVerified) {
		t.Errorf("rmud provenance = %q, want Verified", list[0].Provenance)
	}
	if list[0].Name != mapper.RMUDSpec().Name {
		t.Errorf("builtin name = %q, want %q", list[0].Name, mapper.RMUDSpec().Name)
	}
	// The advertised spec must be the real one — a copy made from it has to compile.
	if err := mapper.ValidateSpecJSON(string(list[0].Spec)); err != nil {
		t.Errorf("the builtin spec the UI would copy does not compile: %v", err)
	}
}

// TestMapperProfileCreateUpdateDelete walks the CRUD round trip through HTTP and
// pins the two normalizations the write path performs: spec.name is forced to
// the row name, and unknown keys from the request body are dropped.
func TestMapperProfileCreateUpdateDelete(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken,
		map[string]any{"name": "  Tortilla  ", "spec": englishSpecMap()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID   int64           `json:"id"`
		Name string          `json:"name"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}
	if created.Name != "Tortilla" {
		t.Errorf("name = %q, want trimmed", created.Name)
	}
	spec, err := mapper.ParseSpecJSON(created.Spec)
	if err != nil {
		t.Fatalf("stored spec does not parse: %v", err)
	}
	if spec.Name != "Tortilla" {
		t.Errorf("spec.name = %q — the write path must force it to the row name", spec.Name)
	}
	if strings.Contains(string(created.Spec), "junk_field") {
		t.Errorf("unknown request keys were persisted: %s", created.Spec)
	}

	// Update.
	resp, body = doJSON(t, http.MethodPut, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken,
		map[string]any{"name": "Tortilla 2", "spec": englishSpecMap()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d: %s", resp.StatusCode, body)
	}

	// Delete, then it is gone from the list.
	resp, body = doJSON(t, http.MethodDelete, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d: %s", resp.StatusCode, body)
	}
	list := getJSON[[]map[string]any](t, ts.URL+"/api/mapper-profiles", s.apiToken)
	if len(list) != builtinDetectorCount() {
		t.Errorf("after delete the list should hold only the builtins, got %d", len(list))
	}

	// Updating/deleting a gone id is a 404, not a 500.
	resp, _ = doJSON(t, http.MethodDelete, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", resp.StatusCode)
	}
}

// TestMapperProfileCreateRejectsUncompilableSpec: the compile error must reach
// the client verbatim — it names the failing rule and is what the form shows.
func TestMapperProfileCreateRejectsUncompilableSpec(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	bad := englishSpecMap()
	bad["exits"] = map[string]any{"guard": "", "pattern": `^x(.*)$`, "none_token": "none"}

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken,
		map[string]any{"name": "bad", "spec": bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "empty exits guard") {
		t.Errorf("error body = %q, want Compile's verbatim message", body)
	}
	if list := getJSON[[]map[string]any](t, ts.URL+"/api/mapper-profiles", s.apiToken); len(list) != builtinDetectorCount() {
		t.Errorf("a rejected create must not store anything, list has %d entries", len(list))
	}
}

func TestMapperProfileDuplicateNameIsA400(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	body := map[string]any{"name": "dup", "spec": englishSpecMap()}
	if resp, b := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("first create = %d: %s", resp.StatusCode, b)
	}
	resp, b := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate create = %d, want 400 (%s)", resp.StatusCode, b)
	}
	if !strings.Contains(b, "already exists") {
		t.Errorf("duplicate error = %q", b)
	}
}

// TestMapperProfileValidateEndpoint is the form's inline-feedback path: it must
// never write, and must return the exact compile message so the TS side never
// has to reimplement a validation rule.
func TestMapperProfileValidateEndpoint(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles/validate", s.apiToken,
		map[string]any{"name": "draft", "spec": englishSpecMap()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate status = %d: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !out.OK || out.Error != "" {
		t.Errorf("a valid spec validated as %+v", out)
	}

	bad := englishSpecMap()
	bad["strip"] = map[string]any{"mode": "fixed_width", "width": 0}
	_, body = doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles/validate", s.apiToken,
		map[string]any{"name": "draft", "spec": bad})
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if out.OK || !strings.Contains(out.Error, "fixed_width") {
		t.Errorf("invalid spec validated as %+v, want ok=false with the width rule named", out)
	}

	// Nothing was written by either call.
	if list := getJSON[[]map[string]any](t, ts.URL+"/api/mapper-profiles", s.apiToken); len(list) != builtinDetectorCount() {
		t.Errorf("validate must not write; list has %d entries", len(list))
	}
	// A `validate` path segment must not be parsed as a profile id.
	if resp, _ := doJSON(t, http.MethodPut, ts.URL+"/api/mapper-profiles/validate", s.apiToken,
		map[string]any{"name": "x", "spec": englishSpecMap()}); resp.StatusCode == http.StatusOK {
		t.Error("PUT /mapper-profiles/validate should not be a successful update")
	}
}

func TestMapperProfileReplayEndpointUsesRecentMudOutput(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	sessionID := sess.SessionID()
	mudLines := []string{
		"old noise",
		"",
		"The Old Road",
		"            Ancient stones line the road.",
		"Obvious exits: north, east.",
		"The Quiet Yard",
		"            Nothing moves here.",
		"Obvious exits: south.",
	}
	for i, line := range mudLines {
		var overlays []storage.LogOverlay
		if i == len(mudLines)-1 {
			overlays = []storage.LogOverlay{{OverlayType: "gag"}}
		}
		if _, err := s.store.AppendLogEntryWithOverlays(sessionID, "main", line, line, overlays); err != nil {
			t.Fatalf("append mud log: %v", err)
		}
	}
	// Gag is applied after the live mapper hook, so even a hidden exits line must
	// remain in replay or the two paths recognize different rooms.
	// Local output lives in the same log table but never reaches the live room
	// detector, so replay must not count or parse it.
	if _, err := s.store.AppendLogEntry(sessionID, "main", "Echo Room", "Obvious exits: west."); err != nil {
		t.Fatalf("append echo log: %v", err)
	}

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles/replay", s.apiToken,
		map[string]any{"name": "draft", "spec": englishSpecMap(), "session_id": sessionID, "limit": 100})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		OK             bool  `json:"ok"`
		SessionID      int64 `json:"session_id"`
		ScannedLines   int   `json:"scanned_lines"`
		CandidateLines int   `json:"candidate_lines"`
		MatchedBlocks  int   `json:"matched_blocks"`
		Samples        []struct {
			LogID int64  `json:"log_id"`
			Hint  string `json:"hint"`
			Desc  string `json:"desc"`
			Exits string `json:"exits"`
		} `json:"samples"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode replay: %v (%s)", err, body)
	}
	if !out.OK || out.SessionID != sessionID || out.ScannedLines != len(mudLines) || out.CandidateLines != 2 || out.MatchedBlocks != 2 {
		t.Fatalf("replay result = %+v", out)
	}
	if len(out.Samples) != 2 || out.Samples[0].Hint != "The Old Road" || out.Samples[1].Hint != "The Quiet Yard" {
		t.Errorf("samples = %+v", out.Samples)
	}
	if out.Samples[0].Desc != "Ancient stones line the road." || out.Samples[1].Exits != "south" {
		t.Errorf("parsed samples = %+v", out.Samples)
	}
}

func TestMapperProfileReplayEndpointRejectsInvalidInput(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{"missing session", map[string]any{"name": "draft", "spec": englishSpecMap(), "session_id": sess.SessionID() + 999, "limit": 100}, "session not found"},
		{"too many lines", map[string]any{"name": "draft", "spec": englishSpecMap(), "session_id": sess.SessionID(), "limit": 5001}, "limit"},
		{"bad spec", map[string]any{"name": "draft", "spec": map[string]any{}, "session_id": sess.SessionID(), "limit": 100}, "exits"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles/replay", s.apiToken, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, body)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("body = %q, want %q", body, tc.want)
			}
		})
	}
}

// TestSessionMapperProfileAssignAppliesLive covers AGENTS #2 for the assignment:
// the POST persists the column AND rebuilds the live session's accumulator, and
// the GET reports both the stored assignment and what the session is really
// using.
func TestSessionMapperProfileAssignAppliesLive(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	rec, err := s.store.EnsureDefaultSession("localhost", 1234)
	if err != nil {
		t.Fatalf("EnsureDefaultSession: %v", err)
	}
	base := ts.URL + "/api/sessions/" + itoa(rec.ID) + "/mapper-profile"

	// Default: no assignment, and the live session reports the builtin once loaded.
	sess.LoadMapperProfile()
	var got struct {
		MapperProfileID *int64 `json:"mapper_profile_id"`
		Live            bool   `json:"live"`
		ActiveName      string `json:"active_name"`
		Error           string `json:"error"`
	}
	_, body := doJSON(t, http.MethodGet, base, s.apiToken, nil)
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.MapperProfileID != nil {
		t.Errorf("default assignment = %v, want null", *got.MapperProfileID)
	}
	if !got.Live || got.ActiveName != mapper.RMUDDetectorInfo().Title || got.Error != "" {
		t.Errorf("live status = %+v, want the builtin with no error", got)
	}

	// Create a profile and assign it.
	_, body = doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken,
		map[string]any{"name": "english", "spec": englishSpecMap()})
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}

	resp, body := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"profile_id": created.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign status = %d: %s", resp.StatusCode, body)
	}
	if ref, name, warn := sess.MapperProfileStatus(); ref != mapper.FormatRefForDB(created.ID) || name != "english" || warn != "" {
		t.Errorf("the live session did not pick up the assignment: (%q,%q,%q)", ref, name, warn)
	}

	// Clearing it returns the session to the builtin, live.
	if resp, body := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"profile_id": nil}); resp.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d: %s", resp.StatusCode, body)
	}
	if ref, name, _ := sess.MapperProfileStatus(); ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("after clearing: (%q,%q), want the default builtin", ref, name)
	}

	// A nonexistent id is refused rather than silently becoming a fallback.
	if resp, _ := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"profile_id": 99999}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("assigning a missing profile = %d, want 400", resp.StatusCode)
	}

	// --- reference form -------------------------------------------------------
	// A `builtin:<key>` reference selects a compiled-in detector — the shape an FK
	// column could not express at all.
	if resp, b := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"format": "builtin:diku-en"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign builtin: %d %s", resp.StatusCode, b)
	}
	if ref, name, warn := sess.MapperProfileStatus(); ref != "builtin:diku-en" || name != mapper.DikuENDetectorInfo().Title || warn != "" {
		t.Errorf("builtin assignment did not reach the live session: (%q,%q,%q)", ref, name, warn)
	}
	// A key this build does not carry is refused, not silently accepted and then
	// fallen back from on the next reconnect.
	if resp, _ := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"format": "builtin:no-such-mud"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("assigning an unregistered detector = %d, want 400", resp.StatusCode)
	}
	// A malformed reference is refused too.
	if resp, _ := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"format": "nonsense"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("assigning a malformed reference = %d, want 400", resp.StatusCode)
	}
	// "" is the default and must be accepted (it is how the UI says "use rmud").
	if resp, b := doJSON(t, http.MethodPost, base, s.apiToken, map[string]any{"format": ""}); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign default: %d %s", resp.StatusCode, b)
	}
	if got, err := s.store.GetSessionMapperFormat(rec.ID); err != nil || got != "" {
		t.Errorf("the default must be stored as NULL, got %q err=%v", got, err)
	}
}

// TestMapperProfileEditAppliesToLiveSession is the other half of AGENTS #2:
// editing the profile a running session is USING must reach it with no restart.
func TestMapperProfileEditAppliesToLiveSession(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	rec, _ := s.store.EnsureDefaultSession("localhost", 1234)

	_, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken,
		map[string]any{"name": "english", "spec": englishSpecMap()})
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp, b := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/"+itoa(rec.ID)+"/mapper-profile",
		s.apiToken, map[string]any{"profile_id": created.ID}); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign: %d %s", resp.StatusCode, b)
	}

	// Rename + change the exits guard through the settings API.
	edited := englishSpecMap()
	edited["exits"] = map[string]any{"guard": "Exits here:", "pattern": `^Exits here:\s*(.*?)\s*\.?$`, "none_token": "None"}
	if resp, b := doJSON(t, http.MethodPut, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken,
		map[string]any{"name": "english-v2", "spec": edited}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, b)
	}

	_, name, warn := sess.MapperProfileStatus()
	if name != "english-v2" || warn != "" {
		t.Errorf("the live session did not pick up the EDIT: name=%q warn=%q", name, warn)
	}

	// Deleting it drops the live session back to the builtin, again with no restart.
	if resp, b := doJSON(t, http.MethodDelete, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if ref, name, _ := sess.MapperProfileStatus(); ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("after deleting the assigned profile: (%q,%q), want the default builtin", ref, name)
	}
}

// TestRestoreStateCarriesMapperProfileStatus is the fix for the notification
// path that did not actually work. `make run` passes --mud, so Manager.Connect —
// and with it LoadMapperProfile — runs BEFORE the HTTP server listens: the
// broadcast at resolve time has ZERO attached clients and is dropped, and it is
// never persisted, so it does not come back on reload either. Replaying the
// status at restore is what makes a broken room format reach a human on the
// owner's real startup path, and on every F5 after it.
func TestRestoreStateCarriesMapperProfileStatus(t *testing.T) {
	s, sess := setupTestServer(t)

	// Heal-case first: a session on the builtin reports itself, with no error.
	sess.LoadMapperProfile()
	collect := func() []session.ServerMsg {
		t.Helper()
		var got []session.ServerMsg
		if err := s.sendRestoreState(sess, func(next session.ServerMsg) error {
			got = append(got, next)
			return nil
		}); err != nil {
			t.Fatalf("sendRestoreState: %v", err)
		}
		var out []session.ServerMsg
		for _, m := range got {
			if m.Type == "mapper_profile" {
				out = append(out, m)
			}
		}
		return out
	}

	msgs := collect()
	if len(msgs) != 1 {
		t.Fatalf("restore mapper_profile messages = %d, want 1", len(msgs))
	}
	if msgs[0].MapperProfileName != mapper.RMUDDetectorInfo().Title || msgs[0].MapperProfileError != "" {
		t.Errorf("healthy restore status = %+v, want the default builtin with no error", msgs[0])
	}

	// Now break the assigned profile behind the store's back, exactly as a
	// hand-edited DB would, and re-resolve with NOBODY listening.
	row, err := s.store.CreateMapperProfile("victim", func() string {
		spec := mapper.RMUDSpec()
		spec.Name = "victim"
		blob, mErr := mapper.MarshalSpec(spec)
		if mErr != nil {
			t.Fatalf("MarshalSpec: %v", mErr)
		}
		return string(blob)
	}())
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := s.store.SetSessionMapperFormat(sess.SessionID(), storage.MapperFormatDBRef(row.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	if err := s.store.DB().Exec(
		`UPDATE mapper_profiles SET spec = ? WHERE id = ?`,
		`{"name":"victim","exits":{"guard":"X","pattern":"^X.*$","none_token":"none"},`+
			`"block":{"stops":["blank"],"max_lines":8},`+
			`"strip":{"mode":"greedy","glyphs":"[]"},"header":{"rule":"first"}}`,
		row.ID,
	).Error; err != nil {
		t.Fatalf("corrupt row: %v", err)
	}
	sess.LoadMapperProfile()

	// A client connecting (or reloading) AFTERWARDS still learns about it.
	msgs = collect()
	if len(msgs) != 1 {
		t.Fatalf("restore mapper_profile messages = %d, want 1", len(msgs))
	}
	if msgs[0].MapperProfileError == "" {
		t.Fatal("a late-attaching client must still be told the room format failed to load")
	}
	if !strings.Contains(msgs[0].MapperProfileError, "capture group") {
		t.Errorf("restore error = %q, want the compiler's verbatim reason", msgs[0].MapperProfileError)
	}
	if msgs[0].MapperProfileName != mapper.RMUDDetectorInfo().Title {
		t.Errorf("restore name = %q, want the default builtin it fell back to", msgs[0].MapperProfileName)
	}
}

// TestAssignMapperProfileToMissingSessionIs404: a write addressed at a session
// that does not exist used to return 200 having silently written nothing, which
// reads to the caller as success.
func TestAssignMapperProfileToMissingSessionIs404(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	resp, body := doJSON(t, http.MethodPost, ts.URL+"/api/sessions/987654/mapper-profile",
		s.apiToken, map[string]any{"profile_id": nil})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", resp.StatusCode, body)
	}
}

// TestBuiltinMapperProfileIsReadOnly: the builtin has no row, so a write aimed at
// it must be refused explicitly rather than depending on AUTOINCREMENT never
// handing out id 0.
func TestBuiltinMapperProfileIsReadOnly(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	for _, tc := range []struct{ method, name string }{
		{http.MethodPut, "update"},
		{http.MethodDelete, "delete"},
	} {
		var body any
		if tc.method == http.MethodPut {
			body = map[string]any{"name": "hijack", "spec": englishSpecMap()}
		}
		resp, got := doJSON(t, tc.method, ts.URL+"/api/mapper-profiles/0", s.apiToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s builtin = %d, want 400 (%s)", tc.name, resp.StatusCode, got)
		}
		if !strings.Contains(got, "read-only") {
			t.Errorf("%s builtin message = %q, want it to say read-only", tc.name, got)
		}
	}
}

// builtinDetectorCount is how many compiled-in detectors this test binary links.
// The list-endpoint tests count against it rather than a literal, so landing a
// contributed detector does not break four unrelated assertions.
func builtinDetectorCount() int { return len(mapper.DetectorInfos()) }

// TestSessionMapperFormatIsStoredCanonically is the regression for a silent
// data-integrity bug: the endpoint validated a reference with ParseFormatRef and
// then stored the RAW string.
//
// ParseFormatRef is deliberately lenient (the column can be hand-edited), so it
// accepts "db:007" and "builtin: rmud". But `storage` matches this column by
// EXACT STRING — DeleteMapperProfile clears sessions WHERE mapper_format = 'db:7',
// and SessionIDsUsingMapperProfile finds the live sessions to re-apply the same
// way. A session stored as "db:007" was found by NEITHER: editing that profile
// never reached the live session, and deleting it left the session pointing at a
// row that no longer existed, with nothing reporting either.
func TestSessionMapperFormatIsStoredCanonically(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	rec, err := s.store.EnsureDefaultSession("localhost", 1234)
	if err != nil {
		t.Fatalf("EnsureDefaultSession: %v", err)
	}
	base := ts.URL + "/api/sessions/" + itoa(rec.ID) + "/mapper-profile"

	_, body := doJSON(t, http.MethodPost, ts.URL+"/api/mapper-profiles", s.apiToken,
		map[string]any{"name": "english", "spec": englishSpecMap()})
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}

	// Assign with a NON-canonical but parseable reference, the way a hand-written
	// API call or a future client could.
	noncanonical := "db:0" + itoa(created.ID)
	if resp, b := doJSON(t, http.MethodPost, base, s.apiToken,
		map[string]any{"format": noncanonical}); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign %q: %d %s", noncanonical, resp.StatusCode, b)
	}

	stored, err := s.store.GetSessionMapperFormat(rec.ID)
	if err != nil {
		t.Fatalf("GetSessionMapperFormat: %v", err)
	}
	if want := mapper.FormatRefForDB(created.ID); stored != want {
		t.Fatalf("stored %q, want the canonical %q — a non-canonical reference is invisible to the delete and re-apply queries", stored, want)
	}

	// The two exact-match queries must now find this session.
	ids, err := s.store.SessionIDsUsingMapperProfile(created.ID)
	if err != nil {
		t.Fatalf("SessionIDsUsingMapperProfile: %v", err)
	}
	if len(ids) != 1 || ids[0] != rec.ID {
		t.Errorf("SessionIDsUsingMapperProfile = %v, want [%d] — the live re-apply after an edit would miss this session", ids, rec.ID)
	}

	// An edit reaches the live session.
	edited := englishSpecMap()
	if resp, b := doJSON(t, http.MethodPut, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken,
		map[string]any{"name": "english-v2", "spec": edited}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, b)
	}
	if _, name, _ := sess.MapperProfileStatus(); name != "english-v2" {
		t.Errorf("the live session did not pick up the edit: name=%q", name)
	}

	// And deleting it clears the assignment instead of leaving a dangling one.
	if resp, b := doJSON(t, http.MethodDelete, ts.URL+"/api/mapper-profiles/"+itoa(created.ID), s.apiToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if got, _ := s.store.GetSessionMapperFormat(rec.ID); got != "" {
		t.Errorf("after deleting the profile the session still references %q — it points at a row that no longer exists", got)
	}
}

// TestBuiltinReferenceIsStoredCanonically covers the same hazard on the builtin
// side, where the leniency is whitespace rather than leading zeros.
func TestBuiltinReferenceIsStoredCanonically(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	rec, err := s.store.EnsureDefaultSession("localhost", 1234)
	if err != nil {
		t.Fatalf("EnsureDefaultSession: %v", err)
	}
	base := ts.URL + "/api/sessions/" + itoa(rec.ID) + "/mapper-profile"

	if resp, b := doJSON(t, http.MethodPost, base, s.apiToken,
		map[string]any{"format": "builtin: diku-en "}); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign: %d %s", resp.StatusCode, b)
	}
	if got, _ := s.store.GetSessionMapperFormat(rec.ID); got != "builtin:diku-en" {
		t.Errorf("stored %q, want the canonical \"builtin:diku-en\"", got)
	}
}
