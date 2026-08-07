package storage

import (
	"math"
	"strings"
	"testing"
)

// Exact-target portal reproduction: a legacy seam can be promoted/retargeted by
// occurrence order, survives room maintenance, and undo restores the literal
// prior row rather than re-importing a legacy draft.
func TestPortalCRUDExactTargetAndUndo(t *testing.T) {
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	from := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	to := EdgeCell{Zone: "Beta", X: 0, Y: 0, L: 1}
	var created PortalWriteResult
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Portal: &PortalTarget{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L}, PortalCommand: "prepare;enter gate", PortalAttrs: EdgeAttrPatch{Phrase: strPtr("say friend;enter")}, PortalResult: &created})
	if err != nil || !found {
		t.Fatalf("create: found=%v err=%v", found, err)
	}
	edges, _ := store.ListMapEdges(id)
	var portal *MapEdge
	for i := range edges {
		if edges[i].Kind == EdgeKindSeam && edges[i].Source == EdgeSourceManual && edges[i].Command == "prepare;enter gate" {
			portal = &edges[i]
		}
	}
	if portal == nil {
		t.Fatal("exact portal missing")
	}
	if created.Selector.Order != portal.SeamOrder || created.Target == nil || created.Target.Cell() != to || created.Command != portal.Command {
		t.Fatalf("create result is not authoritative: %+v portal=%+v", created, portal)
	}
	got, ok := portal.To()
	if !ok || got != to || portal.TargetTag != nil || portal.SeamOrder != 1 {
		t.Fatalf("portal=%+v target=%+v ok=%v", portal, got, ok)
	}

	order := portal.SeamOrder
	updateBefore, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpdatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		PortalSelector: &PortalSelector{Order: order}, PortalPatch: PortalPatch{Command: strPtr("enter revised"), Attrs: EdgeAttrPatch{Weight: floatPtr(.5)}}})
	if err != nil || !found {
		t.Fatalf("update: found=%v err=%v", found, err)
	}
	edges, _ = store.ListMapEdges(id)
	for _, e := range edges {
		if e.Kind == EdgeKindSeam && e.From() == from && e.SeamOrder == order {
			if e.Command != "enter revised" || e.Phrase != "say friend;enter" || e.Weight != .5 {
				t.Fatalf("partial update lost fields: %+v", e)
			}
		}
	}
	if applied, err := store.RestoreTopologyBefore(id, updateBefore); err != nil || !applied {
		t.Fatalf("undo update: applied=%v err=%v", applied, err)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo create: applied=%v err=%v", applied, err)
	}
	edges, _ = store.ListMapEdges(id)
	for _, e := range edges {
		if e.Kind == EdgeKindSeam && e.Command == "prepare;enter gate" {
			t.Fatal("undo create left portal")
		}
	}
}

func TestPortalDeleteLeavesSparseOrders(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	from, to := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}, EdgeCell{Zone: "Beta", X: 0, Y: 0, L: 1}
	for _, command := range []string{"one", "two"} {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Portal: &PortalTarget{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L}, PortalCommand: command}); err != nil || !found {
			t.Fatalf("create %s: %v %v", command, found, err)
		}
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeletePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, PortalSelector: &PortalSelector{Order: 1}}); err != nil || !found {
		t.Fatalf("delete: %v %v", found, err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Portal: &PortalTarget{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L}, PortalCommand: "three"}); err != nil || !found {
		t.Fatalf("third: %v %v", found, err)
	}
	edges, _ := store.ListMapEdges(id)
	have := map[int]bool{}
	for _, e := range edges {
		if e.Kind == EdgeKindSeam && e.From() == from {
			have[e.SeamOrder] = true
		}
	}
	if !have[0] || !have[2] || !have[3] || have[1] {
		t.Fatalf("orders renumbered: %v", have)
	}
}

