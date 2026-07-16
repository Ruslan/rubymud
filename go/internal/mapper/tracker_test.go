package mapper

import (
	"encoding/json"
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
	tr.PushMove("S")
	// A room-event that matches nothing adjacent and no unique fingerprint (a
	// teleport to an unknown place) => lost (red) on the last-known room.
	pos, _ := tr.Reconcile(ev("Незнакомое место", "somewhere strange", "N E S W"))
	if pos.Confidence != Red {
		t.Fatalf("expected red after teleport mismatch, got %+v", pos)
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
