package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

const mcpSessionHeader = "Mcp-Session-Id"

func mcpPost(t *testing.T, ts *httptest.Server, body string, mcpSessionID string) (*http.Response, jsonRPCResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if mcpSessionID != "" {
		req.Header.Set(mcpSessionHeader, mcpSessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	var rpc jsonRPCResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
			resp.Body.Close()
			t.Fatalf("decode MCP response: %v", err)
		}
	}
	return resp, rpc
}

func mcpResultText(t *testing.T, rpc jsonRPCResponse) string {
	t.Helper()
	result, ok := rpc.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want tool result", rpc.Result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want one text item", result["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content item = %#v", content[0])
	}
	text, _ := item["text"].(string)
	return text
}

func mcpInitialize(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, _ := mcpPost(t, ts, mcpInitializeRequest, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", resp.StatusCode)
	}
	id := resp.Header.Get(mcpSessionHeader)
	if id == "" {
		t.Fatal("initialize did not return Mcp-Session-Id")
	}
	return id
}

func mcpCall(t *testing.T, ts *httptest.Server, mcpSessionID, name, arguments string) jsonRPCResponse {
	t.Helper()
	resp, rpc := mcpPost(t, ts, fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":2,"params":{"name":%q,"arguments":%s}}`, name, arguments), mcpSessionID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d", name, resp.StatusCode)
	}
	return rpc
}

func mcpToolIsError(t *testing.T, rpc jsonRPCResponse) bool {
	t.Helper()
	result, ok := rpc.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want tool result", rpc.Result)
	}
	isError, _ := result["isError"].(bool)
	return isError
}

func addMCPTestSession(t *testing.T, s *Server) *session.Session {
	t.Helper()
	record, err := s.store.CreateSession("other", "other", 4321)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	sess := &session.Session{}
	setMcpUnexportedField(t, sess, "sessionID", record.ID)
	setMcpUnexportedField(t, sess, "store", s.store)
	setMcpUnexportedField(t, sess, "vm", vm.New(s.store, record.ID))
	setMcpUnexportedField(t, sess, "conn", &mockConn{})
	clientsField := reflect.ValueOf(sess).Elem().FieldByName("clients")
	setMcpUnexportedField(t, sess, "clients", reflect.MakeMap(clientsField.Type()).Interface())

	managerMapField := reflect.ValueOf(s.manager).Elem().FieldByName("sessions")
	sessions := reflect.NewAt(managerMapField.Type(), unsafe.Pointer(managerMapField.UnsafeAddr())).Elem().Interface().(map[int64]*session.Session)
	sessions[record.ID] = sess
	return sess
}

type mcpFailingConn struct{ mockConn }

func (*mcpFailingConn) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type mcpLogOnWriteConn struct {
	mockConn
	store     *storage.Store
	sessionID int64
	buffer    string
	wrote     chan struct{}
	once      sync.Once
}

type mcpBlockingRandom struct {
	started chan struct{}
	release chan struct{}
}

func (r *mcpBlockingRandom) Read(p []byte) (int, error) {
	close(r.started)
	<-r.release
	for i := range p {
		p[i] = byte(i + 1)
	}
	return len(p), nil
}

func (c *mcpLogOnWriteConn) Write(p []byte) (int, error) {
	n, err := c.mockConn.Write(p)
	if err != nil {
		return n, err
	}
	buffer := c.buffer
	if buffer == "" {
		buffer = "main"
	}
	if _, err := c.store.AppendLogEntry(c.sessionID, buffer, "post-command", "post-command"); err != nil {
		return 0, err
	}
	c.once.Do(func() { close(c.wrote) })
	return n, nil
}

