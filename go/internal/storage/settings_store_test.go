package storage

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSetSettingsRollsBackEveryValueWhenOneWriteFails(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewTestStore(db)
	if err := store.SetSettings(map[string]string{
		"allow_exec_command":     "false",
		"allow_webfetch_command": "false",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_webfetch_setting
		BEFORE UPDATE ON app_settings
		WHEN NEW.key = 'allow_webfetch_command' AND NEW.value = 'true'
		BEGIN
			SELECT RAISE(ABORT, 'webfetch rejected');
		END;
	`).Error; err != nil {
		t.Fatalf("create rejecting trigger: %v", err)
	}

	err = store.SetSettings(map[string]string{
		"allow_exec_command":     "true",
		"allow_webfetch_command": "true",
	})
	if err == nil {
		t.Fatal("SetSettings succeeded despite rejecting trigger")
	}

	for _, key := range []string{"allow_exec_command", "allow_webfetch_command"} {
		got, getErr := store.GetSetting(key)
		if getErr != nil {
			t.Fatalf("GetSetting(%s): %v", key, getErr)
		}
		if got != "false" {
			t.Fatalf("%s after failed batch = %q, want false", key, got)
		}
	}
}
