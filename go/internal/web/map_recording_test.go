package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appi18n "rubymud/go/internal/i18n"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

func TestMapRecordingRESTStrictStartStateStop(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()
	frozen := seedTrackerSet(t, s, sid)
	if err := s.store.DB().Model(&storage.MapSet{}).Where("id=?", frozen).Update("editable", false).Error; err != nil {
		t.Fatal(err)
	}

	post := func(path, body string) (int, map[string]any) {
		req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/%s", ts.URL, sid, path), strings.NewReader(body), s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	// Continuous autorecord is intentionally refused on the imported/frozen
	// active set, with one stable reason code in both REST locales.
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z"}); !ok || !exact {
		t.Fatal("frozen-set anchor failed")
	}
	setLiveRoom(t, sess, "Первая", "one", "S")
	if code, out := post("map-recording-start", `{}`); code != http.StatusOK || out["reason_code"] != "mapper.recording.map_set_not_editable" || out["reason"] != "room autorecord requires an editable map set" {
		t.Fatalf("frozen-set English refusal code=%d out=%#v", code, out)
	}
	s.setMapperLanguage(appi18n.Russian)
	if code, out := post("map-recording-start", `{}`); code != http.StatusOK || out["reason_code"] != "mapper.recording.map_set_not_editable" || !strings.Contains(out["reason"].(string), "редактируемого") {
		t.Fatalf("frozen-set Russian refusal code=%d out=%#v", code, out)
	}
	s.setMapperLanguage(appi18n.English)

	editable, err := s.store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sid, editable); err != nil {
		t.Fatal(err)
	}
	sess.LoadActiveMapSet()
	if ok, exact := sess.AnchorMapPosition(mapper.Coord{Zone: "Z"}); !ok || !exact {
		t.Fatal("anchor failed")
	}
	setLiveRoom(t, sess, "Первая", "one", "S")

	if code, _ := post("map-recording-start", `{"unknown":1}`); code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", code)
	}
	if code, _ := post("map-recording-start", `{} {}`); code != http.StatusBadRequest {
		t.Fatalf("trailing status=%d", code)
	}
	if code, out := post("map-recording-start", `{}`); code != 200 || out["started"] != true {
		t.Fatalf("start code=%d out=%#v", code, out)
	}

	req, _ := newAuthenticatedRequest(http.MethodGet, fmt.Sprintf("%s/api/sessions/%d/map-recording-state", ts.URL, sid), nil, s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&state)
	if state["mode"] != "armed" {
		t.Fatalf("state=%#v", state)
	}
	if code, out := post("map-recording-stop", `{}`); code != 200 || out["stopped"] != true {
		t.Fatalf("stop code=%d out=%#v", code, out)
	}
}

func TestMapReconciliationRESTIsStrictAndRequiresCurrentConflict(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	post := func(path, body string) int {
		req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/%s", ts.URL, sess.SessionID(), path), strings.NewReader(body), s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("map-reconciliation/preview", `{"conflict_id":1,"target":{"zone":"Z","x":0,"y":0,"l":0},"layouts":[],"unknown":1}`); code != 400 {
		t.Fatalf("unknown preview field=%d", code)
	}
	if code := post("map-reconciliation/apply", `{"conflict_id":1,"token":"x","extra":1}`); code != 400 {
		t.Fatalf("unknown apply field=%d", code)
	}
	if code := post("map-reconciliation/preview", `{"conflict_id":1,"target":{"zone":"Z","x":0,"y":0,"l":0},"layouts":[]}`); code != 409 {
		t.Fatalf("no-conflict preview=%d", code)
	}
	if code := post("map-reconciliation/apply", `{"conflict_id":1,"token":"x"}`); code != 409 {
		t.Fatalf("no-conflict apply=%d", code)
	}
}

func TestMapRecordingStopReturnsExplicitConflictWhileApplyReserved(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	s.mapRecordingTryStop = func(*session.Session) (session.MapRecordingState, bool) {
		return session.MapRecordingState{Mode: session.MapRecordingConflict, Revision: 8}, false
	}
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/sessions/%d/map-recording-stop", ts.URL, sess.SessionID()), strings.NewReader(`{}`), s.apiToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 409 || body["stopped"] != false || body["reason"] != "reconciliation apply is in progress" {
		t.Fatalf("status=%d body=%#v", resp.StatusCode, body)
	}
}