// This is the client-facing regression: a client that initialized once and
// read output must receive only subsequently unseen output before its command.
func TestMCPContinuityInitializeHeaderAndImplicitCursor(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)

	firstID, err := s.store.AppendLogEntry(sess.SessionID(), "main", "already seen", "already seen")
	if err != nil {
		t.Fatalf("append first log: %v", err)
	}

	resp, _ := mcpPost(t, ts, mcpInitializeRequest, "")
	mcpSessionID := resp.Header.Get(mcpSessionHeader)
	resp.Body.Close()
	if mcpSessionID == "" {
		t.Fatal("initialize did not return Mcp-Session-Id")
	}

	resp, rpc := mcpPost(t, ts, fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":2,"params":{"name":"mud_get_output","arguments":{"session_id":%d}}}`, sess.SessionID()), mcpSessionID)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("get output status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := mcpResultText(t, rpc); !strings.Contains(got, fmt.Sprintf("[#%d] already seen", firstID)) {
		t.Fatalf("get output = %q", got)
	}

	secondID, err := s.store.AppendLogEntry(sess.SessionID(), "main", "arrived while thinking", "arrived while thinking")
	if err != nil {
		t.Fatalf("append second log: %v", err)
	}
	resp, rpc = mcpPost(t, ts, fmt.Sprintf(`{"jsonrpc":"2.0","method":"tools/call","id":3,"params":{"name":"mud_send_command","arguments":{"session_id":%d,"command":"look","sync":0}}}`, sess.SessionID()), mcpSessionID)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("send command status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	text := mcpResultText(t, rpc)
	if !strings.Contains(text, "Messages since your last seen output:") || !strings.Contains(text, fmt.Sprintf("[#%d] arrived while thinking", secondID)) {
		t.Fatalf("continuity response = %q", text)
	}
	if strings.Contains(text, "already seen") {
		t.Fatalf("continuity repeated seen output: %q", text)
	}
}

func TestMCPBufferDefaultsToMainAndCanSelectChat(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	mainID, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "main line", "main line")
	chatID, _ := s.store.AppendLogEntry(sess.SessionID(), "chat", "chat line", "chat line")
	client := mcpInitialize(t, ts)

	main := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID())))
	if !strings.Contains(main, fmt.Sprintf("[#%d] main line", mainID)) || strings.Contains(main, "chat line") {
		t.Fatalf("default main output = %q", main)
	}
	chat := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"chat"}`, sess.SessionID())))
	if !strings.Contains(chat, fmt.Sprintf("[#%d] chat line", chatID)) || strings.Contains(chat, "main line") {
		t.Fatalf("chat output = %q", chat)
	}
}

func TestMCPBufferContinuityRangeAndSearchStayInSelectedWindow(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", "main before", "main before")
	chatBefore, _ := s.store.AppendLogEntry(sess.SessionID(), "chat", "chat before", "chat before")
	_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", "main needle", "main needle")
	chatMatch, _ := s.store.AppendLogEntry(sess.SessionID(), "chat", "chat needle", "chat needle")
	mainAfter, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "main after", "main after")
	chatAfter, _ := s.store.AppendLogEntry(sess.SessionID(), "chat", "chat after", "chat after")
	client := mcpInitialize(t, ts)

	chat := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"chat"}`, sess.SessionID())))
	if strings.Contains(chat, "main ") || !strings.Contains(chat, fmt.Sprintf("[#%d] chat after", chatAfter)) {
		t.Fatalf("chat output leaked another window: %q", chat)
	}
	if got, _ := s.mcpSessions.cursorForBuffer(client, sess.SessionID(), "chat"); got != chatAfter {
		t.Fatalf("chat cursor = %d, want %d", got, chatAfter)
	}
	if got, _ := s.mcpSessions.cursorForBuffer(client, sess.SessionID(), "main"); got != 0 {
		t.Fatalf("main cursor advanced while reading chat: %d", got)
	}

	main := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID())))
	if strings.Contains(main, "chat ") || !strings.Contains(main, fmt.Sprintf("[#%d] main after", mainAfter)) {
		t.Fatalf("main output leaked another window: %q", main)
	}
	if got, _ := s.mcpSessions.cursorForBuffer(client, sess.SessionID(), "main"); got != mainAfter {
		t.Fatalf("main cursor = %d, want %d", got, mainAfter)
	}

	rangeText := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output_range", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","before_id":%d,"limit":2}`, sess.SessionID(), chatAfter)))
	if !strings.Contains(rangeText, fmt.Sprintf("[#%d] chat before", chatBefore)) || !strings.Contains(rangeText, fmt.Sprintf("[#%d] chat needle", chatMatch)) || strings.Contains(rangeText, "main ") {
		t.Fatalf("chat range = %q", rangeText)
	}
	if got, _ := s.mcpSessions.cursorForBuffer(client, sess.SessionID(), "chat"); got != chatAfter {
		t.Fatalf("older chat range rolled cursor back to %d, want %d", got, chatAfter)
	}

	search := mcpResultText(t, mcpCall(t, ts, client, "mud_search", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","query":"needle","context":1}`, sess.SessionID())))
	if !strings.Contains(search, fmt.Sprintf("[#%d] chat before", chatBefore)) || !strings.Contains(search, fmt.Sprintf("[#%d] chat needle", chatMatch)) || !strings.Contains(search, fmt.Sprintf("[#%d] chat after", chatAfter)) || strings.Contains(search, "main ") {
		t.Fatalf("chat search/context crossed buffers: %q", search)
	}
	rpc := mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","last_seen_log_id":%d}`, sess.SessionID(), mainAfter))
	if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "different buffer") {
		t.Fatalf("cross-buffer cursor response = %q", mcpResultText(t, rpc))
	}
}

