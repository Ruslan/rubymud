package mapper

import (
	"sort"
	"strings"

	"rubymud/go/internal/mapimport"
)

// Confidence is the tracker's freshness/position confidence (plan §5 / §8
// "Контракт свежести/потери"): green = anchored (room-event matched a resolved
// cell), yellow = tracker-only (dead-reckoning, no legit resolve), red = lost
// (mismatch, no anchor).
type Confidence string

const (
	Green  Confidence = "green"  // 🟢 anchored
	Yellow Confidence = "yellow" // 🟡 tracker-only
	Red    Confidence = "red"    // 🔴 lost
)

// pendingCap bounds the FIFO pending-moves queue (speedwalks/aliases can enqueue
// many directions before the first room block). Beyond this we drop the head so
// the queue can never grow without bound.
const pendingCap = 64

// Position is the tracker's current best guess. Valid=false means no position at
// all (start of session / never anchored).
type Position struct {
	Valid      bool
	Coord      Coord
	Confidence Confidence
	Reason     string // populated on yellow/red
}

// Tracker is the per-session backend position state machine. It is NOT safe for
// concurrent use; the owning session serializes access (parser feed and move
// detection both run under the session's coordination). It holds the active
// set's index and the FIFO pending-moves queue.
type Tracker struct {
	idx     *Index
	pos     Position
	pending []string // canonical direction letters awaiting confirmation
}

// NewTracker builds a tracker over an index (may be nil => everything degrades
// to red / no-op). The initial position is invalid+red (no anchor yet).
func NewTracker(idx *Index) *Tracker {
	return &Tracker{idx: idx, pos: Position{Confidence: Red, Reason: "no position yet"}}
}

// SetIndex swaps the index (active-set change / import). Position is invalidated
// because coords are only meaningful within one set; the queue is flushed.
func (t *Tracker) SetIndex(idx *Index) {
	t.idx = idx
	t.pos = Position{Confidence: Red, Reason: "index rebuilt — position reset"}
	t.pending = t.pending[:0]
}

// Index returns the current index (may be nil).
func (t *Tracker) Index() *Index { return t.idx }

// Position returns a snapshot of the current position.
func (t *Tracker) Position() Position { return t.pos }

// PendingCount returns the number of unconfirmed moves in the FIFO queue.
// Orthogonal to confidence: you can be green with pending>0.
func (t *Tracker) PendingCount() int { return len(t.pending) }

// Pending returns a copy of the pending direction letters.
func (t *Tracker) Pending() []string {
	return append([]string(nil), t.pending...)
}

// CurrentRoom returns the IndexRoom at the current position, or nil.
func (t *Tracker) CurrentRoom() *IndexRoom {
	if t.idx == nil || !t.pos.Valid {
		return nil
	}
	return t.idx.Room(t.pos.Coord)
}

// PushMove enqueues a detected outgoing movement direction (canonical letter).
// Called from the move-detection point after alias/VM expansion. Directions that
// are not movement are filtered by the caller (MoveDir).
func (t *Tracker) PushMove(dir string) {
	if _, ok := dirDelta[dir]; !ok {
		return
	}
	t.pending = append(t.pending, dir)
	if len(t.pending) > pendingCap {
		t.pending = t.pending[len(t.pending)-pendingCap:]
	}
}

// Anchor sets the tracker position explicitly (manual re-anchor / mud_anchor_here
// / mud_set_active default). It resolves to a known room if one exists at the
// coord; confidence becomes green and the pending queue is flushed.
func (t *Tracker) Anchor(c Coord) (Position, bool) {
	if t.idx == nil {
		t.pos = Position{Valid: true, Coord: c, Confidence: Yellow, Reason: "no active map set index"}
		t.pending = t.pending[:0]
		return t.pos, false
	}
	if t.idx.Room(c) == nil {
		// Anchor to a coord not in the set: keep it but mark yellow.
		t.pos = Position{Valid: true, Coord: c, Confidence: Yellow, Reason: "anchored to unmapped cell"}
		t.pending = t.pending[:0]
		return t.pos, false
	}
	t.pos = Position{Valid: true, Coord: c, Confidence: Green}
	t.pending = t.pending[:0]
	return t.pos, true
}

// CancelHead handles a "не можете идти" refusal: pop only the head of the queue
// (the failed step). Position does not move. Returns true if a head was popped.
func (t *Tracker) CancelHead() bool {
	if len(t.pending) == 0 {
		return false
	}
	t.pending = t.pending[1:]
	return true
}

