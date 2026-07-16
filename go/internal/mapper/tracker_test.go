package mapper

import (
	"encoding/json"
	"strings"
	"testing"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/storage"
)

// mkRoom builds a storage.Room with JSON-encoded direction columns and a
// fingerprint computed the same way the import path does.
func mkRoom(zone string, x, y, l int, tag int, hint, desc, exits string, ch int, opts ...func(*storage.Room)) storage.Room {
	edirs, doors := mapimport.NormExits(exits)
	ej, _ := json.Marshal(nonNilJSON(edirs))
	dj, _ := json.Marshal(nonNilJSON(doors))
	tg := tag
	r := storage.Room{
		Zone: zone, X: x, Y: y, L: l, Tag: &tg,
		Hint: hint, Desc: desc, Exits: exits,
		EDirs: string(ej), Doors: string(dj), Ch: ch,
		Automaps:    "[]",
		Fingerprint: mapimport.Fingerprint(hint, desc, exits),
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func nonNilJSON(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func withAutomaps(seams ...string) func(*storage.Room) {
	return func(r *storage.Room) {
		j, _ := json.Marshal(seams)
		r.Automaps = string(j)
	}
}

func withDT() func(*storage.Room) {
	return func(r *storage.Room) { r.IsDT = true }
}

func withPipe() func(*storage.Room) {
	return func(r *storage.Room) { r.Pipe = true }
}

// linear 3-room corridor along the S axis (x increases going south).
func linearIndex() *Index {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Первая", "desc one", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "Вторая", "desc two", "N S", 0),
		mkRoom("Z", 2, 0, 0, 3, "Третья", "desc three", "N", 0),
	}
	return BuildIndex(1, rooms)
}

func ev(hint, desc, exits string) RoomEvent {
	return RoomEvent{Hint: hint, Desc: desc, Exits: exits}
}

func TestTrackerSuccessfulStep(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	if tr.Position().Confidence != Green {
		t.Fatalf("anchor not green: %v", tr.Position())
	}
	tr.PushMove("S")
	if tr.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", tr.PendingCount())
	}
	pos, changed := tr.Reconcile(ev("Вторая", "desc two", "N S"))
	if !changed {
		t.Fatal("expected change on successful step")
	}
	if pos.Confidence != Green || pos.Coord.X != 1 {
		t.Errorf("after step pos = %+v, want green @ x=1", pos)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("pending not popped: %d", tr.PendingCount())
	}
}

func TestTrackerHeadCancelOnRefusal(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("N") // there is no N exit; the MUD refuses
	if !tr.CancelHead() {
		t.Fatal("CancelHead should pop the failed head")
	}
	if tr.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0 after cancel", tr.PendingCount())
	}
	if tr.Position().Coord.X != 0 || tr.Position().Confidence != Green {
		t.Errorf("position moved on refusal: %+v", tr.Position())
	}
	if !IsRefusal("Вы не можете идти в этом направлении.") {
		t.Error("IsRefusal should match the RU refusal phrase")
	}
}

// TestTrackerClosedDoorHeadCancel is the field bug: a closed-door block ("Дверь
// закрыта") must be treated as a movement refusal so the pending FIFO head is
// popped — otherwise pending_moves leaks and stays stuck at 1 while position
// holds. Position must not drift.
func TestTrackerClosedDoorHeadCancel(t *testing.T) {
	// The closed-door line is recognized as a refusal (case-insensitive; it is
	// sentence-initial and capitalized live).
	if !IsRefusal("Дверь закрыта.") {
		t.Fatal("IsRefusal should match the RU closed-door phrase 'Дверь закрыта'")
	}
	// A non-movement line must NOT be treated as a refusal (conservative match).
	if IsRefusal("Вы открываете дверь на север.") {
		t.Error("a plain door-open narration must not be treated as a refusal")
	}

	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S") // queued a step; the door in that direction is shut
	if tr.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", tr.PendingCount())
	}
	// The session bumper calls CancelHead when IsRefusal fires; simulate that.
	if IsRefusal("Дверь закрыта.") {
		tr.CancelHead()
	}
	if tr.PendingCount() != 0 {
		t.Errorf("closed door should clear pending_moves, got %d", tr.PendingCount())
	}
	if tr.Position().Coord.X != 0 || tr.Position().Confidence != Green {
		t.Errorf("position must not drift on a closed-door block: %+v", tr.Position())
	}
}

