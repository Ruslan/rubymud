package session

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestSendCommandEmptyInputSendsNewline(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        vm.New(store, 1),
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("", "input"); err != nil {
		t.Fatalf("SendCommand('', 'input'): %v", err)
	}

	if got := conn.String(); got != "\n" {
		t.Fatalf("empty input command write = %q, want %q", got, "\n")
	}

	// Verify no side effects in history
	hist, err := store.RecentInputHistory(1, 10)
	if err != nil {
		t.Fatalf("RecentInputHistory failed: %v", err)
	}
	if len(hist) > 0 {
		t.Errorf("expected empty history, got %v", hist)
	}

	// Verify no side effects in logs (no command hint)
	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs failed: %v", err)
	}
	for _, entry := range logs {
		if len(entry.Commands) > 0 {
			t.Errorf("expected no command hints in logs, but found in entry %d", entry.ID)
		}
	}
}

func TestSendCommandEmptyOtherSourceDoesNothing(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        vm.New(store, 1),
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("", "key"); err != nil {
		t.Fatalf("SendCommand('', 'key'): %v", err)
	}

	if got := conn.String(); got != "" {
		t.Fatalf("empty key command write = %q, want %q", got, "")
	}
}

func TestSendCommandAliasVarGetterShowsForInputOnly(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, entry := range msg.Entries {
			out = append(out, entry.Text)
		}
		return nil
	})

	if err := sess.SendCommand("#var {direct} {value}", "input"); err != nil {
		t.Fatalf("SendCommand(direct setter): %v", err)
	}
	if len(out) != 1 || out[0] != "#variable {direct} = {value}" {
		t.Fatalf("direct setter output = %v, want variable echo", out)
	}

	out = nil
	if err := sess.SendCommand("#var {direct}", "input"); err != nil {
		t.Fatalf("SendCommand(direct getter): %v", err)
	}
	if len(out) != 1 || out[0] != "#variable {direct} = {value}" {
		t.Fatalf("direct getter output = %v, want variable echo", out)
	}

	out = nil
	if err := sess.SendCommand("#alias {каст1} {#var {kast1} {%0}}", "input"); err != nil {
		t.Fatalf("SendCommand(alias): %v", err)
	}
	out = nil

	if err := sess.SendCommand("каст1 Тартис", "input"); err != nil {
		t.Fatalf("SendCommand(alias setter): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("input alias setter output = %v, want hidden nested setter echo", out)
	}
	vars := sess.vm.Variables()
	if got := vars["kast1"]; got != "Тартис" {
		t.Fatalf("alias setter stored %q, want %q", got, "Тартис")
	}

	out = nil
	if err := sess.SendCommand("каст1", "input"); err != nil {
		t.Fatalf("SendCommand(alias getter): %v", err)
	}
	if len(out) != 1 || out[0] != "#variable {kast1} = {Тартис}" {
		t.Fatalf("input alias getter output = %v, want current variable echo", out)
	}
	vars = sess.vm.Variables()
	if got := vars["kast1"]; got != "Тартис" {
		t.Fatalf("alias getter changed value to %q, want %q", got, "Тартис")
	}

	out = nil
	if err := sess.SendCommand("каст1", "trigger"); err != nil {
		t.Fatalf("SendCommand(non-input alias getter): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("non-input alias getter output = %v, want hidden internal echo", out)
	}
}

func TestSendCommandBroadcastsVariablesForNestedVariableChanges(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	var variableMessages []ServerMsg
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type == "variables" {
			variableMessages = append(variableMessages, msg)
		}
		return nil
	})

	if err := sess.SendCommand("#alias {settarget} {#var {target} {%0}}", "input"); err != nil {
		t.Fatalf("SendCommand(alias): %v", err)
	}
	variableMessages = nil

	if err := sess.SendCommand("settarget goblin", "input"); err != nil {
		t.Fatalf("SendCommand(alias variable setter): %v", err)
	}
	if len(variableMessages) != 1 {
		t.Fatalf("alias variable setter broadcast %d variables messages, want 1", len(variableMessages))
	}
	if len(variableMessages[0].Variables) != 1 || variableMessages[0].Variables[0].Key != "target" || variableMessages[0].Variables[0].Value != "goblin" {
		t.Fatalf("variables broadcast = %+v, want target=goblin", variableMessages[0].Variables)
	}

	variableMessages = nil
	if err := sess.SendCommand("settarget", "input"); err != nil {
		t.Fatalf("SendCommand(alias variable getter): %v", err)
	}
	if len(variableMessages) != 0 {
		t.Fatalf("alias variable getter broadcast %d variables messages, want 0", len(variableMessages))
	}
}

