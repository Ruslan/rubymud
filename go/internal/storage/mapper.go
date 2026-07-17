package storage

import (
	"database/sql"
	"encoding/json"

	"gorm.io/gorm"
)

// MapSet is one imported archive of maps (a global entity a session references).
//
// Editable/ForkedFromID model the write-mode fork (plan §8): an imported set is
// the user's frozen source of truth (Editable=false); the first topology write
// forks it copy-on-write into an editable copy (Editable=true, ForkedFromID = the
// source set's id) and writes land there.
type MapSet struct {
	ID            int64       `gorm:"primaryKey" json:"id"`
	Name          string      `json:"name"`
	SourceArchive string      `json:"source_archive"`
	ImportedAt    *SQLiteTime `json:"imported_at"`
	ZoneCount     int         `json:"zone_count"`
	RoomCount     int         `json:"room_count"`
	SeamCount     int         `json:"seam_count"`
	Note          string      `json:"note"`
	Editable      bool        `gorm:"column:editable" json:"editable"`
	ForkedFromID  *int64      `gorm:"column:forked_from_id" json:"forked_from_id"`
}

func (MapSet) TableName() string { return "map_sets" }

// Room is a persisted map room. edirs/doors/automaps are JSON-encoded text
// columns; the JSON tags below expose the decoded forms via the Room* helpers,
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
	Automaps    string  `json:"automaps"` // JSON array text
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
}

// CreateMapSet inserts one map_sets row and all its rooms in a single
// transaction. Returns the new map set id. Room.MapSetID is set by this call.
func (s *Store) CreateMapSet(in MapSetInput) (int64, error) {
	var id int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
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
		return nil
	})
	return id, err
}

// ForkMapSet forks a source map set copy-on-write into a fresh EDITABLE copy
// (plan §8 "writable vs imported набор"). It creates a new map_sets row
// (editable=1, forked_from_id=srcID, name "<orig> (editable)", copied counts) and
// copies EVERY child of the source into the new set in ONE transaction:
//   - all rooms          (rooms.map_set_id -> newID)
//   - all room_annotations (room_annotations.map_set_id -> newID; the overlay is
//     keyed by logical coord, not rooms.id, so it copies by re-keying the set id)
//   - all room_images     (re-pointed to the copied rooms by logical coord, so the
//     image travels with its room even though rooms.id changes on copy)
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
			Name:          src.Name + " (editable)",
			SourceArchive: src.SourceArchive,
			ImportedAt:    nowSQLiteTimePtr(),
			ZoneCount:     src.ZoneCount,
			RoomCount:     src.RoomCount,
			SeamCount:     src.SeamCount,
			Note:          src.Note,
			Editable:      true,
			ForkedFromID:  &srcID,
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

// ListMapSets returns all map sets, newest first.
func (s *Store) ListMapSets() ([]MapSet, error) {
	var sets []MapSet
	err := s.db.Order("id DESC").Find(&sets).Error
	return sets, err
}

// GetMapSet returns one map set by id.
func (s *Store) GetMapSet(id int64) (MapSet, error) {
	var set MapSet
	err := s.db.First(&set, id).Error
	return set, err
}

// DeleteMapSet removes a map set. Child rooms/images/annotations are removed
// explicitly (FK ON DELETE CASCADE is declared, but we do not rely on it being
// enforced at runtime for every driver path). Sessions pointing at the set are
// reset to NULL so they degrade gracefully.
func (s *Store) DeleteMapSet(id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE sessions SET active_map_set_id = NULL WHERE active_map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM room_images WHERE room_id IN (SELECT id FROM rooms WHERE map_set_id = ?)", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM rooms WHERE map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM room_annotations WHERE map_set_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&MapSet{}, id).Error; err != nil {
			return err
		}
		return nil
	})
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

// SlimRoom is the wire form for map rendering / MCP: compact keys per §3 of the
// plan. img is 1 when a room_images row exists for the room, else 0.
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
	A   []string `json:"a"`  // automaps seams
	P   bool     `json:"p"`  // pipe
	I   *int     `json:"i"`  // imageindex
	S   bool     `json:"s"`  // is_dt
	DX  int      `json:"dx"`
	DY  int      `json:"dy"`
	DL  int      `json:"dl"`
	Img int      `json:"img"` // 0|1 image present
}

