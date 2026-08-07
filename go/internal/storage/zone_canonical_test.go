package storage

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestMapperZoneNamesAreCanonicalAcrossWritesAndLookups(t *testing.T) {
	store := newMapperTestStore(t)
	nfdSource := "Cafe\u0301"
	nfcSource := norm.NFC.String(nfdSource)
	nfdTarget := "Re\u0301gion"
	nfcTarget := norm.NFC.String(nfdTarget)
	if nfdSource == nfcSource || nfdTarget == nfcTarget {
		t.Fatal("test needs distinct NFD and NFC zone names")
	}
	rawSource := "  " + nfdSource + "  "
	rawTarget := "  " + nfdTarget + "  "
	tag := 7

	id, err := store.CreateMapSet(MapSetInput{Rooms: []Room{
		{Zone: rawSource, X: 0, Y: 0, L: 0, Hint: "source", EDirs: "[]", Doors: "[]"},
		{Zone: rawTarget, X: 0, Y: 0, L: 0, Tag: &tag, Hint: "target", EDirs: "[]", Doors: "[]"},
	}, LegacySeams: []LegacySeamDraft{{
		From: EdgeCell{Zone: rawSource, X: 0, Y: 0, L: 0}, Entries: []string{nfcTarget + "|go|7"},
	}}})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	zones, err := store.ListZones(id)
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 || zones[0].Zone != nfcSource || zones[1].Zone != nfcTarget {
		t.Fatalf("stored zones = %+v, want NFC-trimmed %q and %q", zones, nfcSource, nfcTarget)
	}
	if found, err := store.RoomExistsInSet(id, rawSource, 0, 0, 0); err != nil || !found {
		t.Fatalf("RoomExistsInSet(noncanonical) = %v, %v; want true, nil", found, err)
	}
	if rooms, err := store.ListSlimRooms(id, rawSource); err != nil || len(rooms) != 1 || rooms[0].Z != nfcSource {
		t.Fatalf("ListSlimRooms(noncanonical) = %+v, %v; want canonical source room", rooms, err)
	}

	hint := "updated through NFD key"
	before, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoUpsertRoom, Zone: rawSource, X: 0, Y: 0, L: 0,
		Fields: RoomFields{Hint: &hint},
	})
	if err != nil || !found {
		t.Fatalf("NFD upsert: found=%v err=%v", found, err)
	}
	if len(before.Rooms) != 1 || before.Rooms[0].Room.Zone != nfcSource {
		t.Fatalf("undo snapshot zone = %+v, want canonical %q", before.Rooms, nfcSource)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo canonical-keyed write: applied=%v err=%v", applied, err)
	}
	if room := roomAt(t, store, id, nfcSource, 0, 0, 0); room == nil || room.Hint != "source" {
		t.Fatalf("undo did not restore canonical room: %+v", room)
	}

	anno, err := store.UpsertRoomAnnotation(id, rawSource, 0, 0, 0, AnnotationFields{Note: ptrStr("canonical")})
	if err != nil || anno.Zone != nfcSource {
		t.Fatalf("UpsertRoomAnnotation = %+v, %v; want canonical zone %q", anno, err, nfcSource)
	}
	if _, ok, err := store.GetRoomAnnotation(id, rawSource, 0, 0, 0); err != nil || !ok {
		t.Fatalf("GetRoomAnnotation(noncanonical) = ok:%v err:%v, want true, nil", ok, err)
	}

	var portal PortalWriteResult
	before, found, err = store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoCreatePortal, Zone: rawSource, X: 0, Y: 0, L: 0,
		Portal: &PortalTarget{Zone: rawTarget, X: 0, Y: 0, L: 0}, PortalCommand: "enter",
		PortalResult: &portal,
	})
	if err != nil || !found {
		t.Fatalf("NFD portal: found=%v err=%v", found, err)
	}
	if portal.Source.Zone != nfcSource || portal.Target == nil || portal.Target.Zone != nfcTarget {
		t.Fatalf("portal result = %+v, want canonical endpoints", portal)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo portal: applied=%v err=%v", applied, err)
	}

	phrase := "canonical seam selector"
	before, found, err = store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoSetEdgeAttrs, Zone: rawSource, X: 0, Y: 0, L: 0,
		Seam:  &SeamSelector{TargetZone: rawTarget, TargetTag: &tag, Command: "go"},
		Attrs: EdgeAttrPatch{Phrase: &phrase},
	})
	if err != nil || !found {
		t.Fatalf("NFD seam selector: found=%v err=%v", found, err)
	}
	if applied, err := store.RestoreTopologyBefore(id, before); err != nil || !applied {
		t.Fatalf("undo seam attributes: applied=%v err=%v", applied, err)
	}
}
