package storage

import (
	"strings"
	"testing"

	"rubymud/go/internal/mapimport"
)

// These cover the phase-5 slice-3 topology CRUD arms of ApplyTopologyOp
// (upsert/link/unlink/delete) and the GENERALIZED full-room before-state snapshot
// (RoomSnapshot list) + RestoreRoomSnapshots undo, at the storage layer.

func roomAt(t *testing.T, store *Store, setID int64, zone string, x, y, l int) *Room {
	t.Helper()
	var r Room
	err := store.DB().Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		setID, zone, x, y, l).First(&r).Error
	if err != nil {
		return nil
	}
	return &r
}

func edirsOf(t *testing.T, store *Store, setID int64, zone string, x, y, l int) []string {
	t.Helper()
	r := roomAt(t, store, setID, zone, x, y, l)
	if r == nil {
		return nil
	}
	return decodeStrArray(r.EDirs)
}

// TestUpsertCreatesThenPartialUpdatesThenUndo: create a new room, partial-update
// only some fields (others preserved), and undo both. Undo of the update restores
// the prior fields; undo of the create deletes the room. Fingerprint updates on a
// hint change.
func TestUpsertCreatesThenPartialUpdatesThenUndo(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	j := store.WriteJournal()

	// Create a NEW room at an empty cell (5,5,0) in zone Alpha.
	hint := "New Room"
	desc := "a fresh corridor"
	exits := "N S"
	dt := true
	fCreate := RoomFields{Hint: &hint, Desc: &desc, Exits: &exits, IsDT: &dt}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Alpha", X: 5, Y: 5, L: 0, Fields: fCreate})
	if err != nil || !found {
		t.Fatalf("create upsert: found=%v err=%v", found, err)
	}
	if len(before) != 1 || before[0].Existed {
		t.Fatalf("create snapshot must be one Existed=false cell, got %+v", before)
	}
	j.Push(id, UndoEntry{Label: "upsert", Before: before})

	created := roomAt(t, store, id, "Alpha", 5, 5, 0)
	if created == nil {
		t.Fatal("room was not created")
	}
	if created.Hint != "New Room" || !created.IsDT {
		t.Errorf("created fields wrong: %+v", created)
	}
	wantFP := mapimport.Fingerprint("New Room", "a fresh corridor", created.Exits)
	if created.Fingerprint != wantFP {
		t.Errorf("fingerprint not recomputed on create: got %q want %q", created.Fingerprint, wantFP)
	}
	gotExits := edirsOf(t, store, id, "Alpha", 5, 5, 0)
	if !contains(gotExits, "N") || !contains(gotExits, "S") {
		t.Errorf("created exits wrong: %v", gotExits)
	}

	// Partial UPDATE: change only the hint. desc/exits/is_dt must be preserved.
	newHint := "Renamed Room"
	before2, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Alpha", X: 5, Y: 5, L: 0, Fields: RoomFields{Hint: &newHint}})
	if err != nil || !found {
		t.Fatalf("update upsert: found=%v err=%v", found, err)
	}
	if len(before2) != 1 || !before2[0].Existed || before2[0].Room.Hint != "New Room" {
		t.Fatalf("update snapshot must capture prior room, got %+v", before2)
	}
	j.Push(id, UndoEntry{Label: "upsert", Before: before2})

	updated := roomAt(t, store, id, "Alpha", 5, 5, 0)
	if updated.Hint != "Renamed Room" {
		t.Errorf("hint not updated: %q", updated.Hint)
	}
	if updated.Desc != "a fresh corridor" || !updated.IsDT {
		t.Errorf("partial update clobbered preserved fields: %+v", updated)
	}
	if !contains(edirsOf(t, store, id, "Alpha", 5, 5, 0), "N") {
		t.Error("partial update clobbered exits")
	}
	// Fingerprint recomputed on hint change.
	if updated.Fingerprint != mapimport.Fingerprint("Renamed Room", "a fresh corridor", updated.Exits) {
		t.Error("fingerprint not recomputed on hint update")
	}

	// Undo the UPDATE — hint back to "New Room", everything else intact.
	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo update: applied=%v err=%v", applied, err)
	}
	afterUndo := roomAt(t, store, id, "Alpha", 5, 5, 0)
	if afterUndo == nil || afterUndo.Hint != "New Room" {
		t.Errorf("undo of update did not restore hint: %+v", afterUndo)
	}

	// Undo the CREATE — the room must be gone.
	entry, _ = j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo create: applied=%v err=%v", applied, err)
	}
	if roomAt(t, store, id, "Alpha", 5, 5, 0) != nil {
		t.Error("undo of create did not delete the room")
	}
}

