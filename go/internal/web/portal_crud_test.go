package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"rubymud/go/internal/storage"
)

func portalRequest(t *testing.T, ts *httptest.Server, token string, sid int64, method, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := newAuthenticatedRequest(method, fmt.Sprintf("%s/api/sessions/%d/map-portals", ts.URL, sid), bytes.NewBufferString(body), token)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func portalTestSet(t *testing.T, s *Server, sid int64) int64 {
	t.Helper()
	id, err := s.store.CreateMapSet(storage.MapSetInput{Name: "portal REST", RoomCount: 2, ZoneCount: 2, Rooms: []storage.Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Hint: "source", EDirs: "[]", Doors: "[]"},
		{Zone: "B", X: 0, Y: 0, L: 0, Hint: "target", EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sid, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRESTPortalCRUDReturnsLockedAuthoritativeSelector(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	original := portalTestSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	body := `{"zone":"A","x":0,"y":0,"l":0,"target":{"zone":"B","x":0,"y":0,"l":0},"command":"prepare;enter"}`
	resp, out := portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodPost, body)
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("create status=%d body=%#v", resp.StatusCode, out)
	}
	if out["forked_to"] == nil || out["active_map_set"] == nil {
		t.Fatalf("COW response missing set ids: %#v", out)
	}
	portal, ok := out["portal"].(map[string]any)
	if !ok {
		t.Fatalf("portal result missing: %#v", out)
	}
	selector := portal["selector"].(map[string]any)
	if selector["order"] != float64(0) || portal["command"] != "prepare;enter" {
		t.Fatalf("authoritative create selector=%#v", portal)
	}
	forkID := activeSetID(t, s, sess.SessionID())
	if forkID == original {
		t.Fatal("portal create did not fork")
	}

	resp, out = portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodPatch,
		`{"zone":"A","x":0,"y":0,"l":0,"order":0,"command":"enter revised","weight":0.5}`)
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("update=%d %#v", resp.StatusCode, out)
	}
	portal = out["portal"].(map[string]any)
	if portal["command"] != "enter revised" || portal["selector"].(map[string]any)["order"] != float64(0) {
		t.Fatalf("update result=%#v", portal)
	}

	resp, out = portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodDelete,
		`{"zone":"A","x":0,"y":0,"l":0,"order":0}`)
	if resp.StatusCode != http.StatusOK || out["ok"] != true || out["portal"].(map[string]any)["deleted"] != true {
		t.Fatalf("delete=%d %#v", resp.StatusCode, out)
	}
	undo := postJSON(t, ts, s.apiToken, sess.SessionID(), "map-undo", `{}`)
	if undo["applied"] != true {
		t.Fatalf("undo delete=%#v", undo)
	}
	edges, err := s.store.ListMapEdges(forkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) == 0 {
		t.Fatal("undo did not restore portal")
	}
}

func TestRESTPortalStrictBoundary(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	portalTestSet(t, s, sess.SessionID())
	sess.LoadActiveMapSet()
	url := fmt.Sprintf("%s/api/sessions/%d/map-portals", ts.URL, sess.SessionID())
	unauth, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(`{}`))
	unauth.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(unauth); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth resp=%v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	badCT, _ := newAuthenticatedRequest(http.MethodPost, url, bytes.NewBufferString(`{}`), s.apiToken)
	if resp, err := http.DefaultClient.Do(badCT); err != nil || resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("content type resp=%v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	origin, _ := newAuthenticatedRequest(http.MethodPost, url, bytes.NewBufferString(`{}`), s.apiToken)
	origin.Header.Set("Content-Type", "application/json")
	origin.Header.Set("Origin", "https://evil.example")
	if resp, err := http.DefaultClient.Do(origin); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("origin resp=%v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	for _, body := range []string{
		`{"zone":"A","x":0,"y":0,"l":0,"unknown":1}`,
		`{"zone":null,"x":0,"y":0,"l":0}`,
		`{"zone":"A","x":0,"y":0,"l":0,"target":{"zone":"B","x":0,"y":0},"command":"go"}`,
	} {
		resp, out := portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodPost, body)
		if resp.StatusCode != http.StatusOK || out["ok"] != false {
			t.Fatalf("strict body %s: %d %#v", body, resp.StatusCode, out)
		}
	}
	oversized, _ := newAuthenticatedRequest(http.MethodPost, url,
		bytes.NewBufferString(`{"zone":"`+strings.Repeat("x", 1<<20)+`"}`), s.apiToken)
	oversized.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(oversized); err != nil || resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized resp=%v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	unsupported, _ := newAuthenticatedRequest(http.MethodPut, url, bytes.NewBufferString(`{}`), s.apiToken)
	unsupported.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(unsupported); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method resp=%v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	// Zero is a valid coordinate when explicitly present; it must not be treated
	// as an omitted pointer field.
	resp, out := portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodPost, `{"zone":"A","x":0,"y":0,"l":0,"target":{"zone":"B","x":0,"y":0,"l":0},"command":"go"}`)
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("zero coords rejected: %d %#v", resp.StatusCode, out)
	}
	resp, out = portalRequest(t, ts, s.apiToken, sess.SessionID(), http.MethodDelete,
		`{"zone":"A","x":0,"y":0,"l":0,"order":0,"command":"ignored"}`)
	if resp.StatusCode != http.StatusOK || out["ok"] != false {
		t.Fatalf("delete accepted ignored update fields: %d %#v", resp.StatusCode, out)
	}
	if edges, err := s.store.ListMapEdges(activeSetID(t, s, sess.SessionID())); err != nil || len(edges) != 1 {
		t.Fatalf("refused delete mutated portal: edges=%+v err=%v", edges, err)
	}
}