// ListSlimRooms returns the slim rooms for one zone of a set, ordered by grid
// coordinate for stable rendering. img reflects whether a room_images row exists.
func (s *Store) ListSlimRooms(mapSetID int64, zone string) ([]SlimRoom, error) {
	var rooms []Room
	err := s.db.Where("map_set_id = ? AND zone = ?", mapSetID, zone).
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

	out := make([]SlimRoom, 0, len(rooms))
	for _, r := range rooms {
		img := 0
		if imgIDs[r.ID] {
			img = 1
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
			A:   decodeStrArray(r.Automaps),
			P:   r.Pipe,
			I:   r.ImageIndex,
			S:   r.IsDT,
			DX:  r.DX,
			DY:  r.DY,
			DL:  r.DL,
			Img: img,
		})
	}
	return out, nil
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
// This is the phase-4 "update cell from live state" in-place patch: no fork/undo
// (that is phase 5); it mutates the imported set directly so a mis-mapped or
// newly discovered exit becomes known to the tracker on the next reconcile.
func (s *Store) PatchRoomExits(mapSetID int64, zone string, x, y, l int, addExits, removeExits []string) (bool, error) {
	var room Room
	err := s.db.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		mapSetID, zone, x, y, l).First(&room).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	updates := computeExitUpdates(room, addExits, removeExits)
	if err := s.db.Model(&Room{}).Where("id = ?", room.ID).Updates(updates).Error; err != nil {
		return false, err
	}
	return true, nil
}

// PatchRoomExitsWithSnapshot is PatchRoomExits plus a BEFORE-STATE capture for
// the undo journal (plan §8). In ONE transaction it reads the target room's exact
// prior exit state (edirs/doors/ch/exits, verbatim), applies the same idempotent
// add/remove patch, and returns the snapshot. Capturing the literal prior state —
// rather than a symmetric add<->remove inverse — is what makes undo correct even
// when the patch is a no-op: adding an exit the room already has (or removing an
// absent one) snapshots the unchanged state, so undo restores it exactly instead
// of deleting a pre-existing exit or fabricating a phantom one. Returns
// (before, found=true) on success, (_, found=false) when the room is absent.
func (s *Store) PatchRoomExitsWithSnapshot(mapSetID int64, zone string, x, y, l int, addExits, removeExits []string) (before RoomExitState, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var room Room
		rerr := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
			mapSetID, zone, x, y, l).First(&room).Error
		if rerr == gorm.ErrRecordNotFound {
			return nil // found stays false
		}
		if rerr != nil {
			return rerr
		}
		// Capture the exact prior state BEFORE mutating.
		before = RoomExitState{
			Zone: zone, X: x, Y: y, L: l, Exists: true,
			EDirs: room.EDirs, Doors: room.Doors, Ch: room.Ch, Exits: room.Exits,
		}
		found = true
		updates := computeExitUpdates(room, addExits, removeExits)
		return tx.Model(&Room{}).Where("id = ?", room.ID).Updates(updates).Error
	})
	if err != nil {
		return RoomExitState{}, false, err
	}
	return before, found, nil
}

// RestoreRoomExitState writes a room cell's exit fields back to an exact prior
// snapshot (undo of a TopoPatchExits). It restores edirs/doors/ch/exits verbatim.
// Returns found=false (no error) when the target room no longer exists (e.g. it
// was deleted by a later write) so undo soft-reports rather than failing. Only
// Exists=true snapshots are restorable here (TopoPatchExits always snapshots an
// existing room; create/delete restore is slice-3 work).
func (s *Store) RestoreRoomExitState(mapSetID int64, before RoomExitState) (found bool, err error) {
	if !before.Exists {
		return false, nil
	}
	var room Room
	rerr := s.db.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		mapSetID, before.Zone, before.X, before.Y, before.L).First(&room).Error
	if rerr == gorm.ErrRecordNotFound {
		return false, nil
	}
	if rerr != nil {
		return false, rerr
	}
	updates := map[string]any{
		"edirs": before.EDirs,
		"doors": before.Doors,
		"ch":    before.Ch,
		"exits": before.Exits,
	}
	if err := s.db.Model(&Room{}).Where("id = ?", room.ID).Updates(updates).Error; err != nil {
		return false, err
	}
	return true, nil
}

// computeExitUpdates builds the edirs/ch/exits update map for a room after adding
// addExits and removing removeExits (canonical letters), preserving door markers
// for surviving directions. Shared by PatchRoomExits and PatchRoomExitsWithSnapshot.
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
	doorSet := map[string]bool{}
	for _, d := range decodeStrArray(room.Doors) {
		doorSet[d] = true
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
	return map[string]any{
		"edirs": string(edirsJSON),
		"ch":    mask,
		"exits": joinFields(displayParts),
	}
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
	var count int64
	err := s.db.Model(&Room{}).
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
	var val any
	if id > 0 {
		val = id
	} else {
		val = nil
	}
	return s.db.Model(&SessionRecord{}).
		Where("id = ?", sessionID).
		Update("active_map_set_id", val).Error
}
