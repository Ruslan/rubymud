package web

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// These are READ-surface regressions: the map pane already receives this data in
// SlimRoom / materialized edges, so MCP must not silently present an older graph.
func TestMCPMapZoneRendersMaterializedTopology(t *testing.T) {
	tag := 7
	weight := 2.5
	text := formatMcpMapZone(12, "Зона", []storage.SlimRoom{{
		Z: "Зона", X: 1, Y: 2, L: 0, T: &tag, H: "Комната \"A\"",
		E: []string{"N"}, M: []string{"E"}, MB: []string{"N"},
		G: []string{"S"}, GF: []string{"W"},
		EA: []storage.SlimEdgeAttr{{Dir: "E", Weight: &weight, Phrase: "толкнуть ящик", Door: true, Locked: true, OpenCmd: "открыть люк", OpenDelayMS: 2000, NoRoute: true}},
		SE: []storage.SlimSeam{{Order: 0, Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceParsed, Passable: true, TargetZone: "Море", Command: ` войти "сюда" `}, {Order: 1, Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceManual, Passable: true, TargetZone: "Море|2", TargetTag: &tag, Command: ` войти "сюда" `, Weight: &weight, NoRoute: true}},
	}}, 300)

	for _, want := range []string{
		"manual_open=[E]", "manual_blocked=[N]", "grid_parsed=[S]", "grid_fallback=[W]",
		`attrs={dir=E weight=2.5 phrase="толкнуть ящик" door=true locked=true open_cmd="открыть люк" open_delay_ms=2000 no_route=true}`,
		`seam={order=0 kind=seam source=parsed passable=true target_zone="Море" target_tag=null command=" войти \"сюда\" "}`,
		`seam={order=1 kind=seam source=manual passable=true target_zone="Море|2" target_tag=7 command=" войти \"сюда\" " weight=2.5 no_route=true}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("mud_map_zone omits %q:\n%s", want, text)
		}
	}
}

func TestMCPMapZoneBoundsAndEscapesPathologicalSeams(t *testing.T) {
	seams := make([]storage.SlimSeam, 0, 200)
	for i := 0; i < 200; i++ {
		command := fmt.Sprintf("go-%03d", i)
		if i == 0 {
			command = "go\ninjected"
		}
		tag := i
		seams = append(seams, storage.SlimSeam{Order: i, Kind: storage.EdgeKindSeam, Source: storage.EdgeSourceParsed, Passable: true, TargetZone: "B", TargetTag: &tag, Command: command, NoRoute: true})
	}
	nan := math.NaN()
	text := formatMcpMapZone(1, "A", []storage.SlimRoom{{
		Z: "A", H: "room", SE: seams,
		EA: []storage.SlimEdgeAttr{{Dir: "N", Weight: &nan}},
	}}, 1)

	if strings.Contains(text, "\ninjected") {
		t.Fatalf("raw seam command injected an output line:\n%s", text)
	}
	if strings.Contains(text, "NaN") {
		t.Fatalf("invalid defensive weight escaped normalization:\n%s", text)
	}
	if strings.Contains(text, "attrs={dir=N}") {
		t.Fatalf("normalized default-only attrs added output bloat:\n%s", text)
	}
	if got := strings.Count(text, "target_zone="); got > 2*mcpTopologyListLimit {
		t.Fatalf("pathological room emitted %d seam records, cap=%d", got, 2*mcpTopologyListLimit)
	}
	if !strings.Contains(text, "topology_truncated=") {
		t.Fatalf("bounded topology omitted truncation note:\n%s", text)
	}
}

func TestMCPLookMapUsesChForManualEdgeVisibility(t *testing.T) {
	targetZone, targetX, targetY, targetL := "A", 0, 1, 0
	idx := mapper.BuildIndexWithEdges(1, []storage.Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Ch: 4, EDirs: `[]`},
		{Zone: "A", X: 0, Y: 1, L: 0},
	}, []storage.MapEdge{{
		Kind: storage.EdgeKindGrid, Source: storage.EdgeSourceManual, Passable: true,
		FromZone: "A", Dir: "E", ToZone: &targetZone, ToX: &targetX, ToY: &targetY, ToL: &targetL,
	}})
	tracker := mapper.NewTracker(idx)
	tracker.Anchor(mapper.Coord{Zone: "A"})
	var sb strings.Builder
	writeExitLine(&sb, tracker, tracker.CurrentRoom())
	if strings.Contains(sb.String(), "visibility=hidden") {
		t.Fatalf("ch-authoritative exit was mislabeled hidden from empty edirs:\n%s", sb.String())
	}
}

func TestTopologyResolutionDistinguishesDeadAndDanglingSeams(t *testing.T) {
	idx := buildTestMapIndex(1, []storage.Room{{Zone: "A"}})
	dead := mapper.TopologyEdge{Kind: storage.EdgeKindSeam, Passable: true}
	if got := topologyResolution(idx, dead); got != "dead_target" {
		t.Fatalf("unresolved symbolic seam resolution=%q, want dead_target", got)
	}
	dangling := mapper.TopologyEdge{Kind: storage.EdgeKindSeam, Passable: true, HasTarget: true,
		Target: mapper.Coord{Zone: "missing", X: 1}}
	if got := topologyResolution(idx, dangling); got != "dangling" {
		t.Fatalf("resolved stale seam resolution=%q, want dangling", got)
	}
}

func TestMCPLookMapBoundsPathologicalOutgoingSeams(t *testing.T) {
	raw := make([]string, 200)
	for i := range raw {
		raw[i] = fmt.Sprintf("B|go-%03d|7", i)
	}
	idx := mapper.BuildIndexWithEdges(1, []storage.Room{
		{Zone: "A"},
		{Zone: "B", Tag: intPtr(7)},
	}, storage.DeriveImportedEdges(1, []storage.Room{{Zone: "A"}, {Zone: "B", Tag: intPtr(7)}}, []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, raw...)}))
	tracker := mapper.NewTracker(idx)
	tracker.Anchor(mapper.Coord{Zone: "A"})
	var sb strings.Builder
	writeExitLine(&sb, tracker, tracker.CurrentRoom())
	if got := strings.Count(sb.String(), "  seam order="); got != mcpLookMapEdgeLimit {
		t.Fatalf("look map emitted %d seam lines, want cap %d", got, mcpLookMapEdgeLimit)
	}
	if !strings.Contains(sb.String(), "topology truncated: 72 more outgoing edge(s)") {
		t.Fatalf("look map omitted truncation note:\n%s", sb.String())
	}
}

func TestMCPLookMapReadsMaterializedEdgesAndKeepsDuplicateSeams(t *testing.T) {
	s, sess := setupMcpTestServer(t)
	sid := sess.SessionID()
	id, err := s.store.CreateMapSet(storage.MapSetInput{
		Name: "read topology", ZoneCount: 2, RoomCount: 7,
		Rooms: []storage.Room{
			{Zone: "A", X: 0, Y: 0, L: 0, Tag: intPtr(1), Hint: "start", EDirs: `[]`, Doors: `[]`},
			{Zone: "A", X: 0, Y: 1, L: 0, Tag: intPtr(2), Hint: "fallback", EDirs: `[]`, Doors: `[]`},
			{Zone: "A", X: -1, Y: 0, L: 0, Tag: intPtr(3), Hint: "north", EDirs: `[]`, Doors: `[]`},
			{Zone: "A", X: 1, Y: 0, L: 0, Tag: intPtr(4), Hint: "south", EDirs: `["N"]`, Doors: `[]`, Ch: 1},
			{Zone: "A", X: 0, Y: -1, L: 0, Tag: intPtr(5), Hint: "west", EDirs: `[]`, Doors: `[]`},
			{Zone: "B", X: 4, Y: 5, L: 0, Tag: intPtr(7), Hint: "first", EDirs: `[]`, Doors: `[]`},
			{Zone: "B", X: 4, Y: 6, L: 0, Tag: intPtr(8), Hint: "second", EDirs: `[]`, Doors: `[]`},
		},
		LegacySeams: []storage.LegacySeamDraft{seamDraft("A", 0, 0, 0, "B|войти|7", "B|войти|8", "B|???|9")},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := s.store.SetActiveMapSetID(sid, id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	sess.LoadActiveMapSet()

	// The fallback E face gets explicit routing attrs. It must remain visible as a
	// grid-fallback edge rather than being inferred from rooms.edirs.
	weight := 2.5
	phrase := "толкнуть ящик"
	noRoute := true
	if _, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoSetEdgeAttrs, Zone: "A", X: 0, Y: 0, L: 0, Dir: "E",
		Attrs: storage.EdgeAttrPatch{Weight: &weight, Phrase: &phrase, NoRoute: &noRoute}}); err != nil {
		t.Fatalf("set grid attrs: %v", err)
	}
	// A blocked N face is a persisted edge despite not being in edirs.
	if res, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoBlockManualEdge, Zone: "A", X: 0, Y: 0, L: 0, Dir: "N", OneWay: true}); err != nil || !res.Applied {
		t.Fatalf("block edge: result=%+v err=%v", res, err)
	}
	seamTag := 7
	if _, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoSetEdgeAttrs, Zone: "A", X: 0, Y: 0, L: 0,
		Seam: &storage.SeamSelector{TargetZone: "B", TargetTag: &seamTag, Command: "войти"}, Attrs: storage.EdgeAttrPatch{Weight: &weight, Phrase: &phrase, NoRoute: &noRoute}}); err != nil {
		t.Fatalf("set seam attrs: %v", err)
	}

	if got := callTool(t, s, "mud_anchor_here", fmt.Sprintf(`{"session_id":%d,"zone":"A","x":0,"y":0,"l":0}`, sid)); got["isError"] == true {
		t.Fatalf("anchor: %q", toolText(t, got))
	}
	text := toolText(t, callTool(t, s, "mud_look_map", fmt.Sprintf(`{"session_id":%d}`, sid)))
	for _, want := range []string{
		"grid dir=E source=manual passable=true", "resolution=vetoed", "weight=2.5", `phrase="толкнуть ящик"`, "no_route=true",
		"grid dir=N source=manual passable=false", "resolution=blocked",
		`seam order=0 selector={target_zone="B" target_tag=7 command="войти"}`, `seam order=1 selector={target_zone="B" target_tag=8 command="войти"}`,
		`seam order=2 selector={target_zone="B" target_tag=9 command="???"}`, "direction=unparsed", "resolution=dead_target",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("mud_look_map omits %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `command="войти"`) != 2 {
		t.Errorf("same-direction seams collapsed:\n%s", text)
	}

	// This is the actual REST/UI SlimRoom source, not a hand-built formatter
	// input: all four provenance fields and both directed attr kinds must survive
	// the frozen-set fork then remain visible through mud_map_zone.
	zone := toolText(t, callTool(t, s, "mud_map_zone", fmt.Sprintf(`{"session_id":%d,"zone":"A"}`, sid)))
	for _, want := range []string{
		"manual_open=[E]", "manual_blocked=[N]", "grid_parsed=[S]", "grid_fallback=[W]",
		`attrs={dir=E weight=2.5 phrase="толкнуть ящик" no_route=true}`,
		`seam={order=0 kind=seam source=manual passable=true target_zone="B" target_tag=7 command="войти" to={zone="B" x=4 y=5 l=0} weight=2.5 phrase="толкнуть ящик" no_route=true}`,
	} {
		if !strings.Contains(zone, want) {
			t.Errorf("mud_map_zone misses REST topology %q:\n%s", want, zone)
		}
	}
	original := toolText(t, callTool(t, s, "mud_map_zone", fmt.Sprintf(`{"session_id":%d,"map_set":%d,"zone":"A"}`, sid, id)))
	if strings.Contains(original, "manual_open=[E]") || strings.Contains(original, "manual_blocked=[N]") {
		t.Errorf("frozen source set leaked COW topology:\n%s", original)
	}
}