func TestTrackerSpeedwalkMultiplePending(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	tr.PushMove("S")
	if tr.PendingCount() != 2 {
		t.Fatalf("pending = %d, want 2", tr.PendingCount())
	}
	// First room block confirms step 1; one pending remains.
	pos, _ := tr.Reconcile(ev("Вторая", "desc two", "N S"))
	if pos.Coord.X != 1 || pos.Confidence != Green {
		t.Errorf("step1 pos = %+v", pos)
	}
	if tr.PendingCount() != 1 {
		t.Errorf("pending after step1 = %d, want 1", tr.PendingCount())
	}
	// Second block confirms step 2.
	pos, _ = tr.Reconcile(ev("Третья", "desc three", "N"))
	if pos.Coord.X != 2 || pos.Confidence != Green {
		t.Errorf("step2 pos = %+v", pos)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("pending after step2 = %d", tr.PendingCount())
	}
}

func TestTrackerFollowingEmptyQueue(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	// No PushMove — following/teleport arrives with an empty queue. The neighbor
	// search should find the adjacent room matching the event.
	pos, changed := tr.Reconcile(ev("Вторая", "desc two", "N S"))
	if !changed || pos.Confidence != Green || pos.Coord.X != 1 {
		t.Errorf("following neighbor search pos = %+v", pos)
	}
}

func TestTrackerTeleportLostThenReanchor(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	// True teleport: NO pending move (empty queue), an unknown room that matches
	// no neighbor and no unique fingerprint => genuine loss (🔴). Red is now
	// reserved for the no-directional-context case.
	pos, _ := tr.Reconcile(ev("Незнакомое место", "somewhere strange", "N E S W"))
	if pos.Confidence != Red {
		t.Fatalf("expected red after teleport with no pending move, got %+v", pos)
	}
	if pos.Coord.X != 0 {
		t.Errorf("lost indicator should stay on last-known room, got x=%d", pos.Coord.X)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("queue should be flushed on lost, pending=%d", tr.PendingCount())
	}
	// Re-anchor recovers.
	rp, ok := tr.Anchor(Coord{"Z", 2, 0, 0})
	if !ok || rp.Confidence != Green || rp.Coord.X != 2 {
		t.Errorf("re-anchor failed: %+v ok=%v", rp, ok)
	}
}

func TestTrackerSeamCrossing(t *testing.T) {
	// Zone A room with a seam to zone B tag 50; the seam command is "на восток".
	rooms := []storage.Room{
		mkRoom("A", 0, 0, 0, 1, "Берег", "shore", "E", 4,
			withAutomaps("B|на восток|50")),
		mkRoom("B", 9, 9, 0, 50, "Море", "open sea", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"A", 0, 0, 0})
	tr.PushMove("E") // "на восток" seam corresponds to E
	pos, _ := tr.Reconcile(ev("Море", "open sea", "N"))
	if pos.Confidence != Green {
		t.Fatalf("seam step not green: %+v", pos)
	}
	if pos.Coord.Zone != "B" || pos.Coord.X != 9 || pos.Coord.Y != 9 {
		t.Errorf("seam target coord wrong: %+v", pos.Coord)
	}
}

