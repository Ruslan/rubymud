package storage

import (
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

const zoneCanonicalMigration = "027_canonical_zone_names.sql"

func TestMigration027CanonicalizesAllPersistedZoneKeys(t *testing.T) {
	store := newMapperTestStore(t)
	nfdSource, nfdTarget := "Cafe\u0301", "Re\u0301gion"
	nfcSource, nfcTarget := norm.NFC.String(nfdSource), norm.NFC.String(nfdTarget)
	rawSource, rawTarget := "  "+nfdSource+"  ", "  "+nfdTarget+"  "
	id, err := store.CreateMapSet(MapSetInput{Rooms: []Room{
		{Zone: nfcSource, X: 0, Y: 0, L: 0, Hint: "source", EDirs: "[]", Doors: "[]"},
		{Zone: nfcTarget, X: 0, Y: 0, L: 0, Hint: "target", EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ApplyTopologyOp(id, TopologyOp{
		Kind: TopoCreatePortal, Zone: nfcSource, X: 0, Y: 0, L: 0,
		Portal: &PortalTarget{Zone: nfcTarget, X: 0, Y: 0, L: 0}, PortalCommand: "enter",
	}); err != nil || !found {
		t.Fatalf("create portal: found=%v err=%v", found, err)
	}
	if _, err := store.UpsertRoomAnnotation(id, nfcSource, 0, 0, 0, AnnotationFields{Note: ptrStr("keep")}); err != nil {
		t.Fatal(err)
	}
	var roomID int64
	if err := store.DB().Raw(`SELECT id FROM rooms WHERE map_set_id=? AND zone=?`, id, nfcSource).Scan(&roomID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`INSERT INTO room_images (room_id, prompt) VALUES (?, 'keep image')`, roomID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE rooms SET zone=? WHERE map_set_id=? AND zone=?`, rawSource, id, nfcSource).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE rooms SET zone=? WHERE map_set_id=? AND zone=?`, rawTarget, id, nfcTarget).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE room_annotations SET zone=? WHERE map_set_id=?`, rawSource, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE map_edges SET from_zone=?, to_zone=?, target_zone=? WHERE map_set_id=?`, rawSource, rawTarget, rawTarget, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`DELETE FROM schema_migrations WHERE version=?`, zoneCanonicalMigration).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(store.DB()); err != nil {
		t.Fatalf("run migration: %v", err)
	}

	if rooms, err := store.ListSlimRooms(id, nfcSource); err != nil || len(rooms) != 1 || rooms[0].Z != nfcSource {
		t.Fatalf("canonical source lookup = %+v, %v", rooms, err)
	}
	if _, ok, err := store.GetRoomAnnotation(id, nfcSource, 0, 0, 0); err != nil || !ok {
		t.Fatalf("canonical annotation lookup = ok:%v err:%v", ok, err)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil || len(edges) != 1 {
		t.Fatalf("ListMapEdges = %+v, %v", edges, err)
	}
	if edge := edges[0]; edge.FromZone != nfcSource || edge.TargetZone != nfcTarget {
		t.Fatalf("edge zones = %+v, want canonical endpoints", edge)
	} else if to, ok := edge.To(); !ok || to.Zone != nfcTarget {
		t.Fatalf("edge target = %+v ok=%v, want canonical target", to, ok)
	}
	var image RoomImage
	if err := store.DB().First(&image).Error; err != nil || image.RoomID != roomID || image.Prompt != "keep image" {
		t.Fatalf("room image after zone migration = %+v err=%v", image, err)
	}
}

func TestMigration027RefusesCollidingZoneNamespacesAtomically(t *testing.T) {
	store := newMapperTestStore(t)
	nfd := "Cafe\u0301"
	nfc := norm.NFC.String(nfd)
	id, err := store.CreateMapSet(MapSetInput{Rooms: []Room{
		{Zone: nfc, X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: "other", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE rooms SET zone=? WHERE map_set_id=? AND zone='other'`, "  "+nfd+"  ", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`DELETE FROM schema_migrations WHERE version=?`, zoneCanonicalMigration).Error; err != nil {
		t.Fatal(err)
	}
	err = runMigrations(store.DB())
	if err == nil || !strings.Contains(err.Error(), "zone collision") {
		t.Fatalf("collision migration error = %v, want zone collision", err)
	}
	var zones []string
	if err := store.DB().Raw(`SELECT zone FROM rooms WHERE map_set_id=? ORDER BY zone`, id).Scan(&zones).Error; err != nil {
		t.Fatal(err)
	}
	if len(zones) != 2 || zones[0] != "  "+nfd+"  " || zones[1] != nfc {
		t.Fatalf("collision migration changed rows: %q", zones)
	}
	var applied int64
	if err := store.DB().Raw(`SELECT count(*) FROM schema_migrations WHERE version=?`, zoneCanonicalMigration).Scan(&applied).Error; err != nil || applied != 0 {
		t.Fatalf("failed migration was recorded=%d err=%v", applied, err)
	}
}

func TestMigration027CanonicalizesReferencesAndReresolvesSymbolicSeams(t *testing.T) {
	store := newMapperTestStore(t)
	nfd := "Cafe\u0301"
	nfc := norm.NFC.String(nfd)
	tag := 7
	id, err := store.CreateMapSet(MapSetInput{Rooms: []Room{
		{Zone: "Source", X: 0, Y: 0, L: 0, EDirs: "[]", Doors: "[]"},
		{Zone: nfc, X: 4, Y: 5, L: 0, Tag: &tag, EDirs: "[]", Doors: "[]"},
	}, LegacySeams: []LegacySeamDraft{{
		From: EdgeCell{Zone: "Source", X: 0, Y: 0, L: 0}, Entries: []string{nfc + "|go|7"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertRoomAnnotation(id, nfc, 4, 5, 0, AnnotationFields{Note: ptrStr("reference spelling")}); err != nil {
		t.Fatal(err)
	}
	// Reproduce the old state that prompted the migration: the room definition is
	// NFC, while a symbolic seam and annotation use an NFD/space spelling. The
	// target was dead only because the symbolic lookup was exact.
	raw := "  " + nfd + "  "
	if err := store.DB().Exec(`UPDATE map_edges
		SET target_zone=?, to_zone=NULL, to_x=NULL, to_y=NULL, to_l=NULL
		WHERE map_set_id=? AND kind='seam'`, raw, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`UPDATE room_annotations SET zone=? WHERE map_set_id=?`, raw, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Exec(`DELETE FROM schema_migrations WHERE version=?`, zoneCanonicalMigration).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(store.DB()); err != nil {
		t.Fatalf("definition/reference migration must not collide: %v", err)
	}

	edges, err := store.ListMapEdges(id)
	if err != nil || len(edges) != 1 {
		t.Fatalf("ListMapEdges = %+v, %v", edges, err)
	}
	if edges[0].TargetZone != nfc {
		t.Fatalf("symbolic target zone = %q, want %q", edges[0].TargetZone, nfc)
	}
	if target, ok := edges[0].To(); !ok || target != (EdgeCell{Zone: nfc, X: 4, Y: 5, L: 0}) {
		t.Fatalf("formerly dead seam target = %+v ok=%v", target, ok)
	}
	if annotation, ok, err := store.GetRoomAnnotation(id, nfc, 4, 5, 0); err != nil || !ok || annotation.Zone != nfc {
		t.Fatalf("canonical annotation = %+v ok=%v err=%v", annotation, ok, err)
	}
}
