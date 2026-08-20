package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

func TestConcurrentLanguagePUTSerializesCommitCacheAndBroadcast(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	firstCommitted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondCommitted := make(chan struct{}, 1)
	s.appSettingsAfterCommit = func(locale appi18n.Locale) {
		if locale == appi18n.Russian {
			close(firstCommitted)
			<-releaseFirst
			return
		}
		secondCommitted <- struct{}{}
	}
	var mu sync.Mutex
	var events []string
	sess.AttachClient("language-order", func(msg session.ServerMsg) error {
		if msg.Type == "app_language_changed" {
			mu.Lock()
			events = append(events, msg.AppLanguage)
			mu.Unlock()
		}
		return nil
	})
	put := func(language string, done chan<- error) {
		req, _ := newAuthenticatedRequest(http.MethodPut, ts.URL+"/api/app/settings", strings.NewReader(`{"language":"`+language+`"}`), s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}
	done := make(chan error, 2)
	go put("ru", done)
	<-firstCommitted
	go put("en", done)
	select {
	case <-secondCommitted:
		t.Fatal("second language committed while first commit/cache/broadcast sequence was paused")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	stored, err := s.store.GetSetting("language")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if stored != "en" || s.mapperLanguage() != appi18n.English || len(events) != 2 || events[0] != "ru" || events[1] != "en" {
		t.Fatalf("final ordering: db=%q cache=%q events=%v", stored, s.mapperLanguage(), events)
	}
}

func TestMapRecordingRuntimeReasonsLocalizeStateAndConflict(t *testing.T) {
	reasons := []string{
		"position lost", "previous autorecord step is still pending; wait for the new cell to turn green", "occupied expected room content mismatch",
		"active map changed", "room format changed", "room format detector crashed",
		"manual movement command outcome unknown", "map walk command outcome unknown",
		"walk confirmation timeout", "session disconnected", "stopped by user",
		"room autorecord requires an editable map set", "incomplete room event",
		"room event too large",
		"map topology changed while autorecord was pending", "autorecord lifecycle changed",
		"movement confirmation timed out",
	}
	for _, reason := range reasons {
		state := session.MapRecordingState{Mode: session.MapRecordingOff, Reason: reason, Conflict: &session.MapRecordingConflictSnapshot{Reason: reason}}
		en := localizeMapRecordingState(appi18n.English, state)
		if en.Reason != reason || en.ReasonCode == "" || en.Conflict.Reason != reason || en.Conflict.ReasonCode == "" {
			t.Errorf("English %q = %+v", reason, en)
		}
		ru := localizeMapRecordingState(appi18n.Russian, state)
		if ru.Reason == reason || ru.ReasonCode == "" || ru.Conflict.Reason == reason || ru.Conflict.ReasonCode == "" {
			t.Errorf("Russian %q = %+v", reason, ru)
		}
		if got := renderMapperWireText(appi18n.Russian, ru.ReasonCode, ru.ReasonArgs); got != ru.Reason {
			t.Errorf("Russian %q reconstructed %q, want %q", reason, got, ru.Reason)
		}
	}
}

func TestRecordingStartStopHandlerErrorsUseLocalizedWriter(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	if err := s.manager.Disconnect(sess.SessionID()); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	for _, path := range []string{"map-recording-start", "map-recording-stop"} {
		req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/"+path, strings.NewReader(`{}`), s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperRecordingSessionNotConnected) {
			t.Errorf("%s status=%d code=%q", path, resp.StatusCode, resp.Header.Get("X-RubyMUD-Reason-Code"))
		}
		req, _ = newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/sessions/not-an-id/"+path, strings.NewReader(`{}`), s.apiToken)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperInvalidSessionID) {
			t.Errorf("invalid %s status=%d code=%q", path, resp.StatusCode, resp.Header.Get("X-RubyMUD-Reason-Code"))
		}
	}
}

func TestRecordingConflictStateLocalizesThroughActualStopHandler(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	s.mapRecordingTryStop = func(*session.Session) (session.MapRecordingState, bool) {
		reason := "occupied expected room content mismatch"
		return session.MapRecordingState{Mode: session.MapRecordingConflict, Reason: reason, Conflict: &session.MapRecordingConflictSnapshot{Reason: reason}}, false
	}
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/map-recording-stop", strings.NewReader(`{}`), s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	state := out["state"].(map[string]any)
	conflict := state["conflict"].(map[string]any)
	if state["reason_code"] != string(appi18n.MapperRecordingOccupiedMismatch) || conflict["reason_code"] != string(appi18n.MapperRecordingOccupiedMismatch) || state["reason"] == "occupied expected room content mismatch" {
		t.Fatalf("localized conflict state = %#v", state)
	}
}