func TestSendCommandWithTraceReturnsCanonicalCommands(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	if err := store.SetVariable(1, "t1", "orc"); err != nil {
		t.Fatalf("SetVariable(t1): %v", err)
	}
	if err := store.SetVariable(1, "ready", "1"); err != nil {
		t.Fatalf("SetVariable(ready): %v", err)
	}
	conn := &recordingConn{}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: conn, store: store, vm: v, clients: map[int]clientSink{}}

	commands, err := sess.SendCommandWithTrace("bash $t1", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace(variable): %v", err)
	}
	if got, want := strings.Join(commands, ";"), "bash orc"; got != want {
		t.Fatalf("variable trace = %q, want %q", got, want)
	}

	if _, err := sess.SendCommandWithTrace("#alias {hitit} {kick $t1}", "input"); err != nil {
		t.Fatalf("SendCommandWithTrace(alias define): %v", err)
	}
	commands, err = sess.SendCommandWithTrace("hitit", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace(alias): %v", err)
	}
	if got, want := strings.Join(commands, ";"), "kick orc"; got != want {
		t.Fatalf("alias trace = %q, want %q", got, want)
	}

	commands, err = sess.SendCommandWithTrace("#if {$ready == 1} {c1;c2} {bad}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace(if chain): %v", err)
	}
	if got, want := strings.Join(commands, ";"), "c1;c2"; got != want {
		t.Fatalf("if chain trace = %q, want %q", got, want)
	}

	commands, err = sess.SendCommandWithTrace("#showme {local only}", "input")
	if err != nil {
		t.Fatalf("SendCommandWithTrace(local): %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("local-only trace = %v, want empty", commands)
	}
}

func TestSendCommandUnknownHashCommandPassesThrough(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        vm.New(store, 1),
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#foo", "input"); err != nil {
		t.Fatalf("SendCommand(#foo): %v", err)
	}

	if got := conn.String(); got != "#foo\n" {
		t.Fatalf("unknown #command write = %q, want %q", got, "#foo\n")
	}
}

func TestSendCommandTextWithColonStillSends(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        vm.New(store, 1),
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("say hi: there", "input"); err != nil {
		t.Fatalf("SendCommand(say hi: there): %v", err)
	}

	if got := conn.String(); got != "say hi: there\n" {
		t.Fatalf("command with colon write = %q, want %q", got, "say hi: there\n")
	}
}

func TestSendCommandAliasHistorySeparatesInputAndExpanded(t *testing.T) {
	store := newTestStoreWithDeclarations(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}

	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	conn := &recordingConn{}
	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#alias {test_alias} {say cmd1;say cmd2}", "input"); err != nil {
		t.Fatalf("SendCommand(alias define): %v", err)
	}
	if err := sess.SendCommand("test_alias", "input"); err != nil {
		t.Fatalf("SendCommand(test_alias): %v", err)
	}

	inputHistory, err := store.RecentInputHistory(1, 50)
	if err != nil {
		t.Fatalf("RecentInputHistory failed: %v", err)
	}

	foundAliasInput := false
	foundExpandedInInput := false
	for _, line := range inputHistory {
		if line == "test_alias" {
			foundAliasInput = true
		}
		if line == "say cmd1" || line == "say cmd2" {
			foundExpandedInInput = true
		}
	}
	if !foundAliasInput {
		t.Fatalf("expected RecentInputHistory to contain test_alias, got %v", inputHistory)
	}
	if foundExpandedInInput {
		t.Fatalf("expected RecentInputHistory to exclude expanded commands, got %v", inputHistory)
	}

	fullHistory, err := store.ListHistory(1, 100)
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}

	foundAliasInputKind := false
	foundCmd1Expanded := false
	foundCmd2Expanded := false
	for _, entry := range fullHistory {
		if entry.Line == "test_alias" && entry.Kind == "input" {
			foundAliasInputKind = true
		}
		if entry.Line == "say cmd1" && entry.Kind == "expanded" {
			foundCmd1Expanded = true
		}
		if entry.Line == "say cmd2" && entry.Kind == "expanded" {
			foundCmd2Expanded = true
		}
	}

	if !foundAliasInputKind {
		t.Fatal("expected full history to contain test_alias with kind=input")
	}
	if !foundCmd1Expanded || !foundCmd2Expanded {
		t.Fatal("expected full history to contain expanded say commands with kind=expanded")
	}
}

