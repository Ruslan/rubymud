package mapimport

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var tpf0 = []byte("TPF0")

// Decompression-bomb guards. The compressed-upload cap in the web layer does not
// bound *uncompressed* size, so a small zip with a deflate-bombed .mm2 entry
// could OOM the live process. These caps are far above the real corpus (81
// files, largest ~88KB uncompressed) while staying bounded. Exceeding any of
// them returns a clean import error rather than allocating unboundedly.
const (
	maxMM2Entries    = 5000      // member-count cap
	maxMM2EntryBytes = 16 << 20  // per-entry uncompressed cap (16 MB)
	maxMM2TotalBytes = 256 << 20 // total uncompressed cap across the archive (256 MB)
	// maxImportedRooms bounds parsed object accumulation. It is far above the
	// current 8,034-room corpus while preventing a tiny repeated-object stream
	// from expanding into millions of Go structs and map entries.
	maxImportedRooms = 100_000
	// maxImportConflicts keeps an invalid upload's error response useful without
	// allowing a crafted archive to turn a 4xx into an unbounded JSON payload.
	maxImportConflicts = 100
	// maxSourceLabelBytes bounds every filename copied into a REST error report.
	maxSourceLabelBytes = 256
	// maxConflictLabelBytes applies the same hard ceiling to logical zone names.
	maxConflictLabelBytes = 256
	// maxConflictReportJSONBytes bounds the serialized conflict list after JSON
	// escaping. Every list is capped and all copied labels are display-safe.
	maxConflictReportJSONBytes = 192 << 10
)

var (
	// ErrInvalidArchive classifies user-supplied archive failures without
	// exposing archive/zip's implementation errors to REST clients.
	ErrInvalidArchive = errors.New("invalid map archive")
	// ErrArchiveLimit additionally identifies a resource-limit refusal.
	ErrArchiveLimit = errors.New("map archive limit exceeded")
)

// ParseMM2 parses one .mm2 file (a concatenation of independent TPF0 room
// streams) into rooms for the given zone. zone must already be NFC-normalized.
// A room whose stream fails to parse is skipped (mirrors the Python parser's
// try/except-continue), so one corrupt room never aborts a whole zone.
func ParseMM2(zone string, data []byte) []Room {
	rooms, _ := parseMM2Limited(zone, data, -1)
	return rooms
}

// parseMM2Limited is ParseMM2 with an optional successful-room cap. A negative
// limit is unbounded, preserving ParseMM2's public behavior. On overflow it
// stops immediately before appending the next room, so the archive path never
// accumulates more than its configured global room limit.
func parseMM2Limited(zone string, data []byte, limit int) ([]Room, bool) {
	var rooms []Room
	ri := 0
	// Find every "TPF0" marker; parse the object starting right after it.
	for off := 0; ; {
		idx := bytes.Index(data[off:], tpf0)
		if idx < 0 {
			break
		}
		start := off + idx + len(tpf0)
		r := &reader{d: data, i: start}
		props, err := parseObject(r)
		if err != nil {
			off = start
			continue
		}
		// Only accept genuine TMudRoom2 objects; a "TPF0" byte run inside some
		// other embedded stream would otherwise parse into a junk empty Room.
		if cls, _ := props["_class"].(string); cls != "TMudRoom2" {
			off = start
			continue
		}
		if limit >= 0 && len(rooms) >= limit {
			return rooms, true
		}
		rooms = append(rooms, roomFromProps(zone, ri, props))
		ri++
		off = start
	}
	return rooms, false
}

// ImportSummary reports the outcome of importing an archive into a map set.
type ImportSummary struct {
	Name               string
	ZoneCount          int
	RoomCount          int
	SeamCount          int
	Zones              []string         // canonical zone names, in import order
	Unresolved         []string         // seam strings whose target zone/tag is not in the set
	Conflicts          []ImportConflict // validation failures; import is refused when non-empty
	ConflictsTruncated bool             // further conflicts exist beyond the response cap
}

