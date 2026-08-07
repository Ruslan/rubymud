package session

import (
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

func buildTestMapIndex(mapSetID int64, rooms []storage.Room) *mapper.Index {
	return mapper.BuildIndexWithEdges(mapSetID, rooms, storage.DeriveEdges(mapSetID, rooms))
}

func buildTestMapIndexWithSeams(mapSetID int64, rooms []storage.Room, entries ...string) *mapper.Index {
	drafts := []storage.LegacySeamDraft{{From: storage.EdgeCell{Zone: rooms[0].Zone, X: rooms[0].X, Y: rooms[0].Y, L: rooms[0].L}, Entries: entries}}
	return mapper.BuildIndexWithEdges(mapSetID, rooms, storage.DeriveImportedEdges(mapSetID, rooms, drafts))
}
