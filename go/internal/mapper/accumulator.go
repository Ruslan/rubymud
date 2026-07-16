package mapper

import "strings"

// maxBlockLines bounds how far back the accumulator looks to reconstruct a room
// block when the "[ Exits: ]" line arrives. The 3 fixture blocks are ~12 lines
// (header + up to ~11 minimap/desc lines + exits); 32 is a safe cap that keeps
// memory bounded and the reconstruction cheap.
const maxBlockLines = 32

// Accumulator is a stateful room-block detector for the incoming line pipeline.
// It is NOT safe for concurrent use; the session calls Feed from its single
// read loop. Feed is cheap for non-candidate lines: it appends the line to a
// bounded ring and only runs block reconstruction when a line looks like an
// exits line.
type Accumulator struct {
	// ring holds the last maxBlockLines lines (already ANSI-stripped, CR removed).
	ring []string
}

// NewAccumulator builds an empty accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{ring: make([]string, 0, maxBlockLines)}
}

// Feed processes one incoming line that is ALREADY ANSI/control-stripped (the
// session hands its per-line `plainText`, computed once in processLine — the
// accumulator must not strip a second time on the hot path). When the line
// completes a room block it returns the parsed RoomEvent and ok=true; otherwise
// ok=false. The hot-path guard: only when the line starts with "[ Exits:" do we
// attempt the (rare) block reconstruction; every other line is a cheap ring
// append. A trailing CR (which the exits line may carry) is trimmed cheaply.
func (a *Accumulator) Feed(stripped string) (RoomEvent, bool) {
	line := strings.TrimRight(stripped, "\r")

	if !looksLikeExitsLine(strings.TrimSpace(line)) {
		a.push(line)
		return RoomEvent{}, false
	}

	inner, ok := ParseExits(line)
	if !ok {
		a.push(line)
		return RoomEvent{}, false
	}

	ev, built := a.reconstruct(inner)
	// Reset the ring after emitting: the next block starts fresh. This also
	// prevents a stray later exits line from re-consuming an old header.
	a.ring = a.ring[:0]
	if !built {
		return RoomEvent{}, false
	}
	return ev, true
}

func (a *Accumulator) push(line string) {
	if len(a.ring) >= maxBlockLines {
		// drop oldest (shift) — bounded, cheap for a small cap
		copy(a.ring, a.ring[1:])
		a.ring = a.ring[:len(a.ring)-1]
	}
	a.ring = append(a.ring, line)
}

// reconstruct walks the buffered lines to assemble a RoomEvent for a just-seen
// exits line. The block, top to bottom, is: header (room name) → N "minimap +
// desc" lines → exits. We scan backward from the end of the ring, collecting
// candidate body lines until we hit a blank separator or the ring start; the
// first non-blank line of the block is the header, the rest are minimap/desc.
func (a *Accumulator) reconstruct(inner string) (RoomEvent, bool) {
	// Collect contiguous non-blank lines immediately preceding the exits line.
	var block []string
	for i := len(a.ring) - 1; i >= 0; i-- {
		l := a.ring[i]
		if strings.TrimSpace(l) == "" {
			break
		}
		block = append(block, l)
	}
	if len(block) == 0 {
		// No header/body captured (e.g. pipe corridor with no block). Emit an
		// exits-only event so the tracker can still reconcile on exits — but with
		// an empty hint (the tracker treats empty-hint events as pipe-safe).
		return RoomEvent{Exits: inner}, true
	}
	// block is reversed (bottom-up); reverse to top-down.
	for l, r := 0, len(block)-1; l < r; l, r = l+1, r-1 {
		block[l], block[r] = block[r], block[l]
	}

	// Locate the header: the block is [preamble?] header [minimap+desc lines].
	// The header is the last non-minimap line immediately preceding the first
	// minimap-prefixed line, so a following/teleport preamble above it (e.g. "Вы
	// последовали за ...") is excluded. If there is no minimap line at all, the
	// first line is the header (brief/dark room with no map body).
	firstMap := -1
	for i, l := range block {
		if isMinimapPrefixLine(l) {
			firstMap = i
			break
		}
	}
	headerIdx := 0
	if firstMap > 0 {
		headerIdx = firstMap - 1
	}

	hint := strings.TrimSpace(block[headerIdx])
	var descParts []string
	for _, l := range block[headerIdx+1:] {
		prose := stripMinimapColumn(l)
		if prose != "" {
			descParts = append(descParts, prose)
		}
	}
	desc := strings.Join(descParts, " ")
	return RoomEvent{Hint: hint, Desc: desc, Exits: inner}, true
}
