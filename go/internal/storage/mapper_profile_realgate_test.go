package storage_test

// ---------------------------------------------------------------------------
// The REAL compile gate, exercised through the store.
//
// mapper_profile_test.go (package storage) has to use a STUB validator, because
// `storage` cannot import `mapper` — mapper imports storage for storage.Room.
// A stub can drift from the thing it stands in for, which would leave the
// store's central invariant ("the DB must never hold a spec that cannot be
// loaded") tested only against a fiction.
//
// This file closes that gap WITHOUT any production restructuring: an EXTERNAL
// test package (`storage_test`) in the same directory may import mapper, since
// the dependency runs storage_test -> mapper -> storage and never cycles back.
// Importing mapper also runs its init(), which is what installs the real gate —
// so this is simultaneously the test that the registration actually happens.
// ---------------------------------------------------------------------------

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

func newRealGateStore(t *testing.T) *storage.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.New().String())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.MigrateForTest(db); err != nil {
		t.Fatalf("MigrateForTest: %v", err)
	}
	return storage.NewTestStore(db)
}

// TestStoreUsesTheRealCompileGate proves the registration happened (merely
// linking mapper is enough) and that the gate the store enforces is
// mapper.Compile itself, not an approximation of it.
func TestStoreUsesTheRealCompileGate(t *testing.T) {
	store := newRealGateStore(t)

	good, err := mapper.MarshalSpec(mapper.RMUDSpec())
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}
	if _, err := store.CreateMapperProfile("copy-of-builtin", string(good)); err != nil {
		t.Fatalf("a spec mapper.Compile accepts must store: %v", err)
	}

	// Each of these is rejected by a DIFFERENT rule inside mapper.Compile, so the
	// store is demonstrably running the whole compiler and not a shape check.
	for _, c := range []struct {
		name    string
		mutate  func(*mapper.ProfileSpec)
		wantMsg string
	}{
		{"guard that can never match", func(s *mapper.ProfileSpec) { s.Exits.Guard = "NEVER" }, "can never match"},
		{"exits pattern without a capture group", func(s *mapper.ProfileSpec) {
			s.Exits.Pattern = `^\[ Exits:.*\]$`
		}, "capture group"},
		{"no block stops", func(s *mapper.ProfileSpec) { s.Block.Stops = nil }, "no stop conditions"},
		{"header rule with no glyph set", func(s *mapper.ProfileSpec) { s.Strip.Glyphs = "" }, "glyph set"},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := mapper.RMUDSpec()
			c.mutate(&spec)
			blob, mErr := mapper.MarshalSpec(spec)
			if mErr != nil {
				t.Fatalf("MarshalSpec: %v", mErr)
			}
			_, err := store.CreateMapperProfile("bad-"+c.name, string(blob))
			if err == nil {
				t.Fatalf("the store accepted a spec mapper.Compile rejects (%s)", c.name)
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("error = %v, want mention of %q", err, c.wantMsg)
			}
		})
	}

	// Not-JSON is caught too (ParseSpecJSON, before Compile).
	if _, err := store.CreateMapperProfile("not-json", `{`); err == nil {
		t.Error("the store accepted a spec that is not JSON")
	}

	// Only the one valid profile made it in.
	rows, err := store.ListMapperProfiles()
	if err != nil {
		t.Fatalf("ListMapperProfiles: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("stored rows = %d, want only the valid one", len(rows))
	}
}
