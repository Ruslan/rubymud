package web

import (
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

func buildTestMapIndex(mapSetID int64, rooms []storage.Room) *mapper.Index {
	return mapper.BuildIndexWithEdges(mapSetID, rooms, storage.DeriveEdges(mapSetID, rooms))
}
