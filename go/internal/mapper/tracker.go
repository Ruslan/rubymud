package mapper

import (
	"strconv"
	"strings"
	"time"

	"rubymud/go/internal/mapimport"
)

// Confidence is the tracker's freshness/position confidence: green = anchored
// (room-event matched a resolved
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

	// Exit diff, populated when we degrade to yellow by ASSUMING a predicted cell
	// on a soft/hard mismatch (the room-event did not exactly match the map cell,
	// but we still know the travel direction). These describe how the live event's
	// exits differ from the assumed map cell's exits:
	//   ExitsAddedLive   = +live : dirs the game reports that the map lacks (e.g. a
	//                      room that grew a U exit over years of new content).
	//   ExitsRemovedMap  = -map  : dirs the map has that the game no longer reports.
	// Empty on green/red and when there is no mismatch to describe. Consumers
	// (mud_where, room_position broadcast, a future map-patch tool) surface these
	// so accepting "the map is outdated" stays a conscious manual re-anchor.
	ExitsAddedLive  []string
	ExitsRemovedMap []string

	// EventDescribesCell reports whether the room-event that produced THIS
	// position actually describes the cell at Coord. It is read-only reporting —
	// it takes part in NO reconciliation decision and is deliberately absent from
	// snapshot(), so it can never affect change detection or the room_position
	// broadcast cadence.
	//
	// The distinction matters to any WRITE that copies the live room text into
	// the map cell at Coord (the «Записать комнату» autorecord). Valid+Coord alone
	// are not enough: several branches legitimately hold the LAST-KNOWN coord
	// while the player has demonstrably moved somewhere else (ambiguous neighbor
	// match, hint-less pipe event, text veto, true loss). Writing the live text
	// into Coord in those states would overwrite a good, unrelated room.
	//
	// True only where the event resolved TO the cell now at Coord:
	//   step 1  confirmed dead-reckoned step (🟢 at the predicted cell)
	//   step 2  neighbor/seam match (🟢 at the matched cell)
	//   step 3  unique-fingerprint auto-resync (🟢 at the resolved cell)
	//   step 5a hint agrees, exits diverged (🟡 at the assumed cell)
	//   step 5c predicted cell absent from the map (🟡 at the dead-reckoned coord)
	// and after Anchor(), where a human explicitly asserts "I am in this cell".
	// Step 0 (nothing moved: a look with an empty queue) leaves the position, and
	// with it this flag, exactly as it was.
	//
	// False everywhere else: 2-ambiguous, 4 (hint-less), 5b (text veto), 6 (true
	// loss), and with no index at all.
	EventDescribesCell bool
}

// Tracker is the per-session backend position state machine. It is NOT safe for
// concurrent use; the owning session serializes access (parser feed and move
// detection both run under the session's coordination). It holds the active
// set's index and the FIFO pending-moves queue.
// lookReplyWindow bounds how long after a player command a room block may still
// be read as that command's reply (a look). Past it, the block is read exactly
// as if nothing had been sent — as a move — which is the safe direction: it can
// cost a look on a very laggy link its "nothing moved" reading (the pre-rule
// behaviour: a jump to an identical neighbour or a 🔴), but it can never let a
// forgotten chat line hold a position the server has since moved. Three seconds
// is far above a normal round trip (tens to a few hundred milliseconds in the
// field logs) and well below the pace at which a player is dragged around.
const lookReplyWindow = 3 * time.Second

type Tracker struct {
	idx     *Index
	pos     Position
	pending []string // canonical direction letters awaiting confirmation

	// lastFP is the fingerprint of the room event that produced the current
	// position, kept only while that event described the cell (never on 🔴). It lets a repeat of the same block with nothing queued (a look)
	// be recognised as "nothing moved" even where the map has no room to compare
	// against — the unrecorded cell «Записать комнату» is about to write. Anything
	// that moves the position by fiat (anchor, index reset, invalidation) clears it.
	lastFP string

	// commandSent / commandAt: the player sent a command that is not a queued
	// move (a look, an action, chat) after the last queued move, at commandAt.
	// Only a block answering such a command can be a look; one that arrives with
	// nothing outstanding (following, an escort, a server push) is a move by
	// definition. Set by NoteCommand; spent by PushMove (the server replies in
	// order, so the command was answered before the move's block), by the next
	// block that arrives with nothing queued, and by lookReplyWindow.
	commandSent bool
	commandAt   time.Time
	// answering is commandSent && within lookReplyWindow, fixed once per
	// reconcile so every rule in one reconcile reads the same answer.
	answering bool

	// departed is the cell the last consumed queued step left, recorded only
	// when the block that consumed it answered nothing but that step (no command
	// sent after its push). Then the server demonstrably accepted the step, so a
	// look that merely reprints the same block can never put us back there (see
	// "same block again"). A step consumed by a look's block may be a phantom — a
	// refusal the tracker did not recognise — and proves nothing.
	departed   Coord
	departedOK bool

	// now is the tracker's clock: time.Now unless a test injects one.
	now func() time.Time
}

func (t *Tracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
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
	t.lastFP = ""
	t.departedOK = false
}

