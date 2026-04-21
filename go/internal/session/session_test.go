package session

import (
	"bytes"
	"database/sql"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
	"unsafe"

	_ "modernc.org/sqlite"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func TestSendCommandUnknownHashCommandPassesThrough(t *testing.T) {
	store := newTestStore(t)
	conn := &recordingConn{}

	sess := &Session{
		sessionID: 1,
		conn:      conn,
		store:     store,
		vm:        vm.New(nil, 1),
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
		vm:        vm.New(nil, 1),
		clients:   map[int]clientSink{},
	}

	if err := sess.SendCommand("say hi: there", "input"); err != nil {
		t.Fatalf("SendCommand(say hi: there): %v", err)
	}

	if got := conn.String(); got != "say hi: there\n" {
		t.Fatalf("command with colon write = %q, want %q", got, "say hi: there\n")
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
	if got := conn.String(); got != "" {
		t.Fatalf("#var should not write to MUD, got %q", got)
	}

	if err := sess.SendCommand("wield $weapon", "input"); err != nil {
		t.Fatalf("SendCommand(wield $weapon): %v", err)
	}

	if got := conn.String(); got != "wield sword\n" {
		t.Fatalf("live variable substitution write = %q, want %q", got, "wield sword\n")
	}
}

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE log_entries (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, raw_text TEXT NOT NULL DEFAULT '', plain_text TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE log_overlays (id INTEGER PRIMARY KEY, log_entry_id INTEGER NOT NULL, overlay_type TEXT NOT NULL, payload_json TEXT NOT NULL, source_type TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE history_entries (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, kind TEXT NOT NULL, line TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE variables (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, scope TEXT NOT NULL, key TEXT NOT NULL, value TEXT, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX variables_session_scope_key_idx ON variables(session_id, scope, key);`,
		`CREATE TABLE alias_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, name TEXT NOT NULL, template TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX alias_rules_session_name_idx ON alias_rules(session_id, name);`,
		`CREATE TABLE trigger_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, name TEXT, pattern TEXT NOT NULL, command TEXT NOT NULL, is_button INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, stop_after_match INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT 'default');`,
		`CREATE TABLE highlight_rules (id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, pattern TEXT NOT NULL, fg TEXT NOT NULL DEFAULT '', bg TEXT NOT NULL DEFAULT '', bold INTEGER NOT NULL DEFAULT 0, faint INTEGER NOT NULL DEFAULT 0, italic INTEGER NOT NULL DEFAULT 0, underline INTEGER NOT NULL DEFAULT 0, strikethrough INTEGER NOT NULL DEFAULT 0, blink INTEGER NOT NULL DEFAULT 0, reverse INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, group_name TEXT NOT NULL DEFAULT 'default', updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE UNIQUE INDEX highlight_rules_session_pattern_idx ON highlight_rules(session_id, pattern);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec(%q): %v", stmt, err)
		}
	}

	store := &storage.Store{}
	setUnexportedField(t, store, "db", db)
	return store
}

func setUnexportedField(t *testing.T, target any, fieldName string, value any) {
	t.Helper()

	v := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

type recordingConn struct {
	bytes.Buffer
}

func (c *recordingConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (c *recordingConn) Close() error                       { return nil }
func (c *recordingConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *recordingConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *recordingConn) SetDeadline(_ time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }
