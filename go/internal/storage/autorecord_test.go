package storage

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func autorecordInputForTest() AutorecordInput {
	// The reported exits intentionally omit the way back. Recording still creates
	// a two-way passage by default; a rare one-way passage is an explicit edge edit.
	hint, desc, exits := "Unseeded room", "freshly observed", "E"
	return AutorecordInput{
		Source:        EdgeCell{Zone: "Z", X: 0, Y: 0, L: 0},
		Target:        EdgeCell{Zone: "Z", X: 1, Y: 0, L: 0},
		Dir:           "S",
		Command:       "s",
		Fields:        RoomFields{Hint: &hint, Desc: &desc, Exits: &exits},
		RecordBatchID: "test-batch-1",
		RecordOrigin:  "test",
	}
}

func TestAutorecordRoomIsAtomicBidirectionalIdempotentAndUndoable(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `["S"]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	op := TopologyOp{Kind: TopoAutorecordRoom, Autorecord: func() *AutorecordInput { in := autorecordInputForTest(); return &in }()}
	setBefore, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ApplyTopologyOp(id, op)
	if err != nil || !found || before.NoOp {
		t.Fatalf("first autorecord found=%v no_op=%v err=%v", found, before.NoOp, err)
	}
	setAfter, _ := store.GetMapSet(id)
	if setAfter.TopologyRevision != setBefore.TopologyRevision+1 {
		t.Fatalf("topology revision=%d, want %d", setAfter.TopologyRevision, setBefore.TopologyRevision+1)
	}
	room := roomAt(t, store, id, "Z", 1, 0, 0)
	if room == nil || room.Hint != "Unseeded room" || room.VerificationState != "provisional" || room.RecordSource != EdgeSourceAutorecord || room.RecordBatchID != "test-batch-1" {
		t.Fatalf("provisional room=%+v", room)
	}
	var forward MapEdge
	if err := store.DB().Where("map_set_id=? AND kind=? AND source=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", id, EdgeKindCorridor, EdgeSourceAutorecord, "Z", 0, 0, 0, "S").First(&forward).Error; err != nil {
		t.Fatal(err)
	}
	if !forward.Passable || forward.Command != "s" {
		t.Fatalf("forward=%+v", forward)
	}
	var reverse MapEdge
	if err := store.DB().Where("map_set_id=? AND kind=? AND source=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=? AND passable=1", id, EdgeKindCorridor, EdgeSourceAutorecord, "Z", 1, 0, 0, "N").First(&reverse).Error; err != nil {
		t.Fatal(err)
	}
	if reverse.Command != "n" || reverse.ToZone == nil || *reverse.ToZone != "Z" || reverse.ToX == nil || *reverse.ToX != 0 || reverse.ToY == nil || *reverse.ToY != 0 || reverse.ToL == nil || *reverse.ToL != 0 {
		t.Fatalf("reverse=%+v, want canonical two-way passage back to source", reverse)
	}

	second, found, err := store.ApplyTopologyOp(id, op)
	if err != nil || !found || !second.NoOp {
		t.Fatalf("replay found=%v no_op=%v err=%v", found, second.NoOp, err)
	}
	setReplay, _ := store.GetMapSet(id)
	if setReplay.TopologyRevision != setAfter.TopologyRevision {
		t.Fatalf("replay advanced revision=%d from %d", setReplay.TopologyRevision, setAfter.TopologyRevision)
	}

	if applied, err := store.RestoreTopologyBeforeAtRevision(id, before, setAfter.TopologyRevision); err != nil || !applied {
		t.Fatalf("undo applied=%v err=%v", applied, err)
	}
	if roomAt(t, store, id, "Z", 1, 0, 0) != nil {
		t.Fatal("undo left provisional room")
	}
	var edgeCount int64
	if err := store.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND source=?", id, EdgeKindCorridor, EdgeSourceAutorecord).Count(&edgeCount).Error; err != nil {
		t.Fatal(err)
	}
	if edgeCount != 0 {
		t.Fatalf("undo left autorecord edges=%d", edgeCount)
	}
}

func TestApplyTopologyOpBusyRetryRepeatsRevisionFenceAndCommitsOnce(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	store.topologyApplyAttemptHook = func() error {
		if attempts.Add(1) == 1 {
			return fmt.Errorf("database is locked (deterministic busy test)")
		}
		return nil
	}
	in := autorecordInputForTest()
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoAutorecordRoom, Autorecord: &in})
	if err != nil || !found || before.NoOp {
		t.Fatalf("busy retry before=%+v found=%v err=%v", before, found, err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("outer transaction attempts=%d, want 2", got)
	}
	set, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	if set.TopologyRevision != before.Revision+1 {
		t.Fatalf("revision=%d, before=%d; retry did not fence the whole write", set.TopologyRevision, before.Revision)
	}
	if roomAt(t, store, id, "Z", 1, 0, 0) == nil {
		t.Fatal("busy retry did not commit the room")
	}
}

func TestAutorecordOccupiedMismatchRefusesWithoutRevisionOrWrite(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `["S"]`, Doors: `[]`}, {Zone: "Z", X: 1, Y: 0, L: 0, Hint: "Other", Desc: "other", EDirs: `["N"]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	setBefore, _ := store.GetMapSet(id)
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoAutorecordRoom, Autorecord: func() *AutorecordInput { in := autorecordInputForTest(); return &in }()})
	if err == nil || found {
		t.Fatalf("occupied autorecord found=%v err=%v", found, err)
	}
	setAfter, _ := store.GetMapSet(id)
	if setAfter.TopologyRevision != setBefore.TopologyRevision {
		t.Fatalf("refusal advanced revision=%d from %d", setAfter.TopologyRevision, setBefore.TopologyRevision)
	}
}

