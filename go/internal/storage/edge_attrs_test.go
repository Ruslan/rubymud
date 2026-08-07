package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

// Edge-attribute behavior (migration 018): the traversal phrase, the door + lock +
// opening command, and the routing veto.
//
// Every one of these tests names ONE mechanism that has no other owner:
//   - the derivation putting rooms.doors onto the parsed rows (the door migration);
//   - a set attribute making the face hand-drawn, which is the ONLY reason it
//     survives «обновить комнату»;
//   - the undo journal carrying the attributes back, which the room snapshots
//     cannot do (nothing about the rooms implies a phrase);
//   - a fork carrying them, likewise.

// attrsWorldInput is two rooms with a plain two-way passage between them, one of
// which advertises a door on the shared face. Deliberately minimal: what varies
// between the tests below is the markup, not the map.
func attrsWorldInput() MapSetInput {
	return MapSetInput{
		Name: "Attrs World", RoomCount: 2,
		Rooms: []Room{
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Hint: "a", Exits: "(S)", EDirs: `["S"]`, Ch: 2,
				Doors: `["S"]`},
			{Zone: "Alpha", X: 1, Y: 0, L: 0, Hint: "b", Exits: "N", EDirs: `["N"]`, Ch: 1,
				Doors: "[]"},
		},
	}
}

// faceOf returns the grid row leaving the cell in dir, or nil.
func faceOf(t *testing.T, store *Store, id int64, zone string, x, y, l int, dir string) *MapEdge {
	t.Helper()
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	for i := range edges {
		e := &edges[i]
		if e.Kind == EdgeKindGrid && e.FromZone == zone && e.FromX == x && e.FromY == y &&
			e.FromL == l && e.Dir == dir {
			return e
		}
	}
	return nil
}

func strPtr(s string) *string { return &s }

// TestDerivationCarriesRoomDoorsOntoEdges is the door migration itself: the door
// the exits line put in parentheses has to end up on the DIRECTED row that walks
// that face, because that is where mapper.doorGrade now looks. The reverse row
// carries no door — the door was recorded on one side only, and inventing it on the
// other would turn a "presumed" door into a "confirmed" one.
func TestDerivationCarriesRoomDoorsOntoEdges(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	fwd := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if fwd == nil {
		t.Fatal("no row on the a->b face")
	}
	if !fwd.Door {
		t.Errorf("a's south face records a door in rooms.doors but the edge does not: %+v", fwd.EdgeAttrs)
	}
	back := faceOf(t, store, id, "Alpha", 1, 0, 0, "N")
	if back == nil {
		t.Fatal("no row on the b->a face")
	}
	if back.Door {
		t.Errorf("b records no door, yet its edge claims one: %+v", back.EdgeAttrs)
	}
}

// TestSetEdgeAttrsMakesTheFaceHandDrawn: a phrase cannot live on a derived row —
// the next room write recomputes it from the exits line and the phrase is gone. So
// the write promotes the face, keeping the passage it already had.
func TestSetEdgeAttrsMakesTheFaceHandDrawn(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if before := faceOf(t, store, id, "Alpha", 0, 0, 0, "S"); before.Source != EdgeSourceParsed {
		t.Fatalf("precondition: the face should start out derived, got %q", before.Source)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Phrase: strPtr("двигать ящик")},
	}); err != nil || !found {
		t.Fatalf("set attrs: err=%v found=%v", err, found)
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("the face lost its passage")
	}
	if e.Source != EdgeSourceManual {
		t.Errorf("face source = %q, want %q — a derived row cannot hold a phrase", e.Source, EdgeSourceManual)
	}
	if e.Phrase != "двигать ящик" {
		t.Errorf("phrase = %q", e.Phrase)
	}
	if !e.Passable {
		t.Error("the passage was lost: setting an attribute must not close the face")
	}
	if to, ok := e.To(); !ok || to != (EdgeCell{"Alpha", 1, 0, 0}) {
		t.Errorf("target = %v (ok=%v), want b", to, ok)
	}
	// The door the derivation had put there must survive the promotion: the patch
	// said nothing about doors, and an omitted attribute means "leave it alone".
	if !e.Door {
		t.Error("promoting the face dropped the door the derivation had found")
	}
}

