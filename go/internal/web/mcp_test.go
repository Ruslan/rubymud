package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"rubymud/go/internal/buildinfo"
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
	buf      bytes.Buffer
	writeErr error
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	return m.buf.Write(b)
}

func (m *mockConn) Close() error { return nil }

func setupMcpTestServer(t *testing.T) (*Server, *session.Session) {
	return setupMcpTestServerWithConfig(t, MCPConfig{Enabled: true})
}

func setupMcpTestServerWithConfig(t *testing.T, mcp MCPConfig) (*Server, *session.Session) {
	t.Helper()

	dbName := uuid.New().String()
	dsn := "file:" + dbName + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	// Build the schema from the real embedded migrations (same DDL as production:
	// unique indexes, FK cascades, sessions.active_map_set_id) rather than a GORM
	// AutoMigrate approximation.
	if err := storage.MigrateForTest(db); err != nil {
		t.Fatalf("MigrateForTest: %v", err)
	}

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

	s, err := New("127.0.0.1:0", manager, store, os.TempDir(), BasicAuth{}, mcp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "mudhost-mcp" {
		t.Errorf("unexpected server name: %v", result["serverInfo"])
	}
	if serverInfo["version"] != buildinfo.Version {
		t.Errorf("server version = %v, want build version %q", serverInfo["version"], buildinfo.Version)
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

func TestMCPRequiredScalarArgumentsRejectMissingOrNull(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	for _, tc := range []struct {
		tool string
		args string
	}{
		{tool: "mud_get_output_range", args: fmt.Sprintf(`{"session_id":%d}`, sess.SessionID())},
		{tool: "mud_get_output_range", args: fmt.Sprintf(`{"session_id":%d,"before_id":null}`, sess.SessionID())},
		{tool: "mud_search", args: fmt.Sprintf(`{"session_id":%d}`, sess.SessionID())},
		{tool: "mud_search", args: fmt.Sprintf(`{"session_id":%d,"query":null}`, sess.SessionID())},
		{tool: "mud_send_command", args: fmt.Sprintf(`{"session_id":%d,"sync":0}`, sess.SessionID())},
		{tool: "mud_send_command", args: fmt.Sprintf(`{"session_id":%d,"command":null,"sync":0}`, sess.SessionID())},
		{tool: "mud_set_variable", args: fmt.Sprintf(`{"session_id":%d,"value":"x"}`, sess.SessionID())},
		{tool: "mud_set_variable", args: fmt.Sprintf(`{"session_id":%d,"key":"x","value":null}`, sess.SessionID())},
	} {
		t.Run(tc.tool+tc.args, func(t *testing.T) {
			res, err := s.mcpCallTool(context.Background(), json.RawMessage(fmt.Sprintf(
				`{"name":%q,"arguments":%s}`, tc.tool, tc.args)), "")
			if err != nil {
				t.Fatalf("required argument produced server error: %v", err)
			}
			if res.(map[string]any)["isError"] != true {
				t.Fatalf("missing/null required argument succeeded: %#v", res)
			}
		})
	}
}

func TestMCPGetOutput(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	// Add some logs
	s.store.AppendLogEntry(sess.SessionID(), "main", "Hello world", "Hello world")
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

func TestMCPSendCommandBroadcastsSuccessfulHintToEveryBrowser(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	if _, err := sess.SendCommandWithTrace("#alias {agentlook} {look here}", "input"); err != nil {
		t.Fatalf("define alias: %v", err)
	}
	entryID, err := s.store.AppendLogEntry(sess.SessionID(), "main", "prompt>", "prompt>")
	if err != nil {
		t.Fatal(err)
	}
	var first, second []session.ServerMsg
	sess.AttachClient("first", func(msg session.ServerMsg) error {
		first = append(first, msg)
		return nil
	})
	sess.AttachClient("second", func(msg session.ServerMsg) error {
		second = append(second, msg)
		return nil
	})
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	rpc := mcpCall(t, ts, "", "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"agentlook","sync":0}`, sess.SessionID()))
	if rpc.Error != nil || mcpToolIsError(t, rpc) {
		t.Fatalf("MCP command failed: %+v", rpc)
	}
	for name, messages := range map[string][]session.ServerMsg{"first": first, "second": second} {
		var hints []session.ServerMsg
		for _, msg := range messages {
			if msg.Type == "command_hint" {
				hints = append(hints, msg)
			}
		}
		if len(hints) != 1 || hints[0].Command != "look here" || hints[0].EntryID != entryID || hints[0].Buffer != "main" {
			t.Fatalf("%s browser hints=%+v, want one canonical live hint on entry %d", name, hints, entryID)
		}
	}
	entries, err := s.store.LogEntriesForBuffer(sess.SessionID(), "main", 10)
	var restored []string
	for _, entry := range entries {
		if entry.ID == entryID {
			restored = entry.Commands
		}
	}
	if err != nil || len(restored) != 1 || restored[0] != "look here" {
		t.Fatalf("restored entries=%+v err=%v, want one persisted canonical hint", entries, err)
	}
}

func TestMCPSendCommandDoesNotBroadcastFailedWrite(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	if _, err := s.store.AppendLogEntry(sess.SessionID(), "main", "prompt>", "prompt>"); err != nil {
		t.Fatal(err)
	}
	setMcpUnexportedField(t, sess, "conn", &mockConn{writeErr: errors.New("write failed")})
	var messages []session.ServerMsg
	sess.AttachClient("observer", func(msg session.ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	rpc := mcpCall(t, ts, "", "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0}`, sess.SessionID()))
	if rpc.Error != nil || !mcpToolIsError(t, rpc) {
		t.Fatalf("failed write result=%+v, want tool error", rpc)
	}
	for _, msg := range messages {
		if msg.Type == "command_hint" {
			t.Fatalf("failed MCP write was shown as sent: %+v", msg)
		}
	}
	entries, err := s.store.LogEntriesForBuffer(sess.SessionID(), "main", 10)
	if err != nil || len(entries) != 1 || len(entries[0].Commands) != 0 {
		t.Fatalf("failed MCP write persisted as sent: entries=%+v err=%v", entries, err)
	}
}

func TestGenericCommandCannotOptIntoTrustedMCPHintBySourceString(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	if _, err := s.store.AppendLogEntry(sess.SessionID(), "main", "prompt>", "prompt>"); err != nil {
		t.Fatal(err)
	}
	var messages []session.ServerMsg
	sess.AttachClient("observer", func(msg session.ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})

	if err := sess.SendCommand("look", "mcp"); err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if msg.Type == "command_hint" {
			t.Fatalf("generic source string acquired trusted MCP publication: %+v", msg)
		}
	}
}

func TestMCPSearch(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	defer ts.Close()

	// Add some logs
	s.store.AppendLogEntry(sess.SessionID(), "main", "First line", "First line")
	s.store.AppendLogEntry(sess.SessionID(), "main", "Matching target here", "Matching target here")
	s.store.AppendLogEntry(sess.SessionID(), "main", "Last line", "Last line")

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
