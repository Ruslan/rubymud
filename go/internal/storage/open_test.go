package storage

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestRoomImagesRootFollowsDatabasePath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mudhost.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if got, want := store.RoomImagesRoot(), filepath.Join(filepath.Dir(dbPath), "room_images"); got != want {
		t.Fatalf("RoomImagesRoot = %q, want %q", got, want)
	}
}

func TestOpenDerivesImageRootForDiskBackedFileURI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "space db.sqlite")
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "cache=shared"}).String()
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got, want := store.RoomImagesRoot(), filepath.Join(filepath.Dir(dbPath), "room_images"); got != want {
		t.Fatalf("RoomImagesRoot = %q, want %q", got, want)
	}
}

func TestResolveSQLitePathFileURIs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	absPath := filepath.Join(dir, "absolute name.sqlite")
	absURI := (&url.URL{Scheme: "file", Path: absPath}).String()
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "encoded relative", raw: "file:relative%20name.sqlite?cache=shared", want: filepath.Join(dir, "relative name.sqlite")},
		{name: "encoded absolute", raw: absURI, want: absPath},
		{name: "memory", raw: "file:volatile?mode=memory&cache=shared", want: ""},
		{name: "encoded memory", raw: "file:%3Amemory%3A?cache=shared", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn, got, err := ResolveSQLitePath(tc.raw)
			if err != nil {
				t.Fatalf("ResolveSQLitePath(%q): %v", tc.raw, err)
			}
			if dsn != tc.raw {
				t.Fatalf("dsn = %q, want original %q", dsn, tc.raw)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenPreservesSQLiteMemoryDSNsWithoutImageRoot(t *testing.T) {
	for _, dsn := range []string{":memory:", "file:" + uuid.NewString() + "?mode=memory&cache=shared", "file:%3Amemory%3A?cache=shared"} {
		store, err := Open(dsn)
		if err != nil {
			t.Fatalf("Open(%q): %v", dsn, err)
		}
		if got := store.RoomImagesRoot(); got != "" {
			_ = store.Close()
			t.Fatalf("RoomImagesRoot(%q) = %q, want empty for memory/URI database", dsn, got)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close(%q): %v", dsn, err)
		}
	}
}