// SetIndexPreservingPosition swaps the index to a rebuilt one (a topology write
// changed the graph, so the index must be rebuilt) WITHOUT resetting the tracker
// to 🔴 when the player has not moved. It captures the current
// (coord, confidence, pending queue) before the swap and, if that coord STILL
// EXISTS in the new index, restores the position + confidence + pending queue
// verbatim. It falls to 🔴 (SetIndex semantics) only when there is no valid
// position to preserve or the current cell no longer exists in the new index
// (e.g. it was deleted by the write). This is the topology-write refresh path:
// adding an exit to the CURRENT room must NOT lose position (same footgun class
// as the annotation-DT reset — a naive SetIndex would flush pending + go red).
//
// A nil new index degrades to SetIndex(nil) (no set — nothing to preserve into).
func (t *Tracker) SetIndexPreservingPosition(idx *Index) {
	prevPos := t.pos
	prevPending := append([]string(nil), t.pending...)

	if idx == nil {
		t.SetIndex(nil)
		return
	}
	// Only a valid position anchored to a still-existing cell can be preserved.
	if !prevPos.Valid || idx.Room(prevPos.Coord) == nil {
		t.SetIndex(idx)
		return
	}
	t.idx = idx
	t.pos = prevPos
	t.pending = prevPending
}

// RelocatePosition changes only the namespace of an exact logical cell. It is
// used by an atomic zone move before an index rebuild, so confidence and pending
// movement remain meaningful: coordinates and graph endpoints did not change.
func (t *Tracker) RelocatePosition(from, to string, x, y, l int) bool {
	if !t.pos.Valid || t.pos.Coord != (Coord{Zone: from, X: x, Y: y, L: l}) {
		return false
	}
	t.pos.Coord.Zone = to
	return true
}

// Index returns the current index (may be nil).
func (t *Tracker) Index() *Index { return t.idx }

// Position returns a snapshot of the current position.
func (t *Tracker) Position() Position { return t.pos }

// Invalidate drops a timed-out external movement expectation. It intentionally
// clears the FIFO so a later room event cannot be reconciled against a command
// whose outcome was never observed.
func (t *Tracker) Invalidate(reason string) {
	t.pos = Position{Confidence: Red, Reason: reason}
	t.pending = t.pending[:0]
	t.lastFP = ""
	t.departedOK = false
}

// NoteCommand records that the player sent a command that is not a queued move
// — anything written to the game that the move detector did not recognise as a
// direction. The session calls it from its ordinary-command write path (input,
// keys, aliases, triggers, MCP); mapper-owned writes (route hops, the scan
// probe) and telnet negotiation do not, because none of them can be a look.
//
// It is the only thing that lets a block arriving with an empty queue be read as
// "nothing moved", and only while the command is outstanding: sent after the
// last queued move, not yet answered by a block, and no older than
// lookReplyWindow. The signal is deliberately coarse inside that window: it
// cannot tell a look from chat typed while the server is moving the player, nor
// from a command that moves without being a recognised direction (a typed flee, a
// multi-word passage command, an unknown abbreviation). Those residual cases
// land in an identical room as "nothing moved"; everything else about them is
// unchanged.
func (t *Tracker) NoteCommand() {
	t.commandSent = true
	t.commandAt = t.clock()
}

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
	// Replies come in order: whatever was sent before this move is answered
	// before the move's own block, so it can no longer be what a later block
	// answers. A look typed after the move ("e;см") is noted after this.
	t.commandSent = false
	if len(t.pending) > pendingCap {
		t.pending = t.pending[len(t.pending)-pendingCap:]
	}
}