// TestEdgeAttrsSurviveRoomRerecord is the reason the layer exists at all: the
// player re-records the room from the live text (which knows nothing about a phrase
// or a lock) and the markup is still there afterwards.
func TestEdgeAttrsSurviveRoomRerecord(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{
			Weight: floatPtr(.5),
			Phrase: strPtr("двигать ящик"), Locked: boolPtr(true),
			OpenCmd: strPtr("дёрнуть рычаг"), NoRoute: boolPtr(true),
		},
	}); err != nil || !found {
		t.Fatalf("set attrs: err=%v found=%v", err, found)
	}
	// «Обновить комнату из живого»: same cell, exits re-parsed from the display string.
	exits := "(S)"
	hint := "a (обновлено)"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: "Alpha", X: 0, Y: 0, L: 0,
		Fields: RoomFields{Hint: &hint, Exits: &exits},
	}); err != nil || !found {
		t.Fatalf("re-record room: err=%v found=%v", err, found)
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("the hand-drawn face vanished on re-record")
	}
	if e.Weight != .5 || e.Phrase != "двигать ящик" || !e.Locked || e.OpenCmd != "дёрнуть рычаг" || !e.NoRoute {
		t.Errorf("re-recording the room erased the markup: %+v", e.EdgeAttrs)
	}
}

// TestUndoRestoresEdgeAttrs: the undo journal snapshots rooms plus the hand-drawn
// rows, and NOTHING about the rooms implies an attribute — so if the manual half of
// TopologyBefore did not carry them, undo would silently drop them.
func TestUndoRestoresEdgeAttrs(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Weight: floatPtr(.1), Phrase: strPtr("лезть ковёр"), NoRoute: boolPtr(true)},
	}); err != nil || !found {
		t.Fatalf("first write: err=%v found=%v", err, found)
	}
	// A SECOND write, whose before-state is the first one's result. Undoing it must
	// bring back the phrase — not the pristine derived face.
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Weight: floatPtr(.5), Phrase: strPtr("совсем другое"), NoRoute: boolPtr(false)},
	})
	if err != nil || !found {
		t.Fatalf("second write: err=%v found=%v", err, found)
	}
	if e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S"); e.Weight != .5 || e.Phrase != "совсем другое" || e.NoRoute {
		t.Fatalf("second write did not land: %+v", e.EdgeAttrs)
	}
	if _, err := store.RestoreTopologyBefore(id, before); err != nil {
		t.Fatalf("undo: %v", err)
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("undo removed the face entirely")
	}
	if e.Weight != .1 || e.Phrase != "лезть ковёр" || !e.NoRoute {
		t.Errorf("undo did not restore the prior attributes: %+v", e.EdgeAttrs)
	}
}

// TestForkCarriesEdgeAttrs: the fork copies edge rows verbatim rather than
// re-deriving them, and four of the five attributes are derivable from nothing — a
// re-deriving fork would drop them and the player's markup would evaporate the first
// time they wrote to a frozen set.
func TestForkCarriesEdgeAttrs(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Weight: floatPtr(.1), Phrase: strPtr("двигать ящик"), Locked: boolPtr(true)},
	}); err != nil || !found {
		t.Fatalf("set attrs: err=%v found=%v", err, found)
	}
	forkID, err := store.ForkMapSet(id)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	e := faceOf(t, store, forkID, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("the fork lost the face")
	}
	if e.Weight != .1 || e.Phrase != "двигать ящик" || !e.Locked {
		t.Errorf("fork dropped the markup: %+v", e.EdgeAttrs)
	}
}

// TestRematerializeKeepsEdgeAttrs: `mudhost --rematerialize-edges` drops every
// DERIVED row and rebuilds it. It must not touch the hand-drawn ones, or the repair
// handle for a stale map would double as an eraser for the player's own markup.
func TestRematerializeKeepsEdgeAttrs(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Weight: floatPtr(.5), OpenCmd: strPtr("say friend"), Door: boolPtr(true)},
	}); err != nil || !found {
		t.Fatalf("set attrs: err=%v found=%v", err, found)
	}
	if _, err := store.RematerializeMapEdges(id); err != nil {
		t.Fatalf("RematerializeMapEdges: %v", err)
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("rematerialize removed the hand-drawn face")
	}
	if e.Weight != .5 || e.OpenCmd != "say friend" || !e.Door {
		t.Errorf("rematerialize erased the markup: %+v", e.EdgeAttrs)
	}
	// And the derived rows around it are still whole.
	if back := faceOf(t, store, id, "Alpha", 1, 0, 0, "N"); back == nil {
		t.Error("rematerialize lost the reverse derived row")
	}
}