func TestMCPBufferUnknownAndInvalidNames(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	s.store.AppendLogEntry(sess.SessionID(), "main", "main only", "main only")
	client := mcpInitialize(t, ts)

	rpc := mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"unknown"}`, sess.SessionID()))
	if mcpToolIsError(t, rpc) || strings.Contains(mcpResultText(t, rpc), "main only") || strings.Contains(mcpResultText(t, rpc), "[#") {
		t.Fatalf("unknown buffer was not an honest empty output: %q", mcpResultText(t, rpc))
	}
	for _, buffer := range []string{"", "bad\nname", strings.Repeat("x", mcpBufferMaxLength+1)} {
		rpc = mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":%q}`, sess.SessionID(), buffer))
		if !mcpToolIsError(t, rpc) {
			t.Fatalf("invalid buffer %q succeeded: %q", buffer, mcpResultText(t, rpc))
		}
	}
	for _, rawBuffer := range []string{"null", "42", "true"} {
		rpc = mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":%s}`, sess.SessionID(), rawBuffer))
		if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "must be a string") {
			t.Fatalf("invalid buffer value %s response = %q", rawBuffer, mcpResultText(t, rpc))
		}
	}
	caseDifferent := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"MAIN"}`, sess.SessionID())))
	if strings.Contains(caseDifferent, "main only") || strings.Contains(caseDifferent, "[#") {
		t.Fatalf("buffer matching was not exact-case: %q", caseDifferent)
	}
	unicodeID, _ := s.store.AppendLogEntry(sess.SessionID(), "чат", "unicode buffer", "unicode buffer")
	unicodeText := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"чат"}`, sess.SessionID())))
	if !strings.Contains(unicodeText, fmt.Sprintf("[#%d] unicode buffer", unicodeID)) {
		t.Fatalf("Unicode buffer output = %q", unicodeText)
	}
	spaced := mcpResultText(t, mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":" main "}`, sess.SessionID())))
	if strings.Contains(spaced, "main only") || strings.Contains(spaced, "[#") {
		t.Fatalf("exact whitespace in buffer name was silently trimmed: %q", spaced)
	}
}

func TestMCPBufferRejectsGaggedAndPrunedSelectedCursors(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	gagged, _ := s.store.AppendLogEntryWithOverlays(sess.SessionID(), "chat", "hidden", "hidden", []storage.LogOverlay{{OverlayType: "gag"}})
	pruned, _ := s.store.AppendLogEntry(sess.SessionID(), "chat", "old chat", "old chat")
	_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", "interleaved main", "interleaved main")
	_, _ = s.store.AppendLogEntry(sess.SessionID(), "chat", "new chat", "new chat")
	client := mcpInitialize(t, ts)

	rpc := mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","last_seen_log_id":%d}`, sess.SessionID(), gagged))
	if !mcpToolIsError(t, rpc) {
		t.Fatalf("gagged selected-buffer cursor succeeded: %q", mcpResultText(t, rpc))
	}
	if err := s.store.DB().Delete(&storage.LogRecord{}, pruned).Error; err != nil {
		t.Fatalf("delete pruned chat cursor: %v", err)
	}
	rpc = mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","last_seen_log_id":%d}`, sess.SessionID(), pruned))
	if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "retained") {
		t.Fatalf("pruned selected-buffer cursor response = %q", mcpResultText(t, rpc))
	}
}

