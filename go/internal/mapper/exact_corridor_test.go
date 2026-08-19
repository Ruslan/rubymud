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

func TestReconciledReverseSuppressionPreventsDiagonalRoute(t *testing.T) {
	z, l := "Z", 0
	cell := func(x, y int) (*string, *int, *int, *int) { xx, yy := x, y; return &z, &xx, &yy, &l }
	edge := func(fx, fy int, dir string, tx, ty int) storage.MapEdge {
		tz, x, y, ll := cell(tx, ty)
		return storage.MapEdge{Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceParsed, Passable: true, FromZone: z, FromX: fx, FromY: fy, Dir: dir, ToZone: tz, ToX: x, ToY: y, ToL: ll, EdgeAttrs: storage.EdgeAttrs{Weight: 1}}
	}
	rooms := []storage.Room{{Zone: z, X: 0, Y: 0}, {Zone: z, X: 0, Y: 1}, {Zone: z, X: 0, Y: 2}, {Zone: z, X: 1, Y: 2}, {Zone: z, X: 1, Y: 1}}
	edges := []storage.MapEdge{edge(0, 0, "E", 0, 1), edge(0, 1, "E", 0, 2), edge(0, 2, "S", 1, 2), edge(1, 2, "W", 1, 1)}
	tz, tx, ty, tl := cell(0, 0)
	edges = append(edges,
		storage.MapEdge{Kind: storage.EdgeKindCorridor, Source: storage.EdgeSourceReconciled, Passable: true, FromZone: z, FromX: 1, FromY: 1, Dir: "N", ToZone: tz, ToX: tx, ToY: ty, ToL: tl, Command: "climb exact", EdgeAttrs: storage.EdgeAttrs{Weight: 1}},
		storage.MapEdge{Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceReconciled, Passable: false, FromZone: z, FromX: 0, FromY: 1, Dir: "S", ToZone: func() *string { v := z; return &v }(), ToX: func() *int { v := 1; return &v }(), ToY: func() *int { v := 1; return &v }(), ToL: func() *int { v := 0; return &v }(), EdgeAttrs: storage.EdgeAttrs{Weight: 1}},
	)
	idx := BuildIndexWithEdges(1, rooms, edges)
	if got := idx.Room(Coord{z, 0, 1, 0}).GridEdge("S"); got == nil || got.Passable {
		t.Fatalf("reverse suppression missing: %#v", got)
	}
	result := idx.FindPath(Coord{z, 0, 1, 0}, func(r *IndexRoom) bool { return r.Coord == (Coord{z, 1, 1, 0}) })
	if !result.Reachable || len(result.Steps) != 3 {
		t.Fatalf("routing used suppressed diagonal: %#v", result)
	}
}
