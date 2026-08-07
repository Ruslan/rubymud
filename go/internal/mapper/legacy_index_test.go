package mapper

import "rubymud/go/internal/storage"

// buildTestIndex is legacy inference for fixtures only. Production has no
// room-derived index constructor and must supply authoritative map_edges rows.
func buildTestIndex(mapSetID int64, rooms []storage.Room) *Index {
	return BuildIndexWithEdges(mapSetID, rooms, storage.DeriveEdges(mapSetID, rooms))
}

func deriveFixtureEdges(mapSetID int64, rooms []storage.Room, drafts []storage.LegacySeamDraft) []storage.MapEdge {
	return storage.DeriveImportedEdges(mapSetID, rooms, drafts)
}

func seamDraft(from storage.Room, entries ...string) storage.LegacySeamDraft {
	return storage.LegacySeamDraft{
		From:    storage.EdgeCell{Zone: from.Zone, X: from.X, Y: from.Y, L: from.L},
		Entries: entries,
	}
}