func TestMCPBufferSchemasExposeTheSameBoundedOptionalArgument(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	listed := s.mcpListTools().(map[string]any)["tools"].([]any)
	want := map[string]bool{
		"mud_get_output":       true,
		"mud_get_output_range": true,
		"mud_search":           true,
		"mud_send_command":     true,
	}
	for _, raw := range listed {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if !want[name] {
			continue
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		buffer, ok := props["buffer"].(map[string]any)
		if !ok || buffer["type"] != "string" || buffer["minLength"] != 1 || buffer["maxLength"] != mcpBufferMaxLength || buffer["pattern"] == nil {
			t.Fatalf("%s buffer schema = %#v", name, props["buffer"])
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("tools missing buffer schemas: %#v", want)
	}
}

func TestMCPBufferSendFiltersResponseWithoutChangingCommandDestination(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	conn := &mcpLogOnWriteConn{store: s.store, sessionID: sess.SessionID(), buffer: "chat", wrote: make(chan struct{})}
	setMcpUnexportedField(t, sess, "conn", conn)
	client := mcpInitialize(t, ts)

	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","command":"look","sync":0.01}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	if !strings.Contains(text, "post-command") || strings.Contains(text, "No output received") || conn.buf.String() != "look\n" {
		t.Fatalf("chat command response/destination = %q; wrote=%q", text, conn.buf.String())
	}

	conn = &mcpLogOnWriteConn{store: s.store, sessionID: sess.SessionID(), buffer: "main", wrote: make(chan struct{})}
	setMcpUnexportedField(t, sess, "conn", conn)
	rpc = mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","command":"score","sync":0.01}`, sess.SessionID()))
	text = mcpResultText(t, rpc)
	if !strings.Contains(text, "No output received") || strings.Contains(text, "post-command") || conn.buf.String() != "score\n" {
		t.Fatalf("other-buffer response did not time out cleanly: %q; wrote=%q", text, conn.buf.String())
	}
}

func TestMCPBufferPerWindowTruncationAndGagFiltering(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	for i := 0; i < 1001; i++ {
		_, _ = s.store.AppendLogEntry(sess.SessionID(), "chat", fmt.Sprintf("chat-%04d", i), fmt.Sprintf("chat-%04d", i))
		_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", fmt.Sprintf("main-%04d", i), fmt.Sprintf("main-%04d", i))
	}
	_, _ = s.store.AppendLogEntryWithOverlays(sess.SessionID(), "chat", "chat-secret", "chat-secret", []storage.LogOverlay{{OverlayType: "gag"}})
	client := mcpInitialize(t, ts)
	text := mcpResultText(t, mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"buffer":"chat","command":"look","sync":0}`, sess.SessionID())))
	if !strings.Contains(text, "More than 1000 unseen messages") || strings.Count(text, "chat-") != 200 || strings.Contains(text, "main-") || strings.Contains(text, "chat-secret") {
		t.Fatalf("chat truncation/gag filtering = %q", text)
	}
}

func TestMCPContinuityClientAndMudSessionIsolation(t *testing.T) {
	s, first := setupMcpTestServer(t)
	second := addMCPTestSession(t, s)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)

	firstID, _ := s.store.AppendLogEntry(first.SessionID(), "main", "first session", "first session")
	secondID, _ := s.store.AppendLogEntry(second.SessionID(), "main", "second session", "second session")
	clientA := mcpInitialize(t, ts)
	clientB := mcpInitialize(t, ts)
	mcpCall(t, ts, clientA, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, first.SessionID()))
	mcpCall(t, ts, clientB, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, second.SessionID()))

	if got, _ := s.mcpSessions.cursor(clientA, first.SessionID()); got != firstID {
		t.Fatalf("client A first cursor = %d, want %d", got, firstID)
	}
	if got, _ := s.mcpSessions.cursor(clientA, second.SessionID()); got != 0 {
		t.Fatalf("client A second cursor = %d, want 0", got)
	}
	if got, _ := s.mcpSessions.cursor(clientB, second.SessionID()); got != secondID {
		t.Fatalf("client B second cursor = %d, want %d", got, secondID)
	}
	rpc := mcpCall(t, ts, clientA, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0,"last_seen_log_id":%d}`, first.SessionID(), secondID))
	if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "different MUD session") {
		t.Fatalf("session-mismatched cursor response = %q", mcpResultText(t, rpc))
	}
}

func TestMCPContinuityReadToolsRememberOnlyShownOutput(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	first, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "alpha", "alpha")
	second, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "needle", "needle")
	third, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "omega", "omega")

	for _, tc := range []struct {
		name string
		tool string
		args string
		want int64
	}{
		{name: "output", tool: "mud_get_output", args: fmt.Sprintf(`{"session_id":%d,"limit":1}`, sess.SessionID()), want: third},
		{name: "range", tool: "mud_get_output_range", args: fmt.Sprintf(`{"session_id":%d,"before_id":%d,"limit":1}`, sess.SessionID(), third+1), want: third},
		{name: "search", tool: "mud_search", args: fmt.Sprintf(`{"session_id":%d,"query":"needle","context":0}`, sess.SessionID()), want: second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := mcpInitialize(t, ts)
			mcpCall(t, ts, client, tc.tool, tc.args)
			if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != tc.want {
				t.Fatalf("%s cursor = %d, want %d (entries %d/%d/%d)", tc.name, got, tc.want, first, second, third)
			}
		})
	}
}

func TestMCPContinuityExplicitCursorOverridesRememberedCursor(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	first, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "first", "first")
	second, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "second", "second")
	client := mcpInitialize(t, ts)
	mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"limit":1}`, sess.SessionID()))

	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0,"last_seen_log_id":0}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	for _, id := range []int64{first, second} {
		if !strings.Contains(text, fmt.Sprintf("[#%d]", id)) {
			t.Fatalf("explicit zero did not include #%d: %q", id, text)
		}
	}

	// Stateless clients have no implicit cursor but an explicit one remains a
	// complete continuity contract.
	rpc = mcpCall(t, ts, "", "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0,"last_seen_log_id":%d}`, sess.SessionID(), first))
	text = mcpResultText(t, rpc)
	if !strings.Contains(text, fmt.Sprintf("[#%d]", second)) || strings.Contains(text, fmt.Sprintf("[#%d]", first)) {
		t.Fatalf("stateless explicit cursor response = %q", text)
	}
}

