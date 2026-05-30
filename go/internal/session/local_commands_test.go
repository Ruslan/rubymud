package session

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rubymud/go/internal/vm"
)

func TestDisabledExecDoesNotRunOrWriteToMud(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}

	commands, err := sess.SendCommandWithTrace("#exec {/bin/echo} {nope}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace: %v", err)
	}
	if len(commands) != 0 || conn.String() != "" {
		t.Fatalf("disabled exec commands=%v conn=%q, want local only", commands, conn.String())
	}
	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1].RawText, "#exec is disabled in Settings") {
		t.Fatalf("missing disabled exec echo: %+v", logs)
	}
}

func TestDisabledWebFetchDoesNotRequestOrWriteToMud(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}

	commands, err := sess.SendCommandWithTrace("#webfetch {https://example.invalid/items.txt} {q} {orc}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace: %v", err)
	}
	if len(commands) != 0 || conn.String() != "" {
		t.Fatalf("disabled webfetch commands=%v conn=%q, want local only", commands, conn.String())
	}
	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1].RawText, "#webfetch is disabled in Settings") {
		t.Fatalf("missing disabled webfetch echo: %+v", logs)
	}
}

func TestEnabledWebFetchEncodesQueryAndEchoesResponse(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	if err := store.SetSetting("allow_webfetch_command", "true"); err != nil {
		t.Fatalf("SetSetting allow_webfetch_command: %v", err)
	}
	var gotQuery string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprintln(w, "found="+gotQuery)
	}))
	defer server.Close()

	oldResolver := resolvePublicHostFunc
	oldClient := webFetchHTTPClientFunc
	resolvePublicHostFunc = func(ctx context.Context, host string) (net.IP, error) { return net.ParseIP("93.184.216.34"), nil }
	webFetchHTTPClientFunc = func() *http.Client {
		client := server.Client()
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		return client
	}
	defer func() { resolvePublicHostFunc = oldResolver; webFetchHTTPClientFunc = oldClient }()

	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}

	commands, err := sess.SendCommandWithTrace("#webfetch {"+server.URL+"/items.txt} {q} {red orc & blue}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace: %v", err)
	}
	if len(commands) != 0 || conn.String() != "" {
		t.Fatalf("webfetch commands=%v conn=%q, want local only", commands, conn.String())
	}
	if gotQuery != "red orc & blue" {
		t.Fatalf("server query q=%q, want decoded value", gotQuery)
	}
	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	var text strings.Builder
	for _, entry := range logs {
		text.WriteString(entry.RawText)
		text.WriteByte('\n')
	}
	if !strings.Contains(text.String(), "found=red orc & blue") {
		t.Fatalf("webfetch response missing from logs:\n%s", text.String())
	}
}

func TestWebFetchRejectsNonHTTPSAndPrivateIP(t *testing.T) {
	if lines := (&Session{}).runLocalWebFetch("http://example.com/", "q", "x"); !strings.Contains(strings.Join(lines, "\n"), "only https") {
		t.Fatalf("http rejection lines = %v", lines)
	}
	if lines := (&Session{}).runLocalWebFetch("https://127.0.0.1/", "q", "x"); !strings.Contains(strings.Join(lines, "\n"), "blocked private/local") {
		t.Fatalf("private IP rejection lines = %v", lines)
	}
}

func TestWebFetchRejectsUserInfoInInitialURLAndRedirect(t *testing.T) {
	if lines := (&Session{}).runLocalWebFetch("https://user:pass@example.com/items.txt", "q", "x"); !strings.Contains(strings.Join(lines, "\n"), "userinfo") {
		t.Fatalf("userinfo URL rejection lines = %v", lines)
	}

	client := webFetchHTTPClient()
	req, err := http.NewRequest(http.MethodGet, "https://user:pass@example.com/items.txt", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := client.CheckRedirect(req, []*http.Request{{}}); err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Fatalf("userinfo redirect rejection = %v, want userinfo error", err)
	}
}

func TestTriggerWebFetchStaysLocal(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}
	if err := sess.SendCommand("#action {^go$} {#webfetch {https://example.invalid/items.txt} {q} {orc}}", "input"); err != nil {
		t.Fatalf("SendCommand(action): %v", err)
	}
	conn.Reset()

	sess.processLine("go")
	if got := conn.String(); got != "" {
		t.Fatalf("trigger webfetch wrote to MUD: %q", got)
	}
}
