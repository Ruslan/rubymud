package session

import (
	"bytes"
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestTriggerMatchingWithTelnetExtraBytes(t *testing.T) {
	store := newTestStore(t)
	// Add a trigger for "Hello"
	db := store.DB()
	db.Create(&storage.Profile{ID: 1, Name: "Default"})
	db.Create(&storage.SessionProfile{SessionID: 1, ProfileID: 1, OrderIndex: 0})
	db.Create(&storage.TriggerRule{
		ProfileID: 1,
		Pattern:   "^Hello$",
		Command:   "say matched",
		Enabled:   true,
	})

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

	// Simulate "Hello\r\x00\n" which is a common Telnet variant
	input := "Hello\r\x00\n"

	decoder := newTelnetDecoder()
	events := decoder.Feed([]byte(input))

	var lineBuf bytes.Buffer
	for _, ev := range events {
		if ev.typ == telEventText {
			lineBuf.Write(ev.data)
		}
	}

	processBufferedLines(&lineBuf, sess)

	entries, err := store.ListHistory(1, 10)
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}

	found := false
	for _, h := range entries {
		if h.Line == "say matched" && h.Kind == "trigger" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Trigger did not fire for input %q", input)
	}
}

func TestStripANSIPreservesHighBytes(t *testing.T) {
	// Example bytes that might represent a legacy encoding (like CP1251 "Привет")
	input := string([]byte{0xCF, 0xD0, 0xC8, 0xC2, 0xC5, 0xD2})

	// This test ensures that stripping ANSI and control characters preserves high bytes
	// (> 127). This is intended for potential future support of legacy encodings;
	// note that this does not implement full decoding for those encodings.
	plain := stripANSI(input)
	if plain != input {
		t.Errorf("High bytes were modified: got %q, want %q", plain, input)
	}
}

func TestTriggerMultilineGA(t *testing.T) {
	store := newTestStore(t)
	db := store.DB()
	db.Create(&storage.Profile{ID: 1, Name: "Default"})
	db.Create(&storage.SessionProfile{SessionID: 1, ProfileID: 1, OrderIndex: 0})
	db.Create(&storage.TriggerRule{
		ProfileID: 1,
		Pattern:   "^match$",
		Command:   "say matched",
		Enabled:   true,
	})

	v := vm.New(store, 1)
	_ = v.Reload()

	conn := &recordingConn{}
	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	// Input containing a full line, then the target line, then a prompt without newline
	input := "line1\nmatch\nprompt> "

	var lineBuf bytes.Buffer
	lineBuf.Write([]byte(input))

	// Simulate telEventFlush (GA) which currently calls flushLine directly
	// This should fail because flushLine doesn't split by newline
	flushLine(&lineBuf, sess)

	entries, _ := store.ListHistory(1, 10)
	found := false
	for _, h := range entries {
		if h.Line == "say matched" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Trigger ^match$ did not fire for multiline input with GA")
	}
}

func TestANSILeakage(t *testing.T) {
	// Simulated colored Russian text: ESC[0;32m354жESC[0;0m
	input := "\x1b[0;32m354ж\x1b[0;0m"
	processed := normalizeLine(input)

	// Processed text should still contain the literal ESC bytes, not the escaped string "[\x1b]"
	if bytes.Contains([]byte(processed), []byte("[\\x1b]")) {
		t.Errorf("processed text contains escaped [\\x1b]: %q", processed)
	}

	plain := stripANSI(processed)
	// stripANSI should have removed all ANSI codes, and high bytes like 'ж' should remain
	expectedPlain := "354ж"
	if plain != expectedPlain {
		t.Errorf("expected plain text %q, got %q", expectedPlain, plain)
	}
}

func TestBlankLinePreservation(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)
	_ = v.Reload()

	sess := &Session{
		sessionID: 1,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{},
	}

	// Buffer containing text, a blank line, and more text
	input := "line1\n\nline2\n"

	var lineBuf bytes.Buffer
	lineBuf.Write([]byte(input))
	processBufferedLines(&lineBuf, sess)

	entries, _ := sess.store.LogEntriesForBuffer(1, "main", 10)
	// We expect 3 entries: "line1", "", "line2"
	if len(entries) != 3 {
		t.Errorf("expected 3 log entries for %q, got %d", input, len(entries))
	}

	// Verify the blank line is present
	foundBlank := false
	for _, h := range entries {
		if h.PlainText == "" {
			foundBlank = true
			break
		}
	}
	if !foundBlank {
		t.Errorf("blank line not found in log history for %q", input)
	}
}

func TestGAPromptNoBlankLine(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)
	sess := &Session{sessionID: 1, store: store, vm: v, clients: map[int]clientSink{}}

	var lineBuf bytes.Buffer
	// Simulate telEventFlush (GA) on empty buffer
	flushLine(&lineBuf, sess)

	entries, _ := sess.store.LogEntriesForBuffer(1, "main", 10)
	if len(entries) != 0 {
		t.Errorf("expected 0 log entries for empty flush, got %d", len(entries))
	}
}

func TestOutputBatching(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	var lastMsg ServerMsg
	send := func(msg ServerMsg) error {
		lastMsg = msg
		return nil
	}

	sess := &Session{
		sessionID: 1,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{1: {id: 1, name: "test", send: send}},
	}

	// 1. Outside batch
	sess.BroadcastEcho("immediate")
	if lastMsg.Type != "output" || len(lastMsg.Entries) != 1 || lastMsg.Entries[0].Text != "immediate" {
		t.Errorf("expected immediate broadcast, got %+v", lastMsg)
	}

	// 2. Inside batch
	sess.beginOutputBatch()
	sess.BroadcastEcho("line1")
	sess.BroadcastEcho("line2")

	// Should not have updated lastMsg to line2 yet
	if lastMsg.Entries[0].Text == "line2" {
		t.Errorf("broadcast happened inside batch unexpectedly")
	}

	sess.flushOutputBatch()
	if len(lastMsg.Entries) != 2 || lastMsg.Entries[0].Text != "line1" || lastMsg.Entries[1].Text != "line2" {
		t.Errorf("expected batched broadcast with 2 entries, got %+v", lastMsg)
	}
}

func TestHintOrderingInBatch(t *testing.T) {
	store := newTestStore(t)
	v := vm.New(store, 1)

	var messages []ServerMsg
	send := func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	}

	sess := &Session{
		sessionID: 1,
		store:     store,
		vm:        v,
		clients:   map[int]clientSink{1: {id: 1, name: "test", send: send}},
	}

	// Start batch
	sess.beginOutputBatch()

	// Simulate the trigger case where hint is generated before the entry is queued:
	// 1. broadcastCommandHint (called by ApplyEffects)
	sess.broadcastCommandHint("say hello", 123, "main")
	// 2. broadcastEntry (called by processLine)
	sess.broadcastEntry(storage.LogEntry{ID: 123, RawText: "welcome", Buffer: "main"})

	// Flush batch
	sess.flushOutputBatch()

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Message 1 must be the output batch
	if messages[0].Type != "output" {
		t.Errorf("first message should be output, got %s", messages[0].Type)
	}
	if len(messages[0].Entries) != 1 || messages[0].Entries[0].ID != 123 {
		t.Errorf("output batch incorrect: %+v", messages[0].Entries)
	}

	// Message 2 must be the command hint
	if messages[1].Type != "command_hint" || messages[1].Command != "say hello" || messages[1].EntryID != 123 {
		t.Errorf("second message should be command_hint for entry 123, got %+v", messages[1])
	}
}
