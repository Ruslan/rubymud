package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rubymud/go/internal/vm"
)

func TestSendCommandAliasExecRunsLocalScriptWithSafeArgs(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "items_db_client")
	script := "#!/bin/sh\nprintf 'item=%s\\n' \"$1\"\nprintf 'argc=%s\\n' \"$#\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write test script: %v", err)
	}

	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	if err := store.SetSetting("allow_exec_command", "true"); err != nil {
		t.Fatalf("SetSetting allow_exec_command: %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}

	if err := sess.SendCommand("#alias {лор} {#exec {"+scriptPath+"} {%1}}", "input"); err != nil {
		t.Fatalf("SendCommand(alias): %v", err)
	}

	commands, err := sess.SendCommandWithTrace("лор {red;blue}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace(exec alias): %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("exec alias canonical commands = %v, want empty local-only trace", commands)
	}
	if got := conn.String(); got != "" {
		t.Fatalf("exec alias wrote to MUD connection: %q", got)
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
	if !strings.Contains(text.String(), "item=red;blue\n") {
		t.Fatalf("exec output missing argv value, logs:\n%s", text.String())
	}
	if !strings.Contains(text.String(), "argc=1\n") {
		t.Fatalf("exec output should receive one safe argv arg, logs:\n%s", text.String())
	}
}