func TestMapperProfileReplayValidationUsesJSONEnvelope(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	for _, test := range []struct {
		body string
		code appi18n.Key
	}{
		{`{"session_id":` + strconv.FormatInt(sess.SessionID(), 10) + `}`, appi18n.MapperProfileMissingSpec},
		{`{"session_id":` + strconv.FormatInt(sess.SessionID(), 10) + `,"spec":{"name":"broken"}}`, appi18n.MapperProfileValidationFailed},
	} {
		req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/mapper-profiles/replay", strings.NewReader(test.body), s.apiToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || out["error_code"] != string(test.code) || out["error_detail"] == "" || !strings.Contains(out["error"].(string), "формат") {
			t.Errorf("replay validation = status %d body %#v", resp.StatusCode, out)
		}
	}
	req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/mapper-profiles/validate", strings.NewReader(`{"name":"draft"}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var validated map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&validated); err != nil {
		t.Fatal(err)
	}
	if validated["error_code"] != string(appi18n.MapperProfileMissingSpec) || validated["error_detail"] != "missing spec" {
		t.Fatalf("validate envelope = %#v", validated)
	}
}

func TestAppLanguageUpdateBroadcastsOncePerActiveSessionOnlyWhenChanged(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	var mu sync.Mutex
	var got []session.ServerMsg
	for _, name := range []string{"tab-a", "tab-b"} {
		sess.AttachClient(name, func(msg session.ServerMsg) error {
			if msg.Type == "app_language_changed" {
				mu.Lock()
				got = append(got, msg)
				mu.Unlock()
			}
			return nil
		})
	}
	put := func(body string) {
		t.Helper()
		req, _ := newAuthenticatedRequest(http.MethodPut, ts.URL+"/api/app/settings", strings.NewReader(body), s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT settings status = %d", resp.StatusCode)
		}
	}
	put(`{"language":"ru"}`)
	put(`{"language":"ru"}`)
	put(`{"allow_exec_command":true}`)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 { // one session broadcast, delivered once to each attached tab
		t.Fatalf("language messages = %+v, want one per tab from one session broadcast", got)
	}
	for _, msg := range got {
		if msg.AppLanguage != "ru" {
			t.Fatalf("language message = %+v", msg)
		}
	}
}

func TestRussianZoneMoveAndReconciliationReasonsUseStableCodes(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	postSoft := func(path, body string) map[string]any {
		t.Helper()
		return postJSON(t, ts, s.apiToken, sess.SessionID(), path, body)
	}
	missing := postSoft("map-zones/preview", `{}`)
	if missing["reason_code"] != string(appi18n.MapperZoneSourceTargetRequired) || !strings.Contains(missing["reason"].(string), "исход") {
		t.Fatalf("zone required refusal = %#v", missing)
	}
	rename := postSoft("map-zones/rename/preview", `{"source_zone":"A","target_zone":"B","rooms":[]}`)
	if rename["reason_code"] != string(appi18n.MapperZoneRenameAllRooms) {
		t.Fatalf("rename selector refusal = %#v", rename)
	}

	req, _ := newAuthenticatedRequest(http.MethodPost,
		fmt.Sprintf("%s/api/sessions/%d/map-reconciliation/preview", ts.URL, sess.SessionID()),
		strings.NewReader(`{"conflict_id":1,"target":{"zone":"Z","x":0,"y":0,"l":0},"layouts":[]}`), s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusConflict || resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperReconciliationFailed) || !strings.Contains(body.String(), "свер") {
		t.Fatalf("reconciliation refusal: status=%d code=%q body=%q", resp.StatusCode, resp.Header.Get("X-RubyMUD-Reason-Code"), body.String())
	}
	if resp.Header.Get("X-RubyMUD-Reason-Args") == "" {
		t.Fatal("reconciliation raw detail was not preserved in typed args")
	}
	var reasonArgs map[string]any
	if err := json.Unmarshal([]byte(resp.Header.Get("X-RubyMUD-Reason-Args")), &reasonArgs); err != nil {
		t.Fatal(err)
	}
	if got := renderMapperWireText(appi18n.Russian, resp.Header.Get("X-RubyMUD-Reason-Code"), reasonArgs); got != strings.TrimSpace(body.String()) {
		t.Fatalf("reconciliation code + args reconstructed %q, want %q", got, strings.TrimSpace(body.String()))
	}
}

func TestRussianMapSetAndSessionMapperAdminErrors(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	req, _ := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/not-an-id", nil, s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var deleted map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted["error_code"] != string(appi18n.MapperAdminInvalidMapSetID) || !strings.Contains(deleted["error"].(string), "идентификатор") {
		t.Fatalf("delete admin error = %#v", deleted)
	}

	req, _ = newAuthenticatedRequest(http.MethodPost,
		ts.URL+"/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/active-map-set",
		strings.NewReader(`{"map_set_id":999999}`), s.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperAdminMapSetIDNotFound) {
		t.Fatalf("active-set code = %q", resp.Header.Get("X-RubyMUD-Reason-Code"))
	}
	var text bytes.Buffer
	_, _ = text.ReadFrom(resp.Body)
	if !strings.Contains(text.String(), "999999") || !strings.Contains(text.String(), "не найден") {
		t.Fatalf("active-set error = %q", text.String())
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(resp.Header.Get("X-RubyMUD-Reason-Args")), &args); err != nil {
		t.Fatal(err)
	}
	if got := renderMapperWireText(appi18n.Russian, resp.Header.Get("X-RubyMUD-Reason-Code"), args); got != strings.TrimSpace(text.String()) {
		t.Fatalf("active-set code + args reconstructed %q, want %q", got, strings.TrimSpace(text.String()))
	}
}

