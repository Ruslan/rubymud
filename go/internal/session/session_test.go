package session

import (
	"bytes"
	"fmt"
	"io"
	"net"
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
