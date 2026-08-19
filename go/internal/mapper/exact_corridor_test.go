package mapper

import (
	"rubymud/go/internal/storage"
	"testing"
)

func TestExactCorridorRoutesAndTracksOneLiteralCommand(t *testing.T) {
	z, x, y, l := "Z", 0, 0, 0
	rooms := []storage.Room{{Zone: "Z", X: 1, Y: 1, Hint: "5"}, {Zone: "Z", X: 0, Y: 0, Hint: "1"}}
	edge := storage.MapEdge{Kind: storage.EdgeKindCorridor, Source: storage.EdgeSourceReconciled, Passable: true, FromZone: "Z", FromX: 1, FromY: 1, Dir: "N", ToZone: &z, ToX: &x, ToY: &y, ToL: &l, Command: "climb rope", EdgeAttrs: storage.EdgeAttrs{Weight: 1, Phrase: "northward"}}
	idx := BuildIndexWithEdges(1, rooms, []storage.MapEdge{edge})
	result := idx.FindPath(Coord{"Z", 1, 1, 0}, func(r *IndexRoom) bool { return r.Coord == Coord{"Z", 0, 0, 0} })
	steps := result.Steps
	if !result.Reachable {
		t.Fatalf("route unreachable: %+v", result)
	}
	if len(steps) != 1 || steps[0].Command != "climb rope" || steps[0].Phrase != "northward" || steps[0].Cells != 1 {
		t.Fatalf("route=%#v", steps)
	}
	tr := NewTracker(idx)
	tr.Anchor(Coord{"Z", 1, 1, 0})
	got, ok := tr.PredictReportedLanding(Coord{"Z", 1, 1, 0}, "N")
	if !ok || got != (Coord{"Z", 0, 0, 0}) {
		t.Fatalf("landing=%+v ok=%v", got, ok)
	}
}

func TestExactCorridorLandingOnPipeDoesNotAbsorbFollowingStep(t *testing.T) {
	z, x1, x0, y, l := "Z", 1, 0, 0, 0
	rooms := []storage.Room{{Zone: "Z", X: 2, Y: 0, Hint: "start"}, {Zone: "Z", X: 1, Y: 0, Hint: "pipe", Pipe: true}, {Zone: "Z", X: 0, Y: 0, Hint: "end"}}
	edges := []storage.MapEdge{
		{Kind: storage.EdgeKindCorridor, Source: storage.EdgeSourceReconciled, Passable: true, FromZone: "Z", FromX: 2, Dir: "N", ToZone: &z, ToX: &x1, ToY: &y, ToL: &l, Command: "crawl exact", EdgeAttrs: storage.EdgeAttrs{Weight: 1}},
		{Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceParsed, Passable: true, FromZone: "Z", FromX: 1, Dir: "N", ToZone: &z, ToX: &x0, ToY: &y, ToL: &l, EdgeAttrs: storage.EdgeAttrs{Weight: 1}},
	}
	result := BuildIndexWithEdges(1, rooms, edges).FindPath(Coord{"Z", 2, 0, 0}, func(r *IndexRoom) bool { return r.X == 0 })
	if !result.Reachable || len(result.Steps) != 2 || result.Steps[0].Command != "crawl exact" || result.Steps[1].Command != "n" {
		t.Fatalf("exact corridor swallowed following pipe step: %#v", result)
	}
}