// TestSetEdgeAttrsRefusesAFaceWithNoPassage: attributes say HOW a passage is taken.
// On a face with no passage there is nothing to describe, and writing one anyway
// would let a mistyped direction quietly punch a hole in a wall.
func TestSetEdgeAttrsRefusesAFaceWithNoPassage(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// a claims only S; its EAST face has no room beyond it and no row.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "E",
		Attrs: EdgeAttrPatch{Phrase: strPtr("двигать ящик")},
	}); err != nil || found {
		t.Fatalf("attrs on a face with no passage: err=%v found=%v, want found=false", err, found)
	}
	if e := faceOf(t, store, id, "Alpha", 0, 0, 0, "E"); e != nil {
		t.Errorf("a refused write created a row anyway: %+v", e)
	}
}

// TestSetEdgeAttrsDoesNotPromoteOnANoOp: a write that asserts nothing must leave a
// derived face derived. Otherwise a stray call (an agent echoing back what it read)
// would freeze the face against future room edits for no reason.
func TestSetEdgeAttrsDoesNotPromoteOnANoOp(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// b's north face is derived, carries no door and no markup; re-assert exactly that.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 1, Y: 0, L: 0, Dir: "N",
		Attrs: EdgeAttrPatch{Phrase: strPtr(""), NoRoute: boolPtr(false)},
	}); err != nil || !found {
		t.Fatalf("no-op write: err=%v found=%v", err, found)
	}
	if e := faceOf(t, store, id, "Alpha", 1, 0, 0, "N"); e.Source != EdgeSourceParsed {
		t.Errorf("a no-op write promoted the face to %q", e.Source)
	}
}