func TestMCPContinuityRejectsUnknownExpiredAndInvalidCursors(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	first, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "first", "first")
	second, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "second", "second")

	resp, _ := mcpPost(t, ts, `{"jsonrpc":"2.0","method":"tools/list","id":2}`, "unknown")
	if resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()
		t.Fatalf("unknown MCP session status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
	if len(s.mcpSessions.clients) != 0 {
		t.Fatalf("unknown MCP id created state: %#v", s.mcpSessions.clients)
	}

	client := mcpInitialize(t, ts)
	s.mcpSessions.now = func() time.Time { return time.Unix(7200, 0) }
	s.mcpSessions.mu.Lock()
	s.mcpSessions.clients[client].lastUsed = time.Unix(0, 0)
	s.mcpSessions.mu.Unlock()
	resp, _ = mcpPost(t, ts, `{"jsonrpc":"2.0","method":"tools/list","id":3}`, client)
	if resp.StatusCode != http.StatusNotFound {
		resp.Body.Close()
		t.Fatalf("expired MCP session status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	client = mcpInitialize(t, ts)
	for _, tc := range []struct {
		name   string
		cursor int64
		want   string
	}{
		{name: "negative", cursor: -1, want: "nonnegative"},
		{name: "future", cursor: second + 1, want: "ahead"},
		{name: "pruned", cursor: first, want: "retained"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "pruned" {
				if err := s.store.DB().Delete(&storage.LogRecord{}, first).Error; err != nil {
					t.Fatalf("delete pruned log: %v", err)
				}
			}
			rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0,"last_seen_log_id":%d}`, sess.SessionID(), tc.cursor))
			if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), tc.want) {
				t.Fatalf("%s cursor response = %q", tc.name, mcpResultText(t, rpc))
			}
		})
	}
}

func TestMCPContinuityTruncatesAndFiltersHiddenOutput(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	for i := 0; i < 1001; i++ {
		if _, err := s.store.AppendLogEntry(sess.SessionID(), "main", fmt.Sprintf("line-%04d", i), fmt.Sprintf("line-%04d", i)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	hidden, err := s.store.AppendLogEntryWithOverlays(sess.SessionID(), "main", "secret", "secret", []storage.LogOverlay{{OverlayType: "gag"}})
	if err != nil {
		t.Fatalf("append hidden: %v", err)
	}
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	if !strings.Contains(text, "More than 1000 unseen messages") || !strings.Contains(text, "line-1000") {
		t.Fatalf("truncation response = %q", text)
	}
	if !strings.Contains(text, "line-0801") || strings.Contains(text, "line-0800") || strings.Count(text, "line-") != 200 {
		t.Fatalf("recent subset was not exactly the newest 200 visible lines: %q", text)
	}
	if strings.Contains(text, "line-0000") || strings.Contains(text, "secret") || strings.Contains(text, fmt.Sprintf("[#%d]", hidden)) {
		t.Fatalf("truncated/hidden output leaked or was not bounded: %q", text)
	}
}

func TestMCPContinuityExactThousandIsNotTruncated(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	for i := 0; i < 1000; i++ {
		_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", fmt.Sprintf("exact-%04d", i), fmt.Sprintf("exact-%04d", i))
	}
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	if strings.Contains(text, "More than 1000") || !strings.Contains(text, "exact-0000") || !strings.Contains(text, "exact-0999") || strings.Count(text, "exact-") != 1000 {
		t.Fatalf("exactly 1000 unseen lines were mishandled: %q", text)
	}
}

func TestMCPContinuityReplaysSubstitutionAndCommandHintOverlays(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	start, end := 0, len("danger")
	id, err := s.store.AppendLogEntryWithOverlays(sess.SessionID(), "main", "danger here", "danger here", []storage.LogOverlay{{
		OverlayType: "substitution",
		StartOffset: &start,
		EndOffset:   &end,
		PayloadJSON: `{"replacement_raw":"SAFE","replacement_plain":"SAFE"}`,
	}})
	if err != nil {
		t.Fatalf("append substituted log: %v", err)
	}
	if err := s.store.AppendCommandHintToLatestLogEntry(sess.SessionID(), "consider"); err != nil {
		t.Fatalf("append command hint: %v", err)
	}
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	if !strings.Contains(text, fmt.Sprintf("[#%d] SAFE here", id)) || strings.Contains(text, "danger here") || !strings.Contains(text, "> consider") {
		t.Fatalf("continuity did not preserve applied overlays: %q", text)
	}
}

func TestMCPSessionStoreMonotonicConcurrentMergeAndDeterministicEviction(t *testing.T) {
	store := newMCPSessionStore()
	clock := time.Unix(1, 0)
	store.now = func() time.Time { return clock }
	store.max = 2
	first, err := store.create()
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 64; i++ {
		wg.Add(1)
		go func(cursor int64) {
			defer wg.Done()
			store.mergeCursor(first, 7, cursor)
		}(i)
		wg.Add(1)
		go func(cursor int64) {
			defer wg.Done()
			store.mergeCursorForBuffer(first, 7, "chat", cursor)
		}(i + 64)
	}
	wg.Wait()
	if got, _ := store.cursor(first, 7); got != 64 {
		t.Fatalf("concurrent merged cursor = %d, want 64", got)
	}
	if got, _ := store.cursorForBuffer(first, 7, "chat"); got != 128 {
		t.Fatalf("concurrent chat cursor = %d, want 128", got)
	}
	clock = clock.Add(time.Second)
	if _, err := store.create(); err != nil {
		t.Fatalf("create second: %v", err)
	}
	clock = clock.Add(time.Second)
	if _, err := store.create(); err != nil {
		t.Fatalf("create third: %v", err)
	}
	if store.exists(first) {
		t.Fatal("oldest client survived bounded eviction")
	}
}

func TestMCPSessionStoreDefaultCapacityStaysAt512(t *testing.T) {
	store := newMCPSessionStore()
	clock := time.Unix(1, 0)
	store.now = func() time.Time { return clock }
	first, err := store.create()
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	for i := 1; i < mcpSessionMaxClients+1; i++ {
		clock = clock.Add(time.Second)
		if _, err := store.create(); err != nil {
			t.Fatalf("create %d: %v", i+1, err)
		}
	}
	if got := len(store.clients); got != mcpSessionMaxClients {
		t.Fatalf("active clients = %d, want %d", got, mcpSessionMaxClients)
	}
	if store.exists(first) {
		t.Fatal("513th client did not evict the least recently used client")
	}
}

func TestMCPSessionStoreBoundsAndEvictsCursorStreamsPerClient(t *testing.T) {
	store := newMCPSessionStore()
	clock := time.Unix(1, 0)
	store.now = func() time.Time { return clock }
	client, err := store.create()
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	for i := 0; i < mcpSessionMaxStreamsPerClient; i++ {
		store.mergeCursorForBuffer(client, 7, fmt.Sprintf("buffer-%03d", i), int64(i+1))
		clock = clock.Add(time.Second)
	}
	if got, _ := store.cursorForBuffer(client, 7, "buffer-000"); got != 1 {
		t.Fatalf("cursor selected for LRU touch = %d, want 1", got)
	}
	clock = clock.Add(time.Second)
	store.mergeCursorForBuffer(client, 7, fmt.Sprintf("buffer-%03d", mcpSessionMaxStreamsPerClient), int64(mcpSessionMaxStreamsPerClient+1))

	state := store.clients[client]
	if got := len(state.cursors); got != mcpSessionMaxStreamsPerClient {
		t.Fatalf("cursor streams = %d, want bounded at %d", got, mcpSessionMaxStreamsPerClient)
	}
	if got, _ := store.cursorForBuffer(client, 7, "buffer-000"); got != 1 {
		t.Fatalf("recently used cursor was evicted: %d", got)
	}
	if got, _ := store.cursorForBuffer(client, 7, "buffer-001"); got != 0 {
		t.Fatalf("least-recently used cursor survived stream eviction: %d", got)
	}
	if got, _ := store.cursorForBuffer(client, 7, fmt.Sprintf("buffer-%03d", mcpSessionMaxStreamsPerClient)); got != int64(mcpSessionMaxStreamsPerClient+1) {
		t.Fatalf("newest cursor = %d, want %d", got, mcpSessionMaxStreamsPerClient+1)
	}
}

func TestMCPSessionStoreOmittedAndExplicitMainShareCursor(t *testing.T) {
	store := newMCPSessionStore()
	client, err := store.create()
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	store.mergeCursor(client, 7, 10)
	store.mergeCursorForBuffer(client, 7, "main", 20)
	if got, _ := store.cursor(client, 7); got != 20 {
		t.Fatalf("default main cursor = %d, want explicit main cursor 20", got)
	}
	if got := len(store.clients[client].cursors); got != 1 {
		t.Fatalf("omitted and explicit main allocated %d cursor streams, want 1", got)
	}
}

func TestMCPContinuitySendFailureAndNoOutputDoNotSkipUnseen(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	id, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "unseen", "unseen")
	client := mcpInitialize(t, ts)
	setMcpUnexportedField(t, sess, "conn", &mcpFailingConn{})
	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0}`, sess.SessionID()))
	if !mcpToolIsError(t, rpc) {
		t.Fatalf("send failure result = %q", mcpResultText(t, rpc))
	}
	if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != 0 {
		t.Fatalf("failed send advanced cursor to %d", got)
	}
	setMcpUnexportedField(t, sess, "conn", &mockConn{})
	rpc = mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0.01}`, sess.SessionID()))
	if !strings.Contains(mcpResultText(t, rpc), fmt.Sprintf("[#%d] unseen", id)) || !strings.Contains(mcpResultText(t, rpc), "No output received") {
		t.Fatalf("retry did not retain unseen output: %q", mcpResultText(t, rpc))
	}
	if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != id {
		t.Fatalf("zero-output response cursor = %d, want %d", got, id)
	}
}

func TestMCPTransportRejectsInvalidNotificationIDsWithoutAllocatingState(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	initialClients := len(s.mcpSessions.clients)
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","id":7}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		res := httptest.NewRecorder()
		s.handleMCP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, res.Code)
		}
	}
	if got := len(s.mcpSessions.clients); got != initialClients {
		t.Fatalf("invalid notifications allocated %d MCP clients, want %d", got, initialClients)
	}
}

func TestMCPTransportProtocolVersionAndSessionHeaderValidation(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)

	resp, rpc := mcpPost(t, ts, `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2025-06-18"}}`, "")
	client := resp.Header.Get(mcpSessionHeader)
	resp.Body.Close()
	result := rpc.Result.(map[string]any)
	if got := result["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("negotiated protocol version = %v, want 2025-06-18", got)
	}
	if client == "" {
		t.Fatal("initialize response missing Mcp-Session-Id")
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":2}`))
	req.Header.Set(mcpSessionHeader, client)
	req.Header.Set("MCP-Protocol-Version", "unsupported")
	res := httptest.NewRecorder()
	s.handleMCP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unsupported protocol status = %d, want 400", res.Code)
	}
}