func TestReadLoopClosesSessionOnRemoteDisconnect(t *testing.T) {
	tests := []struct {
		name    string
		readErr error
	}{
		{name: "EOF", readErr: io.EOF},
		{name: "read error", readErr: errors.New("connection reset")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			record, err := store.CreateSession("test", "localhost", 1234)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			if err := store.MarkSessionConnected(record.ID); err != nil {
				t.Fatalf("MarkSessionConnected: %v", err)
			}

			conn := &recordingConn{readErr: tt.readErr}
			sess := &Session{
				sessionID: record.ID,
				mudAddr:   "localhost:1234",
				conn:      conn,
				readSrc:   conn,
				store:     store,
				vm:        vm.New(store, record.ID),
				clients:   map[int]clientSink{},
				done:      make(chan struct{}),
			}

			var statuses []string
			sess.AttachClient("test", func(msg ServerMsg) error {
				if msg.Type == "status" {
					statuses = append(statuses, msg.Status)
				}
				return nil
			})

			sess.RunReadLoop()

			if !sess.IsClosed() {
				t.Fatal("session should be closed after remote read failure")
			}
			updated, err := store.GetSession(record.ID)
			if err != nil {
				t.Fatalf("GetSession: %v", err)
			}
			if updated.Status != "disconnected" {
				t.Fatalf("stored session status = %q, want disconnected", updated.Status)
			}
			if len(statuses) != 1 || statuses[0] != "disconnected" {
				t.Fatalf("status broadcasts = %v, want [disconnected]", statuses)
			}
		})
	}
}

func TestCloseBroadcastsDisconnectedOnce(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}
	sess := &Session{
		sessionID: 1,
		mudAddr:   "localhost:1234",
		conn:      conn,
		store:     store,
		clients:   map[int]clientSink{},
		done:      make(chan struct{}),
	}

	var statuses []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type == "status" {
			statuses = append(statuses, msg.Status)
		}
		return nil
	})

	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if len(statuses) != 1 || statuses[0] != "disconnected" {
		t.Fatalf("status broadcasts = %v, want [disconnected]", statuses)
	}
}

func TestVariableUpdateAppliesImmediatelyInLiveSession(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#var {weapon} {sword}", "input"); err != nil {
		t.Fatalf("SendCommand(#var): %v", err)
	}

	// substitution in VM should be immediate because sess.SendCommand calls vm.Reload if it changes settings
	if err := sess.SendCommand("wield $weapon", "input"); err != nil {
		t.Fatalf("SendCommand(wield $weapon): %v", err)
	}

	if got := conn.String(); got != "wield sword\n" {
		t.Fatalf("live variable substitution write = %q, want %q", got, "wield sword\n")
	}
}

func TestLineWithUnclosedSubColorCanLeakToNextLine(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#sub {danger} {[31mdanger}", "input"); err != nil {
		t.Fatalf("SendCommand(#sub): %v", err)
	}
	if err := sess.SendCommand("#highlight {blue} {danger}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			out = append(out, e.Text)
		}
		return nil
	})
	sess.beginOutputBatch()
	sess.processLine("danger zone")
	sess.processLine("plain next")
	sess.flushOutputBatch()

	if len(out) < 2 {
		t.Fatalf("expected at least 2 broadcasted lines, got %d (%q)", len(out), out)
	}

	first := out[0]
	second := out[1]

	if !strings.Contains(first, "\x1b[34mdanger\x1b[0m\x1b[31m") {
		t.Fatalf("expected first line to contain highlight reset+restore sequence, got %q", first)
	}
	if strings.Contains(second, "\x1b[") {
		t.Fatalf("expected second line payload to have no ANSI codes, got %q", second)
	}
}

