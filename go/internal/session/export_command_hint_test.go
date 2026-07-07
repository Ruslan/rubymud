package session

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

// collectExport drains StreamExportLog into a slice (test convenience).
func collectExport(t *testing.T, store *storage.Store, opts storage.ExportStreamOptions) []storage.ExportStreamItem {
	t.Helper()
	var items []storage.ExportStreamItem
	if err := store.StreamExportLog(opts, func(it storage.ExportStreamItem) error {
		items = append(items, it)
		return nil
	}); err != nil {
		t.Fatalf("StreamExportLog: %v", err)
	}
	return items
}

// TestLocalEchoExcludedFromExport verifies that local client echo (e.g.
// #showme) — written through Store.AppendLogEntry with source_type="echo" — does
// NOT appear in the export stream, while genuine server output (source_type
// "mud") does. It drives the real session echo path (like production) so the
// test is not tautological: removing the source_type='mud' filter in
// buildExportUnion makes the echo leak and this test fail.
func TestLocalEchoExcludedFromExport(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	// Genuine server output (source_type="mud").
	if _, err := store.AppendLogEntryWithOverlays(1, "main", "A dragon appears.", "A dragon appears.", nil); err != nil {
		t.Fatalf("AppendLogEntryWithOverlays: %v", err)
	}

	// Local echo via the real VM path (#showme) -> source_type="echo".
	if err := sess.SendCommand("#showme LOCALECHOMARKER", "input"); err != nil {
		t.Fatalf("SendCommand(#showme): %v", err)
	}

	items := collectExport(t, store, storage.ExportStreamOptions{SessionID: 1, IncludeCommands: true})

	var gotServer, gotEcho bool
	for _, it := range items {
		if it.Kind != "output" {
			continue
		}
		if strings.Contains(it.Ansi, "A dragon appears.") {
			gotServer = true
		}
		if strings.Contains(it.Ansi, "LOCALECHOMARKER") {
			gotEcho = true
		}
	}
	if !gotServer {
		t.Fatalf("server output missing from export: %+v", items)
	}
	if gotEcho {
		t.Fatalf("local echo (#showme) leaked into export: %+v", items)
	}
}

// TestConnectSourcedCommandsExcludedFromExport locks in a security-relevant
// invariant: commands sent with source="connect" (session-init / auto-login)
// must NOT produce a command_hint overlay and therefore must NOT appear in the
// colored-HTML export stream. This keeps auto-login credentials out of a
// shareable export (see docs/dev/planned/html-log-export.md security note).
//
// It drives the real SendCommandWithTrace path (same as production) so removing
// the `if source != "connect"` guard in commands.go makes this test fail.
func TestConnectSourcedCommandsExcludedFromExport(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	// A visible log entry must exist for a command_hint to attach to.
	if _, err := store.AppendLogEntry(1, "main", "Welcome to the MUD.", "Welcome to the MUD."); err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}

	// Auto-login / init command (source="connect") -> must NOT get a hint.
	if _, err := sess.SendCommandWithTrace("autologin-secret", "connect"); err != nil {
		t.Fatalf("SendCommandWithTrace(connect): %v", err)
	}

	// Human-typed command (source="input") -> DOES get a command_hint overlay.
	if _, err := sess.SendCommandWithTrace("northgate", "input"); err != nil {
		t.Fatalf("SendCommandWithTrace(input): %v", err)
	}

	items := collectExport(t, store, storage.ExportStreamOptions{SessionID: 1, IncludeCommands: true})

	var gotHuman, gotConnect bool
	for _, it := range items {
		if it.Kind != "command" {
			continue
		}
		switch it.Ansi {
		case "northgate":
			gotHuman = true
		case "autologin-secret":
			gotConnect = true
		}
	}

	if !gotHuman {
		t.Fatalf("expected human-typed command 'northgate' in export, got %+v", items)
	}
	if gotConnect {
		t.Fatalf("connect-sourced command leaked into export (auto-login credential exposure): %+v", items)
	}
}
