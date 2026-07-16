package mapimport

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Room is the normalized parse of one TMudRoom2 object. Field values and null
// semantics mirror the golden oracle (rmud_index.json):
//   - Tag / ImageIndex / BColor are pointers so an absent DFM property is null,
//     not 0. BColor may be an int RGB or a "cl..." color-ident string, so it is
//     kept as `any` (the raw parsed value).
//   - X/Y/L are signed int8 grid coords (negatives kept). DX/DY/DL are the
//     logical "home" coords (not checked by the golden but stored/computed).
//   - Ch/EDirs/Doors are ordered direction-letter lists (sorted N,S,E,W,U,D-ish
//     — actually sorted lexically to match the oracle's sorted() output).
//   - Automaps are the raw "Zone|command|tag" seam strings, unmodified.
type Room struct {
	Zone       string
	RI         int
	Tag        *int
	Hint       string
	Desc       string
	Exits      string
	EDirs      []string
	Doors      []string
	Ch         []string
	X          int
	Y          int
	L          int
	DX         int
	DY         int
	DL         int
	Note       string
	BColor     any // nil, int, or string ("clRed"); preserved as parsed
	IsDT       bool
	Pipe       bool
	ImageIndex *int
	Automaps   []string

	// Fingerprint is computed (hint+desc+sorted exits, normalized). Not in the
	// oracle but stored for the tracker's resolve index.
	Fingerprint string
}

// dirMap maps direction words/letters (RU + EN) to a canonical letter. Mirrors
// rmud_locate.DIR_MAP.
var dirMap = map[string]string{
	"n": "N", "с": "N", "север": "N", "north": "N",
	"s": "S", "ю": "S", "юг": "S", "south": "S",
	"e": "E", "в": "E", "восток": "E", "east": "E",
	"w": "W", "з": "W", "запад": "W", "west": "W",
	"u": "U", "вв": "U", "вверх": "U", "up": "U",
	"d": "D", "вн": "D", "вниз": "D", "down": "D",
}

// chOrder is the 6 connectivity flags in the order they map to bits ChN..ChD.
var chOrder = []struct {
	dir   string
	field string
}{
	{"N", "ChN"},
	{"S", "ChS"},
	{"E", "ChE"},
	{"W", "ChW"},
	{"U", "ChU"},
	{"D", "ChD"},
}

// chBit returns the bitmask bit for a direction letter (N..D => bits 0..5),
// matching chOrder.
func chBit(dir string) int {
	for i, c := range chOrder {
		if c.dir == dir {
			return 1 << i
		}
	}
	return 0
}

// ChMask packs the Ch direction list into the 6-bit bitmask stored in the DB.
func (r Room) ChMask() int {
	mask := 0
	for _, d := range r.Ch {
		mask |= chBit(d)
	}
	return mask
}

var wordRE = regexp.MustCompile(`[а-яёa-z0-9]+`)
var parenRE = regexp.MustCompile(`\(([^)]*)\)`)
var wsRE = regexp.MustCompile(`\s+`)

// normExits parses an exits string into (canonical dirs, door dirs). Mirrors
// rmud_locate.norm_exits. Parenthesised groups are doors.
func normExits(exits string) (dirs []string, doors []string) {
	if exits == "" {
		return nil, nil
	}
	lower := strings.ToLower(exits)
	dirSet := map[string]bool{}
	doorSet := map[string]bool{}
	for _, m := range parenRE.FindAllStringSubmatch(exits, -1) {
		for _, w := range wordRE.FindAllString(strings.ToLower(m[1]), -1) {
			if d, ok := dirMap[w]; ok {
				doorSet[d] = true
			}
		}
	}
	for _, w := range wordRE.FindAllString(lower, -1) {
		if d, ok := dirMap[w]; ok {
			dirSet[d] = true
		}
	}
	return sortedKeys(dirSet), sortedKeys(doorSet)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// asInt extracts an int from a parsed DFM value that may be int, int64, or nil.
func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	}
	return 0, false
}