func TestAutorecordRefusesAuthoritativeFaceBeforeCreatingRoom(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	to := EdgeCell{Zone: "Z", X: 1, Y: 0, L: 0}
	if err := store.DB().Create(&MapEdge{
		MapSetID: id, Kind: EdgeKindGrid, Source: EdgeSourceManual, Passable: true,
		FromZone: "Z", FromX: 0, FromY: 0, FromL: 0, Dir: "S",
		ToZone: &to.Zone, ToX: &to.X, ToY: &to.Y, ToL: &to.L,
		EdgeAttrs: EdgeAttrs{Weight: 1},
	}).Error; err != nil {
		t.Fatal(err)
	}
	setBefore, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoAutorecordRoom, Autorecord: func() *AutorecordInput { in := autorecordInputForTest(); return &in }()})
	if err == nil || found {
		t.Fatalf("authoritative face overwrite found=%v err=%v", found, err)
	}
	setAfter, err := store.GetMapSet(id)
	if err != nil {
		t.Fatal(err)
	}
	if setAfter.TopologyRevision != setBefore.TopologyRevision {
		t.Fatalf("refusal advanced revision=%d from %d", setAfter.TopologyRevision, setBefore.TopologyRevision)
	}
	if roomAt(t, store, id, "Z", 1, 0, 0) != nil {
		t.Fatal("authoritative-face refusal created target room")
	}
	var face MapEdge
	if err := store.DB().Where("map_set_id=? AND from_zone=? AND from_x=? AND from_y=? AND from_l=? AND dir=?", id, "Z", 0, 0, 0, "S").First(&face).Error; err != nil {
		t.Fatal(err)
	}
	if face.Source != EdgeSourceManual || !face.Passable {
		t.Fatalf("authoritative face changed: %+v", face)
	}
}

func TestAutorecordRejectsOversizedRoomBlock(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", EDirs: `[]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	in := autorecordInputForTest()
	tooLarge := strings.Repeat("x", autorecordMaxDesc+1)
	in.Fields.Desc = &tooLarge
	setBefore, _ := store.GetMapSet(id)
	_, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoAutorecordRoom, Autorecord: &in})
	if err == nil || found {
		t.Fatalf("oversized autorecord found=%v err=%v", found, err)
	}
	setAfter, _ := store.GetMapSet(id)
	if setAfter.TopologyRevision != setBefore.TopologyRevision || roomAt(t, store, id, "Z", 1, 0, 0) != nil {
		t.Fatalf("oversized autorecord mutated topology: before=%d after=%d", setBefore.TopologyRevision, setAfter.TopologyRevision)
	}
}

func TestAutorecordStorageConcurrentReplayHasOneRoomAndEdgePair(t *testing.T) {
	store := newMapperTestStore(t)
	frozen, err := store.CreateMapSet(MapSetInput{Rooms: []Room{{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "Start", Desc: "start", EDirs: `["S"]`, Doors: `[]`}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ForkMapSet(frozen)
	if err != nil {
		t.Fatal(err)
	}
	op := TopologyOp{Kind: TopoAutorecordRoom, Autorecord: func() *AutorecordInput { in := autorecordInputForTest(); return &in }()}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock := store.TopologyLock(id)
			lock.Lock()
			defer lock.Unlock()
			_, _, err := store.ApplyTopologyOp(id, op)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent replay error: %v", err)
		}
	}
	var rooms, edges int64
	if err := store.DB().Model(&Room{}).Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=?", id, "Z", 1, 0, 0).Count(&rooms).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&MapEdge{}).Where("map_set_id=? AND kind=? AND source=?", id, EdgeKindCorridor, EdgeSourceAutorecord).Count(&edges).Error; err != nil {
		t.Fatal(err)
	}
	if rooms != 1 || edges != 2 {
		t.Fatalf("concurrent result rooms=%d edges=%d", rooms, edges)
	}
}