// Anchor sets the tracker position explicitly (manual re-anchor / mud_anchor_here
// / mud_set_active default). It resolves to a known room if one exists at the
// coord; confidence becomes green and the pending queue is flushed.
func (t *Tracker) Anchor(c Coord) (Position, bool) {
	// The anchored cell was asserted, not observed: no earlier block describes it,
	// and no step led here.
	t.lastFP = ""
	t.departedOK = false
	if t.idx == nil {
		t.pos = Position{Valid: true, Coord: c, Confidence: Yellow, Reason: "no active map set index",
			EventDescribesCell: true}
		t.pending = t.pending[:0]
		return t.pos, false
	}
	if t.idx.Room(c) == nil {
		// Anchor to a coord not in the set: keep it but mark yellow.
		t.pos = Position{Valid: true, Coord: c, Confidence: Yellow, Reason: "anchored to unmapped cell",
			EventDescribesCell: true}
		t.pending = t.pending[:0]
		return t.pos, false
	}
	t.pos = Position{Valid: true, Coord: c, Confidence: Green, EventDescribesCell: true}
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

// RefusalPhrases are the substrings that signal a blocked move — the move was
// sent but the MUD rejected it, so the pending FIFO head must be cancelled (no
// position change). Matched on a stripped line by the session. Kept conservative
// and specific to avoid false-positives on non-movement lines:
//   - "не можете идти" — RMUD generic blocked move ("Вы не можете идти ...").
//   - "дверь закрыта"  — RMUD closed-door block: the door in the walked direction
//     is shut, so the step did not happen and the queued head must be popped
//     (otherwise pending_moves leaks and stays stuck at >0 while position holds).
var RefusalPhrases = []string{"не можете идти", "дверь закрыта", "ворота закрыты"}

// IsRefusal reports whether a stripped line is a movement refusal. Matching is
// case-insensitive (the closed-door line "Дверь закрыта" is sentence-initial and
// capitalized); the phrases are lowercase.
func IsRefusal(stripped string) bool {
	low := strings.ToLower(stripped)
	for _, p := range RefusalPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// Reconcile applies a parsed room-event to the tracker and returns the resulting
// position after per-step reconciliation, auto-resync, and edge-case handling. The
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
	matchFP := fp
	if strings.TrimSpace(ev.Hint) == "" {
		// Exits-only pipe events deliberately do not identify a physical room.
		matchFP = ""
	}
	evExits, _ := mapimport.NormExits(ev.Exits)
	from, queued := t.pos.Coord, len(t.pending)
	t.answering = t.commandSent && t.clock().Sub(t.commandAt) <= lookReplyWindow
	defer t.noteEvent(matchFP, from, queued)

	// sameBlock: this block may be a look that reprints the block which put us
	// on the current cell. If a step put us here and nothing but that step was
	// outstanding when it was consumed, its cell of departure is off limits to
	// every search below — the server accepted the step that left it.
	var except *Coord
	if t.lookInPlace(matchFP) && matchFP == t.lastFP && t.departedOK {
		d := t.departed
		except = &d
	}

	// 0) NOTHING MOVED. A block that answers a command the player sent (not a
	//    queued move) and is identical to the MAPPED cell we hold is a look or a
	//    re-print after an action, and the position stands as it was.
	//
	//    Every search below exists to explain a block that is NOT where we are
	//    (following, teleport, a stale cell), so none of them looks at the current
	//    cell. Reaching them with a look was harmless only while the room was
	//    unique in the map: the global resolve happened to land back on it. On a
	//    map that repeats rooms word for word (a monastery's cells and corridors)
	//    the same look went 🔴 when an identical cell existed anywhere and jumped
	//    🟢 into an identical NEIGHBOUR.
	//
	//    Only a block that answers an outstanding command counts: one sent after
	//    the last queued move, not yet answered, at most lookReplyWindow ago. A
	//    block with nothing outstanding (following, an NPC escort, an automatic
	//    flee, a server push, or a command long since answered) cannot be a look
	//    and skips this step: the searches decide it exactly as they always did.
	//    What remains ambiguous is a block that does answer some command but was
	//    produced by a move the tracker did not see — chat or a trigger's command
	//    sent within the window while the server moves the player, or a command
	//    that moves without being a recognised direction (a typed flee, a
	//    multi-word passage command, an unknown abbreviation). If that move lands
	//    in a word-for-word copy of the current cell, it is taken as "nothing
	//    moved" and the position stays one cell behind; a different room is
	//    unaffected. See NoteCommand.
	//
	//    Only a cell the map HAS can confirm us here. On unrecorded ground the
	//    coordinate is our own dead-reckoned guess, and a mapped cell matching the
	//    block is better evidence than that guess — that is how a position pushed
	//    off course (a step the server refused without the tracker noticing)
	//    finds its way back. That case keeps the searches below and is decided
	//    only where they find nothing (see "same block again" before 5.5).
	//
	//    A 🔴 position is not a claim that we are there, so it is never revived
	//    here (lookInPlace excludes it); recovery stays with the fingerprint
	//    resync and manual anchors.
	if t.lookInPlace(matchFP) {
		if current := t.idx.Room(t.pos.Coord); current != nil && matchesFingerprint(current, matchFP) {
			// An exit diff the position still carries (a mutable hatch that was
			// open, exits that diverged on arrival) is no longer what the server
			// prints: the cell now reads exactly as mapped, which is the
			// confirmation that diff stood in for.
			if len(t.pos.ExitsAddedLive)+len(t.pos.ExitsRemovedMap) > 0 {
				t.pos = Position{Valid: true, Coord: current.Coord, Confidence: Green, EventDescribesCell: true}
			}
			return
		}
	}

	// A room may explicitly declare individual exits as mutable. Only those
	// directions are allowed to differ while the stable name+description still
	// match; every unmarked exit remains part of the room identity. An unmarked
	// exit-only variant must stop here at yellow: allowing neighbor/global lookup
	// to continue could jump to a distant text twin whose fingerprint happens to
	// contain the newly observed exits.
	if t.pos.Valid && t.pos.Confidence == Green && len(t.pending) == 0 {
		if current := t.idx.Room(t.pos.Coord); current != nil {
			if added, removed, exitOnly := current.ExitOnlyVariant(ev); exitOnly {
				if _, _, mutable := current.MutableExitVariant(ev); mutable {
					t.pos = Position{Valid: true, Coord: current.Coord, Confidence: Green,
						ExitsAddedLive: added, ExitsRemovedMap: removed, EventDescribesCell: true}
					return
				}
				reason := "current room exits diverged — explicit mutable-exit confirmation required"
				if diff := formatExitDiff(added, removed); diff != "" {
					reason += "; exits " + diff
				}
				t.pos = Position{Valid: true, Coord: current.Coord, Confidence: Yellow, Reason: reason,
					ExitsAddedLive: added, ExitsRemovedMap: removed, EventDescribesCell: true}
				return
			}
		}
	}

	// 1) If we have a valid position and a pending head, try the predicted cell.
	if t.pos.Valid && len(t.pending) > 0 {
		dir := t.pending[0]
		if pred, ok := t.predict(t.pos.Coord, dir); ok {
			// A single physical step may jump a whole pipe run: the server emits ONE
			// room-event for the far (real) cell, while the map stores the pipe cells
			// in between. When the immediate predicted cell is a pipe (untagged /
			// empty hint) and the EVENT carries a real hint, keep stepping in the same
			// direction through the pipe run to the first non-pipe cell and reconcile
			// THAT cell — otherwise the empty-hint pipe cell mismatches the real event
			// and flaps a false 🔴 for one step. (Hint-less events — walking INTO a
			// pipe the server does report — are handled separately below at 🟡.)
			if ev.Hint != "" {
				if skipped, ok2 := t.skipPipeRun(pred, dir); ok2 {
					pred = skipped
				}
			}
			if room := t.idx.Room(pred); room != nil && matchesFingerprint(room, matchFP) {
				// confirmed step
				t.pos = Position{Valid: true, Coord: pred, Confidence: Green, EventDescribesCell: true}
				t.pending = t.pending[1:]
				return
			}
		}
	}

	// 2) Following / teleport with an empty queue: search neighbors (incl seams)
	//    for a cell matching the event.
	if t.pos.Valid && len(t.pending) == 0 {
		nb, ambiguous := t.neighborMatch(t.pos.Coord, matchFP, true, except)
		if nb != nil {
			t.pos = Position{Valid: true, Coord: nb.Coord, Confidence: Green, EventDescribesCell: true}
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

	// 3) Legit auto-resync by unique fingerprint anywhere in the set — the intended
	//    RE-LOCALIZATION path when we have NO reliable pending directional context
	//    (recovering from 🔴/lost, or an empty queue whose neighbor search failed).
	//    A unique-hint match legitimately green-anchors THERE.
	//
	//    CRITICAL GATE (text is a veto, not a SELECTOR during dead-reckoning):
	//    when we are mid dead-reckoning step (valid position AND a pending
	//    directional move), we do NOT run whole-zone auto-resync. "Unique in the
	//    MAP" ≠ "unique in the WORLD" — a stale map can have one of two real twin
	//    rooms (e.g. NE/SE towers both "Смотровая площадка"), so a unique-hint match
	//    to a cell that is NOT the predicted neighbor would text-select a false jump
	//    to a far, non-adjacent cell and green-anchor there (worst outcome). During
	//    a dead-reckoning step, only the graph-predicted neighbor (step 1) may
	//    green-anchor; a non-neighbor unique-hint match is a twin/teleport
	//    suspicion, handled at 🟡/🔴 below — never 🟢.
	if ev.Hint != "" && !(t.pos.Valid && len(t.pending) > 0) {
		if r := t.idx.resolveFingerprint(fp); r != nil && (except == nil || r.Coord != *except) {
			t.pos = Position{Valid: true, Coord: r.Coord, Confidence: Green, EventDescribesCell: true}
			t.pending = t.pending[:0]
			return
		}
	}

	// 4) Pipe corridor / obscured room / no hint: never flip to red for lack of
	//    visible identity. Hold yellow on dead-reckoning if we have a position;
	//    else stay wherever. Obscured is distinct from a structural pipe so the
	//    UI can tell the player to restore visibility rather than edit topology.
	if ev.Hint == "" {
		reason := "pipe corridor (no hint to reconcile)"
		unknownReason := "pipe corridor, position unknown"
		if ev.Obscured {
			reason = "room obscured; restore visibility"
			unknownReason = "room obscured; position unknown until visibility is restored"
		}
		if t.pos.Valid {
			// Advance dead-reckoning through the pipe if a head is pending.
			if len(t.pending) > 0 {
				if pred, ok := t.predict(t.pos.Coord, t.pending[0]); ok {
					t.pos = Position{Valid: true, Coord: pred, Confidence: Yellow, Reason: reason}
					t.pending = t.pending[1:]
					return
				}
			}
			t.pos.Confidence = Yellow
			t.pos.Reason = reason
			// This branch MUTATES t.pos in place rather than replacing it, so the
			// previous event's flag must be cleared explicitly: a hint-less event
			// describes no cell.
			t.pos.EventDescribesCell = false
			return
		}
		// No position and a pipe: stay yellow-unknown, do not go red.
		t.pos = Position{Valid: false, Confidence: Yellow, Reason: unknownReason}
		return
	}

	// 5) We had a position AND a pending directional move, but the room-event did
	//    not exactly match and no auto-resync fired. Split the mismatch by whether
	//    the HINT (name) agrees with the assumed cell — "text as veto":
	//
	//    (a) hint MATCHES, only the EXITS differ (e.g. a superset SW ⊂ SWU — the
	//        room grew a U over years): the map is merely outdated. Degrade to 🟡
	//        by ASSUMING the predicted cell and surface the exit diff (+live/-map).
	//        We do NOT auto-upgrade to 🟢 — accepting the outdated map must be a
	//        conscious manual re-anchor (mud_anchor_here).
	//
	//    (b) hint CONTRADICTS the assumed cell (the name differs): the server most
	//        likely teleported us / broke the edge. Holding a 🟡 "assumed" position
	//        would be a FALSE position an agent could path from. Full-flush to 🔴
	//        (text veto). Text is a veto, NOT a selector — we never re-anchor to a
	//        cell by name here; 🔴 waits for the exact-fingerprint auto-resync
	//        (still requires resolution to exactly one cell) or a manual anchor.
	if t.pos.Valid && len(t.pending) > 0 {
		dir := t.pending[0]

		// (0) THE STEP WAS TAKEN THROUGH A FACE THE PLAYER MARKED "NO PASSAGE". The
		//     tracker keeps refusing to predict through it — the mark is the player's
		//     own statement and outranks a guess — but it must SAY SO. Falling through
		//     to the text-veto below produced "lost — hint contradicts assumed cell
		//     (likely teleport); re-anchor with mud_anchor_here", which names the wrong
		//     cause (nothing teleported) and the wrong remedy (a re-anchor does not
		//     clear a mark, and the next step through the same face lands here again).
		//
		//     It matters most on unmapped ground: WITHOUT a negation this same step is
		//     the 🟡 "edge/cell not in map" state that «Записать комнату» draws a new
		//     room from; WITH one the player would silently lose that ability. The
		//     reason therefore names the mark and how to drop it.
		if e := t.deniedFace(t.pos.Coord, dir); e {
			lost := t.pos.Coord
			t.pos = Position{Valid: true, Coord: lost, Confidence: Red,
				Reason: "step " + DirRU(dir) + " crosses a face YOU marked as no-passage — the map cannot follow it. " +
					"Clear that mark (edge editor in ~map, or mud_room_mark_edge mark=\"auto\") if the passage is real, then re-anchor."}
			t.pending = t.pending[:0]
			return
		}

		assumed := t.pos.Coord
		if pred, ok := t.predict(t.pos.Coord, dir); ok {
			assumed = pred
			if skipped, ok2 := t.skipPipeRun(pred, dir); ok2 {
				assumed = skipped
			}
		}
		assumedRoom := t.idx.Room(assumed)

		// (c) The predicted neighbor / edge is NOT in the map (unmapped edge or
		//     off-map cell): we walked a direction the map doesn't record (e.g.
		//     ascent through a U exit the stale map lacks). Do NOT fall through to
		//     whole-zone auto-resync (that would text-select a far unique-hint twin
		//     and false-🟢). Hold 🟡 at the dead-reckoning coord with an
		//     "edge not in map" reason; wait for a legit resync from a cleared
		//     state or a manual re-anchor.
		if assumedRoom == nil {
			t.pos = Position{
				Valid:      true,
				Coord:      assumed,
				Confidence: Yellow,
				Reason:     "assumed " + coordStr(assumed) + " by " + DirRU(dir) + " — edge/cell not in map (map may be outdated)",
				// The event describes the room we just walked into, which IS this
				// dead-reckoned coord — this is the "draw a new room" case.
				EventDescribesCell: true,
			}
			t.pending = t.pending[1:]
			return
		}

		if added, removed, ok := assumedRoom.MutableExitVariant(ev); ok {
			t.pos = Position{Valid: true, Coord: assumed, Confidence: Green,
				ExitsAddedLive: added, ExitsRemovedMap: removed, EventDescribesCell: true}
			t.pending = t.pending[1:]
			return
		}

		hintAgrees := ev.Hint != "" &&
			mapimport.NormText(assumedRoom.Hint) == mapimport.NormText(ev.Hint)

		if hintAgrees {
			// (a) exits diverged, hint matches -> 🟡 assumed cell + diff.
			added, removed := exitDiff(assumedRoom.EDirs, evExits)
			reason := "assumed " + coordStr(assumed) + " by " + DirRU(dir) + " (exits diverged — map may be outdated)"
			if d := formatExitDiff(added, removed); d != "" {
				reason += "; exits " + d
			}
			t.pos = Position{
				Valid:           true,
				Coord:           assumed,
				Confidence:      Yellow,
				Reason:          reason,
				ExitsAddedLive:  added,
				ExitsRemovedMap: removed,
				// The hint matched the assumed cell, so the event describes it.
				EventDescribesCell: true,
			}
			t.pending = t.pending[1:]
			return
		}

		// (b) hint contradicts -> text veto -> 🔴 full-flush; do NOT hold a false
		//     assumed position. Indicator stays on the last-known room.
		lost := t.pos.Coord
		t.pos = Position{Valid: true, Coord: lost, Confidence: Red,
			Reason: "lost — hint contradicts assumed cell (likely teleport); re-anchor with mud_anchor_here"}
		t.pending = t.pending[:0]
		return
	}

	// SAME BLOCK AGAIN. Nothing in the map explained this block — no neighbour, no
	//    unique fingerprint — but it is the very block that put us on this cell, and
	//    no move is queued: a look on ground the map does not have yet (or whose
	//    recorded exits differ). Losing the position here would throw away the 🟡
	//    "edge/cell not in map" state «Записать комнату» writes from and force the
	//    player to walk out and back in; nothing moved, so nothing changes.
	if t.lookInPlace(matchFP) && matchFP == t.lastFP {
		return
	}

	// 5.5) ADJACENCY FALLBACK. The graph knew of no neighbour matching this event and
	//    no unique fingerprint resolved it, but the cell next door does match. Take
	//    it — at 🟡, never 🟢: "I think you are here, the graph does not confirm it."
	//
	//    This exists because moving the neighbour search onto the materialized edges
	//    (see neighborMatch) is a correction the map data does not always deserve:
	//    629 of the 700 candidate faces it removed were rooms whose `ch` disagrees
	//    with an empty `edirs`, and losing the position outright on those turned 110
	//    live (cell, event) pairs into 🔴. A 🔴 costs the player their position,
	//    stops «Записать комнату» and demands a manual re-anchor; a 🟡 with a
	//    position costs them nothing but a colour.
	//
	//    IT CANNOT REVIVE THE 721cd67 FALSE-GREEN. That bug was whole-zone
	//    fingerprint selection jumping ~15 cells to a twin during a dead-reckoning
	//    step. This branch only ever considers cells ADJACENT to where we already
	//    are, only when the queue is EMPTY (never mid-step), and only yields 🟡 —
	//    all three of which that bug's fix turned on.
	//
	//    It runs AFTER the unique-fingerprint resync above on purpose: a unique
	//    fingerprint is hard evidence and deserves its 🟢; adjacency is a guess.
	if t.pos.Valid && len(t.pending) == 0 {
		nb, ambiguous := t.neighborMatch(t.pos.Coord, matchFP, false, except)
		if nb != nil {
			t.pos = Position{Valid: true, Coord: nb.Coord, Confidence: Yellow,
				Reason:             "assumed " + coordStr(nb.Coord) + " — adjacent and matching, but the map records no way there",
				EventDescribesCell: true}
			return
		}
		if ambiguous {
			t.pos = Position{Valid: true, Coord: t.pos.Coord, Confidence: Yellow,
				Reason: "ambiguous match (multiple adjacent cells) — dead-reckoning"}
			return
		}
	}

	// 6) True loss: we have no directional context (no pending move — teleport/
	//    following whose neighbor search failed) or no position at all. This is the
	//    only path that yields 🔴.
	if t.pos.Valid {
		lost := t.pos.Coord
		t.pos = Position{Valid: true, Coord: lost, Confidence: Red, Reason: "lost — no directional context (teleport/following, no match)"}
		t.pending = t.pending[:0]
		return
	}
	t.pos = Position{Valid: false, Confidence: Red, Reason: "no resolve — unknown position"}
	t.pending = t.pending[:0]
}

// lookInPlace reports whether this block can be a look at the cell we hold: it
// answers a command the player sent after the last queued move and within
// lookReplyWindow, no move is queued, the position rests on a
// block or an anchor that describes the cell, and the block carries an identity
// at all.
//
// A 🔴 position is never a look target, and needs no check of its own here:
// every 🔴 the tracker produces clears EventDescribesCell (the lost coord is held
// for display only). TestTrackerLookInPlaceNeverRevivesALostPosition pins that
// invariant on the real loss paths.
func (t *Tracker) lookInPlace(matchFP string) bool {
	return t.answering && t.pos.Valid && len(t.pending) == 0 &&
		t.pos.EventDescribesCell && matchFP != ""
}

// noteEvent runs after every reconcile. It records which block the position now
// rests on (only a block that describes the held cell — never a 🔴, see
// lookInPlace — can later stand for "the same block again"), which cell a
// consumed step left, and whether the outstanding command has been answered: a
// block that arrived with nothing queued answers it; one consumed as a queued
// step does not, so "e;см" still reads the second block as a look.
//
// A step's departure is recorded only when no command was sent after its push
// (commandSent is spent by PushMove, so it is set here exactly when one was):
// otherwise the consuming block may be a look's reply and the step a phantom
// the server refused in words the tracker does not know.
//
// The departure is deliberately NOT cleared when the position changes without a
// step. It is consulted only when a look reprints the very block the position
// rests on, and a position reached without a step rests on a MAPPED cell whose
// own fingerprint is that block — step 0 settles that look before the departure
// is ever read.
func (t *Tracker) noteEvent(matchFP string, from Coord, queued int) {
	if t.pos.Valid && t.pos.EventDescribesCell {
		t.lastFP = matchFP
	} else {
		t.lastFP = ""
	}
	if queued > 0 && len(t.pending) == queued-1 {
		if t.commandSent {
			t.departedOK = false
		} else {
			t.departed, t.departedOK = from, true
		}
	}
	if queued == 0 {
		t.commandSent = false
	}
}

// deniedFace reports whether the face leaving c in dir carries a hand-drawn
// negation — the one reason predict() refuses a step that the player can fix, and
// therefore the one that has to be reported in its own words rather than as a
// suspected teleport.
func (t *Tracker) deniedFace(c Coord, dir string) bool {
	room := t.idx.Room(c)
	if room == nil {
		return false
	}
	e := room.GridEdge(dir)
	return e != nil && !e.Passable
}

// coordStr formats a coord for human-readable reasons: "(x,y,l)".
func coordStr(c Coord) string {
	return "(" + strconv.Itoa(c.X) + "," + strconv.Itoa(c.Y) + "," + strconv.Itoa(c.L) + ")"
}

// predict returns the predicted neighbor coord for a step from c in dir,
// accounting for seams: if the current room has a seam whose command matches the
// direction word, the prediction is the seam target (discontinuous coords).
//
// It consults the MATERIALIZED edge of the face before falling back to the raw
// coordinate delta, because since the hand-drawn layer exists the two can
// disagree, and the edge is the authority the router already uses:
//
//   - a face carrying a hand-drawn NEGATION has no prediction at all (ok=false).
//     The player's mark says this step is impossible; predicting the cell anyway
//     is how the tracker used to CONFIRM a step onto it and green-anchor on a cell
//     it had been told was unreachable — a false fix an agent would then path from.
//   - a face carrying an edge predicts that edge's target. For the grid-adjacent
//     marks the hand-drawn layer writes that equals the delta, so nothing changes today; it
//     is what keeps this correct when a jump link (non-adjacent target) arrives.
//
// The DELTA FALLBACK IS LOAD-BEARING and deliberately kept: unmapped territory has
// no edge rows at all, and dead-reckoning into it is exactly what «Записать
// комнату» writes from (the candidate cell). "No row" must keep meaning "unknown",
// never "impossible" — only an explicit negation means impossible.
func (t *Tracker) predict(c Coord, dir string) (Coord, bool) {
	room := t.idx.Room(c)
	// Seam check first: a seam command may correspond to this direction.
	if room != nil {
		for _, e := range room.seams {
			if e.Passable && seamDir(e.Command) == dir && e.To != nil {
				return *e.To, true
			}
		}
	}
	return t.predictGrid(c, dir)
}

// predictGrid follows the authoritative directed grid face when one exists,
// including an exact endpoint in another zone. The coordinate fallback preserves
// dead-reckoning into unmapped territory. Pipe expansion uses this narrower
// helper because an arbitrary seam command must not be swallowed into one long
// corridor step.
func (t *Tracker) predictGrid(c Coord, dir string) (Coord, bool) {
	if room := t.idx.Room(c); room != nil {
		if e := room.GridEdge(dir); e != nil {
			if !e.Passable {
				return Coord{}, false
			}
			if e.To != nil {
				return *e.To, true
			}
		}
	}
	d, ok := dirDelta[dir]
	if !ok {
		return Coord{}, false
	}
	return Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}, true
}

// PredictReportedLanding applies the same seam/grid and pipe-run semantics used
// by Reconcile for a room event with a non-empty hint.
func (t *Tracker) PredictReportedLanding(c Coord, dir string) (Coord, bool) {
	pred, ok := t.predict(c, dir)
	if !ok {
		return Coord{}, false
	}
	if skipped, skippedOK := t.skipPipeRun(pred, dir); skippedOK {
		pred = skipped
	}
	return pred, true
}

// maxPipeSkip caps how many consecutive pipe cells the dead-reckoning skip will
// step over, to guard against an unbounded loop on malformed pipe data.
const maxPipeSkip = 32

// skipPipeRun, given a predicted cell `c` and the travel direction `dir`, walks
// forward in the same direction through any run of pipe corridors (untagged /
// empty-hint cells with Pipe=true) and returns the first NON-pipe (real/tagged)
// cell — the cell the server actually reports after a single physical step that
// jumps the pipe run. It returns (c,false) when the immediate cell is not a pipe
// (nothing to skip), and stops at a missing cell or the skip cap. It never
// crosses a seam (seams are handled by predict), but it may cross a zone boundary
// through an exact directed grid edge after rooms were split into another zone.
func (t *Tracker) skipPipeRun(c Coord, dir string) (Coord, bool) {
	room := t.idx.Room(c)
	if room == nil || !room.Pipe {
		return c, false
	}
	cur := c
	for i := 0; i < maxPipeSkip; i++ {
		next, ok := t.predictGrid(cur, dir)
		if !ok {
			return cur, true
		}
		nr := t.idx.Room(next)
		if nr == nil {
			// Pipe run runs off the map — best we can do is the last pipe cell.
			return cur, true
		}
		cur = next
		if !nr.Pipe {
			// Reached the first real cell past the pipe run.
			return cur, true
		}
	}
	return cur, true
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
// matching the event while following or teleporting with an empty queue. It collects
// every matching candidate and returns the match ONLY when it is unique: if more
// than one neighbor/seam equally matches, it returns nil+ambiguous=true so the
// caller stays 🟡 rather than green-anchoring to an arbitrary cell. Iteration is
// over a fixed direction order (N,S,E,W,U,D) so results are deterministic, not
// dependent on Go map order.
//
// WHICH NEIGHBOURS COUNT is decided by the MATERIALIZED edges when graphOnly is
// set, and by bare grid adjacency when it is not. Both modes exist, and reconcile
// runs them in that order, because they carry different amounts of evidence:
//
//	graphOnly=true   the graph says you can walk there  -> good enough to 🟢
//	graphOnly=false  the cells are merely adjacent      -> at most 🟡
//
// THE GRAPH MODE is the one that green-anchors, so it must read the same source
// the router does:
//
//   - a denied face is not a neighbour, so a cell the player marked unreachable
//     can no longer be anchored to by a name match;
//   - a hidden exit IS a neighbour even though no exits line mentions it, so
//     following someone through a secret passage resolves instead of dropping to 🟡.
//
// Switching to it also ended a THIRD re-derivation of connectivity next to the
// router's and the materializer's, and that has consequences well beyond the
// hand-drawn layer. MEASURED on the live editable set (8 043 rooms, 19 189 edges),
// TWO mechanisms drop candidates the old rule offered — 700 faces over 505 rooms:
//
//   - `ch` is authoritative over `edirs`: a room with ch bits and an empty edirs
//     list is explicit, not permissive, so denied directions stay out of the graph.
//   - bare adjacency is not connectivity when an explicit neighboring face
//     denies the reverse link.
//
// Correcting the rule cost real behaviour: 110 of 17 511 (cell, event) pairs went
// from a resolved position to 🔴 "no directional context", and 🔴 is not just
// noise — it halts «Записать комнату» and demands a re-anchor. Hence the ADJACENT
// mode: when the graph knows of no candidate, reconcile retries on bare adjacency
// and holds the result at 🟡. The player keeps a position and an honest label ("the
// graph does not confirm this"), instead of losing the position to a rule change.
//
// The adjacent mode still refuses a hand-drawn NEGATION: that is the player's own
// explicit "there is no way here", not an inference the map failed to record.
//
// A cell with no room in the index is never a candidate; when the tracker stands
// on a cell that is not in the map at all there are no rows to read, so even the
// graph mode falls back to raw deltas for that one hop.
func (t *Tracker) neighborMatch(c Coord, fingerprint string, graphOnly bool, except *Coord) (match *IndexRoom, ambiguous bool) {
	room := t.idx.Room(c)
	var cands []*IndexRoom
	// grid neighbors, in a fixed direction order.
	for _, dir := range dirOrder {
		d := dirDelta[dir]
		nc := Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}
		if room != nil {
			e := room.GridEdge(dir)
			switch {
			case e != nil && !e.Passable:
				continue // hand-drawn negation — refused in BOTH modes
			case e == nil:
				if graphOnly {
					continue
				}
			default:
				if e.To != nil {
					nc = *e.To
				}
			}
		}
		if except != nil && nc == *except {
			continue
		}
		if nb := t.idx.Room(nc); nb != nil && matchesFingerprint(nb, fingerprint) {
			cands = appendUniqueRoom(cands, nb)
		}
	}
	// The materialized seam already contains the importer's target resolution.
	// Dead seams have To=nil and are not graph neighbours.
	if room != nil {
		for _, e := range room.seams {
			if e.Passable && e.To != nil && (except == nil || *e.To != *except) {
				if tr := t.idx.Room(*e.To); tr != nil && matchesFingerprint(tr, fingerprint) {
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

// matchesFingerprint uses the same normalized hint+description+exits identity
// as import and global resync. Room names are routinely repeated in MUDs; the
// cleaned description is what distinguishes those cells. Door markers remain
// irrelevant because mapimport.Fingerprint canonicalizes the exit directions.
func matchesFingerprint(room *IndexRoom, fingerprint string) bool {
	return fingerprint != "" && room.Fingerprint == fingerprint
}

// MutableExitVariant accepts an exit-only variant of this exact room when, and
// only when, every changed direction was explicitly marked mutable. It never
// participates in global lookup, so repeated room text cannot select a distant
// cell. added/removed are returned for scan/frontier UI even on a trusted match.
func (r *IndexRoom) MutableExitVariant(ev RoomEvent) (added, removed []string, ok bool) {
	if r == nil || len(r.MutableExits) == 0 {
		return nil, nil, false
	}
	added, removed, ok = r.ExitOnlyVariant(ev)
	if !ok {
		return added, removed, false
	}
	allowed := make(map[string]bool, len(r.MutableExits))
	for _, dir := range r.MutableExits {
		allowed[dir] = true
	}
	for _, dir := range append(append([]string(nil), added...), removed...) {
		if !allowed[dir] {
			return added, removed, false
		}
	}
	return added, removed, true
}

// ExitOnlyVariant identifies a safe-to-offer mutable-exit candidate. It does
// not itself trust the observation: callers must either require MutableExits or
// ask the player before persisting the differing directions.
func (r *IndexRoom) ExitOnlyVariant(ev RoomEvent) (added, removed []string, ok bool) {
	if r == nil || strings.TrimSpace(ev.Hint) == "" || strings.TrimSpace(ev.Desc) == "" {
		return nil, nil, false
	}
	if mapimport.NormText(r.Hint) != mapimport.NormText(ev.Hint) || mapimport.NormText(r.Desc) != mapimport.NormText(ev.Desc) {
		return nil, nil, false
	}
	evExits, _ := mapimport.NormExits(ev.Exits)
	added, removed = exitDiff(r.EDirs, evExits)
	return added, removed, len(added)+len(removed) > 0
}

// containsDir is a membership test over a direction list. Since migration 018 moved
// doors onto the edges its only remaining caller is the corpus test's PRE-018
// doorGrade (edge_corpus_test.go), which reads rooms.doors on purpose: it is the
// reference the edge-based grade has to reproduce face for face. It stays here, and
// exported to that test by being in the same package, precisely so that reference
// cannot quietly become a copy of the thing it checks.
func containsDir(list []string, dir string) bool {
	for _, d := range list {
		if d == dir {
			return true
		}
	}
	return false
}

// exitDiff computes the exit-set difference between a live room-event and an
// assumed map cell, in canonical direction order:
//
//	addedLive   = +live : dirs in the event's exits but not the map cell's edirs.
//	removedMap  = -map  : dirs in the map cell's edirs but not the event's exits.
//
// Both are nil when the sets are equal. mapEDirs is the map cell's canonical exit
// letters; evExits is the event's canonical exit letters (door markers already
// stripped by NormExits).
func exitDiff(mapEDirs, evExits []string) (addedLive, removedMap []string) {
	mapSet := map[string]bool{}
	for _, d := range mapEDirs {
		mapSet[d] = true
	}
	evSet := map[string]bool{}
	for _, d := range evExits {
		evSet[d] = true
	}
	for _, d := range dirOrder {
		if evSet[d] && !mapSet[d] {
			addedLive = append(addedLive, d)
		}
		if mapSet[d] && !evSet[d] {
			removedMap = append(removedMap, d)
		}
	}
	return addedLive, removedMap
}

// formatExitDiff renders the exit diff as "+U -D" style tokens for the Reason
// string; returns "" when there is no diff.
func formatExitDiff(addedLive, removedMap []string) string {
	parts := make([]string, 0, len(addedLive)+len(removedMap))
	for _, d := range addedLive {
		parts = append(parts, "+"+d)
	}
	for _, d := range removedMap {
		parts = append(parts, "-"+d)
	}
	return strings.Join(parts, " ")
}