// RefusalPhrases are the substrings that signal a blocked move ("Вы не можете
// идти" / "не можете идти"). Matched on a stripped line by the session.
var RefusalPhrases = []string{"не можете идти"}

// IsRefusal reports whether a stripped line is a movement refusal.
func IsRefusal(stripped string) bool {
	for _, p := range RefusalPhrases {
		if strings.Contains(stripped, p) {
			return true
		}
	}
	return false
}

// Reconcile applies a parsed room-event to the tracker and returns the resulting
// position (plan §5 per-step reconciliation + auto-resync + edge cases). The
// changed flag reports whether the position/confidence/queue changed (so the
// session only broadcasts on change).
func (t *Tracker) Reconcile(ev RoomEvent) (pos Position, changed bool) {
	before := t.snapshot()
	t.reconcile(ev)
	after := t.snapshot()
	return t.pos, before != after
}

// snapshot is a comparable summary for change detection.
type trackerSnap struct {
	valid      bool
	coord      Coord
	conf       Confidence
	reason     string
	pendingLen int
}

func (t *Tracker) snapshot() trackerSnap {
	return trackerSnap{
		valid:      t.pos.Valid,
		coord:      t.pos.Coord,
		conf:       t.pos.Confidence,
		reason:     t.pos.Reason,
		pendingLen: len(t.pending),
	}
}

func (t *Tracker) reconcile(ev RoomEvent) {
	if t.idx == nil {
		t.pos = Position{Valid: false, Confidence: Red, Reason: "no active map set"}
		return
	}

	fp := mapimport.Fingerprint(ev.Hint, ev.Desc, ev.Exits)
	evExits, _ := mapimport.NormExits(ev.Exits)

	// 1) If we have a valid position and a pending head, try the predicted cell.
	if t.pos.Valid && len(t.pending) > 0 {
		dir := t.pending[0]
		if pred, ok := t.predict(t.pos.Coord, dir); ok {
			if room := t.idx.Room(pred); room != nil && matches(room, ev, evExits) {
				// confirmed step
				t.pos = Position{Valid: true, Coord: pred, Confidence: Green}
				t.pending = t.pending[1:]
				return
			}
		}
	}

	// 2) Following / teleport with an empty queue: search neighbors (incl seams)
	//    for a cell matching the event.
	if t.pos.Valid && len(t.pending) == 0 {
		nb, ambiguous := t.neighborMatch(t.pos.Coord, ev, evExits)
		if nb != nil {
			t.pos = Position{Valid: true, Coord: nb.Coord, Confidence: Green}
			return
		}
		if ambiguous {
			// Multiple neighbors/seams equally match — do not guess a cell. Hold
			// the last-known position at 🟡 (dead-reckoning) rather than false-🟢.
			t.pos = Position{Valid: true, Coord: t.pos.Coord, Confidence: Yellow,
				Reason: "ambiguous match (multiple candidate cells) — dead-reckoning"}
			return
		}
	}

	// 3) Legit auto-resync by unique fingerprint anywhere in the set. Skipped for
	//    hint-less events (pipe corridors): an empty hint is not a reliable
	//    fingerprint to resolve on.
	if ev.Hint != "" {
		if r := t.idx.resolveFingerprint(fp); r != nil {
			t.pos = Position{Valid: true, Coord: r.Coord, Confidence: Green}
			t.pending = t.pending[:0]
			return
		}
	}

	// 4) Pipe corridor / no hint: never flip to red for lack of a hint. Hold
	//    yellow on dead-reckoning if we have a position; else stay wherever.
	if ev.Hint == "" {
		if t.pos.Valid {
			// Advance dead-reckoning through the pipe if a head is pending.
			if len(t.pending) > 0 {
				if pred, ok := t.predict(t.pos.Coord, t.pending[0]); ok {
					t.pos = Position{Valid: true, Coord: pred, Confidence: Yellow, Reason: "pipe corridor (no hint to reconcile)"}
					t.pending = t.pending[1:]
					return
				}
			}
			t.pos.Confidence = Yellow
			t.pos.Reason = "pipe corridor (no hint to reconcile)"
			return
		}
		// No position and a pipe: stay yellow-unknown, do not go red.
		t.pos = Position{Valid: false, Confidence: Yellow, Reason: "pipe corridor, position unknown"}
		return
	}

	// 5) We had a position and a pending head but the room did not match the
	//    prediction, and no resync: lost. Indicator stays on last-known room but
	//    confidence is red; flush the queue.
	if t.pos.Valid {
		lost := t.pos.Coord
		t.pos = Position{Valid: true, Coord: lost, Confidence: Red, Reason: "mismatch on step (position lost)"}
		t.pending = t.pending[:0]
		return
	}

	// 6) No position at all and no resolve: remain lost/unknown.
	t.pos = Position{Valid: false, Confidence: Red, Reason: "no resolve — unknown position"}
	t.pending = t.pending[:0]
}