func TestProcessLineSanitizesBELAndBroadcastsMetadata(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	var entries []ClientLogEntry
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type == "output" {
			entries = append(entries, msg.Entries...)
		}
		return nil
	})

	sess.processLine("[\a][*** system ***]")

	if len(entries) != 1 {
		t.Fatalf("broadcast entries = %d, want 1", len(entries))
	}
	if strings.Contains(entries[0].Text, "\a") {
		t.Fatalf("broadcast text contains raw BEL: %q", entries[0].Text)
	}
	if !strings.Contains(entries[0].Text, "[BEL]") {
		t.Fatalf("broadcast text = %q, want [BEL] marker", entries[0].Text)
	}
	if got, want := entries[0].BellPositions, []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bell positions = %v, want %v", got, want)
	}

	logs, err := store.RecentLogs(1, 1)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if strings.Contains(logs[0].RawText, "\a") || strings.Contains(logs[0].PlainText, "\a") {
		t.Fatalf("stored log contains raw BEL: raw=%q plain=%q", logs[0].RawText, logs[0].PlainText)
	}
	bellCount := 0
	for _, overlay := range logs[0].Overlays {
		if overlay.OverlayType == "bell" {
			bellCount++
		}
	}
	if bellCount != 1 {
		t.Fatalf("bell overlay count = %d, want 1; overlays=%+v", bellCount, logs[0].Overlays)
	}
}

func TestProcessLineBELIgnoresLiteralBackslashTextAndTracksMultipleBELs(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	sess.processLine(`[\\x07] literal`)
	sess.processLine("a\ab\a")

	logs, err := store.RecentLogs(1, 2)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %d, want 2", len(logs))
	}
	for _, overlay := range logs[0].Overlays {
		if overlay.OverlayType == "bell" {
			t.Fatalf("literal backslash text created bell overlay: %+v", logs[0].Overlays)
		}
	}
	var positions []int
	for _, overlay := range logs[1].Overlays {
		if overlay.OverlayType == "bell" && overlay.StartOffset != nil {
			positions = append(positions, *overlay.StartOffset)
		}
	}
	if want := []int{1, 7}; !reflect.DeepEqual(positions, want) {
		t.Fatalf("multiple BEL positions = %v, want %v", positions, want)
	}
}

func TestProcessLineBELPositionsUseSanitizedPlainOffsets(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	sess.processLine("Ж\x1b[31m\a!")

	logs, err := store.RecentLogs(1, 1)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if got, want := logs[0].PlainText, "Ж[BEL]!"; got != want {
		t.Fatalf("plain text = %q, want %q", got, want)
	}
	var positions []int
	for _, overlay := range logs[0].Overlays {
		if overlay.OverlayType == "bell" && overlay.StartOffset != nil {
			positions = append(positions, *overlay.StartOffset)
		}
	}
	if want := []int{len("Ж")}; !reflect.DeepEqual(positions, want) {
		t.Fatalf("BEL positions = %v, want sanitized plain byte offsets %v", positions, want)
	}
}

func TestProcessLineBELInsideEscapeStillSanitizes(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	sess.processLine("\x1b]0;Title\aAlert")

	logs, err := store.RecentLogs(1, 1)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if strings.Contains(logs[0].RawText, "\a") || strings.Contains(logs[0].PlainText, "\a") {
		t.Fatalf("BEL inside escape leaked raw control char: raw=%q plain=%q", logs[0].RawText, logs[0].PlainText)
	}
	foundBell := false
	for _, overlay := range logs[0].Overlays {
		if overlay.OverlayType == "bell" {
			foundBell = true
		}
	}
	if !foundBell {
		t.Fatalf("BEL inside escape did not create bell overlay: %+v", logs[0].Overlays)
	}
}

func TestProcessLineNonBELControlInsideEscapeStillEscapes(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}
	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}

	sess.processLine("\x1b]0;\x01Title")

	logs, err := store.RecentLogs(1, 1)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if strings.Contains(logs[0].RawText, "\x01") || strings.Contains(logs[0].PlainText, "\x01") {
		t.Fatalf("control char inside escape leaked raw byte: raw=%q plain=%q", logs[0].RawText, logs[0].PlainText)
	}
	if !strings.Contains(logs[0].RawText, "[\\x01]") {
		t.Fatalf("raw text = %q, want escaped control marker", logs[0].RawText)
	}
}

func TestHighlightPreservesOuterColorWithinSameLine(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#highlight {blue} {Стражник}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			out = append(out, e.Text)
		}
		return nil
	})

	sess.beginOutputBatch()
	sess.processLine("\x1b[31mНаемный Стражник проходит мимо.")
	sess.flushOutputBatch()

	if len(out) == 0 {
		t.Fatalf("expected at least 1 broadcasted line, got %d", len(out))
	}

	line := out[0]
	if !strings.Contains(line, "\x1b[34mСтражник\x1b[0m\x1b[31m") {
		t.Fatalf("expected highlight reset+restore sequence, got %q", line)
	}
	if !strings.Contains(line, "\x1b[31m проходит мимо.") {
		t.Fatalf("expected tail text to remain in outer red color, got %q", line)
	}
}

