package mapper

import "strings"

// Accumulator is a stateful room-block detector for the incoming line pipeline.
// It is NOT safe for concurrent use; the session reaches it through
// SpecDetector.Feed from its single read loop. Feeding is cheap for non-candidate
// lines: it appends the line to a
// bounded ring and only runs block reconstruction when a line looks like an
// exits line.
//
// All format rules — the exits guard/regex, the block back-scan stops, the
// left-column strip and the header rule — come from the Profile handed to
// NewAccumulator, which is captured by value at construction. See profile.go.
// The Profile's rule methods take pointer receivers, so reaching them through
// the addressable a.profile field costs nothing per call; nothing is duplicated
// out of the profile onto this struct, so nothing can drift from it.
type Accumulator struct {
	profile Profile
	// ring holds aligned stripped/raw forms of the last profile.ringBound() lines.
	ring []bufferedLine
	// suppressUntilBoundary swallows the first block this accumulator would emit,
	// unless it first sees a block boundary. It exists for the profile-swap path:
	// a replacement accumulator starts with an EMPTY ring, so if the swap lands
	// mid-block the exits line still arrives and still "completes" a block — one
	// with no header and no body. That is not a harmless missed block: it emits an
	// empty-hint event, which the tracker reads as a pipe corridor and answers
	// with "yellow / no hint to reconcile", demoting a confirmed green player and
	// blocking «Записать комнату» until the next clean block. Swallowing it costs
	// at most the one block that was already torn in half.
	suppressUntilBoundary bool
}

type bufferedLine struct {
	stripped string
	raw      string
}

// NewAccumulator builds an empty accumulator driven by the given profile. Use
// BuiltinRMUD() for the historical (RMUD) behaviour.
func NewAccumulator(profile Profile) *Accumulator {
	a := &Accumulator{profile: profile}
	a.ring = make([]bufferedLine, 0, a.profile.ringBound())
	return a
}

// Profile returns the profile this accumulator was built with.
func (a *Accumulator) Profile() Profile { return a.profile }

// AtBlockBoundary reports whether the accumulator is currently BETWEEN blocks
// rather than part-way through one: either nothing has been buffered since the
// last emit, or the most recent line was a block stop. The profile-swap path
// asks the OUTGOING accumulator this (under its own profile's stop rules, which
// are the rules those lines were read with) to decide whether the replacement
// needs to suppress a torn block.
func (a *Accumulator) AtBlockBoundary() bool {
	if len(a.ring) == 0 {
		return true
	}
	return a.profile.isBlockStop(a.ring[len(a.ring)-1].stripped)
}

// SuppressUntilBoundary makes this accumulator swallow the next block it would
// emit, unless it sees a block boundary first (in which case nothing is
// swallowed — the boundary proves the block it is now collecting is whole).
// Used only when replacing a mid-block accumulator; see the field comment.
func (a *Accumulator) SuppressUntilBoundary() { a.suppressUntilBoundary = true }

// Reset drops in-flight state — the buffered ring and any armed swap
// suppression — without changing the profile. After Reset the accumulator
// behaves exactly as a freshly constructed one, which is the RoomDetector.Reset
// contract.
//
// NO PRODUCTION CALLER TODAY. Every path that would want it (connect, profile
// swap, reconnect) builds a FRESH detector instead, so there is nothing stale to
// drop. It exists because it is part of the interface every contributed detector
// must implement, and the conformance suite exercises it (reset_returns_to_fresh)
// — a contributor whose Reset is wrong finds out from their own test run rather
// than from a future caller that starts calling it. Do not add a call site to
// justify it; the interface is the justification.
//
// The ring is truncated rather than reallocated: capacity is the profile's
// ringBound and re-earning it would allocate.
func (a *Accumulator) Reset() {
	a.ring = a.ring[:0]
	a.suppressUntilBoundary = false
}

