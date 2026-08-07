package web

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

type failReadCloser struct{ read bool }

func (r *failReadCloser) Read([]byte) (int, error) {
	r.read = true
	return 0, errors.New("body should not be read")
}

func (r *failReadCloser) Close() error { return nil }

// countedZeroReadCloser produces an unbounded-looking upload without a
// Content-Length. The multipart prefix opens a file part; the zero payload is
// intentionally never terminated, so ParseMultipartForm must consume through
// MaxBytesReader rather than reject based on request metadata.
type countedZeroReadCloser struct {
	remaining int64
	read      int64
}

func (r *countedZeroReadCloser) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 0
	}
	r.remaining -= int64(len(p))
	r.read += int64(len(p))
	return len(p), nil
}

func (r *countedZeroReadCloser) Close() error { return nil }

func TestRESTImportRejectsOversizedRequestBeforeMultipartParsing(t *testing.T) {
	s, _ := setupTestServer(t)
	body := &failReadCloser{}
	req := httptest.NewRequest(http.MethodPost, "/api/map-sets/import", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Header.Set("X-Session-Token", s.apiToken)
	req.ContentLength = maxUploadRequestBytes + 1
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if body.read {
		t.Fatal("oversized body was read")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestRESTImportRejectsOversizedChunkedRequest(t *testing.T) {
	s, _ := setupTestServer(t)
	prefix := []byte("--x\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"world.zip\"\r\nContent-Type: application/zip\r\n\r\n")
	body := &countedZeroReadCloser{remaining: maxUploadRequestBytes + 1}
	req := httptest.NewRequest(http.MethodPost, "/api/map-sets/import", io.NopCloser(io.MultiReader(bytes.NewReader(prefix), body)))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Header.Set("X-Session-Token", s.apiToken)
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	if body.read == 0 {
		t.Fatal("chunked request was rejected before MaxBytesReader read its body")
	}
	var count int64
	if err := s.store.DB().Model(&storage.MapSet{}).Count(&count).Error; err != nil {
		t.Fatalf("count map sets: %v", err)
	}
	if count != 0 {
		t.Fatalf("map sets after rejected chunked upload = %d, want 0", count)
	}
}

func TestRESTDeleteMapSetClearsLiveSessionAndImageDirectory(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	setID, err := s.store.CreateMapSet(storage.MapSetInput{
		Name:  "to delete",
		Rooms: []storage.Room{{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), setID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()
	sess.WithMapTracker(func(tracker *mapper.Tracker) {
		if tracker.Index() == nil {
			t.Fatal("live tracker has no index before delete")
		}
	})

	var notices []session.ServerMsg
	sess.AttachClient("test", func(msg session.ServerMsg) error {
		notices = append(notices, msg)
		return nil
	})
	s.roomImagesRoot = filepath.Join(t.TempDir(), "room_images")
	setDir := filepath.Join(s.roomImagesRoot, strconv.FormatInt(setID, 10))
	if err := os.MkdirAll(setDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(setDir, "image.png"), []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sibling := filepath.Join(s.roomImagesRoot, strconv.FormatInt(setID, 10)+"0")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("MkdirAll sibling: %v", err)
	}

	req, err := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/"+strconv.FormatInt(setID, 10), nil, s.apiToken)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", resp.StatusCode)
	}

	if _, err := s.store.GetMapSet(setID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetMapSet after delete = %v, want record not found", err)
	}
	if _, ok, err := s.store.GetActiveMapSetID(sess.SessionID()); err != nil || ok {
		t.Fatalf("active map set after delete = ok:%v err:%v, want cleared", ok, err)
	}
	sess.WithMapTracker(func(tracker *mapper.Tracker) {
		if tracker.Index() != nil {
			t.Fatal("live tracker index survived deleted active set")
		}
	})
	if _, err := os.Stat(setDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("set image dir stat = %v, want absent", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling image dir was affected: %v", err)
	}
	changed := false
	for _, notice := range notices {
		if notice.Type == "map_sets_changed" {
			changed = true
		}
	}
	if !changed {
		t.Fatalf("notifications = %+v, want map_sets_changed", notices)
	}
}

func TestMapSetImageTombstoneRecoveryRestoresUncommittedAndRemovesCommitted(t *testing.T) {
	s, _ := setupTestServer(t)
	s.roomImagesRoot = filepath.Join(t.TempDir(), "room_images")
	if err := os.MkdirAll(s.roomImagesRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	liveID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "live"})
	if err != nil {
		t.Fatalf("CreateMapSet live: %v", err)
	}
	liveTombstone := filepath.Join(s.roomImagesRoot, ".deleting-"+strconv.FormatInt(liveID, 10)+"-crash")
	if err := os.MkdirAll(liveTombstone, 0o755); err != nil {
		t.Fatalf("MkdirAll live tombstone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(liveTombstone, "image.png"), []byte("live"), 0o644); err != nil {
		t.Fatalf("WriteFile live tombstone: %v", err)
	}

	deadID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "dead"})
	if err != nil {
		t.Fatalf("CreateMapSet dead: %v", err)
	}
	if err := s.store.DeleteMapSet(deadID); err != nil {
		t.Fatalf("DeleteMapSet dead: %v", err)
	}
	deadTombstone := filepath.Join(s.roomImagesRoot, ".deleting-"+strconv.FormatInt(deadID, 10)+"-crash")
	if err := os.MkdirAll(deadTombstone, 0o755); err != nil {
		t.Fatalf("MkdirAll dead tombstone: %v", err)
	}

	if err := s.recoverMapSetImageTombstones(); err != nil {
		t.Fatalf("recoverMapSetImageTombstones: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.roomImagesRoot, strconv.FormatInt(liveID, 10), "image.png")); err != nil {
		t.Fatalf("live image was not restored: %v", err)
	}
	if _, err := os.Stat(liveTombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live tombstone stat = %v, want absent", err)
	}
	if _, err := os.Stat(deadTombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead tombstone stat = %v, want absent", err)
	}
}

func TestTombstoneMapSetIDRequiresCanonicalPositiveID(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int64
		ok   bool
	}{
		{name: "canonical", want: 12, ok: true},
		{name: "leading zero", want: 0, ok: false},
		{name: "zero", want: 0, ok: false},
		{name: "negative", want: 0, ok: false},
	} {
		name := ".deleting-12-crash"
		switch tc.name {
		case "leading zero":
			name = ".deleting-012-crash"
		case "zero":
			name = ".deleting-0-crash"
		case "negative":
			name = ".deleting--12-crash"
		}
		got, ok := tombstoneMapSetID(name)
		if got != tc.want || ok != tc.ok {
			t.Errorf("tombstoneMapSetID(%q) = (%d, %v), want (%d, %v)", name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestServerNewRecoversMapSetImageTombstones(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keepSet bool
		wantDir bool
	}{
		{name: "rollback restores", keepSet: true, wantDir: true},
		{name: "committed delete removes", keepSet: false, wantDir: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "mudhost.db")
			store, err := storage.Open(dbPath)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			id, err := store.CreateMapSet(storage.MapSetInput{Name: "recover"})
			if err != nil {
				t.Fatalf("CreateMapSet: %v", err)
			}
			if !tc.keepSet {
				if err := store.DeleteMapSet(id); err != nil {
					t.Fatalf("DeleteMapSet: %v", err)
				}
			}
			root := store.RoomImagesRoot()
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			tombstone := filepath.Join(root, ".deleting-"+strconv.FormatInt(id, 10)+"-crash")
			if err := os.MkdirAll(tombstone, 0o755); err != nil {
				t.Fatalf("MkdirAll tombstone: %v", err)
			}
			server, err := New(":0", session.NewManager(store), store, t.TempDir(), BasicAuth{}, MCPConfig{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() { _ = server.Close() })
			original := filepath.Join(root, strconv.FormatInt(id, 10))
			if tc.wantDir {
				if _, err := os.Stat(original); err != nil {
					t.Fatalf("restored dir: %v", err)
				}
			} else if _, err := os.Stat(tombstone); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("committed tombstone stat = %v, want removed", err)
			}
		})
	}
}

func TestMapSetImageCleanupRejectsSymlinkRoot(t *testing.T) {
	s, _ := setupTestServer(t)
	realRoot := filepath.Join(t.TempDir(), "real-images")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkRoot := filepath.Join(t.TempDir(), "room_images")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	s.roomImagesRoot = linkRoot
	if _, err := s.stageMapSetImageDir(1); err == nil {
		t.Fatal("stageMapSetImageDir accepted a symlink root")
	}
}

func TestRESTDeleteMapSetSerializesConcurrentRequests(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	id, err := s.store.CreateMapSet(storage.MapSetInput{Name: "race"})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	s.roomImagesRoot = filepath.Join(t.TempDir(), "room_images")
	if err := os.MkdirAll(filepath.Join(s.roomImagesRoot, strconv.FormatInt(id, 10)), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/"+strconv.FormatInt(id, 10), nil, s.apiToken)
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("DELETE: %v", err)
				return
			}
			statuses <- resp.StatusCode
			resp.Body.Close()
		}()
	}
	wg.Wait()
	close(statuses)
	got := map[int]int{}
	for status := range statuses {
		got[status]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusNotFound] != 1 {
		t.Fatalf("statuses = %v, want one 200 and one 404", got)
	}
}

func TestRESTDeleteMapSetRequiresDeletingForkBeforeSourceImages(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	s.roomImagesRoot = filepath.Join(t.TempDir(), "room_images")
	sourceID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "source", Rooms: []storage.Room{{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}}})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	var sourceRoom storage.Room
	if err := s.store.DB().Where("map_set_id = ?", sourceID).Take(&sourceRoom).Error; err != nil {
		t.Fatalf("source room: %v", err)
	}
	imagePath := filepath.Join(s.roomImagesRoot, strconv.FormatInt(sourceID, 10), "vibe.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(imagePath, []byte("fork-safe"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := s.store.DB().Create(&storage.RoomImage{RoomID: sourceRoom.ID, FullPath: imagePath}).Error; err != nil {
		t.Fatalf("create image: %v", err)
	}
	forkID, err := s.store.ForkMapSet(sourceID)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}

	// Source and fork share FullPath, so source deletion is an explicit conflict
	// rather than a filesystem ownership inference.
	req, err := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/"+strconv.FormatInt(sourceID, 10), nil, s.apiToken)
	if err != nil {
		t.Fatalf("source delete request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("source DELETE: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "delete editable forks first") {
		t.Fatalf("source DELETE = %d %s, want structured 409 fork conflict", resp.StatusCode, body)
	}
	if _, err := s.store.GetMapSet(sourceID); err != nil {
		t.Fatalf("source map set after conflict: %v", err)
	}
	if _, err := s.store.GetMapSet(forkID); err != nil {
		t.Fatalf("fork map set after conflict: %v", err)
	}
	if got, err := os.ReadFile(imagePath); err != nil || string(got) != "fork-safe" {
		t.Fatalf("source image after conflict = %q, %v", got, err)
	}
	var forkRoom storage.Room
	if err := s.store.DB().Where("map_set_id = ?", forkID).Take(&forkRoom).Error; err != nil {
		t.Fatalf("fork room: %v", err)
	}
	var forkImage storage.RoomImage
	if err := s.store.DB().Where("room_id = ?", forkRoom.ID).Take(&forkImage).Error; err != nil {
		t.Fatalf("fork image: %v", err)
	}
	if forkImage.FullPath != imagePath {
		t.Fatalf("fork FullPath = %q, want %q", forkImage.FullPath, imagePath)
	}
	if got, err := os.ReadFile(forkImage.FullPath); err != nil || string(got) != "fork-safe" {
		t.Fatalf("fork image after source conflict = %q, %v", got, err)
	}

	// A fork has its own empty directory, so deleting it cannot touch the shared
	// source image directory. Once it is gone, source cleanup owns that directory.
	deleteMapSetRequest(t, ts.URL, s.apiToken, forkID, http.StatusOK)
	if got, err := os.ReadFile(imagePath); err != nil || string(got) != "fork-safe" {
		t.Fatalf("source image after fork delete = %q, %v", got, err)
	}
	deleteMapSetRequest(t, ts.URL, s.apiToken, sourceID, http.StatusOK)
	if _, err := os.Stat(filepath.Dir(imagePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source image dir after source delete = %v, want absent", err)
	}
}

func TestRESTDeleteMapSetForkConflictPreflightSkipsImageStaging(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	realRoot := filepath.Join(t.TempDir(), "real-images")
	sourceID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "source"})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, err := s.store.ForkMapSet(sourceID); err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	sourceDir := filepath.Join(realRoot, strconv.FormatInt(sourceID, 10))
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll source image dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "image.png"), []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	linkRoot := filepath.Join(t.TempDir(), "room_images")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// Staging rejects symlink roots. A 409 here proves preflight answers before
	// it tries to touch or rename room_images/<sourceID>.
	s.roomImagesRoot = linkRoot
	deleteMapSetRequest(t, ts.URL, s.apiToken, sourceID, http.StatusConflict)
	if got, err := os.ReadFile(filepath.Join(sourceDir, "image.png")); err != nil || string(got) != "source" {
		t.Fatalf("source image after fork conflict = %q, %v", got, err)
	}
}