// TestZoneFeedCarriesEdgeAttrs: the map pane's edge editor renders its controls
// from SlimRoom.EA, so a face whose attributes never reach the wire is a face the
// player cannot see or take back — the same trap the hand-drawn layer was given `m`
// and `mb` to avoid. Only the faces that SAY something travel, which is why the
// assertion is about a specific face rather than about six slots per room.
func TestZoneFeedCarriesEdgeAttrs(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{
			Phrase: strPtr("двигать ящик"), Locked: boolPtr(true),
			OpenCmd: strPtr("дёрнуть рычаг"), NoRoute: boolPtr(true),
		},
	}); err != nil || !found {
		t.Fatalf("set attrs: err=%v found=%v", err, found)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	var a, b *SlimRoom
	for i := range rooms {
		switch rooms[i].X {
		case 0:
			a = &rooms[i]
		case 1:
			b = &rooms[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("zone feed lost a room: %+v", rooms)
	}
	var got *SlimEdgeAttr
	for i := range a.EA {
		if a.EA[i].Dir == "S" {
			got = &a.EA[i]
		}
	}
	if got == nil {
		t.Fatalf("the marked face is missing from the zone feed: %+v", a.EA)
	}
	want := SlimEdgeAttr{Dir: "S", Phrase: "двигать ящик", Door: true,
		Locked: true, OpenCmd: "дёрнуть рычаг", NoRoute: true}
	if *got != want {
		t.Errorf("EA[S] = %+v, want %+v", *got, want)
	}
	// The unmarked reverse face carries nothing at all — an empty list, not a row of
	// defaults, or every zone payload would grow by six entries per room.
	for _, e := range b.EA {
		t.Errorf("the unmarked cell shipped an attribute entry: %+v", e)
	}
}

// --- the hand-drawn face vs. the door the room records ----------------------
//
// Since doors moved onto the edges, the DERIVATION is what puts rooms.doors on a
// face — and it skips a face a hand-drawn row owns (dropFacesTakenByManual). So a
// mark on a face whose room records a door used to delete that door from the graph
// silently: the route stopped opening a door it had always opened, and the
// element-by-element acceptance on the live set could not see it, because that set
// carries no hand-drawn rows at all.

// TestHandDrawnFaceInheritsTheRoomsDoor: drawing on a face takes over what the
// derivation had on it, doors included. Both halves of the default (paired) mark.
func TestHandDrawnFaceInheritsTheRoomsDoor(t *testing.T) {
	store := newMapperTestStore(t)
	// a(0,0) records a door on S (toward b); give b a door on its N face too, so the
	// pair's REVERSE half is checked against a real value rather than against false.
	in := attrsWorldInput()
	in.Rooms[1].Doors = `["N"]`
	in.Rooms[1].Exits = "(N)"
	id, err := store.CreateMapSet(in)
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	fwd := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if fwd == nil || fwd.Source != EdgeSourceManual {
		t.Fatalf("the face is not hand-drawn: %+v", fwd)
	}
	if !fwd.Door {
		t.Error("drawing on the face deleted the door rooms.doors records for it")
	}
	back := faceOf(t, store, id, "Alpha", 1, 0, 0, "N")
	if back == nil || back.Source != EdgeSourceManual {
		t.Fatalf("the reverse face is not hand-drawn: %+v", back)
	}
	if !back.Door {
		t.Error("the pair's reverse half lost the door its own room records")
	}
}

// ...and a face whose room records NO door must not gain one. The seed is the
// derivation's value, not a blanket true.
func TestHandDrawnFaceDoesNotInventADoor(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput()) // b records no doors
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 1, Y: 0, L: 0, Dir: "N", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	e := faceOf(t, store, id, "Alpha", 1, 0, 0, "N")
	if e == nil {
		t.Fatal("no row on the marked face")
	}
	if e.Door {
		t.Errorf("a hand-drawn row invented a door its room never recorded: %+v", e.EdgeAttrs)
	}
}

// TestBackfillEdgeDoorsCoversHandDrawnRows: the one-time 018 backfill has to reach
// rows a re-materialization would skip. A database upgraded from the hidden-exit
// migration carries hand-drawn rows written before the door column existed; leaving them
// at door=0 would drop a door the map has always recorded, permanently and silently.
func TestBackfillEdgeDoorsCoversHandDrawnRows(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	// Simulate the pre-018 state of BOTH kinds of row: nothing knows about doors.
	if err := store.db.Exec(`UPDATE map_edges SET door = 0 WHERE map_set_id = ?`, id).Error; err != nil {
		t.Fatalf("clear doors: %v", err)
	}
	changed, err := store.BackfillEdgeDoors()
	if err != nil {
		t.Fatalf("BackfillEdgeDoors: %v", err)
	}
	if changed[id] != 1 {
		t.Errorf("changed %d rows, want exactly the one face rooms.doors names", changed[id])
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil || e.Source != EdgeSourceManual {
		t.Fatalf("the hand-drawn row went missing: %+v", e)
	}
	if !e.Door {
		t.Error("the backfill skipped the hand-drawn row — the very row a rematerialization cannot reach")
	}
	// Idempotent: a second run has nothing to say.
	again, err := store.BackfillEdgeDoors()
	if err != nil {
		t.Fatalf("second BackfillEdgeDoors: %v", err)
	}
	if again[id] != 0 {
		t.Errorf("second run rewrote %d rows, want 0", again[id])
	}
}

// --- a mark must not take back words already said ---------------------------
//
// `open`/`blocked` answer "is there a way here". The five attributes answer "how is
// it taken" and "may the router use it". A mark rewrote all five to their zero
// value, and — because marks are PAIRED by default — it did so on the REVERSE face
// too, which the caller never named. That is the "words in the operator's mouth"
// problem the one-sided attribute write exists to avoid, running backwards.

// markedPair marks a hidden exit on the a->b face (the default pair) after both
// faces have been described, and returns the two rows.
func markedPairAfterAttrs(t *testing.T, store *Store, id int64) (fwd, back *MapEdge) {
	t.Helper()
	for _, w := range []struct {
		x, y int
		dir  string
		p    EdgeAttrPatch
	}{
		{0, 0, "S", EdgeAttrPatch{Phrase: strPtr("двигать ящик"), Locked: boolPtr(true), NoRoute: boolPtr(true)}},
		{1, 0, "N", EdgeAttrPatch{Phrase: strPtr("лезть ковёр"), OpenCmd: strPtr("дёрнуть рычаг")}},
	} {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
			Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: w.x, Y: w.y, L: 0, Dir: w.dir, Attrs: w.p,
		}); err != nil || !found {
			t.Fatalf("describe %d,%d %s: err=%v found=%v", w.x, w.y, w.dir, err, found)
		}
	}
	// The PAIRED mark: the caller names only the a->b face.
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	return faceOf(t, store, id, "Alpha", 0, 0, 0, "S"), faceOf(t, store, id, "Alpha", 1, 0, 0, "N")
}

