package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"

	"rubymud/go/internal/mapimport"
)

// ErrMapSetNotFound is returned when a positive active-map-set assignment loses
// the atomic existence check (including a concurrent administrative deletion).
var (
	ErrMapSetNotFound  = errors.New("map set not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrMapSetHasForks  = errors.New("map set has editable forks")
)

// MapSet is one imported archive of maps (a global entity a session references).
//
// Editable/ForkedFromID model the write-mode fork: an imported set is
// the user's frozen source of truth (Editable=false); the first topology write
// forks it copy-on-write into an editable copy (Editable=true, ForkedFromID = the
// source set's id) and writes land there.
type MapSet struct {
	ID               int64       `gorm:"primaryKey" json:"id"`
	Name             string      `json:"name"`
	SourceArchive    string      `json:"source_archive"`
	ImportedAt       *SQLiteTime `json:"imported_at"`
	ZoneCount        int         `json:"zone_count"`
	RoomCount        int         `json:"room_count"`
	SeamCount        int         `json:"seam_count"`
	Note             string      `json:"note"`
	Editable         bool        `gorm:"column:editable" json:"editable"`
	ForkedFromID     *int64      `gorm:"column:forked_from_id" json:"forked_from_id"`
	TopologyRevision int64       `gorm:"column:topology_revision" json:"topology_revision"`
}

func (MapSet) TableName() string { return "map_sets" }

// Room is a persisted map room. edirs/doors are JSON-encoded text; topology is
// stored exclusively in map_edges.
// The JSON tags below expose the decoded forms via the Room* helpers,
// not GORM directly. BColor is stored as text ("" when null; a "cl..." ident or
// the decimal int as a string otherwise) so both representations round-trip.
type Room struct {
	ID          int64   `gorm:"primaryKey" json:"id"`
	MapSetID    int64   `gorm:"column:map_set_id" json:"map_set_id"`
	Zone        string  `json:"zone"`
	X           int     `json:"x"`
	Y           int     `json:"y"`
	L           int     `json:"l"`
	DX          int     `gorm:"column:dx" json:"dx"`
	DY          int     `gorm:"column:dy" json:"dy"`
	DL          int     `gorm:"column:dl" json:"dl"`
	Tag         *int    `json:"tag"`
	Hint        string  `json:"hint"`
	Desc        string  `json:"desc"`
	Exits       string  `json:"exits"`
	EDirs       string  `gorm:"column:edirs" json:"edirs"` // JSON array text
	Doors       string  `json:"doors"`                     // JSON array text
	Ch          int     `json:"ch"`                        // bitmask ChN..ChD
	ImageIndex  *int    `gorm:"column:imageindex" json:"imageindex"`
	Note        string  `json:"note"`
	IsDT        bool    `gorm:"column:is_dt" json:"is_dt"`
	Pipe        bool    `json:"pipe"`
	BColor      *string `gorm:"column:bcolor" json:"bcolor"`
	Fingerprint string  `json:"fingerprint"`
}

func (Room) TableName() string { return "rooms" }

// RoomImage is the optional per-room "vibe" image (schema only this round).
type RoomImage struct {
	ID          int64       `gorm:"primaryKey" json:"id"`
	RoomID      int64       `gorm:"column:room_id" json:"room_id"`
	Thumb       []byte      `json:"-"`
	FullPath    string      `gorm:"column:full_path" json:"full_path"`
	Prompt      string      `json:"prompt"`
	Model       string      `json:"model"`
	Seed        *int64      `json:"seed"`
	GeneratedAt *SQLiteTime `gorm:"column:generated_at" json:"generated_at"`
}

func (RoomImage) TableName() string { return "room_images" }

// MapSetInput is the assembled result of an import, ready to persist.
type MapSetInput struct {
	Name          string
	SourceArchive string
	ZoneCount     int
	RoomCount     int
	SeamCount     int
	Note          string
	Rooms         []Room
	// LegacySeams is raw importer input only. It is materialized into map_edges in
	// CreateMapSet's transaction and is never persisted with rooms.
	LegacySeams []LegacySeamDraft
}

// LegacySeamDraft carries one parsed .mm2 AutoMaps list until import commits.
// Source coordinates make duplicate, partial and same-zone occurrences explicit
// without leaking the retired raw representation into Room/runtime state.
type LegacySeamDraft struct {
	From    EdgeCell
	Entries []string
}

// CreateMapSet inserts one map_sets row and all its rooms in a single
// transaction. Returns the new map set id. Room.MapSetID is set by this call.
func (s *Store) CreateMapSet(in MapSetInput) (int64, error) {
	var err error
	in, err = normalizeMapSetInputZones(in)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		set := MapSet{
			Name:          in.Name,
			SourceArchive: in.SourceArchive,
			ImportedAt:    nowSQLiteTimePtr(),
			ZoneCount:     in.ZoneCount,
			RoomCount:     in.RoomCount,
			SeamCount:     in.SeamCount,
			Note:          in.Note,
		}
		if err := tx.Create(&set).Error; err != nil {
			return err
		}
		id = set.ID
		if len(in.Rooms) == 0 {
			return nil
		}
		for i := range in.Rooms {
			in.Rooms[i].MapSetID = set.ID
			in.Rooms[i].ID = 0
		}
		// Batch insert keeps a large corpus (thousands of rooms) off many
		// round-trips without blocking anything on the hot path.
		if err := tx.CreateInBatches(in.Rooms, 500).Error; err != nil {
			return err
		}
		// Materialize the set's topology in the SAME transaction: a set must never
		// be visible with rooms but no edges, or the walk would see an empty graph.
		return insertEdges(tx, DeriveImportedEdges(set.ID, in.Rooms, in.LegacySeams))
	})
	return id, err
}