// predict returns the predicted neighbor coord for a step from c in dir,
// accounting for seams: if the current room has a seam whose command matches the
// direction word, the prediction is the seam target (discontinuous coords).
func (t *Tracker) predict(c Coord, dir string) (Coord, bool) {
	room := t.idx.Room(c)
	// Seam check first: a seam command may correspond to this direction.
	if room != nil {
		for _, a := range room.Automaps {
			if s, ok := ParseSeam(a); ok {
				if seamDir(s.Command) == dir {
					if tr := t.idx.seamTarget(s); tr != nil {
						return tr.Coord, true
					}
				}
			}
		}
	}
	d, ok := dirDelta[dir]
	if !ok {
		return Coord{}, false
	}
	return Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}, true
}

// seamDir maps a seam command ("на восток", "восток", "в") to a canonical dir.
func seamDir(cmd string) string {
	if d, ok := MoveDir(cmd); ok {
		return d
	}
	// Multi-word seam command: take the last token (e.g. "на восток" -> "восток").
	fields := strings.Fields(strings.ToLower(cmd))
	if len(fields) == 0 {
		return ""
	}
	last := fields[len(fields)-1]
	return moveWords[last]
}

// neighborMatch searches all grid neighbors and seam targets of c for a room
// matching the event (plan §5 following/teleport with empty queue). It collects
// every matching candidate and returns the match ONLY when it is unique: if more
// than one neighbor/seam equally matches, it returns nil+ambiguous=true so the
// caller stays 🟡 rather than green-anchoring to an arbitrary cell. Iteration is
// over a fixed direction order (N,S,E,W,U,D) so results are deterministic, not
// dependent on Go map order.
func (t *Tracker) neighborMatch(c Coord, ev RoomEvent, evExits []string) (match *IndexRoom, ambiguous bool) {
	room := t.idx.Room(c)
	var cands []*IndexRoom
	// grid neighbors, in a fixed direction order.
	for _, dir := range dirOrder {
		if room != nil && len(room.EDirs) > 0 && !containsDir(room.EDirs, dir) {
			// exit-constrained (blank exits => permissive)
			continue
		}
		d := dirDelta[dir]
		nc := Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}
		if nb := t.idx.Room(nc); nb != nil && matches(nb, ev, evExits) {
			cands = appendUniqueRoom(cands, nb)
		}
	}
	// seam targets (Tag is non-unique, so a seam may resolve to an arbitrary
	// cand[0]; still counted as a distinct candidate for ambiguity).
	if room != nil {
		for _, a := range room.Automaps {
			if s, ok := ParseSeam(a); ok {
				if tr := t.idx.seamTarget(s); tr != nil && matches(tr, ev, evExits) {
					cands = appendUniqueRoom(cands, tr)
				}
			}
		}
	}
	switch len(cands) {
	case 0:
		return nil, false
	case 1:
		return cands[0], false
	default:
		return nil, true
	}
}

// appendUniqueRoom appends r unless an entry with the same Coord is already
// present (the same cell reachable two ways is one candidate, not two).
func appendUniqueRoom(list []*IndexRoom, r *IndexRoom) []*IndexRoom {
	for _, e := range list {
		if e.Coord == r.Coord {
			return list
		}
	}
	return append(list, r)
}

// matches reports whether an IndexRoom matches a room-event by hint + normalized
// exits, tolerant to door-marker differences (exits compared as canonical dir
// sets, doors ignored — mapimport.NormExits already strips door markers). An
// empty event hint is not matched here (pipe path handled separately).
func matches(room *IndexRoom, ev RoomEvent, evExits []string) bool {
	if ev.Hint == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(room.Hint), strings.TrimSpace(ev.Hint)) {
		return false
	}
	return sameDirSet(room.EDirs, evExits)
}

func sameDirSet(a, b []string) bool {
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func containsDir(list []string, dir string) bool {
	for _, d := range list {
		if d == dir {
			return true
		}
	}
	return false
}