// ImportConflict identifies an invalid archive input before anything reaches
// storage. Source is the first entry that claimed the identity and OtherSource
// is the later colliding entry. duplicate_room may have identical source names
// when the two rooms occur in one .mm2 file.
type ImportConflict struct {
	Kind        string `json:"kind"`
	Zone        string `json:"zone,omitempty"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	L           int    `json:"l"`
	Source      string `json:"source,omitempty"`
	OtherSource string `json:"other_source,omitempty"`
}

// ValidationError is a user-fixable archive error. Callers can expose its
// bounded Summary.Conflicts as JSON while keeping ordinary ParseZip callers on
// the usual Go error contract.
type ValidationError struct {
	Message string
	Summary ImportSummary
	kind    error
}

func (e *ValidationError) Error() string { return e.Message }

func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidArchive || target == e.kind
}

func newValidationError(name, message string, kind error) *ValidationError {
	return &ValidationError{Message: message, Summary: ImportSummary{Name: name}, kind: kind}
}

// ParsedArchive is the full parse of an archive: all rooms across all zones plus
// a summary. Rooms are in the same order the golden oracle uses — zones sorted
// by filename, rooms dense within each zone by ri.
type ParsedArchive struct {
	Rooms   []Room
	Summary ImportSummary
}

type zipMM2Entry struct {
	zone     string
	source   string
	sortName string
	body     []byte
	rooms    []Room
}

// ParseZip reads a .zip archive, recursively collects every *.mm2 (ignoring all
// other files), parses each into rooms, and assembles one map set. The zone name
// is the .mm2 basename (without extension), TrimSpace+NFC-normalized. Zones are
// processed in sorted-filename order to match the reference index build.
func ParseZip(data []byte, name string) (*ParsedArchive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, newValidationError(name, "archive is not a valid ZIP file", nil)
	}

	// Collect .mm2 entries. Filenames in the archive may themselves be CP1251 or
	// NFD; the zone name is derived from the basename, TrimSpace'd and NFC-normalized.
	var entries []zipMM2Entry
	var totalBytes int64
	for ordinal, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		decodedName := decodeZipEntryName(f)
		base := path.Base(decodedName)
		if !strings.EqualFold(path.Ext(base), ".mm2") {
			continue
		}
		if len(entries) >= maxMM2Entries {
			return nil, newValidationError(name, fmt.Sprintf("archive has too many .mm2 files (limit %d)", maxMM2Entries), ErrArchiveLimit)
		}
		// Cheap pre-check against the declared uncompressed size (a bomb often
		// lies here, so it is not sufficient alone — the LimitReader below is the
		// real defense).
		if f.UncompressedSize64 > maxMM2EntryBytes {
			return nil, newValidationError(name, fmt.Sprintf("entry %q exceeds the uncompressed size limit", sourceLabel(decodedName, ordinal+1)), ErrArchiveLimit)
		}
		zone := canonicalZoneName(strings.TrimSuffix(base, path.Ext(base)))
		rc, err := f.Open()
		if err != nil {
			return nil, newValidationError(name, fmt.Sprintf("entry %q could not be read", sourceLabel(decodedName, ordinal+1)), nil)
		}
		// Read up to the cap +1 so an over-cap entry is detected (not silently
		// truncated) rather than allocating whatever the deflate stream expands to.
		body, err := io.ReadAll(io.LimitReader(rc, maxMM2EntryBytes+1))
		rc.Close()
		if err != nil {
			return nil, newValidationError(name, fmt.Sprintf("entry %q could not be read", sourceLabel(decodedName, ordinal+1)), nil)
		}
		if int64(len(body)) > maxMM2EntryBytes {
			return nil, newValidationError(name, fmt.Sprintf("entry %q exceeds the uncompressed size limit", sourceLabel(decodedName, ordinal+1)), ErrArchiveLimit)
		}
		totalBytes += int64(len(body))
		if totalBytes > maxMM2TotalBytes {
			return nil, newValidationError(name, fmt.Sprintf("archive exceeds the total uncompressed size limit (%d bytes)", maxMM2TotalBytes), ErrArchiveLimit)
		}
		entries = append(entries, zipMM2Entry{zone: zone, source: sourceLabel(decodedName, ordinal+1), sortName: decodedName, body: body})
	}
	if len(entries) == 0 {
		return nil, newValidationError(name, "archive contains no .mm2 map files", nil)
	}

	// Keep both successful imports and conflict reports deterministic. source is a
	// tiebreaker only; valid archives retain the historical zone ordering.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].zone != entries[j].zone {
			return entries[i].zone < entries[j].zone
		}
		return entries[i].sortName < entries[j].sortName
	})

	pa := &ParsedArchive{Summary: ImportSummary{Name: name}}
	for i := range entries {
		remaining := maxImportedRooms - len(pa.Rooms)
		var exceeded bool
		entries[i].rooms, exceeded = parseMM2Limited(entries[i].zone, entries[i].body, remaining)
		pa.Rooms = append(pa.Rooms, entries[i].rooms...)
		pa.Summary.Zones = append(pa.Summary.Zones, entries[i].zone)
		if exceeded {
			pa.Summary.RoomCount = len(pa.Rooms)
			return nil, newValidationError(name, fmt.Sprintf("archive contains more than %d rooms", maxImportedRooms), ErrArchiveLimit)
		}
	}
	pa.Summary.ZoneCount = len(pa.Summary.Zones)
	pa.Summary.RoomCount = len(pa.Rooms)
	validateArchive(pa, entries)
	if len(pa.Summary.Conflicts) > 0 {
		return nil, &ValidationError{Message: "archive has conflicting or empty map entries", Summary: pa.Summary}
	}

	resolveSeams(pa)
	return pa, nil
}

// decodeZipEntryName decodes a legacy filename before path.Base is applied, so
// both directory components and basenames stay readable in conflict reports.
func decodeZipEntryName(f *zip.File) string {
	name := f.Name
	if f.NonUTF8 && !utf8.ValidString(name) {
		name = decodeCP1251([]byte(name))
	}
	return norm.NFC.String(name)
}

func sourceLabel(name string, ordinal int) string {
	sum := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf(" [entry %d %x]", ordinal, sum[:6])
	display, _ := displayLabel(name)
	return truncateUTF8Bytes(display, maxSourceLabelBytes-len(suffix)) + suffix
}

func conflictZoneLabel(zone string) string {
	sum := sha256.Sum256([]byte(zone))
	display, sanitized := displayLabel(zone)
	if !sanitized && len(display) <= maxConflictLabelBytes {
		return display
	}
	suffix := fmt.Sprintf(" [%x]", sum[:6])
	return truncateUTF8Bytes(display, maxConflictLabelBytes-len(suffix)) + suffix
}

func displayLabel(value string) (string, bool) {
	valid := strings.ToValidUTF8(value, "?")
	changed := valid != value
	display := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '<' || r == '>' || r == '&' {
			changed = true
			return '?'
		}
		return r
	}, valid)
	return display, changed
}

func truncateUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

func canonicalZoneName(base string) string {
	return strings.TrimSpace(norm.NFC.String(base))
}

func validZoneName(zone string) bool {
	if zone == "" || utf8.RuneCountInString(zone) > 256 {
		return false
	}
	for _, r := range zone {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func addConflict(summary *ImportSummary, conflict ImportConflict) {
	if len(summary.Conflicts) >= maxImportConflicts {
		summary.ConflictsTruncated = true
		return
	}
	summary.Conflicts = append(summary.Conflicts, conflict)
}

// validateArchive mirrors the physical uniqueness that storage enforces for
// rooms, but does it before CreateMapSet so a bad upload cannot create a partial
// map set or devolve into a database-specific 500.
func validateArchive(pa *ParsedArchive, entries []zipMM2Entry) {
	type roomKey struct {
		zone    string
		x, y, l int
	}
	firstZone := make(map[string]string, len(entries))
	firstRoom := make(map[roomKey]string, len(pa.Rooms))
	for _, entry := range entries {
		if !validZoneName(entry.zone) {
			addConflict(&pa.Summary, ImportConflict{Kind: "invalid_zone", Zone: conflictZoneLabel(entry.zone), Source: entry.source})
			continue
		}
		if first, ok := firstZone[entry.zone]; ok {
			addConflict(&pa.Summary, ImportConflict{Kind: "duplicate_zone", Zone: conflictZoneLabel(entry.zone), Source: first, OtherSource: entry.source})
		} else {
			firstZone[entry.zone] = entry.source
		}
		if len(entry.rooms) == 0 {
			addConflict(&pa.Summary, ImportConflict{Kind: "empty_zone", Zone: conflictZoneLabel(entry.zone), Source: entry.source})
			continue
		}
		for _, room := range entry.rooms {
			key := roomKey{zone: room.Zone, x: room.X, y: room.Y, l: room.L}
			if first, ok := firstRoom[key]; ok {
				addConflict(&pa.Summary, ImportConflict{Kind: "duplicate_room", Zone: conflictZoneLabel(room.Zone), X: room.X, Y: room.Y, L: room.L, Source: first, OtherSource: entry.source})
				continue
			}
			firstRoom[key] = entry.source
		}
	}
}

// resolveSeams counts every raw automaps occurrence (the authoritative importer
// materializes all of them) and records unresolved occurrences separately. A seam
// "Zone|command|tag" resolves when the set contains a room in the TrimSpace+NFC
// target zone with a matching Tag.
func resolveSeams(pa *ParsedArchive) {
	// Index tags per zone.
	type tagKey struct {
		zone string
		tag  int
	}
	have := map[tagKey]bool{}
	for _, r := range pa.Rooms {
		if r.Tag != nil {
			have[tagKey{r.Zone, *r.Tag}] = true
		}
	}
	seen := map[string]bool{}
	for i := range pa.Rooms {
		for _, s := range pa.Rooms[i].Automaps {
			// Every raw occurrence is materialized, including malformed/dead targets.
			// SeamCount therefore describes the authoritative row count; Unresolved
			// separately describes which of those rows cannot currently be traversed.
			pa.Summary.SeamCount++
			zone, _, tag, ok := parseSeam(s)
			if !ok {
				pa.Summary.Unresolved = appendUnique(pa.Summary.Unresolved, seen, s)
				continue
			}
			if have[tagKey{canonicalZoneName(zone), tag}] {
			} else {
				pa.Summary.Unresolved = appendUnique(pa.Summary.Unresolved, seen, s)
			}
		}
	}
}

func appendUnique(list []string, seen map[string]bool, s string) []string {
	if seen[s] {
		return list
	}
	seen[s] = true
	return append(list, s)
}

// parseSeam splits "TargetZone|command|TargetTag" (a trailing "|" is tolerated).
func parseSeam(s string) (zone, cmd string, tag int, ok bool) {
	parts := strings.Split(s, "|")
	if len(parts) < 3 {
		return "", "", 0, false
	}
	t, err := parseIntStrict(parts[2])
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], parts[1], t, true
}

func parseIntStrict(s string) (int, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, errBadInt
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

type constErr string

func (e constErr) Error() string { return string(e) }

const errBadInt = constErr("not an integer")
