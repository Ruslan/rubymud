package mapperfixture

import (
	"path/filepath"
	"strings"
	"testing"

	"rubymud/go/internal/e2e/fiveroom"
	"rubymud/go/internal/storage"
)

func TestSeedCreatesEditableFiveRoomFalseExpectation(t *testing.T) {
	runDir := t.TempDir()
	dbPath := filepath.Join(runDir, "mudhost.db")
	result, err := Seed(Options{RunDir: runDir, DBPath: dbPath, MUDHost: "127.0.0.1", MUDPort: 40123})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID <= 0 || result.MapSetID <= 0 {
		t.Fatalf("invalid result: %+v", result)
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	active, ok, err := store.GetActiveMapSetID(result.SessionID)
	if err != nil || !ok || active != result.MapSetID {
		t.Fatalf("active=%d ok=%v err=%v, want %d", active, ok, err, result.MapSetID)
	}
	set, err := store.GetMapSet(result.MapSetID)
	if err != nil || !set.Editable {
		t.Fatalf("set=%+v err=%v, want editable", set, err)
	}
	rooms, err := store.ListRooms(result.MapSetID)
	if err != nil || len(rooms) != 5 {
		t.Fatalf("rooms=%d err=%v, want 5", len(rooms), err)
	}

	edges, err := store.ListMapEdges(result.MapSetID)
	if err != nil {
		t.Fatal(err)
	}
	var falseFace bool
	for _, edge := range edges {
		if edge.Kind == storage.EdgeKindCorridor {
			t.Fatalf("seed unexpectedly contains corridor: %+v", edge)
		}
		to, hasTo := edge.To()
		if edge.From() == result.Rooms[5] && edge.Dir == "N" && hasTo && to == result.Rooms[2] {
			falseFace = true
		}
	}
	if !falseFace {
		t.Fatal("seed lacks false derived face 5 --N--> 2")
	}
}

func TestSeedRejectsDatabaseOutsideRunDirectory(t *testing.T) {
	runDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "mudhost.db")
	if _, err := Seed(Options{RunDir: runDir, DBPath: outside, MUDHost: "127.0.0.1", MUDPort: 1}); err == nil {
		t.Fatal("Seed accepted a database outside run directory")
	}
}

func TestSeedReplaysHilloOneWayRoomTextButKeepsDerivedReverseUntilMarked(t *testing.T) {
	runDir := t.TempDir()
	result, err := Seed(Options{
		RunDir: runDir, DBPath: filepath.Join(runDir, "mudhost.db"),
		MUDHost: "127.0.0.1", MUDPort: 40123, OneWayHillo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(runDir, "mudhost.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rooms, err := store.ListRooms(result.MapSetID)
	if err != nil || len(rooms) != 2 {
		t.Fatalf("rooms=%d err=%v, want 2", len(rooms), err)
	}
	if rooms[0].Hint != "Лесистое предгорье" && rooms[1].Hint != "Лесистое предгорье" {
		t.Fatalf("Hillo target missing: %+v", rooms)
	}
	if target := roomByHint(t, rooms, "Лесистое предгорье"); target.Exits != "N S W" || strings.Contains(target.EDirs, `"E"`) {
		t.Fatalf("target exits = %q/%s, want N S W without E", target.Exits, target.EDirs)
	}

	edges, err := store.ListMapEdges(result.MapSetID)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := result.Rooms[2]
	wantTo := result.Rooms[1]
	for _, edge := range edges {
		to, hasTo := edge.To()
		if edge.From() == wantFrom && edge.Dir == "E" && hasTo && to == wantTo && edge.Passable {
			return
		}
	}
	t.Fatal("derived reverse E face missing before the player marks the rare one-way passage")
}

func TestSeedHiddenHatchCarriesConcealedFacesAndDirectedDelays(t *testing.T) {
	runDir := t.TempDir()
	result, err := Seed(Options{RunDir: runDir, DBPath: filepath.Join(runDir, "mudhost.db"), MUDHost: "127.0.0.1", MUDPort: 40123, HiddenHatch: true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(runDir, "mudhost.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	edges, err := store.ListMapEdges(result.MapSetID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"D": "повернуть рычаг", "U": "нажать кнопку"}
	seen := map[string]bool{}
	for _, edge := range edges {
		if command, ok := want[edge.Dir]; ok && edge.Door && edge.OpenCmd == command && edge.OpenDelayMS == 2000 {
			seen[edge.Dir] = true
		}
	}
	if !seen["D"] || !seen["U"] {
		t.Fatalf("delayed directed hatch attrs missing: seen=%v edges=%+v", seen, edges)
	}
	rooms, err := store.ListRooms(result.MapSetID)
	if err != nil {
		t.Fatal(err)
	}
	turn := roomByHint(t, rooms, fiveroom.HatchTurnHint)
	if strings.Contains(turn.EDirs, `"W"`) || turn.Ch&(1<<3) == 0 {
		t.Fatalf("turn must omit printed W but retain physical W connectivity: %+v", turn)
	}
	pipe := roomByHint(t, rooms, "Узкий тоннель")
	if !pipe.Pipe || pipe.EDirs != "[]" || pipe.Ch != (1<<0)|(1<<1) {
		t.Fatalf("legacy pipe shape must carry N/S in ch with empty edirs: %+v", pipe)
	}
}

func roomByHint(t *testing.T, rooms []storage.Room, hint string) storage.Room {
	t.Helper()
	for _, room := range rooms {
		if room.Hint == hint {
			return room
		}
	}
	t.Fatalf("room %q missing", hint)
	return storage.Room{}
}