// TestUpsertEdirsClearVsOmit: explicit empty edirs clears exits; omitted edirs
// preserves them.
func TestUpsertEdirsClearVsOmit(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	// (0,0,0) starts with [N,S].

	// Omit edirs/exits: exits preserved after a hint-only update.
	h := "x"
	store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Alpha", Fields: RoomFields{Hint: &h}})
	if got := edirsOf(t, store, id, "Alpha", 0, 0, 0); !contains(got, "N") || !contains(got, "S") {
		t.Errorf("omitted edirs should preserve exits, got %v", got)
	}

	// Explicit empty edirs clears them.
	var f RoomFields
	f.SetEDirs([]string{})
	store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Alpha", Fields: f})
	if got := edirsOf(t, store, id, "Alpha", 0, 0, 0); len(got) != 0 {
		t.Errorf("explicit empty edirs should clear exits, got %v", got)
	}
}

// TestLinkBidirectionalAndUndo: linking N from (1,0,0) adds N there AND S on the
// grid neighbor (0,0,0) (N delta = x-1). Undo restores BOTH cells exactly.
func TestLinkBidirectionalAndUndo(t *testing.T) {
	store := newMapperTestStore(t)
	// Two adjacent rooms on the N/S axis: (0,0,0) and (1,0,0). N from (1,0,0)
	// steps to x=0 (the neighbor). Neither advertises the linking edge yet.
	id, err := store.CreateMapSet(MapSetInput{
		Name: "L", Rooms: []Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "north cell", EDirs: `[]`, Doors: `[]`},
			{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "south cell", EDirs: `[]`, Doors: `[]`},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	j := store.WriteJournal()

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoLink, Zone: "Z", X: 1, Y: 0, L: 0, Dir: "N"})
	if err != nil || !found {
		t.Fatalf("link: found=%v err=%v", found, err)
	}
	if len(before) != 2 {
		t.Fatalf("link must snapshot BOTH cells, got %d", len(before))
	}
	j.Push(id, UndoEntry{Label: "link N", Before: before})

	if !contains(edirsOf(t, store, id, "Z", 1, 0, 0), "N") {
		t.Error("link did not add N on the source cell")
	}
	if !contains(edirsOf(t, store, id, "Z", 0, 0, 0), "S") {
		t.Error("link did not add reverse S on the neighbor cell")
	}

	// Undo restores BOTH cells to no-exit.
	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo link: applied=%v err=%v", applied, err)
	}
	if len(edirsOf(t, store, id, "Z", 1, 0, 0)) != 0 || len(edirsOf(t, store, id, "Z", 0, 0, 0)) != 0 {
		t.Error("undo of link did not restore both cells to no-exit")
	}
}

// TestLinkOneSidedNoNeighbor: linking toward an empty cell records a one-sided
// exit (still valid) and snapshots only the one cell.
func TestLinkOneSidedNoNeighbor(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	// (0,0,0) exists; its U-neighbor (0,0,1) does not.
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoLink, Zone: "Alpha", X: 0, Y: 0, L: 0, Dir: "U"})
	if err != nil || !found {
		t.Fatalf("link: found=%v err=%v", found, err)
	}
	if len(before) != 1 {
		t.Fatalf("one-sided link must snapshot only the one cell, got %d", len(before))
	}
	if !contains(edirsOf(t, store, id, "Alpha", 0, 0, 0), "U") {
		t.Error("one-sided link did not add U")
	}
}