func TestTrackerPipeStaysYellow(t *testing.T) {
	// A pipe corridor cell with no hint sits south of the anchor.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Вход", "entry", "S", 0),
		func() storage.Room {
			r := mkRoom("Z", 1, 0, 0, 2, "", "", "N S", 0, withPipe())
			return r
		}(),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	// The pipe emits an exits-only event (no hint). Must stay yellow, never red.
	pos, _ := tr.Reconcile(RoomEvent{Hint: "", Exits: "N S"})
	if pos.Confidence != Yellow {
		t.Errorf("pipe should hold yellow, got %+v", pos)
	}
	if pos.Coord.X != 1 {
		t.Errorf("pipe dead-reckoning should advance to x=1, got %+v", pos.Coord)
	}
}

func TestTrackerFingerprintResolveUniqueHint(t *testing.T) {
	tr := NewTracker(linearIndex())
	// No position at all; a room-event with a unique hint auto-resyncs (green).
	pos, _ := tr.Reconcile(ev("Третья", "desc three", "N"))
	if pos.Confidence != Green || pos.Coord.X != 2 {
		t.Errorf("unique-hint resolve failed: %+v", pos)
	}
}

func TestTrackerFingerprintResolveNonUniqueHintUniqueDesc(t *testing.T) {
	// Two rooms share a hint but differ in desc => distinct fingerprints => the
	// event with the matching desc resolves uniquely.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Склад", "северный угол", "E", 0),
		mkRoom("Z", 5, 5, 0, 2, "Склад", "южный угол", "W", 0),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	pos, _ := tr.Reconcile(ev("Склад", "южный угол", "W"))
	if pos.Confidence != Green || pos.Coord.X != 5 {
		t.Errorf("non-unique hint + unique desc resolve failed: %+v", pos)
	}
}

// TestTrackerUnmappedEdgeStepStaysYellowNotFarGreen is the worst-outcome twin-
// tower repro: a dead-reckoning step over an edge the MAP does not record must
// NOT text-select-jump-green to a far unique-hint cell. Position 🟢 at (-6,-4,0)
// "Северо-восточный угол города" (map ch=SW, NO U); command вв (U) predicts the
// EMPTY cell (-6,-4,1). The arriving "Смотровая площадка" is unique in the STALE
// map at a far (9,-3,1) — but the world has TWO towers. Must be 🟡 at the dead-
// reckoning coord (-6,-4,1), NOT 🟢 at (9,-3,1).
func TestTrackerUnmappedEdgeStepStaysYellowNotFarGreen(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Хилло", -6, -4, 0, 103, "Северо-восточный угол города", "ne corner", "S W", chMask("S", "W")),
		// The stale map records only ONE "Смотровая площадка", far away at (9,-3,1).
		mkRoom("Хилло", 9, -3, 1, 50, "Смотровая площадка", "watchtower", "D", chMask("D")),
	}
	tr := NewTracker(BuildIndex(1, rooms))
	tr.Anchor(Coord{"Хилло", -6, -4, 0})
	if tr.Position().Confidence != Green {
		t.Fatalf("anchor not green: %+v", tr.Position())
	}
	tr.PushMove("U") // вв — the map has no U edge here; predicted (-6,-4,1) is empty
	pos, _ := tr.Reconcile(ev("Смотровая площадка", "watchtower", "D"))

	if pos.Confidence == Green {
		t.Fatalf("must NOT green-anchor on an unmapped-edge step (text-selector jump), got %+v", pos)
	}
	if pos.Confidence != Yellow {
		t.Errorf("unmapped-edge dead-reckoning step should be YELLOW, got %+v", pos)
	}
	if pos.Coord == (Coord{"Хилло", 9, -3, 1}) {
		t.Errorf("must NOT jump to the far unique-hint twin cell (9,-3,1): %+v", pos)
	}
	if pos.Coord != (Coord{"Хилло", -6, -4, 1}) {
		t.Errorf("should hold the dead-reckoning coord (-6,-4,1), got %+v", pos.Coord)
	}
	if !strings.Contains(pos.Reason, "not in map") {
		t.Errorf("reason should note the unmapped edge: %q", pos.Reason)
	}
}

