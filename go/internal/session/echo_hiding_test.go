package session

import (
	"io"
	"net"
	"strings"
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestEchoHiding(t *testing.T) {
	store := newTestStoreWithDeclarations(t)
	if err := store.EnsureSessionProfiles(1, "TestSession"); err != nil {
		t.Fatalf("EnsureSessionProfiles failed: %v", err)
	}

	// Create a local listener to mock MUD server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, conn) // Just consume input
		}
	}()

	// Setup session
	v := vm.New(store, 1)
	if err := v.Reload(); err != nil {
		t.Fatalf("VM reload failed: %v", err)
	}
	s, err := New(1, ln.Addr().String(), store, v, "", false)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close()

	// 1. Manual #var should be visible (Depth 0)
	err = s.SendCommand("#var manual 1", "input")
	if err != nil {
		t.Fatalf("SendCommand manual failed: %v", err)
	}
	checkLogContains(t, store, 1, "#variable {manual} = {1}", "manual #var should be visible")

	// 2. Alias with #var should have silent #var but visible outbound command
	s.SendCommand("#alias {scripted} {#var silent 1;say hello}", "input")
	err = s.SendCommand("scripted", "input")
	if err != nil {
		t.Fatalf("SendCommand scripted failed: %v", err)
	}
	checkLogNotContains(t, store, 1, "#variable {silent} = {1}", "scripted #var should be silent")

	// "say hello" should be in command history
	history, _ := store.ListHistory(1, 100)
	found := false
	for _, h := range history {
		if h.Line == "say hello" {
			found = true
			break
		}
	}
	if !found {
		t.Error("scripted 'say hello' not found in history")
	}

	// 3. #showme should always be visible
	s.SendCommand("#var showmsg {ExecutionOutput}", "input")
	s.SendCommand("#alias {visible_alias} {#showme {$showmsg}}", "input")
	err = s.SendCommand("visible_alias", "input")
	if err != nil {
		t.Fatalf("SendCommand visible failed: %v", err)
	}
	checkLogContains(t, store, 1, "ExecutionOutput", "scripted #showme should be visible")

	// 4. Unknown hash command remains visible/outbound
	err = s.SendCommand("#unknown_hash_cmd", "input")
	if err != nil {
		t.Fatalf("SendCommand unknown failed: %v", err)
	}
	// Unknown hash commands currently fall through as ResultCommand
	checkHistoryContains(t, store, 1, "#unknown_hash_cmd", "unknown hash command should be outbound")

	// 5. Trigger path hiding
	s.SendCommand("#action {TriggerMe} {#var trigger_var 1;say trigger_out}", "input")
	s.processLine("TriggerMe")
	checkLogNotContains(t, store, 1, "#variable {trigger_var} = {1}", "trigger-driven #var should be silent")
	checkHistoryContains(t, store, 1, "say trigger_out", "trigger-driven outbound command should be in history")

	// 6. Nested expansion hiding
	s.SendCommand("#alias {level2} {#var v2 2;say level2_out}", "input")
	s.SendCommand("#alias {level1} {#var v1 1;level2}", "input")
	err = s.SendCommand("level1", "input")
	if err != nil {
		t.Fatalf("SendCommand nested failed: %v", err)
	}
	checkLogNotContains(t, store, 1, "#variable {v1} = {1}", "nested level1 #var should be silent")
	checkLogNotContains(t, store, 1, "#variable {v2} = {2}", "nested level2 #var should be silent")
	checkHistoryContains(t, store, 1, "say level2_out", "nested outbound command should be in history")

	// 7. Background source hiding (e.g. scheduler)
	err = s.SendCommand("#var bg_var 1;say bg_out", "scheduler")
	if err != nil {
		t.Fatalf("SendCommand scheduler failed: %v", err)
	}
	checkLogNotContains(t, store, 1, "#variable {bg_var} = {1}", "scheduler-driven #var should be silent")
	checkHistoryContains(t, store, 1, "say bg_out", "scheduler-driven outbound command should be in history")

	// 8. #if else branch hiding
	// 8a. #if else with outbound command
	s.SendCommand("#alias {if_else_out} {#if {1 == 0} {say then} {say else_outbound}}", "input")
	err = s.SendCommand("if_else_out", "input")
	if err != nil {
		t.Fatalf("SendCommand if_else_out failed: %v", err)
	}
	checkHistoryContains(t, store, 1, "say else_outbound", "if-else outbound command should be in history")

	// 8b. #if else with local command
	s.SendCommand("#alias {if_else_local} {#if {1 == 0} {say then} {#var else_var 1}}", "input")
	err = s.SendCommand("if_else_local", "input")
	if err != nil {
		t.Fatalf("SendCommand if_else_local failed: %v", err)
	}
	checkLogNotContains(t, store, 1, "#variable {else_var} = {1}", "if-else-driven #var should be silent")
	// Verify variable actually changed
	if v.Variables()["else_var"] != "1" {
		t.Errorf("if-else-driven #var failed to set variable")
	}
}

func checkHistoryContains(t *testing.T, store *storage.Store, sessionID int64, text string, msg string) {
	t.Helper()
	history, _ := store.ListHistory(sessionID, 100)
	found := false
	for _, h := range history {
		if h.Line == text {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s: history should contain %q", msg, text)
	}
}

func checkLogContains(t *testing.T, store *storage.Store, sessionID int64, text string, msg string) {
	t.Helper()
	entries, _ := store.LogEntriesForBuffer(sessionID, "main", 100)
	found := false
	for _, e := range entries {
		if strings.Contains(e.RawText, text) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s: log should contain %q", msg, text)
	}
}

func checkLogNotContains(t *testing.T, store *storage.Store, sessionID int64, text string, msg string) {
	t.Helper()
	entries, _ := store.LogEntriesForBuffer(sessionID, "main", 100)
	for _, e := range entries {
		if strings.Contains(e.RawText, text) {
			t.Errorf("%s: log should NOT contain %q", msg, text)
		}
	}
}
