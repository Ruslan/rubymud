package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"unsafe"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestVariablesAPI(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	sessionID := sess.SessionID()

	// 1. Create variable
	payload, _ := json.Marshal(map[string]string{
		"key":   "test_var",
		"value": "test_val",
	})
	url := fmt.Sprintf("%s/api/sessions/%d/variables", ts.URL, sessionID)
	req, err := newAuthenticatedRequest(http.MethodPost, url, bytes.NewBuffer(payload), s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(POST %s): %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s status = %d, want 204", url, resp.StatusCode)
	}

	// 2. List variables
	req, err = newAuthenticatedRequest(http.MethodGet, url, nil, s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(GET %s): %v", url, err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	var vars []session.VariableJSON
	json.NewDecoder(resp.Body).Decode(&vars)

	found := false
	for _, v := range vars {
		if v.Key == "test_var" && v.Value == "test_val" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("variable not found in list: %v", vars)
	}

	// 3. Verify VM reloaded
	if v := sess.Variables()["test_var"]; v != "test_val" {
		t.Fatalf("VM variable = %q, want %q", v, "test_val")
	}
}

func TestAliasesAPI(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	url := fmt.Sprintf("%s/api/profiles/1/aliases", ts.URL)

	// 1. Create alias
	payload, _ := json.Marshal(map[string]any{
		"name":     "gre",
		"template": "get all from corpse",
		"enabled":  true,
	})
	req, err := newAuthenticatedRequest(http.MethodPost, url, bytes.NewBuffer(payload), s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(POST %s): %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s status = %d, want 201", url, resp.StatusCode)
	}

	// 2. List aliases to get ID
	req, err = newAuthenticatedRequest(http.MethodGet, url, nil, s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(GET %s): %v", url, err)
	}
	resp, err = http.DefaultClient.Do(req)
	var aliases []storage.AliasRule
	json.NewDecoder(resp.Body).Decode(&aliases)
	if len(aliases) == 0 {
		t.Fatal("no aliases returned")
	}
	aliasID := aliases[0].ID

	// 3. Update alias
	payload, _ = json.Marshal(map[string]any{
		"name":     "gre",
		"template": "get all",
		"enabled":  false,
	})
	req, err = newAuthenticatedRequest(http.MethodPut, fmt.Sprintf("%s/%d", url, aliasID), bytes.NewBuffer(payload), s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(PUT %s/%d): %v", url, aliasID, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s/%d: %v", url, aliasID, err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", resp.StatusCode)
	}

	// 4. Delete alias
	req, err = newAuthenticatedRequest(http.MethodDelete, fmt.Sprintf("%s/%d", url, aliasID), nil, s.apiToken)
	if err != nil {
		t.Fatalf("newAuthenticatedRequest(DELETE %s/%d): %v", url, aliasID, err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s/%d: %v", url, aliasID, err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode)
	}
}

func setupTestServer(t *testing.T) (*Server, *session.Session) {
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	db.AutoMigrate(&storage.AppSetting{}, &storage.SessionRecord{}, &storage.Variable{}, &storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{}, &storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.ProfileVariable{})

	store := storage.NewTestStore(db)
	store.CreateProfile("Default", "")
	
	// Ensure a session exists
	record, err := store.EnsureDefaultSession("localhost", 1234)
	if err != nil {
		t.Fatalf("EnsureDefaultSession: %v", err)
	}

	manager := session.NewManager(store)
	v := vm.New(store, record.ID)

	// Mock session (without real TCP connection)
	sess := &session.Session{}
	setUnexportedField(t, sess, "sessionID", record.ID)
	setUnexportedField(t, sess, "store", store)
	setUnexportedField(t, sess, "vm", v)

	clientsField := reflect.ValueOf(sess).Elem().FieldByName("clients")
	clientsMap := reflect.MakeMap(clientsField.Type())
	setUnexportedField(t, sess, "clients", clientsMap.Interface())

	// Manually inject into manager's private map
	managerMapField := reflect.ValueOf(manager).Elem().FieldByName("sessions")
	reflect.NewAt(managerMapField.Type(), unsafe.Pointer(managerMapField.UnsafeAddr())).Elem().Set(reflect.ValueOf(map[int64]*session.Session{
		record.ID: sess,
	}))

	s := New(":0", manager, store, t.TempDir())
	return s, sess
}

func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %s not found in %T", fieldName, target)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func newAuthenticatedRequest(method, url string, body io.Reader, token string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Session-Token", token)
	return req, nil
}

func TestProfileFileEndpoints(t *testing.T) {
	s, _ := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	profileID := int64(1) // "Default" profile created in setupTestServer
	tok := s.apiToken

	// 1. Add an alias so the export file has content to round-trip
	payload, _ := json.Marshal(map[string]any{"name": "n", "template": "north", "enabled": true})
	req, _ := newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/profiles/%d/aliases", ts.URL, profileID), bytes.NewBuffer(payload), tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create alias: status=%d err=%v", resp.StatusCode, err)
	}

	// 2. Export single profile → file written to configDir
	req, _ = newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/profiles/%d/export", ts.URL, profileID), nil, tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST export status = %d, want 200", resp.StatusCode)
	}
	var exportResult map[string]string
	json.NewDecoder(resp.Body).Decode(&exportResult)
	filename := exportResult["filename"]
	if filename == "" {
		t.Fatal("export response missing filename")
	}

	// Verify file exists on disk
	if _, err := os.Stat(s.configDir + "/" + filename); err != nil {
		t.Fatalf("exported file not on disk: %v", err)
	}

	// 3. List files → should include the exported file
	req, _ = newAuthenticatedRequest(http.MethodGet, fmt.Sprintf("%s/api/profiles/files", ts.URL), nil, tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET files: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET files status = %d, want 200", resp.StatusCode)
	}
	var files []map[string]string
	json.NewDecoder(resp.Body).Decode(&files)
	found := false
	for _, f := range files {
		if f["filename"] == filename {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("exported file %q not in /api/profiles/files listing: %v", filename, files)
	}

	// 4. Import from file → creates a new profile with the same rules
	payload, _ = json.Marshal(map[string]string{"filename": filename})
	req, _ = newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/profiles/import", ts.URL), bytes.NewBuffer(payload), tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST import status = %d, want 204", resp.StatusCode)
	}

	// Verify a new profile exists
	req, _ = newAuthenticatedRequest(http.MethodGet, fmt.Sprintf("%s/api/profiles", ts.URL), nil, tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET profiles: %v", err)
	}
	var profiles []storage.Profile
	json.NewDecoder(resp.Body).Decode(&profiles)
	if len(profiles) < 2 {
		t.Fatalf("expected at least 2 profiles after import, got %d", len(profiles))
	}

	// 5. Export all profiles → one file per profile
	req, _ = newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/profiles/export/all", ts.URL), nil, tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST export/all: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST export/all status = %d, want 200", resp.StatusCode)
	}
	var allFiles []string
	json.NewDecoder(resp.Body).Decode(&allFiles)
	if len(allFiles) < 2 {
		t.Fatalf("expected at least 2 filenames from export/all, got %d: %v", len(allFiles), allFiles)
	}

	// 6. Import all → idempotent (profiles with same name get recreated)
	req, _ = newAuthenticatedRequest(http.MethodPost, fmt.Sprintf("%s/api/profiles/import/all", ts.URL), nil, tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST import/all: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST import/all status = %d, want 204", resp.StatusCode)
	}
}