// TestTrackerUniqueHintNonNeighborDuringStepNoGreen is the invariant: during a
// pending dead-reckoning step, a unique-hint cell that is NOT the predicted
// neighbor must not green-anchor — even when the predicted neighbor DOES exist
// but its content mismatches. (Here the predicted neighbor exists with a
// different name; the event's hint is unique elsewhere. Old code would auto-
// resync to the far cell; now it must stay non-green.)
func TestTrackerUniqueHintNonNeighborDuringStepNoGreen(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Старт", "start", "S", chMask("S")),
		mkRoom("Z", 1, 0, 0, 2, "Сосед", "neighbor", "N S", chMask("N", "S")),
		// A far cell whose hint is unique in the map.
		mkRoom("Z", 20, 20, 0, 3, "Далёкая", "faraway", "N", chMask("N")),
	}
	tr := NewTracker(BuildIndex(1, rooms))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S") // predicted neighbor (1,0,0) "Сосед" exists but won't match
	pos, _ := tr.Reconcile(ev("Далёкая", "faraway", "N"))
	if pos.Confidence == Green {
		t.Fatalf("unique-hint non-neighbor during a step must NOT green-anchor, got %+v", pos)
	}
	if pos.Coord == (Coord{"Z", 20, 20, 0}) {
		t.Errorf("must not text-select the far unique-hint cell: %+v", pos)
	}
}

func TestTrackerFingerprintFullyAmbiguousNoResync(t *testing.T) {
	// Two identical rooms (same hint+desc+exits) => fingerprint collides => no
	// auto-resync; with no position we stay red/unknown (never green).
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Портовый склад", "ящики и тюки", "E W", 0),
		mkRoom("Z", 5, 5, 0, 2, "Портовый склад", "ящики и тюки", "E W", 0),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	pos, _ := tr.Reconcile(ev("Портовый склад", "ящики и тюки", "E W"))
	if pos.Confidence != Red {
		t.Errorf("ambiguous fingerprint from no-position must stay red, got %+v", pos)
	}
}