func TestRESTDeleteMapSetRollbackFailureReturns500(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	s.roomImagesRoot = filepath.Join(t.TempDir(), "room_images")
	sourceID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "source"})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	sourceDir := filepath.Join(s.roomImagesRoot, strconv.FormatInt(sourceID, 10))
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "image.png"), []byte("source"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var hookErr error
	s.mapSetDeleteBeforeStoreDelete = func() {
		_, hookErr = s.store.ForkMapSet(sourceID)
		if hookErr == nil {
			hookErr = os.Mkdir(sourceDir, 0o755)
		}
	}

	req, err := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/"+strconv.FormatInt(sourceID, 10), nil, s.apiToken)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if hookErr != nil {
		t.Fatalf("test hook: %v", hookErr)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("DELETE status = %d body=%s, want 500", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "delete editable forks first") {
		t.Fatalf("rollback failure was reported as 409: %s", body)
	}
	if _, err := s.store.GetMapSet(sourceID); err != nil {
		t.Fatalf("source map set after failed rollback: %v", err)
	}
}

func TestRESTDeleteMapSetSerializesAgainstCOWWithoutOrphanFork(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sourceID, err := s.store.CreateMapSet(storage.MapSetInput{Name: "frozen", Rooms: []storage.Room{{Zone: "A", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"}}})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sess.SessionID(), sourceID); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	s.mapSetDeleteAfterLock = func() { close(locked); <-release }

	deleteDone := make(chan int, 1)
	go func() {
		req, _ := newAuthenticatedRequest(http.MethodDelete, ts.URL+"/api/map-sets/"+strconv.FormatInt(sourceID, 10), nil, s.apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			deleteDone <- 0
			return
		}
		deleteDone <- resp.StatusCode
		resp.Body.Close()
	}()
	<-locked
	lock := s.store.TopologyLock(sourceID)
	if lock.TryLock() {
		lock.Unlock()
		t.Fatal("topology lock acquired while delete handler held it")
	}
	close(release)
	if status := <-deleteDone; status != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", status)
	}
	if !lock.TryLock() {
		t.Fatal("topology lock still held after delete response")
	}
	lock.Unlock()
	if _, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoPatchExits, Zone: "A", X: 0, Y: 0, L: 0, AddExits: []string{"N"}}); !errors.Is(err, session.ErrNoActiveMapSet) {
		t.Fatalf("COW after delete = %v, want ErrNoActiveMapSet", err)
	}
	var count int64
	if err := s.store.DB().Model(&storage.MapSet{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("map set count = %d, want no source or orphan fork", count)
	}
}

func deleteMapSetRequest(t *testing.T, baseURL, token string, id int64, want int) {
	t.Helper()
	req, err := newAuthenticatedRequest(http.MethodDelete, baseURL+"/api/map-sets/"+strconv.FormatInt(id, 10), nil, token)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("DELETE status = %d, want %d", resp.StatusCode, want)
	}
}

func TestRESTImportRejectsNonZipFilename(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("archive", "world.rar")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := file.Write(buildTestZip(t, map[string][]byte{"zone.mm2": buildMinimalTPF0Room(t, 1, "room")})); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := newAuthenticatedRequest(http.MethodPost, ts.URL+"/api/map-sets/import", &body, s.apiToken)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRESTDeleteMapSetRejectsMissingAndInvalidIDs(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/api/map-sets/not-a-number", want: http.StatusBadRequest},
		{path: "/api/map-sets/999", want: http.StatusNotFound},
	} {
		req, err := newAuthenticatedRequest(http.MethodDelete, ts.URL+tc.path, nil, s.apiToken)
		if err != nil {
			t.Fatalf("request %s: %v", tc.path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("DELETE %s status = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
		if got := resp.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("DELETE %s Content-Type = %q, want application/json", tc.path, got)
		}
	}
}
