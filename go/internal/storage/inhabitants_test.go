package storage

import (
	"errors"
	"strings"
	"testing"
)

func inhabitantFields(name, kind string) InhabitantFields {
	return InhabitantFields{
		Name:       name,
		Kind:       kind,
		LookTarget: strings.ToLower(name),
		LookText:   name + " looks back at you.",
		Note:       "known contact",
		Strategy:   "speak politely",
	}
}

func TestRoomInhabitantsCRUDAndMultiplePlacements(t *testing.T) {
	store := newMapperTestStore(t)
	setID, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	alpha := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	beta := EdgeCell{Zone: "Beta", X: 0, Y: 0, L: 1}

	smith, err := store.CreateRoomInhabitant(setID, alpha, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Smith", InhabitantKindNPC),
		PresenceText:     "Smith bends over the anvil.",
		Resident:         true,
	})
	if err != nil {
		t.Fatalf("CreateRoomInhabitant(smith): %v", err)
	}
	rat, err := store.CreateRoomInhabitant(setID, alpha, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Cave rat", InhabitantKindMonster),
		PresenceText:     "A cave rat sniffs the floor.",
	})
	if err != nil {
		t.Fatalf("CreateRoomInhabitant(rat): %v", err)
	}
	if smith.ID == 0 || rat.ID == 0 || smith.ID == rat.ID {
		t.Fatalf("unexpected ids: smith=%d rat=%d", smith.ID, rat.ID)
	}

	rows, err := store.ListRoomInhabitants(setID, alpha)
	if err != nil {
		t.Fatalf("ListRoomInhabitants: %v", err)
	}
	if len(rows) != 2 || rows[0].Name != "Cave rat" || rows[1].Name != "Smith" {
		t.Fatalf("room inhabitants = %+v, want stable name order", rows)
	}

	// One normalized entity may occur in several rooms, with placement-owned
	// presence/resident values.
	if _, err := store.PlaceRoomInhabitant(setID, beta, smith.ID, "Smith is buying ore.", false); err != nil {
		t.Fatalf("PlaceRoomInhabitant(beta): %v", err)
	}
	if _, err := store.PlaceRoomInhabitant(setID, beta, smith.ID, "duplicate", true); !errors.Is(err, ErrDuplicateRoomInhabitant) {
		t.Fatalf("duplicate placement error = %v, want ErrDuplicateRoomInhabitant", err)
	}
	betaRows, err := store.ListRoomInhabitants(setID, beta)
	if err != nil || len(betaRows) != 1 || betaRows[0].Resident || betaRows[0].PresenceText != "Smith is buying ore." {
		t.Fatalf("beta placement = %+v err=%v", betaRows, err)
	}

	updated, found, err := store.UpdateRoomInhabitant(setID, alpha, smith.ID, RoomInhabitantFields{
		InhabitantFields: InhabitantFields{
			Name: "Master Smith", Kind: InhabitantKindOther, LookTarget: "smith",
			LookText: "Soot covers his apron.", Note: "quest giver", Strategy: "bring ore",
		},
		PresenceText: "The smith hammers a glowing blade.",
		Resident:     false,
	})
	if err != nil || !found {
		t.Fatalf("UpdateRoomInhabitant: found=%v err=%v", found, err)
	}
	if updated.Name != "Master Smith" || updated.Kind != InhabitantKindOther || updated.Resident || updated.PresenceText == "" {
		t.Fatalf("updated row = %+v", updated)
	}
	// Entity fields are shared; placement fields are not.
	betaRows, _ = store.ListRoomInhabitants(setID, beta)
	if betaRows[0].Name != "Master Smith" || betaRows[0].PresenceText != "Smith is buying ore." {
		t.Fatalf("normalized entity/placement split lost: %+v", betaRows[0])
	}

	deleted, err := store.DeleteRoomInhabitant(setID, alpha, rat.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteRoomInhabitant: deleted=%v err=%v", deleted, err)
	}
	if _, err := store.GetMapInhabitant(setID, rat.ID); !errors.Is(err, ErrInhabitantNotFound) {
		t.Fatalf("orphan entity should be removed, got %v", err)
	}
	rows, _ = store.ListRoomInhabitants(setID, alpha)
	if len(rows) != 1 || rows[0].ID != smith.ID {
		t.Fatalf("after delete = %+v", rows)
	}
	deleted, err = store.DeleteMapInhabitant(setID, smith.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteMapInhabitant: deleted=%v err=%v", deleted, err)
	}
	alphaRows, _ := store.ListRoomInhabitants(setID, alpha)
	betaRows, _ = store.ListRoomInhabitants(setID, beta)
	if len(alphaRows) != 0 || len(betaRows) != 0 {
		t.Fatalf("entity delete left placements: alpha=%+v beta=%+v", alphaRows, betaRows)
	}
	if deleted, err := store.DeleteMapInhabitant(setID, smith.ID); err != nil || deleted {
		t.Fatalf("second entity delete: deleted=%v err=%v", deleted, err)
	}
}