func TestMarkKeepsTheAttributesOfBothFaces(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	fwd, back := markedPairAfterAttrs(t, store, id)
	if fwd == nil || back == nil {
		t.Fatalf("a face vanished: fwd=%+v back=%+v", fwd, back)
	}
	// The face the caller NAMED keeps everything it was told.
	if fwd.Phrase != "двигать ящик" || !fwd.Locked || !fwd.NoRoute {
		t.Errorf("the marked face lost its attributes: %+v", fwd.EdgeAttrs)
	}
	if !fwd.Door {
		t.Errorf("the marked face lost the door the derivation had put on it: %+v", fwd.EdgeAttrs)
	}
	// ...and so does the one it did NOT name. This is the half that matters most:
	// the operator said nothing whatsoever about this direction.
	if back.Phrase != "лезть ковёр" || back.OpenCmd != "дёрнуть рычаг" {
		t.Errorf("the pair's REVERSE face was emptied by a mark that never mentioned it: %+v", back.EdgeAttrs)
	}
	// The mark itself did land.
	if fwd.Source != EdgeSourceManual || !fwd.Passable {
		t.Errorf("the mark did not apply: %+v", fwd)
	}
}

// An idempotent re-mark is the everyday version of the same bug — and the tool's
// own description invites passing `mark` together with attributes.
func TestRepeatedMarkIsNotDestructive(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	markedPairAfterAttrs(t, store, id)
	for i := 0; i < 3; i++ {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
			Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		}); err != nil || !found {
			t.Fatalf("re-mark %d: err=%v found=%v", i, err, found)
		}
	}
	fwd := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if fwd == nil || fwd.Phrase != "двигать ящик" || !fwd.Locked || !fwd.NoRoute {
		t.Errorf("re-marking an already-marked face destroyed its markup: %+v", fwd)
	}
}

// Flipping a face open -> blocked -> open keeps the markup too: the flip says
// something about the passage, not about how it is taken.
func TestBlockingAndReopeningKeepsTheAttributes(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	markedPairAfterAttrs(t, store, id)
	for _, kind := range []TopologyOpKind{TopoBlockManualEdge, TopoAddManualEdge} {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
			Kind: kind, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		}); err != nil || !found {
			t.Fatalf("%s: err=%v found=%v", kind, err, found)
		}
	}
	fwd := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if fwd == nil || fwd.Phrase != "двигать ящик" || !fwd.Locked {
		t.Errorf("block+reopen lost the markup: %+v", fwd)
	}
}

// "auto" is the ONE mark that drops them, because it hands the face back to the
// derivation — nobody is speaking for it any more. Documented, and pinned so the
// fix above cannot accidentally make markup un-droppable.
func TestClearingTheMarkDropsTheAttributes(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	markedPairAfterAttrs(t, store, id)
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoRemoveManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S", OneWay: true,
	}); err != nil || !found {
		t.Fatalf("auto: err=%v found=%v", err, found)
	}
	fwd := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if fwd == nil {
		t.Fatal("clearing the mark removed the passage the rooms describe")
	}
	if fwd.Source != EdgeSourceParsed {
		t.Errorf("the face did not go back to the derivation: %+v", fwd)
	}
	if fwd.Phrase != "" || fwd.Locked || fwd.NoRoute {
		t.Errorf("hand-drawn attributes outlived the hand-drawn row: %+v", fwd.EdgeAttrs)
	}
	// ...and the door comes back, because the derivation puts it there.
	if !fwd.Door {
		t.Errorf("the derived face lost the door rooms.doors records: %+v", fwd.EdgeAttrs)
	}
}

// A patch that changes NOTHING must not promote a derived face: a manual row owns
// its face for good, so rooms.doors would stop reaching the graph and «обновить
// комнату» could never put it right. The everyday triggers are an agent
// re-asserting a door it just read and a checkbox ticked then unticked.
func TestNoOpPatchDoesNotFreezeADerivedFace(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// a's S face is DERIVED and already carries a door (rooms.doors says so), so
	// Any() is true — the case the previous guard let through.
	before := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if before.Source != EdgeSourceParsed || !before.Door {
		t.Fatalf("precondition: want a derived face carrying a door, got %+v", before)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: EdgeAttrPatch{Door: boolPtr(true)}, // re-asserting what is already true
	}); err != nil || !found {
		t.Fatalf("no-op patch: err=%v found=%v", err, found)
	}
	after := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if after.Source != EdgeSourceParsed {
		t.Errorf("re-asserting a door froze the face against the derivation (source=%q)", after.Source)
	}
}