func TestTrackerAmbiguousNeighborStaysYellow(t *testing.T) {
	// From the anchor cell, TWO grid neighbors both match the same event (same
	// hint+exits). With an empty queue (following), the neighbor search must NOT
	// arbitrarily green-anchor to one — it holds 🟡 (M2).
	rooms := []storage.Room{
		// Anchor at (0,0): permissive exits (blank) so both N (x-1) and S (x+1)
		// are searched.
		mkRoom("Z", 0, 0, 0, 1, "Центр", "center", "", 0),
		mkRoom("Z", -1, 0, 0, 2, "Двойник", "twin room", "S", 0), // north neighbor
		mkRoom("Z", 1, 0, 0, 3, "Двойник", "twin room", "S", 0),  // south neighbor
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	// Following event (empty queue) matching both twins equally.
	pos, changed := tr.Reconcile(ev("Двойник", "twin room", "S"))
	if pos.Confidence == Green {
		t.Fatalf("ambiguous neighbor match must NOT green-anchor: %+v", pos)
	}
	if pos.Confidence != Yellow {
		t.Errorf("ambiguous neighbor match should hold yellow, got %+v", pos)
	}
	if pos.Coord.X != 0 {
		t.Errorf("ambiguous match should keep last-known cell (x=0), got %+v", pos.Coord)
	}
	if !changed {
		t.Errorf("confidence dropped green->yellow, expected changed=true")
	}
}

func TestTrackerUniqueNeighborStillGreen(t *testing.T) {
	// Control for M2: exactly one neighbor matches => still green-anchors.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Центр", "center", "", 0),
		mkRoom("Z", -1, 0, 0, 2, "Север", "north room", "S", 0),
		mkRoom("Z", 1, 0, 0, 3, "Юг", "south room", "N", 0),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	pos, _ := tr.Reconcile(ev("Юг", "south room", "N"))
	if pos.Confidence != Green || pos.Coord.X != 1 {
		t.Errorf("unique neighbor match should green-anchor to x=1, got %+v", pos)
	}
}

// hubPipeIndex builds the exact live repro (Хилло north corridor from Портовые
// ворота): a hub with a pipe run to its north.
//
//	(0,-4)  tag=2   ch=ENSW  "Портовые ворота"      HUB
//	(-1,-4) pipe    ch=NS    ""                      untagged narrow corridor
//	(-2,-4) tag=93  ch=NS    "Невдалеке от портовых ворот"
//	(-3,-4) tag=94  ch=NS    "Вдоль живой изгороди"
//
// Axis: N = x-1. One physical `север` from the hub jumps the length-1 pipe and
// lands directly at "Невдалеке" (-2,-4); the MUD emits ONE event for that cell.
func hubPipeIndex() *Index {
	rooms := []storage.Room{
		mkRoom("Хилло", 0, -4, 0, 2, "Портовые ворота", "hub", "N E S W", chMask("N", "E", "S", "W")),
		mkRoom("Хилло", -1, -4, 0, 0, "", "", "N S", chMask("N", "S"), withPipe()),
		mkRoom("Хилло", -2, -4, 0, 93, "Невдалеке от портовых ворот", "near gates", "N S", chMask("N", "S")),
		mkRoom("Хилло", -3, -4, 0, 94, "Вдоль живой изгороди", "along the hedge", "N S", chMask("N", "S")),
		// Fingerprint-colliding decoys elsewhere in the zone, so these targets do
		// NOT auto-resync (step 3) — this forces the step through the dead-reckoning
		// PREDICTION path (step 1) alone, exactly as live. Without the pipe-skip fix,
		// prediction lands on the pipe cell and flaps 🔴; auto-resync can no longer
		// mask it.
		mkRoom("Хилло", 20, 20, 0, 200, "Невдалеке от портовых ворот", "near gates", "N S", chMask("N", "S")),
		mkRoom("Хилло", 21, 21, 0, 201, "Портовые ворота", "hub", "N E S W", chMask("N", "E", "S", "W")),
	}
	return BuildIndex(1, rooms)
}

// TestTrackerHubIntoPipeNoRedFlap is the golden for the confirmed live bug: a
// `север` step from the hub (0,-4) that jumps a length-1 pipe must land 🟢 at the
// first tagged cell past the pipe (-2,-4) on the FIRST step — no red flap.
// Before the fix: dead-reckoning predicted the pipe cell (-1,-4), whose empty
// hint mismatched the "Невдалеке" event -> false 🔴 frozen at (0,-4).
func TestTrackerHubIntoPipeNoRedFlap(t *testing.T) {
	tr := NewTracker(hubPipeIndex())
	tr.Anchor(Coord{"Хилло", 0, -4, 0})
	if tr.Position().Confidence != Green {
		t.Fatalf("anchor not green: %+v", tr.Position())
	}
	tr.PushMove("N") // север
	pos, changed := tr.Reconcile(ev("Невдалеке от портовых ворот", "near gates", "N S"))
	if !changed {
		t.Fatal("expected change on the hub->pipe step")
	}
	if pos.Confidence != Green {
		t.Errorf("hub->pipe step should be GREEN (no red flap), got %+v", pos)
	}
	if pos.Coord != (Coord{"Хилло", -2, -4, 0}) {
		t.Errorf("should land on the first tagged cell past the pipe (-2,-4), got %+v", pos.Coord)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("pending should be popped, got %d", tr.PendingCount())
	}
}

// TestTrackerCorridorThroughPipeToHub is the reverse direction (corridor -> pipe
// -> hub): a `юг` from (-2,-4) jumps the pipe and lands 🟢 at the hub (0,-4).
func TestTrackerCorridorThroughPipeToHub(t *testing.T) {
	tr := NewTracker(hubPipeIndex())
	tr.Anchor(Coord{"Хилло", -2, -4, 0})
	tr.PushMove("S") // юг: S = x+1, jumps pipe (-1,-4) to hub (0,-4)
	pos, _ := tr.Reconcile(ev("Портовые ворота", "hub", "N E S W"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Хилло", 0, -4, 0}) {
		t.Errorf("corridor->pipe->hub should be green at hub (0,-4), got %+v", pos)
	}
}

// TestTrackerLongPipeRunNoRedFlap covers a pipe run of >=2 cells: hub jumps two
// pipe cells and lands 🟢 on the first tagged cell past them.
func TestTrackerLongPipeRunNoRedFlap(t *testing.T) {
	// (0,0) hub -> (1,0) pipe -> (2,0) pipe -> (3,0) tagged "Конец". Axis S = x+1.
	// A fingerprint decoy for "Конец" blocks auto-resync so only the prediction
	// (with the pipe-skip) can green the step.
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Хаб", "hub", "S", chMask("S")),
		mkRoom("Z", 1, 0, 0, 0, "", "", "N S", chMask("N", "S"), withPipe()),
		mkRoom("Z", 2, 0, 0, 0, "", "", "N S", chMask("N", "S"), withPipe()),
		mkRoom("Z", 3, 0, 0, 5, "Конец", "end", "N", chMask("N")),
		mkRoom("Z", 30, 30, 0, 99, "Конец", "end", "N", chMask("N")),
	}
	tr := NewTracker(BuildIndex(1, rooms))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(ev("Конец", "end", "N"))
	if pos.Confidence != Green || pos.Coord != (Coord{"Z", 3, 0, 0}) {
		t.Errorf("long pipe run should land green on (3,0), got %+v", pos)
	}
}

// TestTrackerSupersetMismatchDegradesToYellow is the graceful-degradation golden
// (the live Хилло repro): name+desc MATCH but the live game reports a SUPERSET of
// the map's exits (a room grew a U exit over years of new content). With a pending
// directional move, the tracker must degrade to 🟡 at the assumed cell — NOT 🔴 —
// and expose the exit diff (+U). No auto-green: accepting the outdated map is a
// conscious manual re-anchor.
func TestTrackerSupersetMismatchDegradesToYellow(t *testing.T) {
	// (-4,-4) tag=95 "Вдоль живой изгороди" [NSW]; N -> (-5,-4) pipe; -> (-6,-4)
	// tag=103 "Северо-восточный угол города" ch=[SW]. Live event exits = S W U.
	rooms := []storage.Room{
		mkRoom("Хилло", -4, -4, 0, 95, "Вдоль живой изгороди", "hedge", "N S W", chMask("N", "S", "W")),
		mkRoom("Хилло", -5, -4, 0, 0, "", "", "N S", chMask("N", "S"), withPipe()),
		mkRoom("Хилло", -6, -4, 0, 103, "Северо-восточный угол города", "ne corner", "S W", chMask("S", "W")),
	}
	tr := NewTracker(BuildIndex(1, rooms))
	tr.Anchor(Coord{"Хилло", -4, -4, 0})
	tr.PushMove("N") // север
	// Live event: name+desc match the assumed cell, but exits are a superset (S W U).
	pos, changed := tr.Reconcile(ev("Северо-восточный угол города", "ne corner", "S W U"))
	if !changed {
		t.Fatal("expected a change on the superset-mismatch step")
	}
	if pos.Confidence != Yellow {
		t.Fatalf("superset mismatch with pending must degrade to YELLOW, got %+v", pos)
	}
	if pos.Coord != (Coord{"Хилло", -6, -4, 0}) {
		t.Errorf("should assume the predicted cell (-6,-4), got %+v", pos.Coord)
	}
	// Exit diff: +U live (game has U, map lacks it), nothing removed.
	if len(pos.ExitsAddedLive) != 1 || pos.ExitsAddedLive[0] != "U" {
		t.Errorf("expected ExitsAddedLive=[U], got %v", pos.ExitsAddedLive)
	}
	if len(pos.ExitsRemovedMap) != 0 {
		t.Errorf("expected no removed map exits, got %v", pos.ExitsRemovedMap)
	}
	if !strings.Contains(pos.Reason, "+U") {
		t.Errorf("reason should surface the +U diff, got %q", pos.Reason)
	}
	if tr.PendingCount() != 0 {
		t.Errorf("pending should be consumed, got %d", tr.PendingCount())
	}
}

// TestTrackerHardHintMismatchVetoesToRed: a HARD mismatch where the event's HINT
// (name) contradicts the assumed cell is a TEXT VETO — the server likely
// teleported us, so we must NOT hold a false 🟡 assumed position. Full-flush to
// 🔴, position NOT advanced to the assumed cell; wait for auto-resync or manual
// re-anchor. (Contrast: exits diverging with a MATCHING hint stays 🟡 — see
// TestTrackerSupersetMismatchDegradesToYellow.)
func TestTrackerHardHintMismatchVetoesToRed(t *testing.T) {
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Старт", "start", "S", chMask("S")),
		mkRoom("Z", 1, 0, 0, 2, "Ожидаемая", "expected", "N S", chMask("N", "S")),
	}
	tr := NewTracker(BuildIndex(1, rooms))
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	// Event whose name/desc contradict the assumed cell (2) and has no unique
	// fingerprint elsewhere -> text veto -> 🔴, NOT an assumed-yellow.
	pos, _ := tr.Reconcile(ev("Другое", "different room", "N E S W"))
	if pos.Confidence != Red {
		t.Fatalf("hint-contradicting mismatch with pending must VETO to RED, got %+v", pos)
	}
	// Position must NOT be advanced to the assumed cell — stays on last-known.
	if pos.Coord != (Coord{"Z", 0, 0, 0}) {
		t.Errorf("red veto should keep last-known cell (0,0,0), got %+v", pos.Coord)
	}
	// No assumed exit diff on a veto (we did not accept the cell).
	if len(pos.ExitsAddedLive) != 0 || len(pos.ExitsRemovedMap) != 0 {
		t.Errorf("veto-red should carry no exit diff, got +%v -%v", pos.ExitsAddedLive, pos.ExitsRemovedMap)
	}
	// Pending flushed on loss.
	if tr.PendingCount() != 0 {
		t.Errorf("queue should be flushed on veto-red, pending=%d", tr.PendingCount())
	}
}