func TestMCPTransportUsesJSONRPCErrorMember(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)

	resp, rpc := mcpPost(t, ts, `{"jsonrpc":"2.0","method":"unknown/method","id":9}`, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown method status = %d, want 200", resp.StatusCode)
	}
	if rpc.Error == nil || rpc.Result != nil {
		t.Fatalf("unknown method response = %#v, want JSON-RPC error without result", rpc)
	}
}

func TestMCPTransportRejectsOversizedSessionHeaderBeforeBodyRead(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	body := &failOnReadBody{}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = body
	req.Header.Set(mcpSessionHeader, strings.Repeat("x", mcpSessionIDMaxLength+1))
	res := httptest.NewRecorder()
	s.handleMCP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("oversized session header status = %d, want 400", res.Code)
	}
	if body.read {
		t.Fatal("oversized session header caused request body read")
	}
}

func TestMCPTransportRejectsBrowserOriginBeforeBodyOrState(t *testing.T) {
	s, _ := setupMcpTestServer(t)
	for _, origin := range []string{"https://attacker.example", ""} {
		body := &failOnReadBody{}
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Body = body
		req.Header["Origin"] = []string{origin}
		res := httptest.NewRecorder()
		s.handleMCP(res, req)
		if res.Code != http.StatusForbidden {
			t.Errorf("Origin %q request status = %d, want 403", origin, res.Code)
		}
		if body.read || len(s.mcpSessions.clients) != 0 {
			t.Errorf("Origin %q request read body=%v, clients=%d", origin, body.read, len(s.mcpSessions.clients))
		}
	}
}