func TestHighlightRestoresInheritedOuterColorAcrossLines(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#highlight {blue} {стражник}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			out = append(out, e.Text)
		}
		return nil
	})

	sess.beginOutputBatch()
	sess.processLine("\x1b[31mПервая строка открывает красный")
	sess.processLine("Наемный стражник проходит мимо.")
	sess.flushOutputBatch()

	if len(out) < 2 {
		t.Fatalf("expected at least 2 broadcasted lines, got %d (%q)", len(out), out)
	}

	second := out[1]
	if !strings.Contains(second, "\x1b[34mстражник\x1b[0m\x1b[31m") {
		t.Fatalf("expected blue highlight with restored inherited red on second line, got %q", second)
	}
	if !strings.Contains(second, "\x1b[31m проходит мимо.") {
		t.Fatalf("expected tail text to remain red on second line, got %q", second)
	}
}

func TestHighlightRepro_786729_786731_MagentaStrazhnik(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#highlight {magenta} {стражник}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			out = append(out, e.Text)
		}
		return nil
	})

	sess.beginOutputBatch()
	sess.processLine("\x1b[1;33m\x1b[1;31mНадменный эльф игнорирует Вас.")
	sess.processLine("Наемный стражник проходит мимо.")
	sess.processLine("Толстый стражник стоит здесь, опираясь на алебарду.")
	sess.flushOutputBatch()

	if len(out) < 3 {
		t.Fatalf("expected at least 3 broadcasted lines, got %d (%q)", len(out), out)
	}

	second := out[1]
	if !strings.Contains(second, "\x1b[35mстражник\x1b[0m\x1b[1;31m") {
		t.Fatalf("expected magenta highlight with restored inherited red on second line, got %q", second)
	}
	if !strings.Contains(second, "\x1b[1;31m проходит мимо.") {
		t.Fatalf("expected second line tail text to remain inherited red, got %q", second)
	}

	third := out[2]
	if !strings.Contains(third, "\x1b[35mстражник\x1b[0m\x1b[1;31m") {
		t.Fatalf("expected magenta highlight with restored inherited red on third line, got %q", third)
	}
	if !strings.Contains(third, "\x1b[1;31m стоит здесь") {
		t.Fatalf("expected third line tail text to remain inherited red, got %q", third)
	}
}

func TestHighlightAtEndOfLinePreservesCarryToNextLine(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("#highlight {magenta} {стражник}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	var out []string
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			out = append(out, e.Text)
		}
		return nil
	})

	sess.beginOutputBatch()
	sess.processLine("\x1b[1;31mПрефикс")
	sess.processLine("Толстый стражник")
	sess.processLine("Еще один стражник идет")
	sess.flushOutputBatch()

	if len(out) < 3 {
		t.Fatalf("expected at least 3 broadcasted lines, got %d (%q)", len(out), out)
	}

	second := out[1]
	if !strings.Contains(second, "\x1b[35mстражник\x1b[0m\x1b[1;31m") {
		t.Fatalf("expected end-of-line highlight to restore inherited red, got %q", second)
	}

	third := out[2]
	if !strings.Contains(third, "\x1b[35mстражник\x1b[0m\x1b[1;31m") {
		t.Fatalf("expected carry to survive into next line and restore red after highlight, got %q", third)
	}
	if !strings.Contains(third, "\x1b[1;31m идет") {
		t.Fatalf("expected third line tail to remain red, got %q", third)
	}
}