// Promoting a legacy seam without explicitly retargeting it must freeze its
// currently-resolved coordinate. Keeping target_tag on a manual row would let a
// later tagged-room change silently move an exact portal.
func TestPortalUpdatePromotesResolvedLegacySeamToExactTarget(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	command := "go revised"

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpdatePortal, Zone: "Alpha", X: 0, Y: 0, L: 0,
		PortalSelector: &PortalSelector{Order: 0},
		PortalPatch:    PortalPatch{Command: &command},
	})
	if err != nil || !found {
		t.Fatalf("promote: found=%v err=%v", found, err)
	}
	edge, err := portalAtOrderTx(store.db, id, EdgeCell{"Alpha", 0, 0, 0}, PortalSelector{Order: 0})
	if err != nil || edge == nil {
		t.Fatalf("read promoted portal: edge=%+v err=%v", edge, err)
	}
	want := EdgeCell{"Beta", 0, 0, 1}
	got, resolved := edge.To()
	if edge.Source != EdgeSourceManual || edge.TargetTag != nil || !resolved || got != want {
		t.Fatalf("promotion retained symbolic identity: %+v, target=%+v resolved=%v", edge, got, resolved)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo promotion: applied=%v err=%v", applied, err)
	}
	edge, _ = portalAtOrderTx(store.db, id, EdgeCell{"Alpha", 0, 0, 0}, PortalSelector{Order: 0})
	if edge == nil || edge.Source != EdgeSourceParsed || edge.TargetTag == nil || *edge.TargetTag != 5 || edge.Command != "go" {
		t.Fatalf("undo did not restore legacy seam literally: %+v", edge)
	}
}

func TestDeadLegacySeamRequiresTargetBeforePortalPromotion(t *testing.T) {
	store := newMapperTestStore(t)
	in := sampleInputWithSeams("Missing|go|99")
	id, _ := store.CreateMapSet(in)
	command := "revised"

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpdatePortal, Zone: "Alpha", X: 0, Y: 0, L: 0,
		PortalSelector: &PortalSelector{Order: 0},
		PortalPatch:    PortalPatch{Command: &command},
	})
	if err != nil || found || len(before.Rooms) != 0 || len(before.Manual.Seams) != 0 {
		t.Fatalf("dead promotion without target: before=%+v found=%v err=%v", before, found, err)
	}
	edge, _ := portalAtOrderTx(store.db, id, EdgeCell{"Alpha", 0, 0, 0}, PortalSelector{Order: 0})
	if edge == nil || edge.Source != EdgeSourceParsed || edge.Command != "go" {
		t.Fatalf("refused promotion mutated seam: %+v", edge)
	}
}

func TestPortalCommandValidationAllowsScriptsButRejectsControlsAndOversize(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	from := EdgeCell{"Alpha", 0, 0, 0}
	target := &PortalTarget{Zone: "Beta", X: 0, Y: 0, L: 1}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
		Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Portal: target,
		PortalCommand: "prepare;enter gate",
	}); err != nil || !found {
		t.Fatalf("semicolon script rejected: found=%v err=%v", found, err)
	}
	for _, command := range []string{"go\nlook", "go\x00look", "go\x1blook", strings.Repeat("x", maxPortalCommandBytes+1)} {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
			Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Portal: target, PortalCommand: command,
		}); err == nil || found {
			t.Errorf("unsafe portal command %q: found=%v err=%v", command, found, err)
		}
	}
}

func TestPortalOrderOverflowIsRejected(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	if err := store.db.Exec(`UPDATE map_edges SET seam_order = ? WHERE map_set_id = ? AND kind = ?`,
		int64(math.MaxInt64), id, EdgeKindSeam).Error; err != nil {
		t.Fatal(err)
	}
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
		Zone: "Alpha", X: 0, Y: 0, L: 0,
		Portal: &PortalTarget{Zone: "Beta", X: 0, Y: 0, L: 1}, PortalCommand: "go",
	})
	if err == nil || found || len(before.Rooms) != 0 {
		t.Fatalf("overflow: before=%+v found=%v err=%v", before, found, err)
	}
}

func TestPortalCreateFailureRollsBackAndReturnsNoSnapshot(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	if err := store.db.Exec(`CREATE TRIGGER fail_manual_portal BEFORE INSERT ON map_edges
		WHEN NEW.kind = 'seam' AND NEW.source = 'manual'
		BEGIN SELECT RAISE(ABORT, 'portal insert failed'); END`).Error; err != nil {
		t.Fatal(err)
	}

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
		Zone: "Alpha", X: 0, Y: 0, L: 0,
		Portal: &PortalTarget{Zone: "Beta", X: 0, Y: 0, L: 1}, PortalCommand: "go",
	})
	if err == nil || found || len(before.Rooms) != 0 || len(before.Manual.Cells) != 0 {
		t.Fatalf("failed create leaked success state: before=%+v found=%v err=%v", before, found, err)
	}
	var count int64
	if err := store.db.Model(&MapEdge{}).Where("map_set_id = ? AND kind = ?", id, EdgeKindSeam).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed create changed rows: count=%d err=%v", count, err)
	}
}

