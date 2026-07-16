package mapper

import "strings"

// Direction letters (canonical). Axis convention (verified in FINDINGS.md):
//   X axis = North/South: N = x-1, S = x+1
//   Y axis = West/East:   W = y-1, E = y+1
//   L (level):            U = l+1, D = l-1

// dirOrder is the canonical iteration order over direction letters, used
// wherever deterministic behavior matters (neighbor search, BFS) instead of Go
// map iteration order.
var dirOrder = []string{"N", "S", "E", "W", "U", "D"}

// Delta is the (dx,dy,dl) grid step for a canonical direction letter.
type Delta struct{ DX, DY, DL int }

// dirDelta maps a canonical direction letter to its grid step.
var dirDelta = map[string]Delta{
	"N": {-1, 0, 0},
	"S": {1, 0, 0},
	"W": {0, -1, 0},
	"E": {0, 1, 0},
	"U": {0, 0, 1},
	"D": {0, 0, -1},
}

// DirDelta returns the grid step for a canonical direction letter and whether it
// is a known direction.
func DirDelta(dir string) (Delta, bool) {
	d, ok := dirDelta[dir]
	return d, ok
}

// dirRU maps a canonical letter to the RU command word used for path output and
// mud_send_command feeding (mirrors rmud_locate.DIR_RU).
var dirRU = map[string]string{
	"N": "с", "S": "ю", "E": "в", "W": "з", "U": "вверх", "D": "вниз",
}

// DirRU returns the RU command word for a canonical direction letter.
func DirRU(dir string) string { return dirRU[dir] }

// oppositeDir maps a direction letter to its reverse (for edge reverse-
// consistency: an edge A→B in dir d means B is entered from direction
// opposite(d)).
var oppositeDir = map[string]string{
	"N": "S", "S": "N", "E": "W", "W": "E", "U": "D", "D": "U",
}

// OppositeDir returns the reverse of a canonical direction letter.
func OppositeDir(dir string) string { return oppositeDir[dir] }

// moveWords maps a movement command token (RU/EN, letters or full words) to a
// canonical direction letter. Mirrors mapimport.dirMap / rmud_locate.DIR_MAP.
// This is the authority for detecting movement commands at the point they are
// written to the MUD (after alias/VM expansion).
var moveWords = map[string]string{
	"n": "N", "с": "N", "север": "N", "north": "N",
	"s": "S", "ю": "S", "юг": "S", "south": "S",
	"e": "E", "в": "E", "восток": "E", "east": "E",
	"w": "W", "з": "W", "запад": "W", "west": "W",
	"u": "U", "вв": "U", "вверх": "U", "up": "U",
	"d": "D", "вн": "D", "вниз": "D", "down": "D",
}

// MoveDir returns the canonical direction letter for a single command token, or
// ("", false) if the token is not a movement command. The token is matched
// case-insensitively and whitespace-trimmed. Only a bare direction command
// counts — a multi-word command (e.g. "открыть дверь") is not a move.
func MoveDir(cmd string) (string, bool) {
	tok := strings.ToLower(strings.TrimSpace(cmd))
	if tok == "" || strings.ContainsAny(tok, " \t") {
		return "", false
	}
	d, ok := moveWords[tok]
	return d, ok
}
