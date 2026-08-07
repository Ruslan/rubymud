// Package mapper implements the read-only auto-mapper's room-event parser and
// backend position tracker. Everything here is pure logic with no DB or network
// access: the parser runs off the hot path in the session line pipeline, and the
// tracker holds an in-memory index of the active map set.
//
// The room format itself is NOT hardcoded: the exits grammar, the block
// boundaries, the left-column strip and the header rule live in a Profile
// value (profile.go) that the Accumulator takes as a parameter. BuiltinRMUD()
// is the profile matching the RMUD server this mapper was written against.
package mapper

// RoomEvent is the parsed result of one completed room block. It carries the
// header (hint), best-effort description prose (left ASCII-minimap columns
// stripped), and the exits with door markers preserved (e.g. "(N) E S W").
type RoomEvent struct {
	Hint  string
	Desc  string
	Exits string // canonical "[ Exits: ... ]" inner tokens, e.g. "(N) E S W" or "none"
}

// ParseExits normalizes a raw exits line (already ANSI/CR-stripped) into the
// inner token string, or returns ok=false if the line is not an exits line.
// Returns "none" verbatim for a dead-end, else the space-joined tokens.
//
// This is a package-level convenience wrapper pinned to the builtin RMUD
// profile. It has no production callers — the session drives the accumulator,
// which uses its own profile — and exists so the RMUD exits grammar can be
// exercised directly from tests and a REPL. Anything format-aware should call
// Profile.ParseExits instead.
func ParseExits(stripped string) (inner string, ok bool) {
	return builtinRMUD.ParseExits(stripped)
}