// ForkMapSet forks a source map set copy-on-write into a fresh EDITABLE copy
// (writable vs imported). It creates a new map_sets row
// (editable=1, forked_from_id=srcID, name "<orig> (editable)", copied counts) and
// copies EVERY child of the source into the new set in ONE transaction:
//   - all rooms          (rooms.map_set_id -> newID)
//   - all room_annotations (room_annotations.map_set_id -> newID; the overlay is
//     keyed by logical coord, not rooms.id, so it copies by re-keying the set id)
//   - all room_images     (re-pointed to the copied rooms by logical coord, so the
//     image travels with its room even though rooms.id changes on copy; FullPath
//     deliberately remains a shared filesystem reference, so the source must
//     remain until its editable forks are deleted)
//
// The source set is left completely untouched (frozen source of truth). Returns
// the new (editable) set id. A source that does not exist is an error.
func (s *Store) ForkMapSet(srcID int64) (int64, error) {
	var newID int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var src MapSet
		if err := tx.First(&src, srcID).Error; err != nil {
			return err
		}

		fork := MapSet{
			Name:             src.Name + " (editable)",
			SourceArchive:    src.SourceArchive,
			ImportedAt:       nowSQLiteTimePtr(),
			ZoneCount:        src.ZoneCount,
			RoomCount:        src.RoomCount,
			SeamCount:        src.SeamCount,
			Note:             src.Note,
			Editable:         true,
			ForkedFromID:     &srcID,
			TopologyRevision: src.TopologyRevision,
		}
		if err := tx.Create(&fork).Error; err != nil {
			return err
		}
		newID = fork.ID

		// Copy rooms. Keep a map from the source room's logical coord to the new
		// room id so images (keyed by rooms.id) can be re-pointed after the copy.
		var srcRooms []Room
		if err := tx.Where("map_set_id = ?", srcID).Find(&srcRooms).Error; err != nil {
			return err
		}
		type coordKey struct {
			zone    string
			x, y, l int
		}
		oldRoomIDByCoord := map[coordKey]int64{}
		newRooms := make([]Room, 0, len(srcRooms))
		for i := range srcRooms {
			r := srcRooms[i]
			oldRoomIDByCoord[coordKey{r.Zone, r.X, r.Y, r.L}] = r.ID
			r.ID = 0
			r.MapSetID = newID
			newRooms = append(newRooms, r)
		}
		if len(newRooms) > 0 {
			if err := tx.CreateInBatches(newRooms, 500).Error; err != nil {
				return err
			}
		}
		// After insert, newRooms[i].ID is populated; build old-room-id -> new-room-id.
		newRoomIDByOldID := map[int64]int64{}
		for i := range newRooms {
			nr := newRooms[i]
			oldID := oldRoomIDByCoord[coordKey{nr.Zone, nr.X, nr.Y, nr.L}]
			newRoomIDByOldID[oldID] = nr.ID
		}

		// Copy room_images, re-pointing room_id to the copied room.
		var srcImages []RoomImage
		if err := tx.Where("room_id IN (SELECT id FROM rooms WHERE map_set_id = ?)", srcID).
			Find(&srcImages).Error; err != nil {
			return err
		}
		if len(srcImages) > 0 {
			newImages := make([]RoomImage, 0, len(srcImages))
			for i := range srcImages {
				img := srcImages[i]
				nid, ok := newRoomIDByOldID[img.RoomID]
				if !ok {
					continue // orphan image (no matching room) — skip
				}
				img.ID = 0
				img.RoomID = nid
				newImages = append(newImages, img)
			}
			if len(newImages) > 0 {
				if err := tx.CreateInBatches(newImages, 500).Error; err != nil {
					return err
				}
			}
		}

		// Copy map_edges. They are keyed by logical coord, not rooms.id, so the copy
		// is a plain map_set_id re-key — no id remapping, unlike room_images above.
		// The fork must carry the topology, not re-derive it: a re-derivation would
		// silently drop any non-derivable (manual) edge the source set carries.
		var srcEdges []MapEdge
		if err := tx.Where("map_set_id = ?", srcID).Order("id ASC").Find(&srcEdges).Error; err != nil {
			return err
		}
		for i := range srcEdges {
			srcEdges[i].MapSetID = newID
		}
		if err := insertEdges(tx, srcEdges); err != nil {
			return err
		}

		// Copy room_annotations (keyed by logical coord, not rooms.id): re-key the
		// map_set_id to the fork so the overlay travels with the copy.
		var srcAnnos []RoomAnnotation
		if err := tx.Where("map_set_id = ?", srcID).Find(&srcAnnos).Error; err != nil {
			return err
		}
		if len(srcAnnos) > 0 {
			newAnnos := make([]RoomAnnotation, 0, len(srcAnnos))
			for i := range srcAnnos {
				a := srcAnnos[i]
				a.ID = 0
				a.MapSetID = newID
				newAnnos = append(newAnnos, a)
			}
			if err := tx.CreateInBatches(newAnnos, 500).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return newID, err
}

// mapSetsWithCurrentCounts replaces import-time room/zone/seam metadata with
// counts from the current authoritative topology while preserving the wire shape.
func mapSetsWithCurrentCounts(db *gorm.DB) *gorm.DB {
	return db.Table("map_sets").
		Select(`map_sets.id, map_sets.name, map_sets.source_archive, map_sets.imported_at,
			COALESCE(room_stats.zone_count, 0) AS zone_count,
			COALESCE(room_stats.room_count, 0) AS room_count,
			COALESCE(edge_stats.seam_count, 0) AS seam_count,
			map_sets.note, map_sets.editable, map_sets.forked_from_id, map_sets.topology_revision`).
		Joins(`LEFT JOIN (
			SELECT map_set_id, COUNT(DISTINCT zone) AS zone_count, COUNT(*) AS room_count
			FROM rooms
			GROUP BY map_set_id
		) AS room_stats ON room_stats.map_set_id = map_sets.id`).
		Joins(`LEFT JOIN (
			SELECT map_set_id, COUNT(*) AS seam_count
			FROM map_edges
			WHERE kind = 'seam'
			GROUP BY map_set_id
		) AS edge_stats ON edge_stats.map_set_id = map_sets.id`)
}

// ListMapSets returns all map sets, newest first.
func (s *Store) ListMapSets() ([]MapSet, error) {
	var sets []MapSet
	err := mapSetsWithCurrentCounts(s.db).
		Order("map_sets.id DESC").
		Scan(&sets).Error
	return sets, err
}

// GetMapSet returns one map set by id.
func (s *Store) GetMapSet(id int64) (MapSet, error) {
	var set MapSet
	err := mapSetsWithCurrentCounts(s.db).
		Where("map_sets.id = ?", id).
		Take(&set).Error
	return set, err
}

// DeleteMapSet removes a map set. Child rooms/images/annotations are removed
// explicitly (FK ON DELETE CASCADE is declared, but we do not rely on it being
// enforced at runtime for every driver path). Sessions pointing at the set are
// reset to NULL so they degrade gracefully.
func (s *Store) DeleteMapSet(id int64) error {
	_, err := s.DeleteMapSetWithAffectedSessions(id)
	return err
}

// PreflightMapSetDelete performs the stable user-facing part of deletion
// validation. DeleteMapSetWithAffectedSessions repeats this check inside its
// transaction because a concurrent writer remains the authority.
func (s *Store) PreflightMapSetDelete(id int64) error {
	if id <= 0 {
		return gorm.ErrRecordNotFound
	}
	return preflightMapSetDelete(s.db, id)
}

func preflightMapSetDelete(db *gorm.DB, id int64) error {
	var count int64
	if err := db.Model(&MapSet{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	var forkCount int64
	if err := db.Model(&MapSet{}).Where("forked_from_id = ?", id).Count(&forkCount).Error; err != nil {
		return err
	}
	if forkCount > 0 {
		return ErrMapSetHasForks
	}
	return nil
}

// DeleteMapSetWithAffectedSessions deletes a map set and returns the sessions
// whose active_map_set_id was cleared by THE SAME transaction. Reading these ids
// before a separate delete lets a concurrent assignment leave a live tracker
// pointing at a deleted graph; keeping both operations in this transaction makes
// SQLite serialize assignment-before-delete and delete-before-assignment safely.
func (s *Store) DeleteMapSetWithAffectedSessions(id int64) ([]int64, error) {
	if id <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var affected []int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := preflightMapSetDelete(tx, id); err != nil {
			return err
		}
		if err := tx.Model(&SessionRecord{}).
			Where("active_map_set_id = ?", id).
			Pluck("id", &affected).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE sessions SET active_map_set_id = NULL WHERE active_map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM room_images WHERE room_id IN (SELECT id FROM rooms WHERE map_set_id = ?)", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM rooms WHERE map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM map_edges WHERE map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM room_annotations WHERE map_set_id = ?", id).Error; err != nil {
			return err
		}
		result := tx.Delete(&MapSet{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	return affected, err
}

// ListZones returns the distinct zone names in a set with a room count each,
// ordered by zone name.
type ZoneInfo struct {
	Zone      string `json:"zone"`
	RoomCount int    `json:"room_count"`
}

func (s *Store) ListZones(mapSetID int64) ([]ZoneInfo, error) {
	var zones []ZoneInfo
	err := s.db.Model(&Room{}).
		Select("zone, count(*) as room_count").
		Where("map_set_id = ?", mapSetID).
		Group("zone").
		Order("zone ASC").
		Scan(&zones).Error
	if zones == nil {
		zones = []ZoneInfo{}
	}
	return zones, err
}

// SlimRoom is the compact wire form for map rendering and MCP. img is 1 when a
// room_images row exists for the room, else 0.
type SlimRoom struct {
	Z   string   `json:"z"` // zone
	T   *int     `json:"t"` // tag
	H   string   `json:"h"` // hint
	X   int      `json:"x"`
	Y   int      `json:"y"`
	L   int      `json:"l"`
	E   []string `json:"e"`  // exit dirs (edirs)
	D   []string `json:"d"`  // door dirs
	Ch  int      `json:"ch"` // connectivity bitmask
	P   bool     `json:"p"`  // pipe
	I   *int     `json:"i"`  // imageindex
	S   bool     `json:"s"`  // is_dt
	DX  int      `json:"dx"`
	DY  int      `json:"dy"`
	DL  int      `json:"dl"`
	Img int      `json:"img"` // 0|1 image present

	// The MATERIALIZED topology of this cell, per outgoing direction. `e` above is
	// the room's exits LINE (what the MUD printed); these three are what the graph
	// actually holds, which is not the same thing and is what the map pane's edge
	// editor edits:
	//   m  — hand-drawn hidden exits ("there is a way here whatever the line says")
	//   mb — hand-drawn negations    ("there is NO way here whatever the line says")
	//   g  — rows PARSED from the room's own exit data that resolve to a target
	//   gf — rows that exist only because BOTH ends carry no exit data at all (the
	//        permissive grid fallback). Kept apart from `g` because the difference is
	//        the whole question the edge editor asks: for a `g` edge the text said
	//        something, for a `gf` edge nothing did — which makes a fallback edge the
	//        prime candidate for a negation, and calling it "parsed from the room
	//        text" in the popup would be a lie.
	// Sending them costs one extra query per zone and no extra round-trip; without
	// them the pane cannot show markup, and markup the player cannot see is markup
	// the player cannot take back.
	M  []string `json:"m"`
	MB []string `json:"mb"`
	G  []string `json:"g"`
	GF []string `json:"gf"`

	// EA is the ATTRIBUTE layer (migration 018), one entry per outgoing face that
	// actually carries something — how the passage is taken (phrase / door + lock +
	// opening command) and whether the router may use it.
	//
	// A LIST OF ONLY THE INTERESTING FACES, not six slots per room: on the live
	// corpus 305 rooms out of 8034 have a door and nothing else has anything, so a
	// per-direction structure on every room would multiply the zone payload for
	// fields that are empty ~99% of the time. Whatever is absent here is the default
	// (no phrase, no door, routable), which is what the edge editor renders.
	EA []SlimEdgeAttr `json:"ea"`
	// SE is the authoritative outgoing seam list. It is sparse per room and
	// ordered by persisted occurrence, so ordinary rooms do not gain payload.
	SE []SlimSeam `json:"se"`
	// XZ is the sparse list of outgoing grid edges that cross a zone boundary.
	// They remain grid edges (and therefore keep their routing/editor meaning),
	// but a zone-local canvas needs their endpoint to make the boundary visible.
	XZ []SlimCrossZoneGrid `json:"xz"`
}

// SlimCrossZoneGrid is a resolved outgoing grid contact. A room has at most one
// row per direction, keeping XZ bounded at six entries without a second fetch.
type SlimCrossZoneGrid struct {
	Dir      string `json:"dir"`
	Source   string `json:"r"`
	Passable bool   `json:"p"`
	ToZone   string `json:"z"`
	ToX      int    `json:"x"`
	ToY      int    `json:"y"`
	ToL      int    `json:"l"`
}

// MapRoomSearchResult is the intentionally small cross-zone search payload.
// Opening a result loads its full zone through ListSlimRooms as usual.
type MapRoomSearchResult struct {
	Zone string `json:"z"`
	Hint string `json:"h"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	L    int    `json:"l"`
}

// SearchMapRooms scans only the five columns needed by the map search. SQLite's
// built-in lower() is ASCII-only, so matching is folded in Go for room names in
// any script. The current zone is returned first without excluding other zones.
func (s *Store) SearchMapRooms(mapSetID int64, query, preferredZone string, limit int) ([]MapRoomSearchResult, error) {
	needle := strings.ToLower(norm.NFC.String(strings.TrimSpace(query)))
	if needle == "" || limit <= 0 {
		return []MapRoomSearchResult{}, nil
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.Raw(`SELECT zone, hint, x, y, l
		FROM rooms
		WHERE map_set_id = ? AND hint <> ''
		ORDER BY id`, mapSetID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	preferred := make([]MapRoomSearchResult, 0, limit)
	other := make([]MapRoomSearchResult, 0, limit)
	for rows.Next() {
		var hit MapRoomSearchResult
		if err := rows.Scan(&hit.Zone, &hit.Hint, &hit.X, &hit.Y, &hit.L); err != nil {
			return nil, err
		}
		haystack := strings.ToLower(norm.NFC.String(hit.Hint))
		if !strings.Contains(haystack, needle) {
			continue
		}
		if hit.Zone == preferredZone {
			if len(preferred) < limit {
				preferred = append(preferred, hit)
			}
		} else if len(other) < limit {
			other = append(other, hit)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]MapRoomSearchResult, 0, limit)
	results = append(results, preferred...)
	remaining := limit - len(results)
	if remaining > 0 && len(other) > remaining {
		other = other[:remaining]
	}
	results = append(results, other...)
	return results, nil
}

// SlimEdgeAttr is one face's attribute set on the wire, with the defaults omitted
// so an ordinary door costs `{"dir":"N","door":true}` and nothing more.
type SlimEdgeAttr struct {
	Dir     string   `json:"dir"`
	Weight  *float64 `json:"weight,omitempty"`
	Phrase  string   `json:"phrase,omitempty"`
	Door    bool     `json:"door,omitempty"`
	Locked  bool     `json:"locked,omitempty"`
	OpenCmd string   `json:"open_cmd,omitempty"`
	NoRoute bool     `json:"no_route,omitempty"`
}

// SlimSeam is one normalized authoritative seam. Nullable selector/target fields
// represent malformed legacy entries and dead seams without a raw automaps read.
type SlimSeam struct {
	Order      int      `json:"o"`
	Kind       string   `json:"k"`
	Source     string   `json:"r"`
	Passable   bool     `json:"p"`
	TargetZone string   `json:"tz"`
	TargetTag  *int     `json:"tt,omitempty"`
	ToZone     *string  `json:"z,omitempty"`
	ToX        *int     `json:"x,omitempty"`
	ToY        *int     `json:"y,omitempty"`
	ToL        *int     `json:"l,omitempty"`
	Command    string   `json:"c"`
	Weight     *float64 `json:"weight,omitempty"`
	Phrase     string   `json:"phrase,omitempty"`
	Door       bool     `json:"door,omitempty"`
	Locked     bool     `json:"locked,omitempty"`
	OpenCmd    string   `json:"open_cmd,omitempty"`
	NoRoute    bool     `json:"no_route,omitempty"`
}

// ListSlimRooms returns the slim rooms for one zone of a set, ordered by grid
// coordinate for stable rendering. img reflects whether a room_images row exists.
func (s *Store) ListSlimRooms(mapSetID int64, zone string) ([]SlimRoom, error) {
	var err error
	zone, err = NormalizeZoneName(zone)
	if err != nil {
		return nil, err
	}
	var rooms []Room
	err = s.db.Where("map_set_id = ? AND zone = ?", mapSetID, zone).
		Order("l ASC, x ASC, y ASC").
		Find(&rooms).Error
	if err != nil {
		return nil, err
	}
	if len(rooms) == 0 {
		return []SlimRoom{}, nil
	}

	// One query for which room ids have an image, to avoid N+1.
	ids := make([]int64, len(rooms))
	for i, r := range rooms {
		ids[i] = r.ID
	}
	imgIDs := map[int64]bool{}
	var withImages []int64
	if err := s.db.Model(&RoomImage{}).
		Where("room_id IN ?", ids).
		Distinct().
		Pluck("room_id", &withImages).Error; err != nil {
		return nil, err
	}
	for _, id := range withImages {
		imgIDs[id] = true
	}

	// One query for the zone's materialized outgoing edges, bucketed per cell and
	// direction (again avoiding N+1). Only grid rows matter here: a seam has no
	// direction and is already carried by `a`.
	//
	// Hand-rolled scan of the columns this needs, for the same reason ListMapEdges
	// is hand-rolled (see the note there): a gorm Find materializes every column of
	// every row through reflection, and a zone is ~1k edge rows re-read on every
	// rooms_changed — i.e. after every single map write. MEASURED on the live set,
	// zone "Вингаард" (441 rooms, 972 edge rows), medians of 15:
	//
	//	rooms + images only (what this cost before edges were sent)  6.5 ms
	//	the edge scan below, hand-rolled                             3.1 ms
	//	the same scan through gorm Find + ORDER BY                  12.3 ms
	//	ListSlimRooms as it now stands                               8.8 ms
	//
	// So sending the hand-drawn layer costs ~35% on a zone fetch rather than the
	// ~2.5x the reflective read cost. There is also NO ORDER BY: the rows go
	// straight into per-cell buckets, so their order is not observable.
	type dirSets struct {
		m, mb, g, gf []string
		attrs        []SlimEdgeAttr
		seams        []SlimSeam
		crossZone    []SlimCrossZoneGrid
	}
	edgesByCell := map[EdgeCell]*dirSets{}
	edgeRows, err := s.db.Raw(`SELECT source, passable, from_x, from_y, from_l, dir, to_zone, to_x, to_y, to_l,
		EXISTS (SELECT 1 FROM rooms target
			WHERE target.map_set_id = map_edges.map_set_id
			  AND target.zone = map_edges.to_zone
			  AND target.x = map_edges.to_x AND target.y = map_edges.to_y AND target.l = map_edges.to_l)
		FROM map_edges WHERE map_set_id = ? AND from_zone = ? AND kind = ?`,
		mapSetID, zone, EdgeKindGrid).Rows()
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var source, dir string
		var passable bool
		var fx, fy, fl int
		var toZone strNull
		var toX, toY, toL intNull
		var targetExists bool
		if err := edgeRows.Scan(&source, &passable, &fx, &fy, &fl, &dir, &toZone, &toX, &toY, &toL, &targetExists); err != nil {
			return nil, err
		}
		cell := EdgeCell{zone, fx, fy, fl}
		d := edgesByCell[cell]
		if d == nil {
			d = &dirSets{}
			edgesByCell[cell] = d
		}
		// A cross-zone grid edge is still represented by m/mb/g/gf below. XZ is
		// an additional compact boundary marker, never a topology conversion.
		if targetExists && toZone.valid && toZone.v != zone && toX.valid && toY.valid && toL.valid {
			d.crossZone = append(d.crossZone, SlimCrossZoneGrid{
				Dir: dir, Source: source, Passable: passable, ToZone: toZone.v,
				ToX: int(toX.v), ToY: int(toY.v), ToL: int(toL.v),
			})
		}
		switch {
		case source == EdgeSourceManual && passable:
			d.m = append(d.m, dir)
		case source == EdgeSourceManual:
			d.mb = append(d.mb, dir)
		case !toZone.valid:
			// A derived row with no target is a dangling exit: it connects the cell to
			// nothing, so the pane must not draw an edge for it.
		case source == EdgeSourceGrid:
			d.gf = append(d.gf, dir)
		default:
			d.g = append(d.g, dir)
		}
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}
	for _, d := range edgesByCell {
		sort.Slice(d.crossZone, func(i, j int) bool {
			return d.crossZone[i].Dir < d.crossZone[j].Dir
		})
	}

	// The ATTRIBUTE layer, in a SECOND, FILTERED query rather than five more columns
	// on the scan above: they are set on a handful of a zone's faces (43 of 972 in
	// Вингаард — all of them doors) and this fetch runs after every map write, so
	// widening the scan would charge the whole zone for the default value. The
	// partial index of migration 018 answers it; its WHERE is repeated verbatim so
	// SQLite can use it.
	// `kind = 'grid'` matters as much here as it does on the scan above, and for a
	// sharper reason: a SEAM row carries dir='' , so the day seam rows can hold
	// attributes one would land in `ea` under an empty direction and the edge editor
	// would read it as a face — silently overwriting a real one. The filter costs
	// nothing and closes that before it can happen.
	attrRows, err := s.db.Raw(`SELECT from_x, from_y, from_l, dir, weight, phrase, door, locked, open_cmd, no_route
		FROM map_edges
		WHERE map_set_id = ? AND from_zone = ? AND kind = ?
		  AND (weight <> 1 OR door = 1 OR locked = 1 OR no_route = 1 OR phrase <> '' OR open_cmd <> '')`,
		mapSetID, zone, EdgeKindGrid).Rows()
	if err != nil {
		return nil, err
	}
	defer attrRows.Close()
	for attrRows.Next() {
		var fx, fy, fl int
		var a SlimEdgeAttr
		var weight float64
		if err := attrRows.Scan(&fx, &fy, &fl, &a.Dir, &weight,
			&a.Phrase, &a.Door, &a.Locked, &a.OpenCmd, &a.NoRoute); err != nil {
			return nil, err
		}
		if weight != 1 {
			a.Weight = &weight
		}
		cell := EdgeCell{zone, fx, fy, fl}
		d := edgesByCell[cell]
		if d == nil {
			d = &dirSets{}
			edgesByCell[cell] = d
		}
		d.attrs = append(d.attrs, a)
	}
	if err := attrRows.Err(); err != nil {
		return nil, err
	}
	seamRows, err := s.db.Raw(`SELECT seam_order, source, passable, from_x, from_y, from_l,
		target_zone, target_tag, to_zone, to_x, to_y, to_l, command,
		weight, phrase, door, locked, open_cmd, no_route
		FROM map_edges WHERE map_set_id = ? AND from_zone = ? AND kind = ?
		ORDER BY from_l ASC, from_x ASC, from_y ASC, seam_order ASC, id ASC`, mapSetID, zone, EdgeKindSeam).Rows()
	if err != nil {
		return nil, err
	}
	defer seamRows.Close()
	for seamRows.Next() {
		var x, y, l int
		var a SlimSeam
		var weight float64
		var tag, toX, toY, toL intNull
		var toZone strNull
		if err := seamRows.Scan(&a.Order, &a.Source, &a.Passable, &x, &y, &l,
			&a.TargetZone, &tag, &toZone, &toX, &toY, &toL, &a.Command,
			&weight, &a.Phrase, &a.Door, &a.Locked, &a.OpenCmd, &a.NoRoute); err != nil {
			return nil, err
		}
		a.Kind = EdgeKindSeam
		if tag.valid {
			v := int(tag.v)
			a.TargetTag = &v
		}
		if toZone.valid {
			v := toZone.v
			a.ToZone = &v
		}
		if toX.valid {
			v := int(toX.v)
			a.ToX = &v
		}
		if toY.valid {
			v := int(toY.v)
			a.ToY = &v
		}
		if toL.valid {
			v := int(toL.v)
			a.ToL = &v
		}
		if weight != 1 {
			a.Weight = &weight
		}
		cell := EdgeCell{zone, x, y, l}
		d := edgesByCell[cell]
		if d == nil {
			d = &dirSets{}
			edgesByCell[cell] = d
		}
		d.seams = append(d.seams, a)
	}
	if err := seamRows.Err(); err != nil {
		return nil, err
	}

	out := make([]SlimRoom, 0, len(rooms))
	for _, r := range rooms {
		img := 0
		if imgIDs[r.ID] {
			img = 1
		}
		var m, mb, g, gf []string
		var ea []SlimEdgeAttr
		var se []SlimSeam
		var xz []SlimCrossZoneGrid
		if d := edgesByCell[(EdgeCell{r.Zone, r.X, r.Y, r.L})]; d != nil {
			m, mb, g, gf, ea, se, xz = d.m, d.mb, d.g, d.gf, d.attrs, d.seams, d.crossZone
		}
		out = append(out, SlimRoom{
			Z:   r.Zone,
			T:   r.Tag,
			H:   r.Hint,
			X:   r.X,
			Y:   r.Y,
			L:   r.L,
			E:   decodeStrArray(r.EDirs),
			D:   decodeStrArray(r.Doors),
			Ch:  r.Ch,
			P:   r.Pipe,
			I:   r.ImageIndex,
			S:   r.IsDT,
			DX:  r.DX,
			DY:  r.DY,
			DL:  r.DL,
			Img: img,
			M:   emptyIfNil(m),
			MB:  emptyIfNil(mb),
			G:   emptyIfNil(g),
			GF:  emptyIfNil(gf),
			EA:  emptyAttrsIfNil(ea),
			SE:  emptySeamsIfNil(se),
			XZ:  emptyCrossZoneIfNil(xz),
		})
	}
	return out, nil
}

func emptyCrossZoneIfNil(v []SlimCrossZoneGrid) []SlimCrossZoneGrid {
	if v == nil {
		return []SlimCrossZoneGrid{}
	}
	return v
}

func emptySeamsIfNil(v []SlimSeam) []SlimSeam {
	if v == nil {
		return []SlimSeam{}
	}
	return v
}

// emptyAttrsIfNil is emptyIfNil for the attribute list: same reason, same shape.
// The UI iterates `ea` unconditionally, and every other list on this record is `[]`
// when empty — one field marshalling as `null` is a trap for the next reader.
func emptyAttrsIfNil(v []SlimEdgeAttr) []SlimEdgeAttr {
	if v == nil {
		return []SlimEdgeAttr{}
	}
	return v
}

// emptyIfNil keeps the slim wire form's list fields as `[]` rather than `null`,
// matching every other list on this record (the UI indexes them unconditionally).
func emptyIfNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func decodeStrArray(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// ListRooms returns every full Room of a map set, ordered by coordinate. Used to
// build the tracker's in-memory index (rooms + fingerprints) for the active set.
// This is called off the hot path (on connect / active-set change / import), not
// per incoming line.
func (s *Store) ListRooms(mapSetID int64) ([]Room, error) {
	var rooms []Room
	err := s.db.Where("map_set_id = ?", mapSetID).
		Order("zone ASC, l ASC, x ASC, y ASC").
		Find(&rooms).Error
	if rooms == nil {
		rooms = []Room{}
	}
	return rooms, err
}

// chBitOrder maps a canonical direction letter to its Ch bitmask bit
// (ChN..ChD => 0..5), mirroring mapper.chBitOrder / mapimport.chBit. Duplicated
// here (tiny, stable) so the storage layer need not import mapper.
var chBitOrder = map[string]int{"N": 0, "S": 1, "E": 2, "W": 3, "U": 4, "D": 5}

// PatchRoomExits updates ONE room of a map set in place: it adds addExits and
// removes removeExits from the room's exit directions (edirs), the ch
// connectivity bitmask, and the display Exits string. Directions are canonical
// upper-case letters ("N".."D"); unknown tokens are ignored. Door markers in the
// display string are preserved for surviving directions. Returns
// (found=false, nil) when no room matches — the caller soft-fails rather than
// 500s. The write is a single UPDATE inside the same store.
//
// This is the in-place "update cell from live state" patch: no fork/undo is
// involved; it mutates the imported set directly so a mis-mapped or
// newly discovered exit becomes known to the tracker on the next reconcile.
func (s *Store) PatchRoomExits(mapSetID int64, zone string, x, y, l int, addExits, removeExits []string) (bool, error) {
	var err error
	zone, err = NormalizeZoneName(zone)
	if err != nil {
		return false, err
	}
	var room Room
	err = s.db.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		mapSetID, zone, x, y, l).First(&room).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	updates := computeExitUpdates(room, addExits, removeExits)
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Room{}).Where("id = ?", room.ID).Updates(updates).Error; err != nil {
			return err
		}
		return recomputeEdgesTx(tx, mapSetID, []EdgeCell{{zone, x, y, l}}, false)
	}); err != nil {
		return false, err
	}
	return true, nil
}

// --- generalized topology apply arms ---------------------------------------
//
// Each arm runs in ONE transaction, captures the affected cells' full before-state
// (TopologyBefore: the whole prior room per cell — or Existed=false when the cell
// was empty — PLUS the hand-drawn edges of the neighbourhood), applies the
// mutation, and returns that before-state for the undo journal. A single
// RestoreTopologyBefore reverses all of them, so create/delete/link/unlink, the
// exit patch and the hidden-exit mark share ONE undo mechanism.

// snapshotRoom captures a cell's before-state within tx: the whole room when it
// exists, or an Existed=false marker carrying just the coord when it does not.
func snapshotRoom(tx *gorm.DB, mapSetID int64, zone string, x, y, l int) (RoomSnapshot, error) {
	var room Room
	err := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		mapSetID, zone, x, y, l).First(&room).Error
	if err == gorm.ErrRecordNotFound {
		return RoomSnapshot{Existed: false, Room: Room{MapSetID: mapSetID, Zone: zone, X: x, Y: y, L: l}}, nil
	}
	if err != nil {
		return RoomSnapshot{}, err
	}
	return RoomSnapshot{Existed: true, Room: room}, nil
}

// patchExitsWithSnapshot is PatchRoomExits plus a full-room BEFORE-STATE capture
// for the undo journal. In ONE transaction it snapshots the target
// room, applies the same idempotent add/remove patch, and returns the snapshot.
// Capturing the literal prior room — rather than a symmetric add<->remove inverse
// — is what makes undo correct even when the patch is a no-op: adding an exit the
// room already has (or removing an absent one) snapshots the unchanged state, so
// undo restores it exactly instead of deleting a pre-existing exit or fabricating
// a phantom one. Returns (before, found=true) on success, (_, found=false) when
// the room is absent (no snapshot).
func (s *Store) patchExitsWithSnapshot(mapSetID int64, zone string, x, y, l int, addExits, removeExits []string) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !snap.Existed {
			return nil // found stays false — no room to patch
		}
		found = true
		before.Rooms = []RoomSnapshot{snap}
		cells := []EdgeCell{{zone, x, y, l}}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}
		updates := computeExitUpdates(snap.Room, addExits, removeExits)
		if err := tx.Model(&Room{}).Where("id = ?", snap.Room.ID).Updates(updates).Error; err != nil {
			return err
		}
		return recomputeEdgesTx(tx, mapSetID, cells, false)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// upsertRoomWithSnapshot creates a room at the cell, or partially updates the
// existing one (only fields the caller provided change; omitted preserved on
// update / defaulted on create). Exit fields are normalized consistently: a given
// display Exits string OR canonical edirs is turned into edirs/ch/exits together
// (Exits wins if both). The fingerprint is recomputed from the resolved
// hint/desc/exits (reusing mapimport.Fingerprint) so auto-resync stays consistent.
// Always found=true (a create is a valid outcome). Snapshots the one cell.
func (s *Store) upsertRoomWithSnapshot(mapSetID int64, zone string, x, y, l int, f RoomFields) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		before.Rooms = []RoomSnapshot{snap}
		found = true
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, []EdgeCell{{zone, x, y, l}}); serr != nil {
			return serr
		}

		// Start from the existing room (update) or a fresh default room at the coord
		// (create), then apply only the provided fields. On create, JSON-array columns
		// default to "[]" (not "") so readers match imported rows and need not tolerate
		// the empty string.
		room := snap.Room
		if !snap.Existed {
			room = Room{MapSetID: mapSetID, Zone: zone, X: x, Y: y, L: l,
				EDirs: "[]", Doors: "[]"}
		}

		if f.Hint != nil {
			room.Hint = *f.Hint
		}
		if f.Desc != nil {
			room.Desc = *f.Desc
		}
		if f.IsDT != nil {
			room.IsDT = *f.IsDT
		}
		if f.Pipe != nil {
			room.Pipe = *f.Pipe
		}
		if f.ImageIndex != nil {
			v := *f.ImageIndex
			room.ImageIndex = &v
		}
		if f.Note != nil {
			room.Note = *f.Note
		}
		if f.DX != nil {
			room.DX = *f.DX
		}
		if f.DY != nil {
			room.DY = *f.DY
		}
		if f.DL != nil {
			room.DL = *f.DL
		}

		// Exit normalization — priority exits > edirs > ch:
		//   - exits (display string): parsed into dir letters AND a door set, so a door
		//     marker like "(S)" records the door (fingerprint stays door-insensitive —
		//     it re-strips markers).
		//   - edirs (canonical letters): dir set only; doors preserved from the room.
		//   - ch (bitmask): reverse the chBitOrder mapping to dir letters so a lone ch
		//     actually takes effect; doors preserved from the room.
		// Whichever is provided rewrites edirs/ch/exits together; ch is always DERIVED
		// from the resolved edirs so the mask never drifts. If none is provided the
		// existing exit fields are preserved.
		var exitDirs []string
		haveExits := false
		doorSet := map[string]bool{}
		for _, d := range decodeStrArray(room.Doors) {
			doorSet[d] = true
		}
		switch {
		case f.Exits != nil:
			var doors []string
			exitDirs, doors = mapimport.NormExits(*f.Exits)
			// The display string is the authority for doors when exits are given.
			doorSet = map[string]bool{}
			for _, d := range doors {
				doorSet[d] = true
			}
			haveExits = true
		case f.EDirsSet():
			exitDirs = normalizeDirLetters(f.EDirs())
			haveExits = true
		case f.Ch != nil:
			exitDirs = dirsFromChMask(*f.Ch)
			haveExits = true
		}
		if haveExits {
			ex := buildExitFields(exitDirs, doorSet)
			room.EDirs = ex.edirs
			room.Ch = ex.ch
			room.Exits = ex.exits
			doorsJSON := doorsArray(exitDirs, doorSet)
			room.Doors = doorsJSON
		}

		// Recompute the fingerprint from the resolved hint/desc/exits (same normalizer
		// as import) so a hint/desc/exits change keeps auto-resync consistent.
		room.Fingerprint = mapimport.Fingerprint(room.Hint, room.Desc, room.Exits)

		if snap.Existed {
			if err := tx.Model(&Room{}).Where("id = ?", room.ID).Save(&room).Error; err != nil {
				return err
			}
		} else {
			room.ID = 0
			if err := tx.Create(&room).Error; err != nil {
				return err
			}
		}
		// Re-derive the WHOLE neighbourhood, not just this cell: the grid fallback
		// is a property of a PAIR (it holds only while BOTH ends are exit-less), so
		// writing exits here can delete edges that were never this room's own.
		// RoomFields carries no tag, so seam resolution cannot have moved.
		return recomputeEdgesTx(tx, mapSetID, []EdgeCell{{zone, x, y, l}}, false)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// linkWithSnapshot adds (add=true) or removes (add=false) exit dir on the target
// cell, and mirrors the reverse opposite(dir) on the grid neighbor room when one
// exists. Both touched cells are snapshotted (the neighbor only when a room is
// there). The target room must exist (found=false otherwise); a missing neighbor
// is fine (one-sided edge on add; nothing to remove on unlink). dir is a canonical
// letter; an unknown dir is a no-op that still snapshots (undo restores the
// unchanged state).
func (s *Store) linkWithSnapshot(mapSetID int64, zone string, x, y, l int, dir string, add bool) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cell, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !cell.Existed {
			return nil // found stays false — cannot link from a nonexistent room
		}
		found = true
		before.Rooms = []RoomSnapshot{cell}

		cells := []EdgeCell{{zone, x, y, l}}
		// The hand-drawn snapshot covers BOTH faces of the pair up front (whether or
		// not the neighbour turns out to hold a room), so capture and restore agree on
		// the same neighbourhood no matter which branch below is taken.
		manualCells := cells
		if d, ok := dirDelta[dir]; ok {
			manualCells = append([]EdgeCell{}, cells...)
			manualCells = append(manualCells, EdgeCell{zone, x + d.dx, y + d.dy, l + d.dl})
		}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, manualCells); serr != nil {
			return serr
		}

		// Patch this cell's exit.
		if err := applyExitPatch(tx, cell.Room, dir, add); err != nil {
			return err
		}

		// Mirror the reverse on the grid neighbor, if a room is there.
		d, ok := dirDelta[dir]
		if !ok {
			return recomputeEdgesTx(tx, mapSetID, cells, false)
		}
		nb, nerr := snapshotRoom(tx, mapSetID, zone, x+d.dx, y+d.dy, l+d.dl)
		if nerr != nil {
			return nerr
		}
		if !nb.Existed {
			return recomputeEdgesTx(tx, mapSetID, cells, false) // one-sided edge — valid
		}
		before.Rooms = append(before.Rooms, nb)
		if err := applyExitPatch(tx, nb.Room, oppositeDir[dir], add); err != nil {
			return err
		}
		// Both endpoints were rewritten, so both neighbourhoods are re-derived.
		cells = append(cells, EdgeCell{zone, x + d.dx, y + d.dy, l + d.dl})
		return recomputeEdgesTx(tx, mapSetID, cells, false)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// roomRecordsDoor reports whether a room's rooms.doors names the given face. It is
// the same membership test the edge derivation runs (storage.edgeRoom.hasDoor); it
// takes a whole Room because the manual-edge path already holds one from its
// snapshot and decoding the column twice would be the only alternative.
func roomRecordsDoor(r Room, dir string) bool {
	for _, d := range DecodeJSONStrArray(r.Doors) {
		if d == dir {
			return true
		}
	}
	return false
}

// manualMark is what a manual-edge op leaves on a face.
type manualMark int

const (
	// manualMarkPassable — a hidden exit: "there IS a way here".
	manualMarkPassable manualMark = iota
	// manualMarkBlocked — a negation: "there is NO way here". Same row, passable=0.
	manualMarkBlocked
	// manualMarkCleared — no hand-drawn row at all; the face goes back to the
	// derivation.
	manualMarkCleared
)

// manualEdgeWithSnapshot writes the HAND-DRAWN row on the face leaving the target
// cell in dir — plus the face back, unless oneWay. It is the storage half of both
// the hidden-exit mark and its negation. Zone moves are the only other producer
// of source='manual' grid rows: they temporarily use the same face-ownership
// mechanism, distinguished by MapEdge.ZoneGuard.
//
// It touches NO room field. That is the entire difference from linkWithSnapshot,
// and the reason the mark survives «обновить комнату»: a room write re-derives the
// cell's edges from its parsed exits, and re-derivation never creates or destroys
// a manual row and skips the face one occupies (dropFacesTakenByManual).
//
// The three marks are one operation because they are one row in three states, and
// they must be mutually exclusive on a face: a face carries at most one row (the
// partial UNIQUE index), so "block" is not "add a second row that cancels the
// first" — it replaces whatever is there.
//
// Scope of THIS operation (deliberately narrow; the schema is wider):
//   - dir must be one of the six canonical letters;
//   - BOTH cells must hold a room. An edge into empty space is not walkable (the
//     BFS skips a target with no room) and could not be re-marked or cleaned up
//     later, so it soft-fails (found=false) instead of being written. The same
//     bound applies to a negation: denying a face of a cell that does not exist
//     denies nothing;
//   - jump links to a non-adjacent room (schema: kind + symbolic target) are not
//     produced here.
//
// Idempotent in every mode: re-marking a face rewrites the same row, clearing a
// face with no row is a no-op. All modes still snapshot, so undo restores the
// literal prior state either way.
func (s *Store) manualEdgeWithSnapshot(mapSetID int64, zone string, x, y, l int, dir string, mark manualMark, oneWay bool) (before TopologyBefore, found bool, err error) {
	d, ok := dirDelta[dir]
	if !ok {
		return TopologyBefore{}, false, nil
	}
	from := EdgeCell{zone, x, y, l}
	to := EdgeCell{zone, x + d.dx, y + d.dy, l + d.dl}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		fromSnap, serr := snapshotRoom(tx, mapSetID, from.Zone, from.X, from.Y, from.L)
		if serr != nil {
			return serr
		}
		toSnap, serr := snapshotRoom(tx, mapSetID, to.Zone, to.X, to.Y, to.L)
		if serr != nil {
			return serr
		}
		if !fromSnap.Existed || !toSnap.Existed {
			return nil // found stays false — both ends must exist
		}
		found = true
		// The room snapshots are unchanged by this op; they are captured anyway so the
		// journal entry names the cell (PrimaryCell / the undo report) and so undo runs
		// the SAME restore path as every other op.
		before.Rooms = []RoomSnapshot{fromSnap, toSnap}
		cells := []EdgeCell{from, to}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}

		// `room` travels with the face so the row can inherit the door the DERIVATION
		// would have put there (see the seed below).
		type manualFace struct {
			cell EdgeCell
			dir  string
			to   EdgeCell
			room Room
		}
		faces := []manualFace{{from, dir, to, fromSnap.Room}}
		if !oneWay {
			faces = append(faces, manualFace{to, oppositeDir[dir], from, toSnap.Room})
		}
		for _, f := range faces {
			// What the face says RIGHT NOW, before we clear it. A mark changes whether
			// there is a way through; it says nothing about how that way is taken, so
			// everything already recorded about the passage has to survive — see the
			// EdgeAttrs assignment below.
			var cur MapEdge
			hadRow := true
			ferr := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND dir = ?",
				mapSetID, EdgeKindGrid, f.cell.Zone, f.cell.X, f.cell.Y, f.cell.L, f.dir).First(&cur).Error
			if ferr == gorm.ErrRecordNotFound {
				hadRow = false
			} else if ferr != nil {
				return ferr
			}
			// One row per face (the partial UNIQUE index), so clear the face first —
			// whatever is on it: a derived row (which the mark REPLACES: the face now
			// belongs to the hand-drawn layer) or a stale manual row (idempotent re-mark,
			// or a hidden exit being turned into a negation and back).
			if err := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND dir = ?",
				mapSetID, EdgeKindGrid, f.cell.Zone, f.cell.X, f.cell.Y, f.cell.L, f.dir).
				Delete(&MapEdge{}).Error; err != nil {
				return err
			}
			if mark == manualMarkCleared {
				// "auto" gives the face back to the derivation, and the attributes go with
				// it: four of the five are things only a person can assert, and the face is
				// no longer a person's to speak for. This is the ONE mark that drops them,
				// and it is the documented way to drop them.
				continue
			}
			// A MARK MUST NOT TAKE BACK WORDS THE OPERATOR ALREADY SAID. `open`/`blocked`
			// answer "is there a way here"; the traversal phrase, the door, its lock and
			// its opening command answer "how is it taken", and the routing veto answers
			// "may the router use it". Rewriting those to the zero value would be bad
			// enough on the face the caller named — but marks are PAIRED by default, so it
			// also silently emptied the REVERSE face, which the caller never mentioned.
			// That is exactly the "putting words in the operator's mouth" the one-sided
			// attribute write exists to avoid, running in reverse. It also made an
			// idempotent re-mark destructive, while the tool's own description invites
			// passing `mark` and attributes together.
			//
			// With no row on the face there is nothing to carry over, so seed the door the
			// DERIVATION would have put there (rooms.doors): a manual row owns its face and
			// the derivation skips it, so without the seed, drawing a hidden exit across a
			// door deleted that door from the graph and a route that used to open it walked
			// into it shut.
			attrs := EdgeAttrs{Weight: 1, Door: roomRecordsDoor(f.room, f.dir)}
			if hadRow {
				attrs = cur.EdgeAttrs
			}
			row := MapEdge{
				MapSetID: mapSetID, Kind: EdgeKindGrid, Source: EdgeSourceManual,
				Passable: mark == manualMarkPassable,
				FromZone: f.cell.Zone, FromX: f.cell.X, FromY: f.cell.Y, FromL: f.cell.L,
				Dir:       f.dir,
				EdgeAttrs: attrs,
			}
			// A negation keeps its target too: it names WHICH neighbour is denied, which
			// is what the pane draws and what makes the row re-findable by query.
			target := f.to
			row.SetTo(&target)
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		// Re-derive the neighbourhood LAST, with the manual rows already in place: on a
		// mark (either kind) it re-creates nothing on the taken faces, and on a clear it
		// gives the freed face back to the derivation — so clearing a face the rooms DO
		// describe restores the parsed row rather than deleting the passage, and that is
		// exactly how a negation is taken back.
		return recomputeEdgesTx(tx, mapSetID, cells, false)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// setEdgeAttrsWithSnapshot writes the ATTRIBUTES of ONE directed face: routing
// weight, traversal phrase, door/lock/opening command, and routing veto.
//
// EXACTLY ONE FACE, ALWAYS. Unlike a hidden exit — a passage a player found, which
// is normally walkable both ways and is therefore marked as a pair by default —
// every attribute here is a statement about the side you arrive from. "Двигать
// ящик" takes you down; it does not bring you back up. A door locked on this side
// is not locked on the other. Writing the pair would put words in the operator's
// mouth about a face they did not look at.
//
// IT MAKES THE FACE HAND-DRAWN. Most attributes are derivable from nothing, so a
// derived row cannot carry them: the next room write re-derives the
// face from the room's exits and the assertion is gone. Promoting the face to
// source='manual' — keeping the passability and the target it already had — is what
// makes it survive, exactly as it does for a hidden exit or a negation. `door` is
// the exception (the derivation sets it from rooms.doors), which is why marking a
// door that is already there is a no-op below rather than a promotion.
//
// Refusals, both soft (found=false):
//   - a direction that is not a canonical letter;
//   - NO EDGE ON THE FACE. Attributes describe how to take a passage; there is no
//     passage here to take. The way to create one is TopoAddManualEdge, and doing it
//     implicitly here would let a typo'd direction quietly punch a hole in a wall.
//
// A patch that changes nothing still snapshots and reports found=true — the same
// contract every other op has, so undo restores the literal prior state.
func (s *Store) setEdgeAttrsWithSnapshot(mapSetID int64, zone string, x, y, l int, dir string, patch EdgeAttrPatch) (before TopologyBefore, found bool, err error) {
	d, ok := dirDelta[dir]
	if !ok {
		return TopologyBefore{}, false, nil
	}
	from := EdgeCell{zone, x, y, l}
	to := EdgeCell{zone, x + d.dx, y + d.dy, l + d.dl}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		fromSnap, serr := snapshotRoom(tx, mapSetID, from.Zone, from.X, from.Y, from.L)
		if serr != nil {
			return serr
		}
		if !fromSnap.Existed {
			return nil // found stays false — no cell to hang the face on
		}
		var cur MapEdge
		ferr := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND dir = ?",
			mapSetID, EdgeKindGrid, from.Zone, from.X, from.Y, from.L, dir).First(&cur).Error
		if ferr == gorm.ErrRecordNotFound {
			return nil // found stays false — no passage on this face to describe
		}
		if ferr != nil {
			return ferr
		}
		found = true

		// Snapshot the same neighbourhood a manual-edge write does, so undo restores
		// the hand-drawn layer around both cells whichever op touched it.
		toSnap, serr := snapshotRoom(tx, mapSetID, to.Zone, to.X, to.Y, to.L)
		if serr != nil {
			return serr
		}
		before.Rooms = []RoomSnapshot{fromSnap}
		cells := []EdgeCell{from}
		if toSnap.Existed {
			before.Rooms = append(before.Rooms, toSnap)
			cells = append(cells, to)
		}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}

		next, changed := patch.Apply(cur.EdgeAttrs)
		if !changed {
			// A WRITE THAT CHANGES NOTHING CHANGES NOTHING — including provenance.
			// Promoting a derived face here would freeze it against the derivation for
			// good (a manual row owns its face, so rooms.doors would stop reaching the
			// graph and «обновить комнату» could never put it right), and the ways to
			// trigger it are entirely ordinary: an agent re-asserting a door it just
			// read back, or a checkbox ticked and unticked.
			//
			// The earlier form of this guard tested `!changed && !next.Any()`, which let
			// exactly that case through: a face that already carries a door (every
			// parsed door face) has Any()==true, so re-asserting the door fell into the
			// promotion below.
			return nil
		}
		row := cur
		row.ID = 0
		row.Source = EdgeSourceManual
		// Changing an attribute is an explicit user assertion, even if this face
		// was previously protected only by a zone move.
		row.ZoneGuard = false
		row.EdgeAttrs = next
		if err := tx.Delete(&MapEdge{}, cur.ID).Error; err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		// Re-derive the neighbourhood with the manual row already owning its face, the
		// same ordering manualEdgeWithSnapshot relies on.
		return recomputeEdgesTx(tx, mapSetID, cells, false)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// setSeamAttrsWithSnapshot promotes one uniquely identified parsed seam to the
// manual ownership layer. The selector is semantic, so fork/rederive can retain
// it; an exact duplicate is refused rather than selecting an arbitrary row.
func (s *Store) setSeamAttrsWithSnapshot(mapSetID int64, zone string, x, y, l int, sel SeamSelector, patch EdgeAttrPatch) (before TopologyBefore, found bool, err error) {
	from := EdgeCell{zone, x, y, l}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !snap.Existed {
			return nil
		}
		query := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND target_zone = ? AND target_tag = ? AND command = ?",
			mapSetID, EdgeKindSeam, zone, x, y, l, sel.TargetZone, *sel.TargetTag, sel.Command)
		if sel.Order != nil {
			query = query.Where("seam_order = ?", *sel.Order)
		}
		var rows []MapEdge
		if err := query.Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 {
			return nil
		}
		cur := rows[0]
		found = true
		before.Rooms = []RoomSnapshot{snap}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, []EdgeCell{from}); serr != nil {
			return serr
		}
		next, changed := patch.Apply(cur.EdgeAttrs)
		if !changed {
			return nil
		}
		// Keep the materialized id and occurrence order so this write does not churn
		// the authoritative seam list.
		if err := tx.Model(&MapEdge{}).Where("id = ?", cur.ID).Updates(map[string]any{
			"source": EdgeSourceManual, "weight": next.Weight, "phrase": next.Phrase,
			"door": next.Door, "locked": next.Locked, "open_cmd": next.OpenCmd, "no_route": next.NoRoute,
		}).Error; err != nil {
			return err
		}
		// Promotion changes only this row. Re-deriving the source would churn every
		// parsed sibling seam and change their materialization order for no benefit.
		return nil
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

const maxPortalCommandBytes = 4096

func validatePortalCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("portal command is required")
	}
	if len(command) > maxPortalCommandBytes {
		return "", fmt.Errorf("portal command exceeds %d bytes", maxPortalCommandBytes)
	}
	for _, r := range command {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("portal command contains a control character")
		}
	}
	return command, nil
}

// portalSnapshots captures both exact endpoints.  The target snapshot matters
// even though a portal does not mutate rooms: deleting that target later must be
// undoable without reviving a stale manual portal into a newly-created room.
func portalSnapshots(tx *gorm.DB, mapSetID int64, from, to EdgeCell) ([]RoomSnapshot, bool, error) {
	fromSnap, err := snapshotRoom(tx, mapSetID, from.Zone, from.X, from.Y, from.L)
	if err != nil || !fromSnap.Existed {
		return nil, false, err
	}
	toSnap, err := snapshotRoom(tx, mapSetID, to.Zone, to.X, to.Y, to.L)
	if err != nil || !toSnap.Existed {
		return nil, false, err
	}
	if from == to { // Explicit policy: directed self-portals are valid.
		return []RoomSnapshot{fromSnap}, true, nil
	}
	return []RoomSnapshot{fromSnap, toSnap}, true, nil
}

func nextPortalOrderTx(tx *gorm.DB, mapSetID int64, from EdgeCell) (int, error) {
	var max sql.NullInt64
	err := tx.Raw(`SELECT MAX(seam_order) FROM map_edges
		WHERE map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?`,
		mapSetID, EdgeKindSeam, from.Zone, from.X, from.Y, from.L).Scan(&max).Error
	if err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	if max.Int64 >= int64(^uint(0)>>1) {
		return 0, fmt.Errorf("portal occurrence order exhausted")
	}
	return int(max.Int64) + 1, nil
}

// createPortalWithSnapshot writes a manual seam whose endpoint is concrete. The
// empty legacy target_tag is intentional: reresolveSeamsTx therefore cannot turn
// this portal back into a zone/tag lookup during room maintenance.
func portalWriteResult(edge MapEdge, deleted bool) PortalWriteResult {
	result := PortalWriteResult{
		Selector: PortalSelector{Order: edge.SeamOrder},
		Source:   PortalTarget{Zone: edge.FromZone, X: edge.FromX, Y: edge.FromY, L: edge.FromL},
		Command:  edge.Command,
		Attrs:    edge.EdgeAttrs,
		Deleted:  deleted,
	}
	if to, ok := edge.To(); ok {
		result.Target = &PortalTarget{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L}
	}
	return result
}

func (s *Store) createPortalWithSnapshot(mapSetID int64, from, to EdgeCell, command string, attrs EdgeAttrPatch, result *PortalWriteResult) (before TopologyBefore, found bool, err error) {
	if strings.TrimSpace(from.Zone) == "" || strings.TrimSpace(to.Zone) == "" {
		return TopologyBefore{}, false, fmt.Errorf("portal source and target zones are required")
	}
	command, err = validatePortalCommand(command)
	if err != nil {
		return TopologyBefore{}, false, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snaps, ok, serr := portalSnapshots(tx, mapSetID, from, to)
		if serr != nil {
			return serr
		}
		if !ok {
			return nil
		}
		order, serr := nextPortalOrderTx(tx, mapSetID, from)
		if serr != nil {
			return serr
		}
		before.Rooms = snaps
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, []EdgeCell{from, to}); serr != nil {
			return serr
		}
		base := EdgeAttrs{Weight: 1}
		next, _ := attrs.Apply(base)
		e := MapEdge{MapSetID: mapSetID, Kind: EdgeKindSeam, Source: EdgeSourceManual, Passable: true,
			FromZone: from.Zone, FromX: from.X, FromY: from.Y, FromL: from.L,
			Command: command, TargetZone: to.Zone, SeamOrder: order, EdgeAttrs: next}
		e.SetTo(&to)
		if serr := insertEdges(tx, []MapEdge{e}); serr != nil {
			return serr
		}
		if result != nil {
			*result = portalWriteResult(e, false)
		}
		found = true
		return nil
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

func portalAtOrderTx(tx *gorm.DB, mapSetID int64, from EdgeCell, sel PortalSelector) (*MapEdge, error) {
	var edge MapEdge
	err := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ? AND seam_order = ?",
		mapSetID, EdgeKindSeam, from.Zone, from.X, from.Y, from.L, sel.Order).First(&edge).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &edge, nil
}

func (s *Store) updatePortalWithSnapshot(mapSetID int64, from EdgeCell, sel PortalSelector, patch PortalPatch, result *PortalWriteResult) (before TopologyBefore, found bool, err error) {
	if patch.Target == nil && patch.Command == nil && patch.Attrs.Empty() {
		return TopologyBefore{}, false, fmt.Errorf("portal update is empty")
	}
	if patch.Target != nil && strings.TrimSpace(patch.Target.Zone) == "" {
		return TopologyBefore{}, false, fmt.Errorf("portal target zone is required")
	}
	var command *string
	if patch.Command != nil {
		v, verr := validatePortalCommand(*patch.Command)
		if verr != nil {
			return TopologyBefore{}, false, verr
		}
		command = &v
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cur, serr := portalAtOrderTx(tx, mapSetID, from, sel)
		if serr != nil || cur == nil {
			return serr
		}
		oldTarget, hasOldTarget := cur.To()
		// A parsed dead seam has no exact coordinate to preserve. It cannot become
		// an exact portal until the caller supplies one explicitly.
		if patch.Target == nil && !hasOldTarget {
			return nil
		}
		newTarget := oldTarget
		if patch.Target != nil {
			newTarget = patch.Target.Cell()
		}
		// Source must still exist. An old dead imported seam has no endpoint, so
		// only validate the new target in that case.
		fromSnap, serr := snapshotRoom(tx, mapSetID, from.Zone, from.X, from.Y, from.L)
		if serr != nil || !fromSnap.Existed {
			return serr
		}
		before.Rooms = []RoomSnapshot{fromSnap}
		if patch.Target != nil {
			toSnap, serr := snapshotRoom(tx, mapSetID, newTarget.Zone, newTarget.X, newTarget.Y, newTarget.L)
			if serr != nil || !toSnap.Existed {
				return serr
			}
			if from != newTarget {
				before.Rooms = append(before.Rooms, toSnap)
			}
		}
		cells := []EdgeCell{from}
		if patch.Target != nil {
			cells = append(cells, newTarget)
		}
		if hasOldTarget && patch.Target != nil && oldTarget != newTarget {
			cells = append(cells, oldTarget)
		}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}
		nextAttrs, attrChanged := patch.Attrs.Apply(cur.EdgeAttrs)
		nextCommand := cur.Command
		if command != nil {
			nextCommand = *command
		}
		changed := cur.Source != EdgeSourceManual || cur.TargetTag != nil || nextCommand != cur.Command || attrChanged || (patch.Target != nil && (!hasOldTarget || oldTarget != newTarget))
		if !changed {
			if result != nil {
				*result = portalWriteResult(*cur, false)
			}
			found = true
			return nil
		}
		updates := map[string]any{"source": EdgeSourceManual, "command": nextCommand,
			"weight": nextAttrs.EffectiveWeight(), "phrase": nextAttrs.Phrase, "door": nextAttrs.Door,
			"locked": nextAttrs.Locked, "open_cmd": nextAttrs.OpenCmd, "no_route": nextAttrs.NoRoute}
		// Promotion freezes the currently resolved coordinate as well as an explicit
		// retarget. No portal produced by this operation retains symbolic tag identity.
		if patch.Target != nil || cur.TargetTag != nil {
			updates["to_zone"], updates["to_x"], updates["to_y"], updates["to_l"] = newTarget.Zone, newTarget.X, newTarget.Y, newTarget.L
			updates["target_zone"], updates["target_tag"] = newTarget.Zone, nil
		}
		if serr := tx.Model(&MapEdge{}).Where("id = ?", cur.ID).Updates(updates).Error; serr != nil {
			return serr
		}
		after := *cur
		after.Source, after.Command, after.EdgeAttrs = EdgeSourceManual, nextCommand, nextAttrs
		if patch.Target != nil || cur.TargetTag != nil {
			after.TargetZone, after.TargetTag = newTarget.Zone, nil
			after.SetTo(&newTarget)
		}
		if result != nil {
			*result = portalWriteResult(after, false)
		}
		found = true
		return nil
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

func (s *Store) deletePortalWithSnapshot(mapSetID int64, from EdgeCell, sel PortalSelector, result *PortalWriteResult) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cur, serr := portalAtOrderTx(tx, mapSetID, from, sel)
		if serr != nil || cur == nil {
			return serr
		}
		fromSnap, serr := snapshotRoom(tx, mapSetID, from.Zone, from.X, from.Y, from.L)
		if serr != nil || !fromSnap.Existed {
			return serr
		}
		before.Rooms = []RoomSnapshot{fromSnap}
		cells := []EdgeCell{from}
		if to, ok := cur.To(); ok {
			cells = append(cells, to)
		}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}
		if serr := tx.Delete(&MapEdge{}, cur.ID).Error; serr != nil {
			return serr
		}
		if result != nil {
			*result = portalWriteResult(*cur, true)
		}
		found = true
		return nil
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// deleteRoomWithSnapshot deletes the room at the cell, snapshotting the WHOLE
// prior room so undo recreates it exactly (exits/ch/fingerprint/flags intact).
// found=false when no room is there. The cell's room_annotations / room_images are
// intentionally LEFT in place (dangling), consistent with the annotation overlay
// model (annotations live on frozen sets and not-yet-mapped cells and are keyed by
// logical coord, so a delete simply leaves them as any other dangling annotation;
// re-creating the room via undo/upsert re-associates them). This is the simplest,
// most consistent choice — no cascade.
func (s *Store) deleteRoomWithSnapshot(mapSetID int64, zone string, x, y, l int) (before TopologyBefore, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !snap.Existed {
			return nil // found stays false — nothing to delete
		}
		found = true
		before.Rooms = []RoomSnapshot{snap}
		cells := []EdgeCell{{zone, x, y, l}}
		if before.Manual, serr = captureManualEdgesTx(tx, mapSetID, cells); serr != nil {
			return serr
		}
		if err := tx.Delete(&Room{}, snap.Room.ID).Error; err != nil {
			return err
		}
		// The HAND-DRAWN rows touching this cell go with it — including the ones a
		// SURVIVING neighbour owns that point INTO the cell. Unlike annotations (left
		// dangling by design), a manual edge is an assertion about two existing cells:
		// left behind it is a passage into nothing that no derivation would ever clean
		// up and that a later, unrelated room at the same coord would inherit. Undo
		// brings them back from before.Manual, which is the whole reason the journal
		// carries them.
		if _, derr := deleteManualEdgesIncidentTx(tx, mapSetID, cells); derr != nil {
			return derr
		}
		if err := tx.Where("map_set_id = ? AND kind = ? AND from_zone = ? AND from_x = ? AND from_y = ? AND from_l = ?",
			mapSetID, EdgeKindSeam, zone, x, y, l).Delete(&MapEdge{}).Error; err != nil {
			return err
		}
		// Deleting a room drops its edges and can turn a neighbour's exit into a
		// dangling one; deleting a TAGGED room can also move a seam's target (a
		// seam points at "the first room of zone Z with tag T"), so re-resolve.
		return recomputeEdgesTx(tx, mapSetID, cells, snap.Room.Tag != nil)
	})
	if err != nil {
		return TopologyBefore{}, false, err
	}
	return before, found, nil
}

// applyExitPatch adds or removes a single canonical direction on room (within tx),
// recomputing edirs/ch/exits (door markers preserved for surviving dirs). Reuses
// computeExitUpdates so link/unlink normalize identically to the exit patch.
func applyExitPatch(tx *gorm.DB, room Room, dir string, add bool) error {
	var addD, remD []string
	if add {
		addD = []string{dir}
	} else {
		remD = []string{dir}
	}
	updates := computeExitUpdates(room, addD, remD)
	return tx.Model(&Room{}).Where("id = ?", room.ID).Updates(updates).Error
}

// RestoreTopologyBefore reverses an op by restoring the exact prior state the
// write recorded (the generalized undo) — BOTH halves of the topology:
//
//   - the rooms, per cell: Existed=true → upsert the room back to its exact prior
//     fields (preserving the surviving row's id, or re-creating it if a later write
//     deleted it); Existed=false → delete any room now at that cell (undo of a
//     create);
//   - the HAND-DRAWN edges of the neighbourhood, made to be exactly what the
//     snapshot holds. This half cannot be skipped the way the derived rows can:
//     those come back from the re-derivation below, a manual row from nothing.
//
// Returns applied=true when at least one snapshot was restored, so undo can
// soft-report if every target cell vanished.
func (s *Store) RestoreTopologyBefore(mapSetID int64, before TopologyBefore) (applied bool, err error) {
	return s.restoreTopologyBefore(mapSetID, before, nil)
}

// RestoreTopologyBeforeAtRevision is the journal-facing undo primitive. It
// refuses to apply a snapshot if any topology write committed after the entry
// reached the top of the stack.
func (s *Store) RestoreTopologyBeforeAtRevision(mapSetID int64, before TopologyBefore, expectedRevision int64) (applied bool, err error) {
	return s.restoreTopologyBefore(mapSetID, before, &expectedRevision)
}

func (s *Store) restoreTopologyBefore(mapSetID int64, before TopologyBefore, expectedRevision *int64) (applied bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		current, rerr := topologyRevisionTx(tx, mapSetID)
		if rerr != nil {
			return rerr
		}
		if expectedRevision != nil && current != *expectedRevision {
			return fmt.Errorf("stale topology revision: expected %d, current %d", *expectedRevision, current)
		}
		inner := &Store{db: tx}
		applied, rerr = inner.restoreTopologyBeforeUnfenced(mapSetID, before)
		if rerr != nil || !applied {
			return rerr
		}
		return advanceTopologyRevisionTx(tx, mapSetID, current)
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *Store) restoreTopologyBeforeUnfenced(mapSetID int64, before TopologyBefore) (applied bool, err error) {
	if before.ZoneMove != nil {
		return s.restoreZoneMoveBeforeUnfenced(mapSetID, *before.ZoneMove)
	}
	snaps := before.Rooms
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cells := make([]EdgeCell, 0, len(snaps))
		tagsChanged := false
		for _, snap := range snaps {
			r := snap.Room
			cells = append(cells, EdgeCell{r.Zone, r.X, r.Y, r.L})
			var existing Room
			ferr := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
				mapSetID, r.Zone, r.X, r.Y, r.L).First(&existing).Error
			found := ferr == nil
			if ferr != nil && ferr != gorm.ErrRecordNotFound {
				return ferr
			}
			// A tagged room appearing or disappearing can move a seam's target.
			if (snap.Existed && r.Tag != nil) || (found && existing.Tag != nil) {
				tagsChanged = true
			}

			if !snap.Existed {
				// The cell had no room before the op — delete whatever it created.
				if found {
					if derr := tx.Delete(&Room{}, existing.ID).Error; derr != nil {
						return derr
					}
					applied = true
				}
				continue
			}

			// The cell had a room — write its exact prior fields back.
			restore := r
			restore.MapSetID = mapSetID
			if found {
				restore.ID = existing.ID
				if uerr := tx.Model(&Room{}).Where("id = ?", existing.ID).Save(&restore).Error; uerr != nil {
					return uerr
				}
			} else {
				// A later write deleted it — re-create the room from the snapshot.
				restore.ID = 0
				if cerr := tx.Create(&restore).Error; cerr != nil {
					return cerr
				}
			}
			applied = true
		}
		// The hand-drawn layer is restored even when NO room snapshot applied: the
		// mark ops change no room at all, so "nothing to restore among the rooms" says
		// nothing about whether there is markup to put back.
		if len(before.Manual.Cells) > 0 {
			changed, merr := restoreManualEdgesTx(tx, mapSetID, before.Manual)
			if merr != nil {
				return merr
			}
			if changed {
				applied = true
			}
			cells = append(cells, before.Manual.Cells...)
		}
		if !applied {
			return nil
		}
		// Restoring the rooms restores the DERIVED topology: every derived edge is
		// produced from them by one rule, so re-deriving the restored neighbourhoods
		// reproduces exactly the derived rows that existed before the undone write —
		// including the fallback edges around a cell that write had made explicit.
		// It runs AFTER the manual restore so the freshly restored manual rows own
		// their faces here (dropFacesTakenByManual), instead of colliding with a
		// derived row on the partial UNIQUE index and rolling the undo back.
		return recomputeEdgesTx(tx, mapSetID, cells, tagsChanged)
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// computeExitUpdates builds the edirs/ch/exits update map for a room after adding
// addExits and removing removeExits (canonical letters), preserving door markers
// for surviving directions. Shared by PatchRoomExits, patchExitsWithSnapshot, and
// the link/unlink single-dir patch (applyExitPatch).
func computeExitUpdates(room Room, addExits, removeExits []string) map[string]any {
	present := map[string]bool{}
	for _, d := range decodeStrArray(room.EDirs) {
		present[d] = true
	}
	for _, d := range removeExits {
		delete(present, d)
	}
	for _, d := range addExits {
		if _, ok := chBitOrder[d]; ok {
			present[d] = true
		}
	}
	dirs := make([]string, 0, len(present))
	for d := range present {
		dirs = append(dirs, d)
	}
	doorSet := map[string]bool{}
	for _, d := range decodeStrArray(room.Doors) {
		doorSet[d] = true
	}
	ex := buildExitFields(dirs, doorSet)
	return map[string]any{
		"edirs": ex.edirs,
		"ch":    ex.ch,
		"exits": ex.exits,
	}
}

// exitFields is the canonical (edirs JSON, ch mask, display exits string) triple
// derived from a set of exit directions plus the door directions to mark.
type exitFields struct {
	edirs string // JSON array text
	ch    int    // connectivity bitmask
	exits string // display string with door markers
}

// buildExitFields renders the canonical edirs/ch/exits triple from a set of
// direction letters (order-independent input; output ordered N,S,E,W,U,D). Door
// letters in doorSet get parenthesized markers in the display string. Unknown
// tokens are ignored. The single source of exit normalization for every write.
func buildExitFields(dirs []string, doorSet map[string]bool) exitFields {
	present := map[string]bool{}
	for _, d := range dirs {
		if _, ok := chBitOrder[d]; ok {
			present[d] = true
		}
	}
	order := []string{"N", "S", "E", "W", "U", "D"}
	edirs := make([]string, 0, len(present))
	mask := 0
	displayParts := make([]string, 0, len(present))
	for _, d := range order {
		if !present[d] {
			continue
		}
		edirs = append(edirs, d)
		mask |= 1 << chBitOrder[d]
		if doorSet[d] {
			displayParts = append(displayParts, "("+d+")")
		} else {
			displayParts = append(displayParts, d)
		}
	}
	edirsJSON, _ := json.Marshal(edirs)
	return exitFields{edirs: string(edirsJSON), ch: mask, exits: joinFields(displayParts)}
}

// normalizeDirLetters upper-cases and filters a caller-provided edirs list down to
// known canonical directions (drops unknown tokens; case-insensitive input).
func normalizeDirLetters(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		u := strings.ToUpper(strings.TrimSpace(d))
		if _, ok := chBitOrder[u]; ok {
			out = append(out, u)
		}
	}
	return out
}

// dirsFromChMask reverses chBitOrder: a connectivity bitmask → the canonical
// direction letters whose bit is set (ordered N,S,E,W,U,D). Lets a lone `ch`
// upsert field derive edirs so it actually takes effect.
func dirsFromChMask(mask int) []string {
	order := []string{"N", "S", "E", "W", "U", "D"}
	out := make([]string, 0, len(order))
	for _, d := range order {
		if mask&(1<<chBitOrder[d]) != 0 {
			out = append(out, d)
		}
	}
	return out
}

// doorsArray renders the JSON doors array for the surviving exit directions:
// door markers only apply to a direction that is actually an exit (ordered
// N,S,E,W,U,D). Mirrors buildExitFields' notion of "present" so Doors and the
// display exits string agree.
func doorsArray(dirs []string, doorSet map[string]bool) string {
	present := map[string]bool{}
	for _, d := range dirs {
		if _, ok := chBitOrder[d]; ok {
			present[d] = true
		}
	}
	order := []string{"N", "S", "E", "W", "U", "D"}
	doors := make([]string, 0, len(present))
	for _, d := range order {
		if present[d] && doorSet[d] {
			doors = append(doors, d)
		}
	}
	b, _ := json.Marshal(doors)
	return string(b)
}

// gridDelta / dirDelta / oppositeDir mirror mapper.dirDelta / mapper.oppositeDir.
// Duplicated here (tiny, stable) so the storage layer need not import mapper —
// which imports storage, so a storage→mapper edge would cycle. Same pattern as
// chBitOrder above. Axis convention (mapper/dirs.go): N=x-1, S=x+1, W=y-1, E=y+1,
// U=l+1, D=l-1.
type gridDelta struct{ dx, dy, dl int }

var dirDelta = map[string]gridDelta{
	"N": {-1, 0, 0},
	"S": {1, 0, 0},
	"W": {0, -1, 0},
	"E": {0, 1, 0},
	"U": {0, 0, 1},
	"D": {0, 0, -1},
}

var oppositeDir = map[string]string{
	"N": "S", "S": "N", "E": "W", "W": "E", "U": "D", "D": "U",
}

// joinFields joins exit tokens with a single space (matches the .mm2 Exits form).
func joinFields(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// RoomExistsInSet reports whether a room with the given logical coordinate
// exists in the map set. Used by the annotation write to soft-note a dangling
// annotation (one on a not-yet-mapped cell) without rejecting it.
func (s *Store) RoomExistsInSet(mapSetID int64, zone string, x, y, l int) (bool, error) {
	var err error
	zone, err = NormalizeZoneName(zone)
	if err != nil {
		return false, err
	}
	var count int64
	err = s.db.Model(&Room{}).
		Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?", mapSetID, zone, x, y, l).
		Count(&count).Error
	return count > 0, err
}

// GetActiveMapSetID returns a session's active_map_set_id, or (0,false) when
// unset/NULL.
func (s *Store) GetActiveMapSetID(sessionID int64) (int64, bool, error) {
	var val sql.NullInt64
	row := s.db.Model(&SessionRecord{}).
		Where("id = ?", sessionID).
		Select("active_map_set_id").Row()
	if err := row.Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !val.Valid {
		return 0, false, nil
	}
	return val.Int64, true, nil
}

// SetActiveMapSetID sets (or clears, when id <= 0) a session's active map set.
// Only the one column is written so concurrent status/connection updates are not
// clobbered.
func (s *Store) SetActiveMapSetID(sessionID int64, id int64) error {
	if id > 0 {
		result := s.db.Exec(`
			UPDATE sessions
			SET active_map_set_id = ?
			WHERE id = ? AND EXISTS (SELECT 1 FROM map_sets WHERE id = ?)
		`, id, sessionID, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return s.classifyActiveMapSetAssignmentMiss(sessionID, id)
		}
		return nil
	}
	result := s.db.Model(&SessionRecord{}).
		Where("id = ?", sessionID).
		Update("active_map_set_id", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *Store) classifyActiveMapSetAssignmentMiss(sessionID, mapSetID int64) error {
	var sessionCount int64
	if err := s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Count(&sessionCount).Error; err != nil {
		return err
	}
	if sessionCount == 0 {
		return ErrSessionNotFound
	}
	var mapSetCount int64
	if err := s.db.Model(&MapSet{}).Where("id = ?", mapSetID).Count(&mapSetCount).Error; err != nil {
		return err
	}
	if mapSetCount == 0 {
		return ErrMapSetNotFound
	}
	// The conditional UPDATE was the authority. If both rows still exist, a
	// concurrent delete/replace raced after it evaluated; expose this as a map
	// set miss rather than pretending the assignment happened.
	return ErrMapSetNotFound
}
