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
