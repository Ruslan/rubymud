package mapimport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
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

// buildZipCP1251Name writes one entry whose name field contains raw CP1251
// bytes and has ZIP's UTF-8 flag unset, as produced by legacy Windows tools.
func buildZipCP1251Name(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	rawName, err := charmap.Windows1251.NewEncoder().String(name)
	if err != nil {
		t.Fatalf("encode CP1251 filename %q: %v", name, err)
	}
	return buildZipNonUTF8Name(t, rawName, body)
}

// buildZipNonUTF8Name writes one entry with ZIP's UTF-8 flag unset. name is
// written verbatim and may be either raw legacy bytes or valid UTF-8.
func buildZipNonUTF8Name(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:    name,
		Method:  zip.Deflate,
		NonUTF8: true,
	})
	if err != nil {
		t.Fatalf("zip create CP1251 entry: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("zip write CP1251 entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func buildZipEntries(t *testing.T, entries []struct {
	name    string
	body    []byte
	nonUTF8 bool
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		var (
			w   io.Writer
			err error
		)
		if entry.nonUTF8 {
			w, err = zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Deflate, NonUTF8: true})
		} else {
			w, err = zw.Create(entry.name)
		}
		if err != nil {
			t.Fatalf("zip create %q: %v", entry.name, err)
		}
		if _, err := w.Write(entry.body); err != nil {
			t.Fatalf("zip write %q: %v", entry.name, err)
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

func synthRoomAt(tag, x, y int8, hint string) []byte {
	b := newTPF0("TMudRoom2")
	b.int8("Tag", tag)
	b.shortStrProp("Hint", hint)
	b.int8("X", x)
	b.int8("Y", y)
	return b.bytes()
}

func importValidation(t *testing.T, err error) *ValidationError {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	return validation
}

func TestParseZipRejectsArchiveWithoutMapFiles(t *testing.T) {
	_, err := ParseZip(buildZip(t, map[string][]byte{
		"readme.txt": []byte("not a map"),
	}), "empty.zip")
	if err == nil {
		t.Fatal("ParseZip accepted archive without .mm2 files")
	}
	if !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error %v does not expose invalid-archive classification", err)
	}
	validation := importValidation(t, err)
	if !strings.Contains(validation.Error(), ".mm2") {
		t.Fatalf("error = %q, want useful .mm2 explanation", validation)
	}
}

func TestParseZipRejectsZoneWithoutRooms(t *testing.T) {
	_, err := ParseZip(buildZip(t, map[string][]byte{
		"empty.mm2": []byte("not a TPF0 room"),
	}), "empty-zone.zip")
	if err == nil {
		t.Fatal("ParseZip accepted .mm2 without rooms")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one empty-zone conflict", validation.Summary.Conflicts)
	}
	got := validation.Summary.Conflicts[0]
	if got.Kind != "empty_zone" || got.Zone != "empty" || !strings.HasPrefix(got.Source, "empty.mm2 ") || len(got.Source) > maxSourceLabelBytes {
		t.Fatalf("conflict = %+v, want empty_zone for empty.mm2", got)
	}
}

func TestParseZipRejectsCanonicalZoneAndRoomIdentityConflicts(t *testing.T) {
	// The names differ in the archive but collapse after the importer's
	// TrimSpace+NFC normalization. The rooms then also share a logical key.
	nfd := norm.NFD.String("Балифой")
	data := buildZip(t, map[string][]byte{
		"one/ " + nfd + " .mm2": synthRoomAt(1, 0, 0, "first"),
		"two/Балифой.mm2":       synthRoomAt(2, 0, 0, "second"),
	})
	_, err := ParseZip(data, "conflicts.zip")
	if err == nil {
		t.Fatal("ParseZip accepted canonical zone/room conflicts")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v, want zone and room conflicts", validation.Summary.Conflicts)
	}
	zone, room := validation.Summary.Conflicts[0], validation.Summary.Conflicts[1]
	if zone.Kind != "duplicate_zone" || zone.Zone != "Балифой" || zone.Source == "" || zone.OtherSource == "" {
		t.Fatalf("zone conflict = %+v", zone)
	}
	if room.Kind != "duplicate_room" || room.Zone != "Балифой" || room.X != 0 || room.Y != 0 || room.L != 0 {
		t.Fatalf("room conflict = %+v", room)
	}
}

func TestParseZipRejectsZoneNamesCollidingAfterCP1251Decode(t *testing.T) {
	raw, err := charmap.Windows1251.NewEncoder().String("Цель.mm2")
	if err != nil {
		t.Fatalf("encode CP1251 filename: %v", err)
	}
	data := buildZipEntries(t, []struct {
		name    string
		body    []byte
		nonUTF8 bool
	}{
		{name: "legacy/" + raw, body: synthRoomAt(1, 0, 0, "legacy"), nonUTF8: true},
		{name: "utf8/Цель.mm2", body: synthRoomAt(2, 1, 0, "utf8")},
	})
	_, err = ParseZip(data, "cp1251-conflict.zip")
	if err == nil {
		t.Fatal("ParseZip accepted zones colliding after CP1251 decoding")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) != 1 || validation.Summary.Conflicts[0].Kind != "duplicate_zone" || validation.Summary.Conflicts[0].Zone != "Цель" {
		t.Fatalf("conflicts = %+v, want CP1251-decoded duplicate zone", validation.Summary.Conflicts)
	}
}

func TestParseZipConflictSourcesAreBoundedAndDistinctForExactDuplicateNames(t *testing.T) {
	name := strings.Repeat("nested/", 80) + "same.mm2"
	data := buildZipEntries(t, []struct {
		name    string
		body    []byte
		nonUTF8 bool
	}{
		{name: name, body: synthRoomAt(1, 0, 0, "first")},
		{name: name, body: synthRoomAt(2, 1, 0, "second")},
	})
	_, err := ParseZip(data, "duplicate-name.zip")
	if err == nil {
		t.Fatal("ParseZip accepted duplicate exact entry names")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) == 0 {
		t.Fatal("missing conflict report")
	}
	conflict := validation.Summary.Conflicts[0]
	if conflict.Source == conflict.OtherSource || len(conflict.Source) > maxSourceLabelBytes || len(conflict.OtherSource) > maxSourceLabelBytes {
		t.Fatalf("conflict sources are not bounded/distinct: %+v", conflict)
	}
}

func TestParseZipBoundsConflictReport(t *testing.T) {
	entries := make(map[string][]byte, maxImportConflicts+5)
	for i := 0; i < maxImportConflicts+5; i++ {
		entries[fmt.Sprintf("z%03d.mm2", i)] = append(
			synthRoomAt(1, 0, 0, "first"),
			synthRoomAt(2, 0, 0, "second")...,
		)
	}
	_, err := ParseZip(buildZip(t, entries), "many-conflicts.zip")
	if err == nil {
		t.Fatal("ParseZip accepted duplicate coordinates")
	}
	validation := importValidation(t, err)
	if got := len(validation.Summary.Conflicts); got != maxImportConflicts {
		t.Fatalf("reported %d conflicts, want cap %d", got, maxImportConflicts)
	}
	if !validation.Summary.ConflictsTruncated {
		t.Fatal("expected capped conflict report to be marked truncated")
	}
}

func TestParseMM2LimitedStopsBeforeAccumulatingPastLimit(t *testing.T) {
	data := append(synthRoom(1, "one"), synthRoom(2, "two")...)
	data = append(data, synthRoom(3, "three")...)
	rooms, exceeded := parseMM2Limited("Z", data, 2)
	if !exceeded || len(rooms) != 2 || rooms[0].RI != 0 || rooms[1].RI != 1 {
		t.Fatalf("rooms=%+v exceeded=%v, want first two and limit overflow", rooms, exceeded)
	}
}

func TestSourceLabelsAreBoundedAndDistinguishDuplicateEntries(t *testing.T) {
	long := strings.Repeat("длинный/", 100) + "zone.mm2"
	first := sourceLabel(long, 1)
	second := sourceLabel(long, 2)
	if len(first) > maxSourceLabelBytes || len(second) > maxSourceLabelBytes {
		t.Fatalf("source labels exceed %d bytes: %d, %d", maxSourceLabelBytes, len(first), len(second))
	}
	if first == second {
		t.Fatalf("duplicate entries have identical labels %q", first)
	}
}

func TestInvalidZoneConflictReportIsUTF8AndStrictlyBounded(t *testing.T) {
	entries := make(map[string][]byte, maxImportConflicts)
	long := strings.Repeat("界", 10_000)
	for i := 0; i < maxImportConflicts; i++ {
		entries[fmt.Sprintf("%03d\x01%s.mm2", i, long)] = synthRoom(1, "room")
	}
	_, err := ParseZip(buildZip(t, entries), "long-invalid-zones.zip")
	if err == nil {
		t.Fatal("ParseZip accepted invalid control-character zones")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) != maxImportConflicts {
		t.Fatalf("conflicts = %d, want %d", len(validation.Summary.Conflicts), maxImportConflicts)
	}
	for _, conflict := range validation.Summary.Conflicts {
		if !utf8.ValidString(conflict.Zone) || strings.ContainsRune(conflict.Zone, '\x01') || len(conflict.Zone) > maxConflictLabelBytes {
			t.Fatalf("zone label is not bounded valid UTF-8: %q (%d bytes)", conflict.Zone, len(conflict.Zone))
		}
	}
	report, err := json.Marshal(validation.Summary.Conflicts)
	if err != nil {
		t.Fatalf("marshal conflict report: %v", err)
	}
	if len(report) > maxConflictReportJSONBytes {
		t.Fatalf("conflict JSON = %d bytes, exceeds fixed ceiling %d", len(report), maxConflictReportJSONBytes)
	}
}

func TestSanitizedZoneLabelsRemainDistinct(t *testing.T) {
	data := buildZip(t, map[string][]byte{
		"A\x01B.mm2": synthRoom(1, "first"),
		"A\x02B.mm2": synthRoom(1, "second"),
	})
	_, err := ParseZip(data, "sanitized-zones.zip")
	if err == nil {
		t.Fatal("ParseZip accepted control-character zones")
	}
	validation := importValidation(t, err)
	if len(validation.Summary.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v, want two invalid zones", validation.Summary.Conflicts)
	}
	if validation.Summary.Conflicts[0].Zone == validation.Summary.Conflicts[1].Zone {
		t.Fatalf("sanitized labels collided: %q", validation.Summary.Conflicts[0].Zone)
	}
}

func TestConflictJSONCeilingWithEscapingWorstCase(t *testing.T) {
	// Quotes, slashes and U+2028/U+2029 are the longest JSON encodings left
	// after displayLabel removes control and HTML-escaped runes.
	expanding := strings.Repeat("\"\\\u2028\u2029", 80)
	zone := conflictZoneLabel(expanding)
	source := sourceLabel(expanding, 1)
	other := sourceLabel(expanding, 2)
	maxInt := int(^uint(0) >> 1)
	conflicts := make([]ImportConflict, maxImportConflicts)
	for i := range conflicts {
		conflicts[i] = ImportConflict{
			Kind: "duplicate_room", Zone: zone,
			X: -maxInt - 1, Y: maxInt, L: -maxInt - 1,
			Source: source, OtherSource: other,
		}
	}
	report, err := json.Marshal(conflicts)
	if err != nil {
		t.Fatalf("marshal worst-case conflicts: %v", err)
	}
	if len(report) > maxConflictReportJSONBytes {
		t.Fatalf("worst-case conflict JSON = %d bytes, exceeds ceiling %d", len(report), maxConflictReportJSONBytes)
	}
}

func TestParseZipResolvesTrimmedSeamTargetZone(t *testing.T) {
	source := newTPF0("TMudRoom2")
	source.int8("Tag", 1)
	source.set("AutoMaps.Strings", []string{" Target |go|7"})
	data := buildZip(t, map[string][]byte{
		"Source.mm2": source.bytes(),
		"Target.mm2": synthRoom(7, "target"),
	})
	pa, err := ParseZip(data, "trimmed-seam.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if len(pa.Summary.Unresolved) != 0 {
		t.Fatalf("unresolved = %q, want trimmed target to resolve", pa.Summary.Unresolved)
	}
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

func TestParseZipDecodesCP1251FilenameAndResolvesAutomaps(t *testing.T) {
	// archive/zip exposes the raw, non-UTF-8 bytes in File.Name and marks the
	// entry NonUTF8. The target zone must still match an AutoMaps string decoded
	// from the CP1251 DFM payload.
	targetRoom := newTPF0("TMudRoom2")
	targetRoom.int8("Tag", 7)
	targetRoom.shortStrProp("Hint", "Целевая комната")
	targetRoom.set("AutoMaps.Strings", []string{"Цель|на восток|7"})
	target := targetRoom.bytes()
	data := buildZipCP1251Name(t, "Цель.mm2", target)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	if len(zr.File) != 1 || !zr.File[0].NonUTF8 {
		t.Fatalf("entry NonUTF8 = %v, want true", len(zr.File) == 1 && zr.File[0].NonUTF8)
	}

	pa, err := ParseZip(data, "cp1251.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if got, want := pa.Summary.Zones, []string{"Цель"}; !strSliceEq(got, want) {
		t.Fatalf("zones = %q, want %q", got, want)
	}
	if got := pa.Rooms[0].Zone; got != "Цель" {
		t.Errorf("room zone = %q, want %q", got, "Цель")
	}
	if pa.Summary.SeamCount != 1 || len(pa.Summary.Unresolved) != 0 {
		t.Errorf("seams = %d unresolved = %q, want resolved single seam", pa.Summary.SeamCount, pa.Summary.Unresolved)
	}
}

func TestParseZipPreservesUTF8ASCIIAndNormalizesNFCNames(t *testing.T) {
	nfdZone := norm.NFD.String("Балифой")
	nfcZone := norm.NFC.String(nfdZone)
	if nfdZone == nfcZone {
		t.Fatal("test zone must have distinct NFD and NFC forms")
	}

	source := newTPF0("TMudRoom2")
	source.int8("Tag", 1)
	source.set("AutoMaps.Strings", []string{nfcZone + "|на восток|7"})
	target := synthRoom(7, "UTF-8 target")
	data := buildZip(t, map[string][]byte{
		"ascii.mm2":      source.bytes(),
		nfdZone + ".mm2": target,
	})

	pa, err := ParseZip(data, "utf8-nfc.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if got, want := pa.Summary.Zones, []string{"ascii", nfcZone}; !strSliceEq(got, want) {
		t.Errorf("zones = %q, want %q", got, want)
	}
	if pa.Summary.SeamCount != 1 || len(pa.Summary.Unresolved) != 0 {
		t.Errorf("seams = %d unresolved = %q, want resolved single seam", pa.Summary.SeamCount, pa.Summary.Unresolved)
	}
}

func TestParseZipKeepsValidUTF8WhenFlagUnset(t *testing.T) {
	nfdZone := norm.NFD.String("Балифой")
	nfcZone := norm.NFC.String(nfdZone)
	data := buildZipNonUTF8Name(t, nfdZone+".mm2", synthRoom(1, "room"))

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	if len(zr.File) != 1 || !zr.File[0].NonUTF8 {
		t.Fatalf("entry NonUTF8 = %v, want true", len(zr.File) == 1 && zr.File[0].NonUTF8)
	}

	pa, err := ParseZip(data, "utf8-no-flag.zip")
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if got, want := pa.Summary.Zones, []string{nfcZone}; !strSliceEq(got, want) {
		t.Errorf("zones = %q, want %q", got, want)
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
	if !errors.Is(err, ErrInvalidArchive) || !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("error %v does not expose archive and limit classifications", err)
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
	if pa.Summary.SeamCount != 2 {
		t.Errorf("seam count = %d, want 2 raw occurrences", pa.Summary.SeamCount)
	}
	if len(pa.Summary.Unresolved) != 1 || pa.Summary.Unresolved[0] != "Нет Зоны|туда|9" {
		t.Errorf("unresolved = %v, want [Нет Зоны|туда|9]", pa.Summary.Unresolved)
	}
}
