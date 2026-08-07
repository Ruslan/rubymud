package storage

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMCPSettingsMigrationDefaultsAndPersistsDigest(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewTestStore(db)
	initial, err := store.GetMCPSettings()
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if initial.Enabled || initial.TokenDigest != "" {
		t.Fatalf("defaults = %+v, want disabled and no digest", initial)
	}
	if err := store.SaveMCPSettings(MCPSettings{Enabled: true, TokenDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A new Store on the same migrated DB simulates the runtime's next start.
	restarted := NewTestStore(db)
	got, err := restarted.GetMCPSettings()
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if !got.Enabled || got.TokenDigest != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" || got.Revision != 1 || got.UpdatedAt.IsZero() {
		t.Fatalf("persisted settings = %+v", got)
	}
}

func TestMCPSettingsPartialUpdatesPreserveOtherState(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewTestStore(db)
	firstDigest := strings.Repeat("1", 64)
	secondDigest := strings.Repeat("2", 64)
	if err := store.SaveMCPSettings(MCPSettings{Enabled: true, TokenDigest: firstDigest}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	disabled, candidateUsed, err := store.UpdateMCPEnabled(false, "")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled || disabled.TokenDigest != firstDigest || candidateUsed || disabled.Revision != 2 {
		t.Fatalf("disabled settings = %+v, candidateUsed=%v", disabled, candidateUsed)
	}
	rotated, err := store.RotateMCPTokenDigest(secondDigest)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated.Enabled || rotated.TokenDigest != secondDigest || rotated.Revision != 3 {
		t.Fatalf("rotated settings = %+v", rotated)
	}
}

func TestMCPSettingsMigrationEnforcesSingletonAndDigestShape(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := MigrateForTest(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	valid := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "second singleton", query: `INSERT INTO mcp_settings(singleton, enabled, token_digest, updated_at) VALUES (2, 0, '', CURRENT_TIMESTAMP)`},
		{name: "short digest", query: `UPDATE mcp_settings SET token_digest = ? WHERE singleton = 1`, args: []any{"abcd"}},
		{name: "uppercase digest", query: `UPDATE mcp_settings SET token_digest = ? WHERE singleton = 1`, args: []any{strings.Repeat("A", 64)}},
		{name: "non-hex digest", query: `UPDATE mcp_settings SET token_digest = ? WHERE singleton = 1`, args: []any{strings.Repeat("g", 64)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Exec(tc.query, tc.args...).Error; err == nil {
				t.Fatal("invalid MCP settings write succeeded")
			}
		})
	}
	if err := db.Exec(`UPDATE mcp_settings SET token_digest = ? WHERE singleton = 1`, valid).Error; err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}
}
