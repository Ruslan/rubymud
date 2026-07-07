package session

import (
	"testing"

	"rubymud/go/internal/storage"
	"rubymud/go/internal/vm"
)

func newTimezoneTestSession(t *testing.T, store *storage.Store, sessionID int64) *Session {
	t.Helper()
	return &Session{
		sessionID: sessionID,
		store:     store,
		vm:        vm.New(store, sessionID),
		clients:   map[int]clientSink{},
	}
}

// TestApplyClientTimezoneFollowPersists verifies that a following session
// (tz_follow=1) adopts the connecting client's zone: it is persisted and pushed
// to the VM so $TIME renders in it.
func TestApplyClientTimezoneFollowPersists(t *testing.T) {
	store := newTestStore(t)
	rec, err := store.CreateSession("s", "example.org", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess := newTimezoneTestSession(t, store, rec.ID)
	sess.ApplyClientTimezone("Europe/Kyiv")

	loaded, err := store.GetSession(rec.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if loaded.Timezone != "Europe/Kyiv" {
		t.Fatalf("timezone = %q, want Europe/Kyiv persisted", loaded.Timezone)
	}
	if got := sess.vm.Location().String(); got != "Europe/Kyiv" {
		t.Fatalf("VM location = %q, want Europe/Kyiv pushed", got)
	}
}

// TestUpdateSessionSyncsLiveVM verifies a manual Settings edit (via the manager)
// pushes the new zone to a live VM immediately, not just to the DB.
func TestUpdateSessionSyncsLiveVM(t *testing.T) {
	store := newTestStore(t)
	rec, err := store.CreateSession("s", "example.org", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	manager := NewManager(store)
	sess := newTimezoneTestSession(t, store, rec.ID)
	manager.sessions[rec.ID] = sess // simulate a connected session

	rec.Timezone = "America/New_York"
	rec.TZFollow = 0
	if err := manager.UpdateSession(rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	if got := sess.vm.Location().String(); got != "America/New_York" {
		t.Fatalf("live VM location = %q, want America/New_York after edit", got)
	}
}

// TestApplyClientTimezonePinnedIgnored verifies that a pinned session
// (tz_follow=0) does not change its timezone when a client from another zone
// connects.
func TestApplyClientTimezonePinnedIgnored(t *testing.T) {
	store := newTestStore(t)
	rec, err := store.CreateSession("s", "example.org", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rec.Timezone = "America/New_York"
	rec.TZFollow = 0
	if err := store.UpdateSession(rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	sess := newTimezoneTestSession(t, store, rec.ID)
	sess.ApplyClientTimezone("Europe/Kyiv")

	loaded, err := store.GetSession(rec.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if loaded.Timezone != "America/New_York" {
		t.Fatalf("pinned timezone changed to %q, want America/New_York unchanged", loaded.Timezone)
	}
}

// TestApplyClientTimezoneInvalidIgnored verifies an unparseable client zone is
// ignored without error and leaves the stored zone untouched.
func TestApplyClientTimezoneInvalidIgnored(t *testing.T) {
	store := newTestStore(t)
	rec, err := store.CreateSession("s", "example.org", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess := newTimezoneTestSession(t, store, rec.ID)
	sess.ApplyClientTimezone("Not/AZone")
	sess.ApplyClientTimezone("")

	loaded, err := store.GetSession(rec.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if loaded.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC unchanged", loaded.Timezone)
	}
}