func TestMCPSessionStoreRandomFailureDoesNotAllocateOrEvict(t *testing.T) {
	store := newMCPSessionStore()
	first, err := store.create()
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	store.random = io.LimitReader(strings.NewReader("short"), 5)
	if _, err := store.create(); err == nil {
		t.Fatal("create with short random source succeeded")
	}
	if len(store.clients) != 1 || !store.exists(first) {
		t.Fatalf("random failure changed existing clients: %#v", store.clients)
	}
}

func TestMCPSessionStoreCloseCannotBeUndoneByInflightCreate(t *testing.T) {
	store := newMCPSessionStore()
	random := &mcpBlockingRandom{started: make(chan struct{}), release: make(chan struct{})}
	store.random = random
	done := make(chan error, 1)
	go func() {
		_, err := store.create()
		done <- err
	}()
	<-random.started
	store.clear()
	close(random.release)
	if err := <-done; err == nil {
		t.Fatal("in-flight create succeeded after store close")
	}
	if len(store.clients) != 0 {
		t.Fatalf("in-flight create resurrected %d clients", len(store.clients))
	}
}

func TestMCPContinuitySearchUsesNewestLimitedGroupsAndExactZeroContext(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	oldID, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "needle old", "needle old")
	for i := 0; i < 20; i++ {
		_, _ = s.store.AppendLogEntry(sess.SessionID(), "main", fmt.Sprintf("filler %d", i), fmt.Sprintf("filler %d", i))
	}
	newID, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "needle new", "needle new")
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_search", fmt.Sprintf(`{"session_id":%d,"query":"needle","context":0,"max_groups":1}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	if !strings.Contains(text, fmt.Sprintf("[#%d] needle new", newID)) || strings.Contains(text, fmt.Sprintf("[#%d] needle old", oldID)) || strings.Contains(text, "filler") {
		t.Fatalf("limited zero-context search = %q", text)
	}
	if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != newID {
		t.Fatalf("search cursor = %d, want shown newest entry %d", got, newID)
	}
}