func TestPortalConcurrentCreatesReturnTheirOwnLockedSelectors(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	portalTestSet(t, s, sess.SessionID())
	var wg sync.WaitGroup
	results := make(chan int, 2)
	errs := make(chan error, 2)
	for _, command := range []string{"same", "same"} {
		wg.Add(1)
		go func(command string) {
			defer wg.Done()
			res, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoCreatePortal, Zone: "A", X: 0, Y: 0, L: 0,
				Portal: &storage.PortalTarget{Zone: "B", X: 0, Y: 0, L: 0}, PortalCommand: command})
			if err != nil || !res.Applied || res.Portal == nil {
				errs <- fmt.Errorf("result=%+v err=%v", res, err)
				return
			}
			results <- res.Portal.Selector.Order
		}(command)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	orders := []int{}
	for order := range results {
		orders = append(orders, order)
	}
	sort.Ints(orders)
	if len(orders) != 2 || orders[0] != 0 || orders[1] != 1 {
		t.Fatalf("concurrent orders=%v", orders)
	}
}

// A writer may read the frozen source id, then wait while another writer forks
// and switches the session. Once it wakes it must restart on the fork lock; the
// source lock no longer serializes it against requests that observed the fork.
func TestTopologyWriterThatWaitedOnSourceReacquiresForkLock(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sourceID := portalTestSet(t, s, sess.SessionID())
	sourceLock := s.store.TopologyLock(sourceID)
	sourceLock.Lock()

	type writeResult struct {
		setID int64
		order int
		err   error
	}
	started := make(chan struct{})
	done := make(chan writeResult, 1)
	go func() {
		close(started)
		res, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoCreatePortal,
			Zone: "A", X: 0, Y: 0, L: 0,
			Portal: &storage.PortalTarget{Zone: "B", X: 0, Y: 0, L: 0}, PortalCommand: "stale-entry",
		})
		order := -1
		if res.Portal != nil {
			order = res.Portal.Selector.Order
		}
		done <- writeResult{setID: res.SetID, order: order, err: err}
	}()
	<-started
	// Give the goroutine time to read sourceID and block on sourceLock.
	time.Sleep(25 * time.Millisecond)

	forkID, err := s.store.ForkMapSet(sourceID)
	if err != nil {
		sourceLock.Unlock()
		t.Fatal(err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), forkID); err != nil {
		sourceLock.Unlock()
		t.Fatal(err)
	}
	forkLock := s.store.TopologyLock(forkID)
	forkLock.Lock()
	sourceLock.Unlock()

	select {
	case got := <-done:
		forkLock.Unlock()
		t.Fatalf("writer bypassed fork lock: result=%+v", got)
	case <-time.After(100 * time.Millisecond):
		// Correct: the stale-entry writer retried and is waiting on forkLock.
	}
	forkLock.Unlock()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("write after fork unlock: %v", got.err)
		}
		if got.setID != forkID || got.order != 0 {
			t.Fatalf("selector does not belong to fork %d: %+v", forkID, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not finish after fork lock was released")
	}
}
