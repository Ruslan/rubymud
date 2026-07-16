// Package mapper implements the read-only auto-mapper's room-event parser and
// backend position tracker (plan §5). Everything here is pure logic with no DB
// or network access: the parser runs off the hot path in the session line
// pipeline, and the tracker holds an in-memory index of the active map set.
package mapper

import (
	"regexp"
	"strings"
)

// RoomEvent is the parsed result of one completed room block. It carries the
// header (hint), best-effort description prose (left ASCII-minimap columns
// stripped), and the exits with door markers preserved (e.g. "(N) E S W").
type RoomEvent struct {
	Hint  string
	Desc  string
	Exits string // canonical "[ Exits: ... ]" inner tokens, e.g. "(N) E S W" or "none"
}

// exitsLineRE matches the exits line AFTER ANSI + CR stripping. It is anchored on
// the "[ Exits: ]" block (server-agnostic), NOT on the RU status prompt. The
// inner group captures the token list (or the literal "none").
var exitsLineRE = regexp.MustCompile(`^\[ Exits:\s*(.*?)\s*\]$`)

// looksLikeExitsLine is the cheap prefix guard used in the hot path before any
// regex work: only a line that begins with "[ Exits:" (post-strip) is a
// candidate for the block-completing regex.
func looksLikeExitsLine(stripped string) bool {
	return strings.HasPrefix(stripped, "[ Exits:")
}

// minimapGlyph reports whether a rune is a minimap-column glyph: room cells
// [ ] [*] [^] [v], corridor connectors - | _ , empty-cell dots, and spaces.
func minimapGlyph(r rune) bool {
	switch r {
	case '[', ']', '*', '^', 'v', '.', '|', '_', '-', ' ':
		return true
	}
	return false
}

// isMinimapPrefixLine reports whether a line begins with a minimap column (a run
// of >=2 minimap glyphs before any prose). Used to locate the block header: the
// header is the last non-minimap line immediately preceding the first minimap
// line, so a following/teleport preamble above it is excluded.
func isMinimapPrefixLine(line string) bool {
	n := 0
	for _, r := range line {
		if !minimapGlyph(r) {
			break
		}
		n++
		if n >= 2 {
			return true
		}
	}
	return false
}

// stripMinimapColumn removes the left ASCII-map column from a "minimap + desc"
// line, returning the right-hand prose (best-effort). The minimap is the leading
// maximal run of minimap glyphs; the description prose is whatever follows. A
// line that is entirely minimap collapses to "".
func stripMinimapColumn(line string) string {
	// Find the end of the leading minimap-glyph run.
	end := 0
	for i, r := range line {
		if minimapGlyph(r) {
			end = i + len(string(r))
			continue
		}
		break
	}
	prose := strings.TrimSpace(line[end:])
	return prose
}

// ParseExits normalizes a raw exits line (already ANSI/CR-stripped) into the
// inner token string, or returns ok=false if the line is not an exits line.
// Returns "none" verbatim for a dead-end, else the space-joined tokens.
func ParseExits(stripped string) (inner string, ok bool) {
	m := exitsLineRE.FindStringSubmatch(strings.TrimSpace(stripped))
	if m == nil {
		return "", false
	}
	inner = strings.TrimSpace(m[1])
	if inner == "" || strings.EqualFold(inner, "none") {
		return "none", true
	}
	return inner, true
}
