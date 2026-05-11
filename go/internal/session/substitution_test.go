package session

import (
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func newSubTestSession(t *testing.T) (*storage.Store, *Session, int64, *[]ServerMsg) {
	t.Helper()
	store := newTestStore(t)
	profile, err := store.CreateProfile("Default", "")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := store.AddProfileToSession(1, profile.ID, 0); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}
	messages := []ServerMsg{}
	sess := &Session{
		sessionID: 1,
		conn:      &recordingConn{},
		store:     store,
		vm:        vm.New(store, 1),
		clients:   map[int]clientSink{},
	}
	sess.AttachClient("test", func(msg ServerMsg) error {
		messages = append(messages, msg)
		return nil
	})
	return store, sess, profile.ID, &messages
}

func TestSubstitutionLiveDisplayKeepsCanonicalLog(t *testing.T) {
	store, sess, profileID, messages := newSubTestSession(t)
	if err := store.SaveSubstitute(profileID, "foo", "bar", false, "default"); err != nil {
		t.Fatalf("SaveSubstitute: %v", err)
	}

	sess.processLine("foo")

	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if logs[0].RawText != "foo" || logs[0].PlainText != "foo" {
		t.Fatalf("canonical log = %q/%q, want foo/foo", logs[0].RawText, logs[0].PlainText)
	}
	if logs[0].DisplayPlainText() != "bar" {
		t.Fatalf("display plain = %q, want bar", logs[0].DisplayPlainText())
	}
	if len(logs[0].Overlays) != 1 || logs[0].Overlays[0].OverlayType != "substitution" {
		t.Fatalf("overlays = %+v, want substitution", logs[0].Overlays)
	}
	if len(*messages) != 1 || len((*messages)[0].Entries) != 1 || (*messages)[0].Entries[0].Text != "bar" {
		t.Fatalf("broadcast messages = %+v, want display bar", *messages)
	}
}

func TestGagHidesLineSuppressesTriggersAndCommandHintsSkipIt(t *testing.T) {
	store, sess, profileID, messages := newSubTestSession(t)
	if err := store.SaveTrigger(profileID, "spam", "kill spammer", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}
	if err := store.SaveSubstitute(profileID, "spam", "", true, "default"); err != nil {
		t.Fatalf("SaveSubstitute gag: %v", err)
	}

	sess.processLine("visible")
	*messages = nil
	sess.processLine("spam")

	if got := sess.conn.(*recordingConn).String(); got != "" {
		t.Fatalf("trigger command write = %q, want empty", got)
	}
	if len(*messages) != 0 {
		t.Fatalf("gag broadcast messages = %+v, want none", *messages)
	}

	var stored []storage.LogRecord
	if err := store.DB().Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("query log records: %v", err)
	}
	if len(stored) != 2 || stored[1].WindowName != "main" || stored[1].PlainText != "spam" {
		t.Fatalf("stored log records = %+v, want hidden spam in main", stored)
	}
	logs, err := store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].PlainText != "visible" {
		t.Fatalf("visible logs = %+v, want only visible", logs)
	}

	if err := sess.SendCommand("look", "input"); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	logs, err = store.RecentLogs(1, 10)
	if err != nil {
		t.Fatalf("RecentLogs after command: %v", err)
	}
	if len(logs) != 1 || len(logs[0].Commands) != 1 || logs[0].Commands[0] != "look" {
		t.Fatalf("command hint logs = %+v, want look on visible line", logs)
	}
}
