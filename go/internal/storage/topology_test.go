package storage

import "testing"

// TestForkMapSetCopiesChildren: forking a frozen set produces a new editable set
// with rooms + annotations (+ images) copied and the original left untouched.
func TestForkMapSetCopiesChildren(t *testing.T) {
	store := newMapperTestStore(t)
	srcID, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Imported sets are frozen.
	src, _ := store.GetMapSet(srcID)
	if src.Editable {
		t.Fatal("imported set must be frozen (editable=false)")
	}
	if src.ForkedFromID != nil {
		t.Fatal("imported set must have no forked_from_id")
	}

	// Attach an annotation and an image so the copy covers both child tables.
	if _, err := store.UpsertRoomAnnotation(srcID, "Alpha", 0, 0, 0, AnnotationFields{
		DT: boolPtr(true), Note: ptrStr("watch out"),
	}); err != nil {
		t.Fatalf("UpsertRoomAnnotation: %v", err)
	}
	var srcRoom Room
	store.DB().Where("map_set_id = ? AND zone = ?", srcID, "Alpha").First(&srcRoom)
	store.DB().Create(&RoomImage{RoomID: srcRoom.ID, FullPath: "vibe.png", Prompt: "moody"})

	forkID, err := store.ForkMapSet(srcID)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	if forkID == srcID {
		t.Fatal("fork id must differ from source")
	}

	fork, err := store.GetMapSet(forkID)
	if err != nil {
		t.Fatalf("GetMapSet(fork): %v", err)
	}
	if !fork.Editable {
		t.Error("fork must be editable")
	}
	if fork.ForkedFromID == nil || *fork.ForkedFromID != srcID {
		t.Errorf("fork forked_from_id = %v, want %d", fork.ForkedFromID, srcID)
	}
	if fork.Name != "Test World (editable)" {
		t.Errorf("fork name = %q, want \"Test World (editable)\"", fork.Name)
	}

	// Rooms copied: same count as the source.
	var srcCount, forkCount int64
	store.DB().Model(&Room{}).Where("map_set_id = ?", srcID).Count(&srcCount)
	store.DB().Model(&Room{}).Where("map_set_id = ?", forkID).Count(&forkCount)
	if srcCount == 0 || forkCount != srcCount {
		t.Errorf("room copy count = %d, want %d", forkCount, srcCount)
	}

	// Annotation copied and re-keyed to the fork.
	if _, ok, _ := store.GetRoomAnnotation(forkID, "Alpha", 0, 0, 0); !ok {
		t.Error("annotation not copied to the fork")
	}

	// Image copied and re-pointed to the FORK's room (not the source room id).
	var forkRoom Room
	store.DB().Where("map_set_id = ? AND zone = ?", forkID, "Alpha").First(&forkRoom)
	var imgCount int64
	store.DB().Model(&RoomImage{}).Where("room_id = ?", forkRoom.ID).Count(&imgCount)
	if imgCount != 1 {
		t.Errorf("image copy on fork room = %d, want 1", imgCount)
	}
	// Source image still points at the source room (untouched).
	var srcImgCount int64
	store.DB().Model(&RoomImage{}).Where("room_id = ?", srcRoom.ID).Count(&srcImgCount)
	if srcImgCount != 1 {
		t.Errorf("source image count = %d, want 1 (untouched)", srcImgCount)
	}

	// Mutating the fork must not touch the source.
	if _, err := store.PatchRoomExits(forkID, "Alpha", 0, 0, 0, []string{"U"}, nil); err != nil {
		t.Fatalf("PatchRoomExits(fork): %v", err)
	}
	forkRooms, _ := store.ListSlimRooms(forkID, "Alpha")
	srcRooms, _ := store.ListSlimRooms(srcID, "Alpha")
	if !slimHasU(forkRooms, 0, 0, 0) {
		t.Error("fork room should have U after patch")
	}
	if slimHasU(srcRooms, 0, 0, 0) {
		t.Error("source room must NOT have U (frozen, untouched)")
	}
}

// TestForkMapSetRollsBackOnError: a failure mid-copy must roll back the WHOLE
// fork transaction — no partial map_sets row, no orphan rooms for the aborted
// fork. We force the failure by dropping the room_annotations table so the
// annotation-copy step inside the transaction errors after the set row + rooms
// were tentatively created.
func TestForkMapSetRollsBackOnError(t *testing.T) {
	store := newMapperTestStore(t)
	srcID, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}

	var setsBefore, roomsBefore int64
	store.DB().Model(&MapSet{}).Count(&setsBefore)
	store.DB().Model(&Room{}).Count(&roomsBefore)

	// Break a child table the fork transaction touches AFTER creating the set +
	// rooms, so the copy fails mid-transaction.
	if err := store.DB().Exec("DROP TABLE room_annotations").Error; err != nil {
		t.Fatalf("drop room_annotations: %v", err)
	}

	if _, err := store.ForkMapSet(srcID); err == nil {
		t.Fatal("expected ForkMapSet to error when a child copy fails")
	}

	// The whole transaction rolled back: set + room counts unchanged (no partial
	// fork left behind).
	var setsAfter, roomsAfter int64
	store.DB().Model(&MapSet{}).Count(&setsAfter)
	store.DB().Model(&Room{}).Count(&roomsAfter)
	if setsAfter != setsBefore {
		t.Errorf("map_sets count = %d, want %d (fork must roll back)", setsAfter, setsBefore)
	}
	if roomsAfter != roomsBefore {
		t.Errorf("rooms count = %d, want %d (fork must roll back)", roomsAfter, roomsBefore)
	}
}

