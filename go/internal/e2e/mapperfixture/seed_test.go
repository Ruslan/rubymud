package mapperfixture

import (
	"path/filepath"
	"testing"

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