func TestDeletedParsedPortalDoesNotResurrectOnRoomMaintenance(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	from := EdgeCell{"Alpha", 0, 0, 0}

	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeletePortal,
		Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		PortalSelector: &PortalSelector{Order: 0},
	}); err != nil || !found {
		t.Fatalf("delete parsed occurrence: found=%v err=%v", found, err)
	}
	hint := "maintained"
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoUpsertRoom,
		Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, Fields: RoomFields{Hint: &hint},
	}); err != nil || !found {
		t.Fatalf("maintain source room: found=%v err=%v", found, err)
	}
	edge, err := portalAtOrderTx(store.db, id, from, PortalSelector{Order: 0})
	if err != nil || edge != nil {
		t.Fatalf("deleted occurrence resurrected: edge=%+v err=%v", edge, err)
	}
}

func TestDeletingPortalTargetRemovesItAndUndoRestoresIt(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	from := EdgeCell{"Alpha", 0, 0, 0}
	target := EdgeCell{"Beta", 0, 0, 1}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
		Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
		Portal: &PortalTarget{Zone: target.Zone, X: target.X, Y: target.Y, L: target.L}, PortalCommand: "exact",
	}); err != nil || !found {
		t.Fatalf("create: found=%v err=%v", found, err)
	}

	before, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoDeleteRoom,
		Zone: target.Zone, X: target.X, Y: target.Y, L: target.L,
	})
	if err != nil || !found {
		t.Fatalf("delete target: found=%v err=%v", found, err)
	}
	if edge, err := portalAtOrderTx(store.db, id, from, PortalSelector{Order: 1}); err != nil || edge != nil {
		t.Fatalf("portal survived missing target: edge=%+v err=%v", edge, err)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo target delete: applied=%v err=%v", applied, err)
	}
	edge, err := portalAtOrderTx(store.db, id, from, PortalSelector{Order: 1})
	got, resolved := EdgeCell{}, false
	if edge != nil {
		got, resolved = edge.To()
	}
	if err != nil || edge == nil || !resolved || got != target || edge.TargetTag != nil {
		t.Fatalf("undo did not restore exact portal: edge=%+v target=%+v resolved=%v err=%v", edge, got, resolved, err)
	}
}

func TestPortalSelfLoopsAndDuplicateCommandsHaveDistinctOccurrences(t *testing.T) {
	store := newMapperTestStore(t)
	id, _ := store.CreateMapSet(sampleInput())
	cell := EdgeCell{"Alpha", 0, 0, 0}
	for i := 0; i < 2; i++ {
		if _, found, err := store.ApplyTopologyOp(id, TopologyOp{Kind: TopoCreatePortal,
			Zone: cell.Zone, X: cell.X, Y: cell.Y, L: cell.L,
			Portal: &PortalTarget{Zone: cell.Zone, X: cell.X, Y: cell.Y, L: cell.L}, PortalCommand: "loop",
		}); err != nil || !found {
			t.Fatalf("create duplicate %d: found=%v err=%v", i, found, err)
		}
	}
	for _, order := range []int{1, 2} {
		edge, err := portalAtOrderTx(store.db, id, cell, PortalSelector{Order: order})
		got, resolved := EdgeCell{}, false
		if edge != nil {
			got, resolved = edge.To()
		}
		if err != nil || edge == nil || !resolved || got != cell || edge.Command != "loop" {
			t.Fatalf("order %d: edge=%+v target=%+v resolved=%v err=%v", order, edge, got, resolved, err)
		}
	}
	rooms, err := store.ListSlimRooms(id, cell.Zone)
	if err != nil {
		t.Fatal(err)
	}
	visible := map[int]bool{}
	for _, room := range rooms {
		if room.X != cell.X || room.Y != cell.Y || room.L != cell.L {
			continue
		}
		for _, seam := range room.SE {
			if seam.Command == "loop" && seam.ToZone != nil && *seam.ToZone == cell.Zone {
				visible[seam.Order] = true
			}
		}
	}
	if !visible[1] || !visible[2] {
		t.Fatalf("same-zone portals missing from slim room feed: %v", visible)
	}
}

func TestIncomingExactPortalLookupUsesEndpointIndex(t *testing.T) {
	store := newMapperTestStore(t)
	rows, err := store.db.Raw(`EXPLAIN QUERY PLAN
		SELECT id FROM map_edges
		WHERE map_set_id = ? AND kind = ? AND source = ? AND target_tag IS NULL
		  AND to_zone = ? AND to_x = ? AND to_y = ? AND to_l = ?`,
		1, EdgeKindSeam, EdgeSourceManual, "Beta", 0, 0, 1).Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "idx_map_edges_exact_portal_to") {
		t.Fatalf("incoming portal lookup scans the edge set:\n%s", joined)
	}
}