// TestUnlinkRemovesBothAndUndo: unlink removes the edge on both cells; undo
// restores both.
func TestUnlinkRemovesBothAndUndo(t *testing.T) {
	store := newMapperTestStore(t)
	// (0,0,0) has S; (1,0,0) has N — a bidirectional S/N edge (S from (0,0,0)
	// steps to x=1).
	id, err := store.CreateMapSet(MapSetInput{
		Name: "U", Rooms: []Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "a", EDirs: `["S"]`, Doors: `[]`, Ch: 2, Exits: "S"},
			{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "b", EDirs: `["N"]`, Doors: `[]`, Ch: 1, Exits: "N"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	j := store.WriteJournal()

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUnlink, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "S"})
	if err != nil || !found {
		t.Fatalf("unlink: found=%v err=%v", found, err)
	}
	if len(before) != 2 {
		t.Fatalf("unlink must snapshot BOTH cells, got %d", len(before))
	}
	j.Push(id, UndoEntry{Label: "unlink S", Before: before})

	if contains(edirsOf(t, store, id, "Z", 0, 0, 0), "S") {
		t.Error("unlink did not remove S from source")
	}
	if contains(edirsOf(t, store, id, "Z", 1, 0, 0), "N") {
		t.Error("unlink did not remove reverse N from neighbor")
	}

	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo unlink: applied=%v err=%v", applied, err)
	}
	if !contains(edirsOf(t, store, id, "Z", 0, 0, 0), "S") || !contains(edirsOf(t, store, id, "Z", 1, 0, 0), "N") {
		t.Error("undo of unlink did not restore both edges")
	}
}

// TestDeleteThenUndoRecreates: delete a room, undo recreates it with all fields
// (exits/ch/fingerprint/flags) intact.
func TestDeleteThenUndoRecreates(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	j := store.WriteJournal()

	orig := roomAt(t, store, id, "Alpha", 0, 0, 0)
	if orig == nil {
		t.Fatal("seed room missing")
	}

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 0, L: 0})
	if err != nil || !found {
		t.Fatalf("delete: found=%v err=%v", found, err)
	}
	if len(before) != 1 || !before[0].Existed {
		t.Fatalf("delete snapshot must capture the whole prior room, got %+v", before)
	}
	j.Push(id, UndoEntry{Label: "delete", Before: before})

	if roomAt(t, store, id, "Alpha", 0, 0, 0) != nil {
		t.Fatal("room not deleted")
	}

	// Delete of a nonexistent room soft-fails (found=false).
	_, found2, _ := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom, Zone: "Alpha", X: 0, Y: 0, L: 0})
	if found2 {
		t.Error("delete of a nonexistent room should return found=false")
	}

	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo delete: applied=%v err=%v", applied, err)
	}
	recreated := roomAt(t, store, id, "Alpha", 0, 0, 0)
	if recreated == nil {
		t.Fatal("undo of delete did not recreate the room")
	}
	if recreated.Hint != orig.Hint || recreated.EDirs != orig.EDirs || recreated.Ch != orig.Ch ||
		recreated.Fingerprint != orig.Fingerprint || recreated.IsDT != orig.IsDT {
		t.Errorf("recreated room fields differ from original:\n got %+v\nwant %+v", recreated, orig)
	}
}

// TestUpsertUndoRestoresZeroValue (review case i): setting a field FROM its zero
// value and undoing must restore the zero value — the full-room snapshot captures
// the literal prior "" (not a "field was empty so skip" heuristic). Room (2,0,0)
// analog: use a fresh room with note "" then set note "X" and undo.
func TestUpsertUndoRestoresZeroValue(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	j := store.WriteJournal()

	// (0,0,0) starts with note "" (unset in sampleInput).
	if r := roomAt(t, store, id, "Alpha", 0, 0, 0); r.Note != "" {
		t.Fatalf("precondition: note should start empty, got %q", r.Note)
	}
	newNote := "X"
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Alpha", Fields: RoomFields{Note: &newNote}})
	if err != nil || !found {
		t.Fatalf("upsert: found=%v err=%v", found, err)
	}
	j.Push(id, UndoEntry{Label: "upsert", Before: before})
	if roomAt(t, store, id, "Alpha", 0, 0, 0).Note != "X" {
		t.Fatal("note not set to X")
	}

	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo: applied=%v err=%v", applied, err)
	}
	if got := roomAt(t, store, id, "Alpha", 0, 0, 0).Note; got != "" {
		t.Errorf("undo did not restore note to its zero value \"\": got %q", got)
	}
}

