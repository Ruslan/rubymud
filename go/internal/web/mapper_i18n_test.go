package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"golang.org/x/text/unicode/norm"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
)

func TestMapperRESTUsesPersistedRussianLocaleAndStableCodes(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	request := func(method, path, body string) map[string]any {
		t.Helper()
		req, err := newAuthenticatedRequest(method, ts.URL+path, bytes.NewBufferString(body), s.apiToken)
		if err != nil {
			t.Fatalf("newAuthenticatedRequest(%s %s): %v", method, path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
		return out
	}

	settings := request(http.MethodPut, "/api/app/settings", `{"language":"ru"}`)
	if settings["language"] != "ru" {
		t.Fatalf("saved language = %#v", settings["language"])
	}

	recorded := request(http.MethodPost, "/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/map-record-room", `{}`)
	if recorded["reason_code"] != string(appi18n.MapperNoActiveMapSet) || !strings.Contains(recorded["reason"].(string), "набор карт") {
		t.Fatalf("Russian recording refusal = %#v", recorded)
	}

	portal := request(http.MethodPost, "/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/map-portals", `{}`)
	if portal["reason_code"] != string(appi18n.MapperPortalSourceRequired) || !strings.Contains(portal["reason"].(string), "исходной клетки") {
		t.Fatalf("Russian portal refusal = %#v", portal)
	}

	profile := request(http.MethodPost, "/api/mapper-profiles/validate", `{"name":"draft"}`)
	if profile["error_code"] != string(appi18n.MapperProfileMissingSpec) || !strings.Contains(profile["error"].(string), "описание формата") {
		t.Fatalf("Russian profile validation = %#v", profile)
	}
}

func TestMapperLanguageUsesExplicitAtomicRefresh(t *testing.T) {
	s, _ := setupTestServer(t)
	if got := s.mapperLanguage(); got != appi18n.English {
		t.Fatalf("initial cached locale = %q", got)
	}
	if err := s.store.SetSetting("language", "ru"); err != nil {
		t.Fatalf("SetSetting(language): %v", err)
	}
	if got := s.mapperLanguage(); got != appi18n.English {
		t.Fatalf("direct DB write bypassed cache contract: %q", got)
	}
	// Tests or administrative paths that write the DB directly must explicitly
	// refresh; the public settings API performs this after its atomic commit.
	s.setMapperLanguage(s.appLanguage())
	if got := s.mapperLanguage(); got != appi18n.Russian {
		t.Fatalf("refreshed cached locale = %q", got)
	}
}

func TestMapperReasonBodyKeepsStableCodeAndTypedArgs(t *testing.T) {
	out := mapperReasonBody(appi18n.Russian, false, appi18n.MapperRecordMovementInProgress, map[string]any{
		"count": 2,
	})
	if out["reason_code"] != string(appi18n.MapperRecordMovementInProgress) {
		t.Fatalf("reason_code = %#v", out["reason_code"])
	}
	args, ok := out["reason_args"].(map[string]any)
	if !ok || args["count"] != 2 {
		t.Fatalf("reason_args = %#v", out["reason_args"])
	}
	if out["reason"] != "идёт перемещение (2 шага в пути) — запись сбросила бы очередь; дождитесь конца" {
		t.Fatalf("reason = %q", out["reason"])
	}
	if got := renderMapperWireText(appi18n.Russian, out["reason_code"].(string), args); got != out["reason"] {
		t.Fatalf("code + typed args reconstructed %q, want exact wire text %q", got, out["reason"])
	}
}

func TestPathResultJSONLocalizesRussianPluralSummaryWithoutTouchingCommands(t *testing.T) {
	tests := []struct {
		steps int
		want  string
	}{
		{1, "1 шаг"},
		{2, "2 шага"},
		{5, "5 шагов"},
		{11, "11 шагов"},
		{21, "21 шаг"},
	}
	for _, test := range tests {
		steps := make([]mapper.PathStep, test.steps)
		for i := range steps {
			steps[i] = mapper.PathStep{Command: "север", Dir: "n"}
		}
		out := pathResultJSONLocale(mapper.PathResult{Reachable: true, Steps: steps}, appi18n.Russian)
		if out["summary"] != test.want {
			t.Errorf("%d-step summary = %q, want %q", test.steps, out["summary"], test.want)
		}
		directions := out["directions"].([]string)
		for _, command := range directions {
			if command != "n" {
				t.Fatalf("MUD command translated: %q", command)
			}
		}
		if out["summary_code"] != "mapper.route_summary" {
			t.Errorf("summary_code = %#v", out["summary_code"])
		}
		args := out["summary_args"].(map[string]any)
		if got := renderMapperWireText(appi18n.Russian, out["summary_code"].(string), args); got != out["summary"] {
			t.Errorf("%d-step code + args reconstructed %q, want %q", test.steps, got, out["summary"])
		}
	}
}

func TestMapperWireTextReconstructsEveryRouteSummaryClauseAfterJSONRoundTrip(t *testing.T) {
	for _, locale := range []appi18n.Locale{appi18n.English, appi18n.Russian} {
		out := pathResultJSONLocale(mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
			{Command: "phrase {steps}", Dir: "N", Seam: true, Door: true, Locked: true, Phrase: "phrase {steps}"},
			{Command: "e", Dir: "E", IsDT: true},
		}}, locale)
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		got := renderMapperWireText(locale, decoded["summary_code"].(string), decoded["summary_args"].(map[string]any))
		if got != decoded["summary"] {
			t.Fatalf("%s reconstructed %q, want exact summary %q (args=%#v)", locale, got, decoded["summary"], decoded["summary_args"])
		}
	}
}