func TestHighlightCarryIsIndependentForCopyBuffer(t *testing.T) {
	store := newTestStore(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}
	if err := store.CreateTrigger(storage.TriggerRule{
		ProfileID:    1,
		Pattern:      `копия`,
		Command:      "",
		Enabled:      true,
		TargetBuffer: "kills",
		BufferAction: "copy",
	}); err != nil {
		t.Fatalf("CreateTrigger(copy): %v", err)
	}

	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("Reload(): %v", err)
	}

	sess := &Session{sessionID: 1, conn: &recordingConn{}, store: store, vm: v, clients: map[int]clientSink{}}
	if err := sess.SendCommand("#highlight {magenta} {стражник}", "input"); err != nil {
		t.Fatalf("SendCommand(#highlight): %v", err)
	}

	mainOut := []string{}
	killsOut := []string{}
	sess.AttachClient("test", func(msg ServerMsg) error {
		if msg.Type != "output" {
			return nil
		}
		for _, e := range msg.Entries {
			if e.Buffer == "kills" {
				killsOut = append(killsOut, e.Text)
			} else {
				mainOut = append(mainOut, e.Text)
			}
		}
		return nil
	})

	sess.beginOutputBatch()
	sess.processLine("\x1b[1;31mГлавный красный")
	sess.processLine("строка без копии")
	sess.processLine("здесь копия и стражник")
	sess.processLine("следом стражник")
	sess.flushOutputBatch()

	if len(mainOut) < 4 {
		t.Fatalf("expected 4 main lines, got %d (%q)", len(mainOut), mainOut)
	}
	if len(killsOut) < 1 {
		t.Fatalf("expected copied line in kills buffer, got %d (%q)", len(killsOut), killsOut)
	}

	if !strings.Contains(mainOut[3], "\x1b[35mстражник\x1b[0m\x1b[1;31m") {
		t.Fatalf("expected main buffer to keep inherited red before 4th line highlight, got %q", mainOut[3])
	}
	if !strings.Contains(killsOut[0], "\x1b[35mстражник\x1b[0m") {
		t.Fatalf("expected copied line to contain highlighted стражник, got %q", killsOut[0])
	}
	if strings.Contains(killsOut[0], "\x1b[1;31m") {
		t.Fatalf("expected copied buffer not to inherit main red carry, got %q", killsOut[0])
	}
}

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()

	// Use unique name for each test database to avoid "table already exists" in shared cache
	dbName := uuid.New().String()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	err = db.AutoMigrate(
		&storage.AppSetting{}, &storage.SessionRecord{}, &storage.Variable{},
		&storage.AliasRule{}, &storage.TriggerRule{}, &storage.HighlightRule{},
		&storage.SubstituteRule{},
		&storage.Profile{}, &storage.SessionProfile{}, &storage.HotkeyRule{}, &storage.ProfileVariable{},
		&storage.LogRecord{}, &storage.LogOverlay{}, &storage.HistoryEntry{},
		&storage.TimerRecord{}, &storage.TimerSubscriptionRecord{},
		&storage.ProfileTimer{}, &storage.ProfileTimerSubscription{},
	)
	if err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	return storage.NewTestStore(db)
}

type recordingConn struct {
	bytes.Buffer
	readErr error
}

func (c *recordingConn) Read(_ []byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}
func (c *recordingConn) Close() error                       { return nil }
func (c *recordingConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *recordingConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *recordingConn) SetDeadline(_ time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

func TestSessionTimerSnapshot(t *testing.T) {
	// We don't need a real connection for this
	s := &Session{
		timers: make(map[string]*Timer),
	}
	s.timers["ticker"] = NewTimer("ticker", 60*time.Second)

	snapshots := s.TimerSnapshots()
	if len(snapshots) != 1 || snapshots[0].Name != "ticker" {
		t.Errorf("expected 1 snapshot for ticker, got %v", snapshots)
	}

	// Modify internal state and ensure snapshot was a copy
	s.timers["ticker"].On()

	if snapshots[0].Enabled {
		t.Error("snapshot should not have been updated after internal state change")
	}
}

func TestMCCPAcceptance(t *testing.T) {
	conn := &recordingConn{}
	sess := &Session{
		conn:   conn,
		mccpOn: true,
	}

	// Server sends WILL MCCP2
	sess.handleTelnetEvent(telEvent{typ: telEventWill, opt: mccp2})

	if !sess.mccpAccepted {
		t.Fatal("expected mccpAccepted to be true after WILL MCCP2 and mccpOn=true")
	}

	// Verify DO MCCP2 was written
	if !bytes.Equal(conn.Bytes(), []byte{telIAC, telDO, mccp2}) {
		t.Errorf("expected DO MCCP2, got %v", conn.Bytes())
	}
}

func TestMCCPDisabledNegotiation(t *testing.T) {
	conn := &recordingConn{}
	sess := &Session{
		conn:   conn,
		mccpOn: false,
	}

	// Server sends WILL MCCP2
	sess.handleTelnetEvent(telEvent{typ: telEventWill, opt: mccp2})

	if sess.mccpAccepted {
		t.Fatal("expected mccpAccepted to be false after WILL MCCP2 and mccpOn=false")
	}

	// Verify DONT MCCP2 was written
	if !bytes.Equal(conn.Bytes(), []byte{telIAC, telDONT, mccp2}) {
		t.Errorf("expected DONT MCCP2, got %v", conn.Bytes())
	}
}