// TestTrackerNoPendingMismatchStaysRed: without a pending move (teleport/
// following) and no neighbor/fingerprint match, the tracker is genuinely lost —
// this remains 🔴.
func TestTrackerNoPendingMismatchStaysRed(t *testing.T) {
	tr := NewTracker(linearIndex())
	tr.Anchor(Coord{"Z", 0, 0, 0})
	// No PushMove: empty queue. Unknown room, no neighbor match, no fingerprint.
	pos, _ := tr.Reconcile(ev("Совершенно чужое", "alien place", "N E S W U D"))
	if pos.Confidence != Red {
		t.Errorf("no-pending mismatch (true loss) must stay RED, got %+v", pos)
	}
}

func TestTrackerDoorMarkerTolerance(t *testing.T) {
	// A live event reports doors "(N) S" while the map stores plain "N S"; the
	// reconciler must match (door markers stripped in the fingerprint/dir set).
	rooms := []storage.Room{
		mkRoom("Z", 0, 0, 0, 1, "Вход", "entry", "S", 0),
		mkRoom("Z", 1, 0, 0, 2, "Комната", "room", "N S", 0),
	}
	idx := BuildIndex(1, rooms)
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 0, 0, 0})
	tr.PushMove("S")
	pos, _ := tr.Reconcile(ev("Комната", "room", "(N) S"))
	if pos.Confidence != Green || pos.Coord.X != 1 {
		t.Errorf("door-marker tolerance failed: %+v", pos)
	}
}
