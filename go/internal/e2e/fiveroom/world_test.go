package fiveroom

import "testing"

func TestWorldWalksFiveRoomLoopAndRefusesInvalidMoves(t *testing.T) {
	w := NewWorld()
	if got := w.Current().ID; got != 1 {
		t.Fatalf("start room = %d, want 1", got)
	}

	wantRooms := []int{2, 3, 4, 5, 1}
	for i, command := range []string{"E", "E", "S", "W", "N"} {
		room, moved := w.Apply(command)
		if !moved || room.ID != wantRooms[i] {
			t.Fatalf("step %d %s = room %d moved=%v, want %d/true", i, command, room.ID, moved, wantRooms[i])
		}
	}

	room, moved := w.Apply("N")
	if moved || room.ID != 1 {
		t.Fatalf("invalid N = room %d moved=%v, want room 1/false", room.ID, moved)
	}
}

func TestRoomsExposeDistinctRMUDBlocks(t *testing.T) {
	seen := map[string]bool{}
	for id := 1; id <= 5; id++ {
		room, ok := RoomByID(id)
		if !ok {
			t.Fatalf("room %d missing", id)
		}
		if room.Hint == "" || room.Desc == "" || room.Exits == "" {
			t.Fatalf("room %d has incomplete block: %+v", id, room)
		}
		if seen[room.Hint+"\x00"+room.Desc+"\x00"+room.Exits] {
			t.Fatalf("room %d duplicates a fingerprint", id)
		}
		seen[room.Hint+"\x00"+room.Desc+"\x00"+room.Exits] = true
	}
}

func TestWorldCanReplayHilloOneWayPassage(t *testing.T) {
	w := NewWorldWithOptions(WorldOptions{OneWayHillo: true})

	if room := w.Current(); room.Hint != "Вдоль оврага" || room.Exits != "N W" {
		t.Fatalf("start room = %+v", room)
	}
	if got := w.ScanLines(); len(got) != 1 || got[0] != "На западе: Лесистое предгорье." {
		t.Fatalf("start scan = %#v", got)
	}

	room, moved := w.Apply("W")
	if !moved || room.ID != 2 {
		t.Fatalf("W = room %d moved=%v, want room 2/true", room.ID, moved)
	}
	if room.Hint != "Лесистое предгорье" || room.Exits != "N S W" {
		t.Fatalf("target room = %+v", room)
	}

	room, moved = w.Apply("E")
	if moved || room.ID != 2 {
		t.Fatalf("E = room %d moved=%v, want room 2/false", room.ID, moved)
	}
}

func TestWorldRevealsCrateDownExitOnlyAfterRussianAction(t *testing.T) {
	w := NewWorldWithOptions(WorldOptions{DynamicCrate: true})
	if got := w.Current().Exits; got != "N" {
		t.Fatalf("initial exits=%q", got)
	}
	if w.ApplyAction("двигать ящик") == false || w.Current().Exits != "N D" {
		t.Fatalf("crate action did not reveal D: %+v", w.Current())
	}
	if got := w.ScanLines(); len(got) != 2 || got[1] != "Внизу: Черная дыра в полу ведет в темноту." {
		t.Fatalf("revealed scan=%#v", got)
	}
	if room, moved := w.Apply("D"); !moved || room.ID != 3 {
		t.Fatalf("D room=%+v moved=%v", room, moved)
	}
}
