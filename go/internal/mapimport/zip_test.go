package mapimport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildZip creates an in-memory .zip with the given entries (name -> content).
func buildZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func synthRoom(tag int8, hint string) []byte {
	b := newTPF0("TMudRoom2")
	b.int8("Tag", tag)
	b.shortStrProp("Hint", hint)
	b.int8("X", 0)
	b.int8("Y", int8(tag))
	return b.bytes()
}

func TestParseZipMultipleZonesNestedAndJunk(t *testing.T) {
	zoneA := append(synthRoom(1, "Комната A1"), synthRoom(2, "Комната A2")...)
	zoneB := synthRoom(1, "Комната B1")

	data := buildZip(t, map[string][]byte{
		"maps/Зона Б.mm2":     zoneB, // nested folder
		"maps/sub/Зона А.mm2": zoneA, // deeper nested
		"readme.txt":          []byte("ignore me"),
		"maps/notes.md":       []byte("ignore me too"),
		"maps/sub/thumb.png":  []byte{0x89, 0x50, 0x4e, 0x47},
	})

	pa, err := ParseZip(data, "test.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if pa.Summary.ZoneCount != 2 {
		t.Errorf("zone count = %d, want 2 (junk ignored)", pa.Summary.ZoneCount)
	}
	if pa.Summary.RoomCount != 3 {
		t.Errorf("room count = %d, want 3", pa.Summary.RoomCount)
	}
	// Zones sorted by filename: "Зона А" < "Зона Б".
	if len(pa.Summary.Zones) != 2 || pa.Summary.Zones[0] != "Зона А" || pa.Summary.Zones[1] != "Зона Б" {
		t.Errorf("zones = %v, want [Зона А, Зона Б]", pa.Summary.Zones)
	}
	// Rooms grouped by zone in that order; ri dense within each.
	if pa.Rooms[0].Zone != "Зона А" || pa.Rooms[0].RI != 0 {
		t.Errorf("room 0 = %s ri=%d", pa.Rooms[0].Zone, pa.Rooms[0].RI)
	}
	if pa.Rooms[1].Zone != "Зона А" || pa.Rooms[1].RI != 1 {
		t.Errorf("room 1 = %s ri=%d", pa.Rooms[1].Zone, pa.Rooms[1].RI)
	}
	if pa.Rooms[2].Zone != "Зона Б" || pa.Rooms[2].RI != 0 {
		t.Errorf("room 2 = %s ri=%d", pa.Rooms[2].Zone, pa.Rooms[2].RI)
	}
}

func TestParseZipRejectsOversizedEntry(t *testing.T) {
	// A highly compressible .mm2 entry that expands past the per-entry cap must
	// be rejected with a clean error, not OOM the process. Zeros deflate to a
	// tiny archive, so the compressed size stays trivial.
	big := make([]byte, maxMM2EntryBytes+1<<20) // cap + 1MB of zeros
	data := buildZip(t, map[string][]byte{
		"bomb.mm2": big,
	})
	if len(data) > 1<<20 {
		t.Fatalf("expected a tiny compressed bomb, got %d bytes", len(data))
	}
	_, err := ParseZip(data, "bomb.zip")
	if err == nil {
		t.Fatal("expected an error for oversized entry, got nil")
	}
	if !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "large") {
		t.Errorf("expected a size-limit error, got %v", err)
	}
}

func TestParseZipRejectsTooManyEntries(t *testing.T) {
	files := map[string][]byte{}
	for i := 0; i <= maxMM2Entries; i++ {
		files[fmt.Sprintf("z%05d.mm2", i)] = synthRoom(1, "R")
	}
	data := buildZip(t, files)
	_, err := ParseZip(data, "many.zip")
	if err == nil {
		t.Fatal("expected an error for too many entries, got nil")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("expected a member-count error, got %v", err)
	}
}

func TestParseZipSeamResolution(t *testing.T) {
	// Zone A room 1 has a seam to Zone B tag 5 (resolvable) and one to a missing
	// zone (unresolved).
	a := newTPF0("TMudRoom2")
	a.int8("Tag", 1)
	a.set("AutoMaps.Strings", []string{"Зона Б|на восток|5", "Нет Зоны|туда|9"})
	roomA := a.bytes()

	b := newTPF0("TMudRoom2")
	b.int8("Tag", 5)
	roomB := b.bytes()

	data := buildZip(t, map[string][]byte{
		"Зона А.mm2": roomA,
		"Зона Б.mm2": roomB,
	})
	pa, err := ParseZip(data, "seams.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if pa.Summary.SeamCount != 1 {
		t.Errorf("seam count = %d, want 1", pa.Summary.SeamCount)
	}
	if len(pa.Summary.Unresolved) != 1 || pa.Summary.Unresolved[0] != "Нет Зоны|туда|9" {
		t.Errorf("unresolved = %v, want [Нет Зоны|туда|9]", pa.Summary.Unresolved)
	}
}