func TestImportEarlyFailurePreservesConflictEnvelope(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/map-sets/import", &body, s.apiToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if conflicts, ok := out["conflicts"].([]any); !ok || conflicts == nil || len(conflicts) != 0 {
		t.Fatalf("early failure conflicts = %#v, want non-nil empty array", out["conflicts"])
	}
	if out["conflicts_truncated"] != false {
		t.Fatalf("early failure conflicts_truncated = %#v, want false", out["conflicts_truncated"])
	}
}

func TestImportValidationErrorLocalizesRussianAndPreservesRawDetail(t *testing.T) {
	s, _ := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	nfd := norm.NFD.String("Балифой")
	zipData := buildTestZip(t, map[string][]byte{
		"one/ " + nfd + " .mm2": buildMinimalTPF0Room(t, 1, "first"),
		"two/Балифой.mm2":       buildMinimalTPF0Room(t, 1, "second"),
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("archive", "broken.zip")
	_, _ = fw.Write(zipData)
	_ = mw.Close()
	req, _ := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/map-sets/import", &body, s.apiToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["error"].(string), "архив") {
		t.Fatalf("localized error = %#v", out["error"])
	}
	if got := renderMapperWireText(appi18n.Russian, out["error_code"].(string), out["error_args"].(map[string]any)); got != out["error"] {
		t.Fatalf("import code + args reconstructed %q, want %q", got, out["error"])
	}
	detail, _ := out["error_detail"].(string)
	if detail == "" || detail == out["error"] {
		t.Fatalf("raw validation detail = %#v, localized error = %#v", detail, out["error"])
	}
	if conflicts, ok := out["conflicts"].([]any); !ok || len(conflicts) != 2 {
		t.Fatalf("validation conflicts = %#v", out["conflicts"])
	}
}

func TestRecordingContextKeysPreserveExactEnglishCompatibility(t *testing.T) {
	if got := renderMapperWireText(appi18n.English, string(appi18n.MapperRecordingSessionNotConnected), nil); got != "session not connected" {
		t.Fatalf("recording state disconnected = %q", got)
	}
	key, ok := mapperRecordingReasonKey("no active map set")
	if !ok || key != appi18n.MapperRecordingNoActiveMapSet {
		t.Fatalf("StartMapRecording no-set key = %q, ok=%v", key, ok)
	}
	if got := renderMapperWireText(appi18n.English, string(key), nil); got != "no active map set" {
		t.Fatalf("StartMapRecording no-set reason = %q", got)
	}
	key, ok = mapperRecordingReasonKey("room autorecord requires an editable map set")
	if !ok || key != appi18n.MapperRecordingMapSetNotEditable {
		t.Fatalf("StartMapRecording frozen-set key = %q, ok=%v", key, ok)
	}
	if got := renderMapperWireText(appi18n.English, string(key), nil); got != "room autorecord requires an editable map set" {
		t.Fatalf("StartMapRecording frozen-set reason = %q", got)
	}
	if got := renderMapperWireText(appi18n.English, string(appi18n.MapperProfileMissingSpec), nil); got != "missing spec" {
		t.Fatalf("profile missing spec = %q", got)
	}
}

func TestRecordingHTTPEnglishCompatibilityUsesContextSpecificReasons(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	if err := s.manager.Disconnect(sess.SessionID()); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	req, _ := newAuthenticatedRequest(http.MethodGet, ts.URL+"/api/sessions/"+strconv.FormatInt(sess.SessionID(), 10)+"/map-recording-state", nil, s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state["reason"] != "session not connected" || state["reason_code"] != string(appi18n.MapperRecordingSessionNotConnected) {
		t.Fatalf("recording disconnected response = %#v", state)
	}

	s2, sess2 := setupTestServer(t)
	ts2 := httptest.NewServer(s2.httpServer.Handler)
	defer ts2.Close()
	start := postJSON(t, ts2, s2.apiToken, sess2.SessionID(), "map-recording-start", `{}`)
	if start["reason"] != "no active map set" || start["reason_code"] != string(appi18n.MapperRecordingNoActiveMapSet) {
		t.Fatalf("recording no-set response = %#v", start)
	}
}

func TestMapperWalkHTTPReturnsRussianCodesForStartCancelAndResume(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	start := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-walk", `{"to":{"zone":"Z","x":1,"y":0,"l":0}}`)
	if start["reason_code"] != string(appi18n.MapperWalkNoGameTransport) || !strings.Contains(start["reason"].(string), "транспорта") {
		t.Fatalf("Russian walk start = %#v", start)
	}
	cancel := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-walk/cancel", `{}`)
	if cancel["reason_code"] != string(appi18n.MapperWalkCancelled) || cancel["reason"] != "остановлено пользователем" {
		t.Fatalf("Russian walk cancel = %#v", cancel)
	}
	resume := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-walk/resume", `{}`)
	if resume["reason_code"] != string(appi18n.MapperWalkNoPausedRoute) || !strings.Contains(resume["reason"].(string), "приостановленного") {
		t.Fatalf("Russian walk resume = %#v", resume)
	}
}

func TestMapperWalkWebSocketSerializationUsesCachedLocale(t *testing.T) {
	for _, test := range []struct {
		locale appi18n.Locale
		walk   string
		record string
	}{{appi18n.English, "cancelled by user", "stopped by user"}, {appi18n.Russian, "остановлено пользователем", "остановлено пользователем"}} {
		t.Run(string(test.locale), func(t *testing.T) {
			s, sess := setupTestServer(t)
			s.setMapperLanguage(test.locale)
			ts := httptest.NewServer(s.httpServer.Handler)
			defer ts.Close()

			wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + s.apiToken + "&session_id=" + strconv.FormatInt(sess.SessionID(), 10)
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("dial websocket: %v", err)
			}
			defer conn.Close()
			for {
				var msg session.ServerMsg
				if err := conn.ReadJSON(&msg); err != nil {
					t.Fatalf("read restore: %v", err)
				}
				if msg.Type == "restore_end" {
					break
				}
			}
			sess.BroadcastServerMsg(session.ServerMsg{Type: "map_walk", WalkReason: "cancelled by user", WalkState: "stopped"})
			var got session.ServerMsg
			if err := conn.ReadJSON(&got); err != nil {
				t.Fatalf("read localized map_walk: %v", err)
			}
			if got.Type != "map_walk" || got.WalkReason != test.walk || got.WalkReasonCode != string(appi18n.MapperWalkCancelled) {
				t.Fatalf("localized websocket message = %+v", got)
			}
			recording := session.MapRecordingState{Mode: session.MapRecordingOff, Reason: "stopped by user"}
			sess.BroadcastServerMsg(session.ServerMsg{Type: "map_recording_state", MapRecording: &recording})
			if err := conn.ReadJSON(&got); err != nil {
				t.Fatalf("read localized map_recording_state: %v", err)
			}
			if got.MapRecording == nil || got.MapRecording.Reason != test.record || got.MapRecording.ReasonCode != string(appi18n.MapperRecordingStoppedByUser) {
				t.Fatalf("localized recording websocket message = %+v", got)
			}
		})
	}
}

func TestRussianEdgeAndPortalValidationReasonsHaveStableReconstructableCodes(t *testing.T) {
	s, sess := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	weight := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-edge-attrs", `{"zone":"Z","x":0,"y":0,"l":0,"dir":"N","weight":0}`)
	if weight["reason_code"] != string(appi18n.MapperEdgeWeightPositive) || !strings.Contains(weight["reason"].(string), "положительным") {
		t.Fatalf("weight refusal = %#v", weight)
	}
	command := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-edge-attrs", `{"zone":"Z","x":0,"y":0,"l":0,"dir":"N","phrase":"n"}`)
	args := command["reason_args"].(map[string]any)
	if command["reason_code"] != string(appi18n.MapperEdgeMovementCommand) || renderMapperWireText(appi18n.Russian, command["reason_code"].(string), args) != command["reason"] {
		t.Fatalf("movement-command refusal = %#v", command)
	}
	portal := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-portals", `{`)
	if portal["reason_code"] != string(appi18n.MapperInvalidBody) || portal["reason_detail"] == "" || portal["reason"] != "неверное тело запроса" {
		t.Fatalf("portal invalid body = %#v", portal)
	}
}

func TestRussianMapperProfileCRUDAndReplayAuthoredErrors(t *testing.T) {
	s, _ := setupTestServer(t)
	s.setMapperLanguage(appi18n.Russian)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	request := func(method, path, body string) (*http.Response, string) {
		t.Helper()
		req, _ := newAuthenticatedRequest(method, ts.URL+path, strings.NewReader(body), s.apiToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var text bytes.Buffer
		_, _ = text.ReadFrom(resp.Body)
		return resp, text.String()
	}

	spec, _ := json.Marshal(englishSpecMap())
	resp, body := request(http.MethodPut, "/api/mapper-profiles/999999", `{"name":"x","spec":`+string(spec)+`}`)
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperProfileNotFound) || !strings.Contains(body, "не найден") {
		t.Fatalf("profile not found: status=%d header=%q body=%q", resp.StatusCode, resp.Header.Get("X-RubyMUD-Reason-Code"), body)
	}
	resp, body = request(http.MethodDelete, "/api/mapper-profiles/0", ``)
	if resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperProfileBuiltinReadOnly) || !strings.Contains(body, "только для чтения") {
		t.Fatalf("builtin readonly: header=%q body=%q", resp.Header.Get("X-RubyMUD-Reason-Code"), body)
	}
	resp, body = request(http.MethodPost, "/api/mapper-profiles/replay", `{"session_id":0}`)
	if resp.Header.Get("X-RubyMUD-Reason-Code") != string(appi18n.MapperProfileSessionNotFound) || !strings.Contains(body, "сессия") {
		t.Fatalf("replay missing session: header=%q body=%q", resp.Header.Get("X-RubyMUD-Reason-Code"), body)
	}
}

