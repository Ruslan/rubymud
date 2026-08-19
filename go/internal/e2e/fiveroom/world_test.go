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
