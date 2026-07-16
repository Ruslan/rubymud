package mapimport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"golang.org/x/text/encoding/charmap"
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
)

// ParseMM2 parses one .mm2 file (a concatenation of independent TPF0 room
// streams) into rooms for the given zone. zone must already be NFC-normalized.
// A room whose stream fails to parse is skipped (mirrors the Python parser's
// try/except-continue), so one corrupt room never aborts a whole zone.
func ParseMM2(zone string, data []byte) []Room {
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
		rooms = append(rooms, roomFromProps(zone, ri, props))
		ri++
		off = start
	}
	return rooms
}

// ImportSummary reports the outcome of importing an archive into a map set.
type ImportSummary struct {
	Name       string
	ZoneCount  int
	RoomCount  int
	SeamCount  int
	Zones      []string // NFC zone names, in import (sorted-filename) order
	Unresolved []string // seam strings whose target zone/tag is not in the set
	Conflicts  []string // duplicate (zone,x,y,l) coords collapsed on insert
}

// ParsedArchive is the full parse of an archive: all rooms across all zones plus
// a summary. Rooms are in the same order the golden oracle uses — zones sorted
// by filename, rooms dense within each zone by ri.
type ParsedArchive struct {
	Rooms   []Room
	Summary ImportSummary
}

// ParseZip reads a .zip archive, recursively collects every *.mm2 (ignoring all
// other files), parses each into rooms, and assembles one map set. The zone name
// is the .mm2 basename (without extension), NFC-normalized. Zones are processed
// in sorted-filename order to match the reference index build.
func ParseZip(data []byte, name string) (*ParsedArchive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	// Collect .mm2 entries. Filenames in the archive may themselves be CP1251 or
	// NFD; the zone name is derived from the basename and NFC-normalized.
	type mm2entry struct {
		zone string
		body []byte
	}
	var entries []mm2entry
	var totalBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		if !strings.EqualFold(path.Ext(base), ".mm2") {
			continue
		}
		if len(entries) >= maxMM2Entries {
			return nil, fmt.Errorf("archive has too many .mm2 files (limit %d)", maxMM2Entries)
		}
		// Cheap pre-check against the declared uncompressed size (a bomb often
		// lies here, so it is not sufficient alone — the LimitReader below is the
		// real defense).
		if f.UncompressedSize64 > maxMM2EntryBytes {
			return nil, fmt.Errorf("entry %q too large uncompressed (%d bytes, limit %d)", base, f.UncompressedSize64, maxMM2EntryBytes)
		}
		zone := decodeZoneName(f, strings.TrimSuffix(base, path.Ext(base)))
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		// Read up to the cap +1 so an over-cap entry is detected (not silently
		// truncated) rather than allocating whatever the deflate stream expands to.
		body, err := io.ReadAll(io.LimitReader(rc, maxMM2EntryBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > maxMM2EntryBytes {
			return nil, fmt.Errorf("entry %q exceeds per-file uncompressed limit (%d bytes)", base, maxMM2EntryBytes)
		}
		totalBytes += int64(len(body))
		if totalBytes > maxMM2TotalBytes {
			return nil, fmt.Errorf("archive exceeds total uncompressed limit (%d bytes)", maxMM2TotalBytes)
		}
		entries = append(entries, mm2entry{zone: zone, body: body})
	}

	// Sort by zone name to match the Python sorted(os.listdir()) discovery order
	// that drives rmud_index.json's zone-block ordering.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].zone < entries[j].zone
	})

	pa := &ParsedArchive{Summary: ImportSummary{Name: name}}
	for _, e := range entries {
		rooms := ParseMM2(e.zone, e.body)
		pa.Rooms = append(pa.Rooms, rooms...)
		pa.Summary.Zones = append(pa.Summary.Zones, e.zone)
	}

	resolveSeams(pa)
	pa.Summary.ZoneCount = len(pa.Summary.Zones)
	pa.Summary.RoomCount = len(pa.Rooms)
	return pa, nil
}

// decodeZoneName produces an NFC zone name from a zip entry. Zip stores names as
// UTF-8 when the UTF-8 flag (bit 11) is set, otherwise (legacy) as CP437 or a
// local codepage — this corpus's RAR/zip origin used CP1251-ish bytes. When the
// flag is unset and the UTF-8 basename looks like replacement/garbage, fall back
// to a CP1251 decode of the raw bytes. Finally NFC-normalize (macOS exports NFD).
func decodeZoneName(f *zip.File, utf8Base string) string {
	base := utf8Base
	if f.NonUTF8 {
		// The reader already decoded bytes as CP437 into f.Name; recover the raw
		// bytes by re-encoding CP437, then decode as CP1251 (the mapper's codepage).
		if raw, err := charmap.CodePage437.NewEncoder().Bytes([]byte(base)); err == nil {
			base = decodeCP1251(raw)
		}
	}
	return norm.NFC.String(base)
}

// resolveSeams counts automaps seams that resolve to a room in this set and
// records the rest as unresolved. A seam "Zone|command|tag" resolves when the
// set contains a room in the (NFC) target zone with a matching Tag.
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
			zone, _, tag, ok := parseSeam(s)
			if !ok {
				pa.Summary.Unresolved = appendUnique(pa.Summary.Unresolved, seen, s)
				continue
			}
			if have[tagKey{norm.NFC.String(zone), tag}] {
				pa.Summary.SeamCount++
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