// The zone feed's lists are all `[]` when empty; one of them marshalling as `null`
// is a trap for the next reader (and the UI iterates them unconditionally).
func TestZoneFeedSendsAnEmptyAttrListNotNull(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	for _, r := range rooms {
		if r.EA == nil {
			t.Errorf("room %d,%d ships ea=null; every other list on this record is []", r.X, r.Y)
		}
	}
	blob, err := json.Marshal(rooms)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), `"ea":null`) {
		t.Errorf("the wire form carries \"ea\":null: %s", blob)
	}
}

// TestZoneFeedIgnoresSeamRows: a SEAM row carries dir=”, so a seam with any
// attribute set would arrive in `ea` under an empty direction and the edge editor —
// which keys its two blocks by direction — would read it as a face. The scan next
// to this one has always filtered kind='grid'; this one must too. Nothing can write
// a seam attribute today, which is exactly why the guard has to be here BEFORE
// something can: by then the symptom would be a face silently overwritten in the UI.
func TestZoneFeedIgnoresSeamRows(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// A seam row leaving a(0,0) with an attribute set, written directly — that is
	// what future seam markup (or a hand-edited DB) produces.
	if err := store.db.Exec(`INSERT INTO map_edges
		(map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, dir,
		 command, target_zone, target_tag, no_route)
		VALUES (?, 'seam', 'manual', 1, 'Alpha', 0, 0, 0, '', 'на север', 'Beta', 7, 1)`,
		id).Error; err != nil {
		t.Fatalf("insert seam row: %v", err)
	}
	rooms, err := store.ListSlimRooms(id, "Alpha")
	if err != nil {
		t.Fatalf("ListSlimRooms: %v", err)
	}
	for _, r := range rooms {
		for _, a := range r.EA {
			if a.Dir == "" {
				t.Errorf("a seam row reached the zone feed as a face: room %d,%d ea=%+v", r.X, r.Y, a)
			}
		}
	}
}

// TestHiddenExitOnADoorFaceTheGraphDenies is where the door SEED is the only thing
// that can put the door on the row — the case the "inherit from the existing row"
// path cannot cover, because there is no existing row.
//
// The shape is the hidden-exit layer's own reason to exist: the exits line says
// "(S)" (so rooms.doors records a door there) while the AUTHORITATIVE ch mask omits
// S, so the derivation produces NO row on that face at all. A player who knows the
// passage is real marks it — and the door the map has always recorded must come
// with it, or the route walks into a shut door it used to open.
func TestHiddenExitOnADoorFaceTheGraphDenies(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{
		Name: "Denied door", RoomCount: 2,
		Rooms: []Room{
			// a prints "(S)" — door on S — but its ch bit for S is clear (only N is set),
			// and ch is authoritative, so no S edge is derived.
			{Zone: "Alpha", X: 0, Y: 0, L: 0, Hint: "a", Exits: "(S)", EDirs: `["S"]`, Ch: 1,
				Doors: `["S"]`},
			// b denies the back-link too, so nothing derives the pair from either side.
			{Zone: "Alpha", X: 1, Y: 0, L: 0, Hint: "b", Exits: "S", EDirs: `["S"]`, Ch: 2,
				Doors: "[]"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S"); e != nil {
		t.Fatalf("precondition: the derivation should produce NO row on this face, got %+v", e)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoAddManualEdge, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "S",
	}); err != nil || !found {
		t.Fatalf("mark: err=%v found=%v", err, found)
	}
	e := faceOf(t, store, id, "Alpha", 0, 0, 0, "S")
	if e == nil {
		t.Fatal("the mark created no row")
	}
	if !e.Door {
		t.Error("the hand-drawn row was created without the door rooms.doors records for that face")
	}
	// The reverse half of the pair takes b's doors, which are empty — no invention.
	if back := faceOf(t, store, id, "Alpha", 1, 0, 0, "N"); back == nil || back.Door {
		t.Errorf("the reverse face invented a door: %+v", back)
	}
}
