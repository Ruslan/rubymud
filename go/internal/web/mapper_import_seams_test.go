package web

import (
	"testing"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/storage"
)

func TestLegacyAutomapsImportDraftMaterializesEdgesWithoutRoomStorage(t *testing.T) {
	fromTag, targetTag := 1, 7
	parsed := []mapimport.Room{
		{Zone: "A", X: 0, Y: 0, L: 0, Tag: &fromTag, Automaps: []string{"B|go|7", "B|go|7", "Dead|lost|99", "B|partial"}},
		{Zone: "B", X: 3, Y: 4, L: 0, Tag: &targetTag},
	}
	rooms := toStorageRooms(parsed)
	drafts := legacySeamDrafts(parsed)
	if len(drafts) != 1 || len(drafts[0].Entries) != 4 || drafts[0].From != (storage.EdgeCell{Zone: "A"}) {
		t.Fatalf("legacy drafts = %+v", drafts)
	}
	server, _ := setupTestServer(t)
	store := server.store
	id, err := store.CreateMapSet(storage.MapSetInput{Name: "import", Rooms: rooms, LegacySeams: drafts})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	var columns int64
	if err := store.DB().Raw(`SELECT count(*) FROM pragma_table_info('rooms') WHERE name = 'automaps'`).Scan(&columns).Error; err != nil || columns != 0 {
		t.Fatalf("rooms legacy column = %d, err=%v", columns, err)
	}
	edges, err := store.ListMapEdges(id)
	if err != nil {
		t.Fatalf("ListMapEdges: %v", err)
	}
	var seams []storage.MapEdge
	for _, edge := range edges {
		if edge.Kind == storage.EdgeKindSeam {
			seams = append(seams, edge)
		}
	}
	if len(seams) != 4 || seams[0].SeamOrder != 0 || seams[1].SeamOrder != 1 || seams[2].TargetZone != "Dead" || seams[3].TargetTag != nil {
		t.Fatalf("materialized seam occurrences = %+v", seams)
	}
}
