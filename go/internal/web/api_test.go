package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	sessionID := sess.SessionID()
	url := fmt.Sprintf("%s/api/sessions/%d/aliases", ts.URL, sessionID)

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
	db.AutoMigrate(&storage.AppSetting{}, &storage.SessionRecord{}, &storage.Variable{}, &storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{})

	store := storage.NewTestStore(db)
	
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

	s := New(":0", manager, store, nil)
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
