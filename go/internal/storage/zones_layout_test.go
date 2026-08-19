package storage

import "testing"

func layoutMoveSet(t *testing.T) (*Store, int64) {
	t.Helper()
	store := newMapperTestStore(t)
	id, err := store.CreateMapSet(MapSetInput{Name: "layout move", Rooms: []Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Hint: "a0", EDirs: "[]", Doors: "[]"},
		{Zone: "A", X: 1, Y: 0, L: 0, Hint: "a1", EDirs: "[]", Doors: "[]"},
		{Zone: "B", X: 5, Y: 5, L: 0, Hint: "b", EDirs: "[]", Doors: "[]"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return store, id
}

func TestZoneMoveRejectsPresentationCollisionsWithoutRevisionAdvance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Store, int64) error
	}{
		{"moved explicit versus target implicit", func(s *Store, id int64) error {
			return s.UpsertRoomLayouts(id, []RoomLayout{{Zone: "A", X: 0, Y: 0, L: 0, DisplayX: 5, DisplayY: 5, DisplayL: 0}})
		}},
		{"moved implicit versus target explicit", func(s *Store, id int64) error {
			return s.UpsertRoomLayouts(id, []RoomLayout{{Zone: "B", X: 5, Y: 5, L: 0, DisplayX: 0, DisplayY: 0, DisplayL: 0}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, id := layoutMoveSet(t)
			if err := tc.setup(store, id); err != nil {
				t.Fatal(err)
			}
			before, _ := store.GetMapSet(id)
			preview, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}})
			if err != nil {
				t.Fatal(err)
			}
			if preview.CollisionCount == 0 {
				t.Fatal("preview missed effective display collision")
			}
			if _, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: "B", Rooms: []ZoneCoord{{0, 0, 0}}, ExpectedRevision: before.TopologyRevision}); err == nil || found {
				t.Fatalf("apply found=%v err=%v", found, err)
			}
			after, _ := store.GetMapSet(id)
			if after.TopologyRevision != before.TopologyRevision || roomAt(t, store, id, "A", 0, 0, 0) == nil {
				t.Fatal("collision partially applied or advanced revision")
			}
		})
	}
}

func TestZoneMoveAndRenameCarryLayoutsThroughUndo(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		all          bool
		rooms        []ZoneCoord
	}{
		{"partial move", "B", false, []ZoneCoord{{0, 0, 0}}},
		{"full rename", "C", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, id := layoutMoveSet(t)
			if err := store.UpsertRoomLayouts(id, []RoomLayout{{Zone: "A", X: 0, Y: 0, L: 0, DisplayX: 8, DisplayY: 9, DisplayL: 2, Pinned: true}}); err != nil {
				t.Fatal(err)
			}
			p, err := store.PreviewZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: tc.target, AllSource: tc.all, Rooms: tc.rooms})
			if err != nil {
				t.Fatal(err)
			}
			before, _, found, err := store.ApplyZoneMove(id, ZoneMoveInput{SourceZone: "A", TargetZone: tc.target, AllSource: tc.all, Rooms: tc.rooms, ExpectedRevision: p.Revision})
			if err != nil || !found {
				t.Fatalf("apply found=%v err=%v", found, err)
			}
			got, _ := store.ListRoomLayouts(id, tc.target)
			if len(got) != 1 || got[0].DisplayL != 2 || !got[0].Pinned {
				t.Fatalf("moved layout=%#v", got)
			}
			if ok, err := store.RestoreTopologyBefore(id, before); err != nil || !ok {
				t.Fatalf("undo ok=%v err=%v", ok, err)
			}
			got, _ = store.ListRoomLayouts(id, "A")
			if len(got) != 1 || got[0].DisplayX != 8 {
				t.Fatalf("restored layout=%#v", got)
			}
		})
	}
}
