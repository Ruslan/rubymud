package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
	"strings"
	"testing"
	"unsafe"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type mockConn struct {
	net.Conn
	buf bytes.Buffer
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.buf.Write(b)
}

func (m *mockConn) Close() error { return nil }

func setupMcpTestServer(t *testing.T) (*Server, *session.Session) {
	t.Helper()

	dbName := uuid.New().String()
	dsn := "file:" + dbName + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	db.AutoMigrate(&storage.AppSetting{}, &storage.SessionRecord{}, &storage.Variable{}, &storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{}, &storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.ProfileVariable{}, &storage.LogRecord{}, &storage.LogOverlay{}, &storage.HistoryEntry{})

	store := storage.NewTestStore(db)
	store.CreateProfile("Default", "")
	record, _ := store.EnsureDefaultSession("localhost", 1234)

	manager := session.NewManager(store)
	v := vm.New(store, record.ID)

	// Mock session (without real TCP connection)
	sess := &session.Session{}
	setMcpUnexportedField(t, sess, "sessionID", record.ID)
	setMcpUnexportedField(t, sess, "store", store)
	setMcpUnexportedField(t, sess, "vm", v)
	setMcpUnexportedField(t, sess, "conn", &mockConn{})

	clientsField := reflect.ValueOf(sess).Elem().FieldByName("clients")
	clientsMap := reflect.MakeMap(clientsField.Type())
	setMcpUnexportedField(t, sess, "clients", clientsMap.Interface())

	// Manually inject into manager's private map
	managerMapField := reflect.ValueOf(manager).Elem().FieldByName("sessions")
	reflect.NewAt(managerMapField.Type(), unsafe.Pointer(managerMapField.UnsafeAddr())).Elem().Set(reflect.ValueOf(map[int64]*session.Session{
		record.ID: sess,
	}))

	s := New(":0", manager, store, os.TempDir())
	return s, sess
}

func setMcpUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	if !v.IsValid() {
		t.Fatalf("field %s not found in %T", fieldName, target)
	}
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func TestMCPInitialize(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	reqBody := `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}`
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatalf("POST initialize: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var res jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	result := res.Result.(map[string]any)
	if result["serverInfo"].(map[string]any)["name"] != "mudhost-mcp" {
		t.Errorf("unexpected server name: %v", result["serverInfo"])
	}
}

func TestMCPToolsList(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	reqBody := `{"jsonrpc":"2.0","method":"tools/list","id":2}`
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatalf("POST tools/list: %v", err)
	}
	defer resp.Body.Close()

	var res jsonRPCResponse
	json.NewDecoder(resp.Body).Decode(&res)
	result := res.Result.(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) < 4 {
		t.Errorf("expected at least 4 tools, got %d", len(tools))
	}
}

func TestMCPGetOutput(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	// Add some logs
	s.store.AppendLogEntry(sess.SessionID(), "Hello world", "Hello world")
	s.store.AppendCommandHintToLatestLogEntry(sess.SessionID(), "look")

	reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":3,"params":{"name":"mud_get_output","arguments":{"session_id":%d}}}`, sess.SessionID())
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatalf("POST tools/call: %v", err)
	}
	defer resp.Body.Close()

	var res jsonRPCResponse
	json.NewDecoder(resp.Body).Decode(&res)
	result := res.Result.(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	text := content["text"].(string)

	if !strings.Contains(text, "Hello world") {
		t.Errorf("output missing log text: %q", text)
	}
	if !strings.Contains(text, "> look") {
		t.Errorf("output missing command hint: %q", text)
	}
}

func TestMCPSendCommand(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":4,"params":{"name":"mud_send_command","arguments":{"session_id":%d,"command":"test cmd"}}}`, sess.SessionID())
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatalf("POST tools/call: %v", err)
	}
	defer resp.Body.Close()

	var res jsonRPCResponse
	json.NewDecoder(resp.Body).Decode(&res)
	result := res.Result.(map[string]any)
	isError := result["isError"].(bool)
	if isError {
		t.Errorf("expected no error, got error result: %v", result)
	}
}

func TestMCPSearch(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	// Add some logs
	s.store.AppendLogEntry(sess.SessionID(), "First line", "First line")
	s.store.AppendLogEntry(sess.SessionID(), "Matching target here", "Matching target here")
	s.store.AppendLogEntry(sess.SessionID(), "Last line", "Last line")

	reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":5,"params":{"name":"mud_search","arguments":{"session_id":%d,"query":"target","context":1}}}`, sess.SessionID())
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatalf("POST tools/call: %v", err)
	}
	defer resp.Body.Close()

	var res jsonRPCResponse
	json.NewDecoder(resp.Body).Decode(&res)
	result := res.Result.(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	text := content["text"].(string)

	if !strings.Contains(text, "First line") {
		t.Errorf("search result missing context before: %q", text)
	}
	if !strings.Contains(text, "*** [#") {
		t.Errorf("search result missing match prefix: %q", text)
	}
	if !strings.Contains(text, "Matching target here") {
		t.Errorf("search result missing match: %q", text)
	}
	if !strings.Contains(text, "Last line") {
		t.Errorf("search result missing context after: %q", text)
	}
}