func asIntPtr(v any) *int {
	if n, ok := asInt(v); ok {
		return &n
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// dtColors are the BColor values that mark a Death Trap (clRed/clMaroon and
// their int equivalents), mirroring rmud_locate.
var dtColors = map[string]bool{"clRed": true, "clMaroon": true}

func isDeathTrap(bcolor any) bool {
	switch t := bcolor.(type) {
	case string:
		return dtColors[t]
	case int:
		return t == 255 || t == 128
	case int64:
		return t == 255 || t == 128
	}
	return false
}

// roomFromProps normalizes a parsed TMudRoom2 property map into a Room. zone must
// already be NFC-normalized; ri is the room's index within its file.
func roomFromProps(zone string, ri int, p map[string]any) Room {
	hint := asString(p["Hint"])
	desc := asString(p["Description"])
	exits := asString(p["Exits"])
	edirs, doors := normExits(exits)

	var ch []string
	for _, c := range chOrder {
		if asBool(p[c.field]) {
			ch = append(ch, c.dir)
		}
	}
	sort.Strings(ch)

	x, _ := asInt(p["X"])
	y, _ := asInt(p["Y"])
	l, _ := asInt(p["L"]) // absent => 0, matching Python p.get("L", 0)

	// DX/DY/DL: logical/home coords. Not in the oracle; default to X/Y/L when the
	// property is absent so a room without displacement has coherent home coords.
	dx := x
	dy := y
	dl := l
	if n, ok := asInt(p["DX"]); ok {
		dx = n
	}
	if n, ok := asInt(p["DY"]); ok {
		dy = n
	}
	if n, ok := asInt(p["DL"]); ok {
		dl = n
	}

	bcolor := p["BColor"] // may be nil, int, or "cl..." string — kept as-is

	automaps := extractAutomaps(p)

	r := Room{
		Zone:       zone,
		RI:         ri,
		Tag:        asIntPtr(p["Tag"]),
		Hint:       hint,
		Desc:       desc,
		Exits:      exits,
		EDirs:      nonNil(edirs),
		Doors:      nonNil(doors),
		Ch:         nonNil(ch),
		X:          x,
		Y:          y,
		L:          l,
		DX:         dx,
		DY:         dy,
		DL:         dl,
		Note:       asString(p["Note"]),
		BColor:     bcolor,
		IsDT:       isDeathTrap(bcolor),
		Pipe:       asBool(p["Pipe"]),
		ImageIndex: asIntPtr(p["ImageIndex"]),
		Automaps:   nonNil(automaps),
	}
	r.Fingerprint = computeFingerprint(hint, desc, edirs)
	return r
}

// extractAutomaps pulls the seam strings from the AutoMaps set/list. The DFM
// stores them under "AutoMaps.Strings" (a set) or occasionally "AutoMaps".
func extractAutomaps(p map[string]any) []string {
	v := p["AutoMaps.Strings"]
	if v == nil {
		v = p["AutoMaps"]
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Fingerprint computes the room fingerprint from a live room-event's raw fields
// exactly as the import path does, so the tracker's resolve-index matches. It
// parses the exits string into canonical direction letters (door markers/parens
// stripped) and delegates to computeFingerprint. Use this — never fork a second
// slightly-different normalization, or resync silently breaks.
func Fingerprint(hint, desc, exits string) string {
	edirs, _ := normExits(exits)
	return computeFingerprint(hint, desc, edirs)
}

// NormExits exposes the exits parser (canonical dirs, door dirs) so the tracker
// reconciler and MCP tools reuse the exact same door-marker handling.
func NormExits(exits string) (dirs []string, doors []string) {
	return normExits(exits)
}

// computeFingerprint hashes normalized hint + desc + sorted exit dirs. Door
// markers/parens are already stripped by normExits (edirs is dir letters only).
// Text is NFC-normalized, trimmed, whitespace-collapsed, lowercased.
func computeFingerprint(hint, desc string, edirs []string) string {
	h := normText(hint)
	d := normText(desc)
	dirs := append([]string(nil), edirs...)
	sort.Strings(dirs)
	joined := h + "\x1f" + d + "\x1f" + strings.Join(dirs, ",")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

func normText(s string) string {
	s = norm.NFC.String(s)
	s = strings.ToLower(s)
	s = wsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