func TestMapperWebSerializationLocalizesPositionAndProfileWarnings(t *testing.T) {
	for _, locale := range []appi18n.Locale{appi18n.English, appi18n.Russian} {
		position := localizeMapperServerMsg(locale, session.ServerMsg{Type: "room_position", PositionReason: "no active map set"})
		if position.PositionReasonCode != string(appi18n.MapperPositionNoActiveMapSet) {
			t.Fatalf("%s position = %+v", locale, position)
		}
		if got := renderMapperWireText(locale, position.PositionReasonCode, position.PositionReasonArgs); got != position.PositionReason {
			t.Fatalf("%s position reconstructed %q, want %q", locale, got, position.PositionReason)
		}

		const raw = "room format db:7 does not compile: capture group missing"
		profile := localizeMapperServerMsg(locale, session.ServerMsg{Type: "mapper_profile", MapperProfileError: raw})
		if profile.MapperProfileErrorCode != string(appi18n.MapperProfileFallback) {
			t.Fatalf("%s profile = %+v", locale, profile)
		}
		if got := renderMapperWireText(locale, profile.MapperProfileErrorCode, profile.MapperProfileErrorArgs); got != profile.MapperProfileError {
			t.Fatalf("%s profile reconstructed %q, want %q", locale, got, profile.MapperProfileError)
		}
		if locale == appi18n.Russian && profile.MapperProfileErrorDetail != raw {
			t.Fatalf("Russian profile raw detail = %q", profile.MapperProfileErrorDetail)
		}
	}
}

func TestRussianWebSocketSerializesPositionAndProfileCodes(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + s.apiToken + "&session_id=" + strconv.FormatInt(sess.SessionID(), 10)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for {
		var msg session.ServerMsg
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type == "restore_end" {
			break
		}
	}
	sess.BroadcastServerMsg(session.ServerMsg{Type: "room_position", PositionReason: "no active map set"})
	var position session.ServerMsg
	if err := conn.ReadJSON(&position); err != nil {
		t.Fatal(err)
	}
	if position.PositionReasonCode != string(appi18n.MapperPositionNoActiveMapSet) || position.PositionReason != "нет активного набора карт" {
		t.Fatalf("position websocket = %+v", position)
	}
	const raw = "room format db:7 does not compile: capture group missing"
	sess.BroadcastServerMsg(session.ServerMsg{Type: "mapper_profile", MapperProfileError: raw})
	var profile session.ServerMsg
	if err := conn.ReadJSON(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.MapperProfileErrorCode != string(appi18n.MapperProfileFallback) || profile.MapperProfileErrorDetail != raw {
		t.Fatalf("profile websocket = %+v", profile)
	}
	reason := "occupied expected room content mismatch"
	recording := session.MapRecordingState{Mode: session.MapRecordingConflict, Reason: reason, Conflict: &session.MapRecordingConflictSnapshot{Reason: reason}}
	sess.BroadcastServerMsg(session.ServerMsg{Type: "map_recording_state", MapRecording: &recording})
	var recorded session.ServerMsg
	if err := conn.ReadJSON(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.MapRecording == nil || recorded.MapRecording.ReasonCode != string(appi18n.MapperRecordingOccupiedMismatch) || recorded.MapRecording.Conflict == nil || recorded.MapRecording.Conflict.ReasonCode != string(appi18n.MapperRecordingOccupiedMismatch) {
		t.Fatalf("recording websocket = %+v", recorded)
	}
}

func TestRussianMapperProfileRESTPreservesFallbackDetail(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	spec := mapper.RMUDSpec()
	spec.Name = "victim"
	raw, err := mapper.MarshalSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.store.CreateMapperProfile("victim", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetSessionMapperFormat(sess.SessionID(), storage.MapperFormatDBRef(row.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DB().Exec(`UPDATE mapper_profiles SET spec = ? WHERE id = ?`, `{"name":"victim","exits":{"guard":"X","pattern":"^X.*$","none_token":"none"},"block":{"stops":["blank"],"max_lines":8},"strip":{"mode":"greedy","glyphs":"[]"},"header":{"rule":"first"}}`, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	sess.LoadMapperProfile()

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	req, _ := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/mapper-profile", nil, s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["error_code"] != string(appi18n.MapperProfileFallback) || out["error_detail"] == "" || !strings.Contains(out["error"].(string), "встроенный") {
		t.Fatalf("profile REST warning = %#v", out)
	}
}

func TestLanguageBroadcastDoesNotBlockOnNoClients(t *testing.T) {
	s, _ := setupTestServer(t)
	done := make(chan struct{})
	go func() {
		s.manager.BroadcastAppLanguageChanged("ru")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("language broadcast blocked")
	}
}