// Feed processes one already ANSI/control-stripped line for compatibility with
// plain-text callers. The live SpecDetector uses FeedLine so ANSI-aware header
// rules also receive Raw. When the line
// completes a room block it returns the parsed RoomEvent and ok=true; otherwise
// ok=false.
//
// HOT PATH. This runs for every line the MUD sends. For a non-exits line the
// entire cost is: one TrimRight, one TrimSpace (both return subslices), one
// literal prefix compare in LooksLikeExits, and one ring append — no regex and
// no allocation. TestFeedNonExitsLineDoesNotAllocate and
// BenchmarkFeedNonExitsLine are the permanent guards on that; keep them green
// rather than trusting this comment. A trailing CR (which the exits line may
// carry) is trimmed cheaply.
func (a *Accumulator) Feed(stripped string) (RoomEvent, bool) {
	return a.FeedLine(StrippedLine(stripped))
}

// FeedLine is the raw-aware form used by SpecDetector. Feed(string) remains as
// the compatibility surface for callers and fixtures that only have plain text.
func (a *Accumulator) FeedLine(input Line) (RoomEvent, bool) {
	line := strings.TrimRight(input.Stripped, "\r")
	raw := input.Raw
	if raw == "" {
		raw = line
	} else {
		raw = strings.TrimRight(raw, "\r")
	}
	buffered := bufferedLine{stripped: line, raw: raw}

	if !a.profile.LooksLikeExits(strings.TrimSpace(line)) {
		a.push(buffered)
		return RoomEvent{}, false
	}

	inner, ok := a.profile.ParseExits(line)
	if !ok {
		a.push(buffered)
		return RoomEvent{}, false
	}

	ev, built := a.reconstruct(inner)
	// Reset the ring after emitting: the next block starts fresh. This also
	// prevents a stray later exits line from re-consuming an old header — which
	// would emit a STALE hint instead of the empty-hint pipe event, and so
	// mis-localize the tracker. TestRingResetAfterEmit covers it.
	a.ring = a.ring[:0]
	if a.suppressUntilBoundary {
		// A block torn in half by a profile swap. Drop it (the ring is already
		// reset) and arm normally from here: at most ONE block is lost, and the
		// tracker never sees the bogus empty-hint event it would otherwise infer a
		// pipe corridor from.
		a.suppressUntilBoundary = false
		return RoomEvent{}, false
	}
	if !built {
		return RoomEvent{}, false
	}
	return ev, true
}

func (a *Accumulator) push(line bufferedLine) {
	// A boundary proves whatever comes next is a whole block, so the swap-time
	// suppression can be lifted without losing anything. The bool is tested first
	// so the common (not-suppressing) case pays one branch and never the prompt
	// regex — this is the incoming-line hot path.
	if a.suppressUntilBoundary && a.profile.isBlockStop(line.stripped) {
		a.suppressUntilBoundary = false
	}
	if max := a.profile.ringBound(); len(a.ring) >= max {
		// drop oldest (shift) — bounded, cheap for a small cap
		copy(a.ring, a.ring[1:])
		a.ring = a.ring[:len(a.ring)-1]
	}
	a.ring = append(a.ring, line)
}

// reconstruct walks the buffered lines to assemble a RoomEvent for a just-seen
// exits line. The block, top to bottom, is: [preamble?] header → N "left column
// + desc" lines → exits. We scan backward from the end of the ring, collecting
// candidate body lines until a profile block stop fires (a blank line or a
// prompt line) or we reach the ring start; the profile's header rule then picks
// the room name out of the collected block.
//
// MaxLines needs no check here: the ring is already capped at it, so the scan
// cannot collect more. See StopMaxLines.
//
// This runs once per completed room block, never per incoming line, so the
// prompt regex and the strip work stay off the hot path.
func (a *Accumulator) reconstruct(inner string) (RoomEvent, bool) {
	// Collect the lines immediately preceding the exits line, newest first.
	var block []bufferedLine
	for i := len(a.ring) - 1; i >= 0; i-- {
		l := a.ring[i]
		if a.profile.isBlockStop(l.stripped) {
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

	hint, bodyStart, found := a.profile.selectHeader(block)
	if !found {
		return RoomEvent{Exits: inner}, true
	}
	var descParts []string
	for _, l := range block[bodyStart:] {
		prose := a.profile.StripColumn(l.stripped)
		if prose != "" {
			descParts = append(descParts, prose)
		}
	}
	desc := strings.Join(descParts, " ")
	return RoomEvent{Hint: hint, Desc: desc, Exits: inner}, true
}
