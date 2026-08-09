package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newWebAuthSettingsTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewTestStore(db)
}

func TestWebAuthSettingsDefaultAndAtomicPersistence(t *testing.T) {
	store := newWebAuthSettingsTestStore(t)

	initial, err := store.GetWebAuthSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if initial.Enabled || initial.Username != "" || initial.PasswordHash != "" || initial.Revision != 0 {
		t.Fatalf("defaults = %+v", initial)
	}

	want := WebAuthSettings{
		Enabled:      true,
		Username:     "operator",
		PasswordHash: "$2a$10$01234567890123456789012345678901234567890123456789012",
		ListenAddr:    "0.0.0.0:8080",
	}
	saved, err := store.SaveWebAuthSettings(want)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !saved.Enabled || saved.Username != want.Username || saved.PasswordHash != want.PasswordHash || saved.ListenAddr != want.ListenAddr || saved.Revision != 1 || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}

	restarted := NewTestStore(store.DB())
	got, err := restarted.GetWebAuthSettings()
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got != saved {
		t.Fatalf("after restart = %+v, want %+v", got, saved)
	}

	var raw AppSetting
	if err := store.DB().Where("key = ?", webAuthSettingKey).Take(&raw).Error; err != nil {
		t.Fatalf("read raw setting: %v", err)
	}
	if raw.Value == "" || raw.Value == "operator-password" {
		t.Fatalf("unexpected raw setting value %q", raw.Value)
	}
}

func TestWebAuthSettingsPublishCommittedRevisions(t *testing.T) {
	store := newWebAuthSettingsTestStore(t)
	updates := make(chan WebAuthSettings, 1)
	unsubscribe := store.SubscribeWebAuthSettings(func(settings WebAuthSettings) {
		updates <- settings
	})
	defer unsubscribe()

	saved, err := store.SaveWebAuthSettings(WebAuthSettings{
		Enabled:      true,
		Username:     "operator",
		PasswordHash: "hash",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	select {
	case got := <-updates:
		if got != saved {
			t.Fatalf("published = %+v, want %+v", got, saved)
		}
	case <-time.After(time.Second):
		t.Fatal("web auth subscriber was not notified")
	}
}