func TestPathResultJSONLocalizesVetoAndKeepsEnglishCompatibility(t *testing.T) {
	res := mapper.PathResult{Reachable: false, VetoedOnly: true, Reason: `a route exists only through edges marked "avoid in routes"`}
	ru := pathResultJSONLocale(res, appi18n.Russian)
	if ru["reason_code"] != string(appi18n.MapperRouteVetoedOnly) {
		t.Fatalf("Russian reason_code = %#v", ru["reason_code"])
	}
	if !strings.Contains(ru["reason"].(string), "не водить сюда") {
		t.Fatalf("Russian veto reason = %q", ru["reason"])
	}
	en := pathResultJSON(res)
	if en["reason"] != res.Reason {
		t.Fatalf("English compatibility reason = %q, want %q", en["reason"], res.Reason)
	}
}

func TestUnreachableRouteCodeAndArgsReconstructTextWithoutTranslatingDiagnostic(t *testing.T) {
	const diagnostic = "custom graph diagnostic {detail}"
	for _, locale := range []appi18n.Locale{appi18n.English, appi18n.Russian} {
		out := pathResultJSONLocale(mapper.PathResult{Reachable: false, Reason: diagnostic}, locale)
		args := out["reason_args"].(map[string]any)
		if got := renderMapperWireText(locale, out["reason_code"].(string), args); got != out["reason"] {
			t.Fatalf("%s reconstructed %q, want %q", locale, got, out["reason"])
		}
		if locale == appi18n.Russian && out["reason_detail"] != diagnostic {
			t.Fatalf("Russian raw diagnostic = %#v, want opaque %q", out["reason_detail"], diagnostic)
		}
	}
}

func TestLocalizedMapperReasonsCoverRecordingPortalEdgeUndoAndImport(t *testing.T) {
	tests := []struct {
		key  appi18n.Key
		args map[string]any
		want string
	}{
		{appi18n.MapperRecordPositionLost, nil, "позиция потеряна"},
		{appi18n.MapperPortalNotFound, nil, "портал"},
		{appi18n.MapperEdgePassageMissing, nil, "направленный проход"},
		{appi18n.MapperNothingToUndo, nil, "Нечего отменять"},
		{appi18n.MapperImportParseFailed, nil, "Не удалось разобрать"},
		{appi18n.MapperProfileMissingSpec, nil, "описание формата"},
	}
	for _, test := range tests {
		out := mapperReasonBody(appi18n.Russian, false, test.key, test.args)
		if !strings.Contains(out["reason"].(string), test.want) {
			t.Errorf("%s = %q, want substring %q", test.key, out["reason"], test.want)
		}
	}
}