func slimHasU(rooms []SlimRoom, x, y, l int) bool {
	for _, r := range rooms {
		if r.X == x && r.Y == y && r.L == l {
			for _, d := range r.E {
				if d == "U" {
					return true
				}
			}
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }

// TestWriteJournalPerSetBoundedInverse exercises the journal directly: push/pop
// per-set keying and bounded cap over the before-state UndoEntry.
func TestWriteJournal(t *testing.T) {
	store := newMapperTestStore(t)
	j := store.WriteJournal()

	// Empty pop.
	if _, ok := j.Pop(1); ok {
		t.Fatal("empty journal Pop should return ok=false")
	}

	// Per-set keying: a push on set 1 is invisible to set 2.
	j.Push(1, UndoEntry{Label: "add U", Before: []RoomSnapshot{
		{Existed: true, Room: Room{Zone: "Z", EDirs: `["S"]`}},
	}})
	if j.Depth(2) != 0 {
		t.Error("set 2 journal must be empty")
	}
	if j.Depth(1) != 1 {
		t.Errorf("set 1 depth = %d, want 1", j.Depth(1))
	}
	if _, ok := j.Pop(2); ok {
		t.Error("Pop on the other set must be empty")
	}
	entry, ok := j.Pop(1)
	if !ok || entry.Label != "add U" || len(entry.Before) != 1 || entry.Before[0].Room.EDirs != `["S"]` {
		t.Errorf("Pop(1) = %+v, ok=%v", entry, ok)
	}

	// Bounded cap: oldest drops past undoJournalCap.
	for i := 0; i < undoJournalCap+50; i++ {
		j.Push(1, UndoEntry{})
	}
	if j.Depth(1) != undoJournalCap {
		t.Errorf("depth after overflow = %d, want %d", j.Depth(1), undoJournalCap)
	}
}

// TestApplyTopologyOpSnapshotAndRestore is the CRITICAL-fix core at the storage
// layer: the before-state snapshot restores the room EXACTLY regardless of whether
// the patch actually changed anything. Three cases:
//   - genuine add: undo removes it;
//   - add an exit the room ALREADY has: snapshot == unchanged, undo does NOT delete
//     the pre-existing exit (the old symmetric-inverse bug deleted it);
//   - remove an ABSENT exit: snapshot == unchanged, undo does NOT fabricate it.
func TestApplyTopologyOpSnapshotAndRestore(t *testing.T) {
	// Room (0,0,0) starts with edirs [N,S], ch=3.
	newStore := func(t *testing.T) (*Store, int64) {
		store := newMapperTestStore(t)
		id, err := store.CreateMapSet(sampleInput())
		if err != nil {
			t.Fatalf("CreateMapSet: %v", err)
		}
		return store, id
	}

	exitsAt := func(t *testing.T, store *Store, id int64) []string {
		t.Helper()
		rooms, _ := store.ListSlimRooms(id, "Alpha")
		for _, r := range rooms {
			if r.X == 0 && r.Y == 0 && r.L == 0 {
				return r.E
			}
		}
		t.Fatal("start room missing")
		return nil
	}

	t.Run("genuine add then undo removes", func(t *testing.T) {
		store, id := newStore(t)
		before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "Alpha", AddExits: []string{"U"}})
		if err != nil || !found {
			t.Fatalf("apply: found=%v err=%v", found, err)
		}
		if got := exitsAt(t, store, id); !contains(got, "U") {
			t.Fatalf("U not added: %v", got)
		}
		if _, err := store.RestoreRoomSnapshots(id, before); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got := exitsAt(t, store, id); contains(got, "U") {
			t.Errorf("undo did not remove U: %v", got)
		}
	})

	t.Run("add existing then undo keeps it (no delete)", func(t *testing.T) {
		store, id := newStore(t)
		// N is already present. Adding N is a no-op patch.
		before, found, _ := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "Alpha", AddExits: []string{"N"}})
		if !found {
			t.Fatal("room should exist")
		}
		if got := exitsAt(t, store, id); !contains(got, "N") {
			t.Fatalf("precondition: N should be present: %v", got)
		}
		if _, err := store.RestoreRoomSnapshots(id, before); err != nil {
			t.Fatalf("restore: %v", err)
		}
		// The bug: undo would DELETE the pre-existing N. Must stay.
		if got := exitsAt(t, store, id); !contains(got, "N") {
			t.Errorf("undo of add-existing deleted the pre-existing N exit: %v", got)
		}
	})

	t.Run("remove absent then undo does not fabricate", func(t *testing.T) {
		store, id := newStore(t)
		// U is absent. Removing U is a no-op patch.
		before, found, _ := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoPatchExits, Zone: "Alpha", RemoveExits: []string{"U"}})
		if !found {
			t.Fatal("room should exist")
		}
		if got := exitsAt(t, store, id); contains(got, "U") {
			t.Fatalf("precondition: U should be absent: %v", got)
		}
		if _, err := store.RestoreRoomSnapshots(id, before); err != nil {
			t.Fatalf("restore: %v", err)
		}
		// The bug: undo would FABRICATE a phantom U. Must stay absent.
		if got := exitsAt(t, store, id); contains(got, "U") {
			t.Errorf("undo of remove-absent fabricated a phantom U exit: %v", got)
		}
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