func TestMCPContinuityOlderRangeDoesNotRollRememberedCursorBack(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	first, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "first", "first")
	second, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "second", "second")
	third, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "third", "third")
	client := mcpInitialize(t, ts)
	mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d,"limit":1}`, sess.SessionID()))
	mcpCall(t, ts, client, "mud_get_output_range", fmt.Sprintf(`{"session_id":%d,"before_id":%d,"limit":1}`, sess.SessionID(), second))
	if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != third {
		t.Fatalf("older range moved cursor to %d, want %d (old entry %d)", got, third, first)
	}
}

func TestMCPContinuityEmptyAndDeletedMudSession(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if mcpToolIsError(t, rpc) {
		t.Fatalf("empty existing session failed: %q", mcpResultText(t, rpc))
	}
	if err := s.store.DeleteSession(sess.SessionID()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	rpc = mcpCall(t, ts, client, "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "not found") {
		t.Fatalf("deleted session response = %q", mcpResultText(t, rpc))
	}
	rpc = mcpCall(t, ts, "", "mud_get_output", fmt.Sprintf(`{"session_id":%d}`, sess.SessionID()))
	if !mcpToolIsError(t, rpc) || !strings.Contains(mcpResultText(t, rpc), "not found") {
		t.Fatalf("stateless deleted session response = %q", mcpResultText(t, rpc))
	}
}

func TestMCPContinuityCancelledSyncWaitDoesNotAdvanceCursor(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	unseen, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "unseen", "unseen")
	client, err := s.mcpSessions.create()
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	wrote := make(chan struct{})
	setMcpUnexportedField(t, sess, "conn", &mcpLogOnWriteConn{store: s.store, sessionID: sess.SessionID(), wrote: wrote})
	ctx, cancel := context.WithCancel(context.Background())
	params := json.RawMessage(fmt.Sprintf(`{"name":"mud_send_command","arguments":{"session_id":%d,"command":"look","sync":10}}`, sess.SessionID()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.mcpCallTool(ctx, params, client)
	}()
	<-wrote
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled MCP sync wait did not stop")
	}
	if got, _ := s.mcpSessions.cursor(client, sess.SessionID()); got != 0 {
		t.Fatalf("cancelled response advanced cursor to %d, unseen was %d", got, unseen)
	}
}

func TestMCPContinuityPreSendBoundaryDoesNotDuplicatePostCommandOutput(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.handleMCP))
	t.Cleanup(ts.Close)
	preID, _ := s.store.AppendLogEntry(sess.SessionID(), "main", "pre-command", "pre-command")
	setMcpUnexportedField(t, sess, "conn", &mcpLogOnWriteConn{store: s.store, sessionID: sess.SessionID(), wrote: make(chan struct{})})
	client := mcpInitialize(t, ts)
	rpc := mcpCall(t, ts, client, "mud_send_command", fmt.Sprintf(`{"session_id":%d,"command":"look","sync":0.01}`, sess.SessionID()))
	text := mcpResultText(t, rpc)
	divider := strings.Index(text, "Command 'look' sent. Response")
	if divider < 0 {
		t.Fatalf("response block missing: %q", text)
	}
	if !strings.Contains(text[:divider], fmt.Sprintf("[#%d] pre-command", preID)) || strings.Contains(text[:divider], "post-command") {
		t.Fatalf("continuity block crossed pre-send boundary: %q", text[:divider])
	}
	if strings.Count(text, "post-command") != 1 || !strings.Contains(text[divider:], "post-command") {
		t.Fatalf("post-command output missing or duplicated: %q", text)
	}
}