// TestLinkExistingEdgeUndoKeepsIt (review case ii, the slice-2 C1 class via link):
// linking a direction the cell ALREADY has is a no-op patch; undo must NOT delete
// the pre-existing edge (the snapshot captured the unchanged state).
func TestLinkExistingEdgeUndoKeepsIt(t *testing.T) {
	store := newMapperTestStore(t)
	// (0,0,0) already has S; (1,0,0) already has N — the S/N edge exists.
	id, err := store.CreateMapSet(MapSetInput{
		Name: "K", Rooms: []Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "a", EDirs: `["S"]`, Doors: `[]`, Ch: 2, Exits: "S"},
			{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "b", EDirs: `["N"]`, Doors: `[]`, Ch: 1, Exits: "N"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	j := store.WriteJournal()

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoLink, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "S"})
	if err != nil || !found {
		t.Fatalf("link: found=%v err=%v", found, err)
	}
	j.Push(id, UndoEntry{Label: "link S", Before: before})

	entry, _ := j.Pop(id)
	if applied, err := store.RestoreRoomSnapshots(id, entry.Before); err != nil || !applied {
		t.Fatalf("undo link: applied=%v err=%v", applied, err)
	}
	// The pre-existing edge must remain on BOTH cells.
	if !contains(edirsOf(t, store, id, "Z", 0, 0, 0), "S") {
		t.Error("undo of link-existing deleted the pre-existing S edge on the source")
	}
	if !contains(edirsOf(t, store, id, "Z", 1, 0, 0), "N") {
		t.Error("undo of link-existing deleted the pre-existing N edge on the neighbor")
	}
}

// TestUpsertChOnlyDerivesEdirs (review case iv): a lone `ch` (no exits/edirs) is
// now honored — edirs is derived from the bitmask.
func TestUpsertChOnlyDerivesEdirs(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())

	// ch=3 == N|S (bits 0,1). Apply to the Beta Hub (0,0,1) which starts with no exits.
	if got := edirsOf(t, store, id, "Beta", 0, 0, 1); len(got) != 0 {
		t.Fatalf("precondition: Beta Hub should have no exits, got %v", got)
	}
	ch := 3
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Beta", X: 0, Y: 0, L: 1, Fields: RoomFields{Ch: &ch}})
	if err != nil || !found {
		t.Fatalf("upsert: found=%v err=%v", found, err)
	}
	got := edirsOf(t, store, id, "Beta", 0, 0, 1)
	if !contains(got, "N") || !contains(got, "S") || len(got) != 2 {
		t.Errorf("ch=3 (N|S) should derive edirs [N,S], got %v", got)
	}
	if r := roomAt(t, store, id, "Beta", 0, 0, 1); r.Ch != 3 {
		t.Errorf("ch should be 3 after derive, got %d", r.Ch)
	}
}

// TestUpsertExitsRecordsDoor (review case v): a door marker in the upsert `exits`
// string ("N (S) U") records the S door in Doors (fingerprint stays
// door-insensitive).
func TestUpsertExitsRecordsDoor(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())

	exits := "N (S) U"
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom, Zone: "Beta", X: 0, Y: 0, L: 1, Fields: RoomFields{Exits: &exits}})
	if err != nil || !found {
		t.Fatalf("upsert: found=%v err=%v", found, err)
	}
	r := roomAt(t, store, id, "Beta", 0, 0, 1)
	doors := decodeStrArray(r.Doors)
	if !contains(doors, "S") {
		t.Errorf(`exits "N (S) U" should record the S door, got doors %v`, doors)
	}
	if len(doors) != 1 {
		t.Errorf("only S should be a door, got %v", doors)
	}
	// Exits/edirs still carry N,S,U.
	e := decodeStrArray(r.EDirs)
	if !contains(e, "N") || !contains(e, "S") || !contains(e, "U") {
		t.Errorf("edirs should be N,S,U, got %v", e)
	}
	// Display string carries the door marker.
	if !strings.Contains(r.Exits, "(S)") {
		t.Errorf("display exits should carry the (S) door marker, got %q", r.Exits)
	}
	// Fingerprint is door-insensitive: same as if S had no marker.
	if r.Fingerprint != mapimport.Fingerprint(r.Hint, r.Desc, "N S U") {
		t.Error("fingerprint must be door-insensitive (markers re-stripped)")
	}
}