func TestListMapInhabitantsFiltersExcludesAndBoundsCatalog(t *testing.T) {
	store := newMapperTestStore(t)
	setID, err := store.CreateMapSet(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	alpha := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	beta := EdgeCell{Zone: "Beta", X: 0, Y: 0, L: 1}
	smith, err := store.CreateRoomInhabitant(setID, alpha, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Master Smith", InhabitantKindNPC),
		Resident:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PlaceRoomInhabitant(setID, beta, smith.ID, "Buying ore.", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoomInhabitant(setID, alpha, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Cave rat", InhabitantKindMonster),
	}); err != nil {
		t.Fatal(err)
	}

	rows, truncated, err := store.ListMapInhabitants(setID, InhabitantListOptions{Limit: 1})
	if err != nil || len(rows) != 1 || !truncated || rows[0].Name != "Cave rat" {
		t.Fatalf("bounded catalog = %+v truncated=%v err=%v", rows, truncated, err)
	}
	rows, truncated, err = store.ListMapInhabitants(setID, InhabitantListOptions{
		Query: "smith", Zone: "Beta", Limit: 20,
	})
	if err != nil || truncated || len(rows) != 1 || rows[0].ID != smith.ID || rows[0].PlacementCount != 2 {
		t.Fatalf("filtered catalog = %+v truncated=%v err=%v", rows, truncated, err)
	}
	rows, truncated, err = store.ListMapInhabitants(setID, InhabitantListOptions{
		ExcludeCell: &alpha, Limit: 20,
	})
	if err != nil || truncated || len(rows) != 0 {
		t.Fatalf("room-excluded catalog = %+v truncated=%v err=%v", rows, truncated, err)
	}
}

func TestRoomInhabitantValidation(t *testing.T) {
	store := newMapperTestStore(t)
	setID, _ := store.CreateMapSet(sampleInput())
	cell := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}

	cases := []struct {
		name   string
		fields RoomInhabitantFields
	}{
		{name: "blank name", fields: RoomInhabitantFields{InhabitantFields: inhabitantFields(" ", InhabitantKindNPC)}},
		{name: "bad kind", fields: RoomInhabitantFields{InhabitantFields: inhabitantFields("thing", "boss")}},
		{name: "long look", fields: RoomInhabitantFields{InhabitantFields: InhabitantFields{Name: "thing", Kind: InhabitantKindOther, LookText: strings.Repeat("я", InhabitantTextLimit+1)}}},
		{name: "long presence", fields: RoomInhabitantFields{InhabitantFields: inhabitantFields("thing", InhabitantKindOther), PresenceText: strings.Repeat("x", InhabitantTextLimit+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.CreateRoomInhabitant(setID, cell, tc.fields); !errors.Is(err, ErrInvalidInhabitant) {
				t.Fatalf("error = %v, want ErrInvalidInhabitant", err)
			}
		})
	}
	if rows, err := store.ListRoomInhabitants(setID, cell); err != nil || len(rows) != 0 {
		t.Fatalf("invalid writes must be atomic: rows=%+v err=%v", rows, err)
	}
}

func TestRoomInhabitantsAreFrozenSetOverlayAndForkWithTopology(t *testing.T) {
	store := newMapperTestStore(t)
	setID, _ := store.CreateMapSet(sampleInput())
	before, _ := store.GetMapSet(setID)
	cell := EdgeCell{Zone: "Alpha", X: 0, Y: 0, L: 0}
	row, err := store.CreateRoomInhabitant(setID, cell, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Keeper", InhabitantKindNPC),
		PresenceText:     "The keeper waits here.", Resident: true,
	})
	if err != nil {
		t.Fatalf("write frozen overlay: %v", err)
	}
	second, err := store.CreateRoomInhabitant(setID, cell, RoomInhabitantFields{
		InhabitantFields: inhabitantFields("Rat", InhabitantKindMonster),
		PresenceText:     "A rat watches the keeper.",
	})
	if err != nil {
		t.Fatalf("write second frozen overlay: %v", err)
	}
	beta := EdgeCell{Zone: "Beta", X: 8, Y: -2, L: 1}
	if _, err := store.PlaceRoomInhabitant(setID, beta, row.ID, "The keeper visits.", false); err != nil {
		t.Fatalf("place shared frozen inhabitant: %v", err)
	}
	after, _ := store.GetMapSet(setID)
	sets, _ := store.ListMapSets()
	if len(sets) != 1 || after.ID != setID || after.Editable || after.TopologyRevision != before.TopologyRevision {
		t.Fatalf("knowledge write forked/changed topology: before=%+v after=%+v sets=%+v", before, after, sets)
	}

	forkID, err := store.ForkMapSet(setID)
	if err != nil {
		t.Fatalf("ForkMapSet: %v", err)
	}
	forkRows, err := store.ListRoomInhabitants(forkID, cell)
	if err != nil || len(forkRows) != 2 {
		t.Fatalf("forked knowledge = %+v err=%v", forkRows, err)
	}
	if forkRows[0].Name != row.Name || forkRows[0].ID == row.ID || forkRows[1].Name != second.Name || forkRows[1].ID == second.ID {
		t.Fatalf("forked entities were not independently re-keyed: source=%+v/%+v fork=%+v", row, second, forkRows)
	}
	forkBeta, err := store.ListRoomInhabitants(forkID, beta)
	if err != nil || len(forkBeta) != 1 || forkBeta[0].ID != forkRows[0].ID || forkBeta[0].Resident {
		t.Fatalf("forked shared placement = %+v err=%v", forkBeta, err)
	}
}

func TestRoomInhabitantMigrationEnforcesMapSetOwnershipWithoutRoomFK(t *testing.T) {
	store := newMapperTestStore(t)
	sqlDB, err := store.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite PRAGMAs are connection-local. Pin this focused schema check to the
	// connection on which foreign_keys is enabled.
	sqlDB.SetMaxOpenConns(1)
	if err := store.DB().Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		t.Fatal(err)
	}
	first, _ := store.CreateMapSet(MapSetInput{Name: "first"})
	second, _ := store.CreateMapSet(MapSetInput{Name: "second"})
	entity, err := store.CreateMapInhabitant(first, inhabitantFields("Keeper", InhabitantKindNPC))
	if err != nil {
		t.Fatal(err)
	}
	now := nowSQLiteTimePtr()
	wrongSet := RoomInhabitant{
		MapSetID: second, Zone: "Nowhere", InhabitantID: entity.ID, UpdatedAt: now,
	}
	if err := store.DB().Create(&wrongSet).Error; err == nil {
		t.Fatal("cross-map-set placement bypassed the composite foreign key")
	}
	// There is deliberately no FK to rooms: valid knowledge may dangle while a
	// topology room is deleted and later restored by undo.
	dangling := RoomInhabitant{
		MapSetID: first, Zone: "Unmapped", X: 99, Y: -7, L: 2,
		InhabitantID: entity.ID, PresenceText: "Seen here once", UpdatedAt: nowSQLiteTimePtr(),
	}
	if err := store.DB().Create(&dangling).Error; err != nil {
		t.Fatalf("dangling room knowledge should be valid: %v", err)
	}
}
