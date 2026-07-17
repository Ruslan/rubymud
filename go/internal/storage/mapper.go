package storage

import (
	"database/sql"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"rubymud/go/internal/mapimport"
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

// --- generalized topology apply arms (plan §8, slice 3) --------------------
//
// Each arm runs in ONE transaction, captures the affected cells' full before-
// snapshots (RoomSnapshot: the whole prior room or Existed=false when the cell was
// empty), applies the mutation, and returns the snapshot list for the undo journal.
// A single RestoreRoomSnapshots reverses all of them (upsert-back / delete), so
// create/delete/link/unlink and the exit patch share ONE undo mechanism.

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
// for the undo journal (plan §8). In ONE transaction it snapshots the target
// room, applies the same idempotent add/remove patch, and returns the snapshot.
// Capturing the literal prior room — rather than a symmetric add<->remove inverse
// — is what makes undo correct even when the patch is a no-op: adding an exit the
// room already has (or removing an absent one) snapshots the unchanged state, so
// undo restores it exactly instead of deleting a pre-existing exit or fabricating
// a phantom one. Returns (before, found=true) on success, (_, found=false) when
// the room is absent (no snapshot).
func (s *Store) patchExitsWithSnapshot(mapSetID int64, zone string, x, y, l int, addExits, removeExits []string) (before []RoomSnapshot, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !snap.Existed {
			return nil // found stays false — no room to patch
		}
		found = true
		before = []RoomSnapshot{snap}
		updates := computeExitUpdates(snap.Room, addExits, removeExits)
		return tx.Model(&Room{}).Where("id = ?", snap.Room.ID).Updates(updates).Error
	})
	if err != nil {
		return nil, false, err
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
func (s *Store) upsertRoomWithSnapshot(mapSetID int64, zone string, x, y, l int, f RoomFields) (before []RoomSnapshot, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		before = []RoomSnapshot{snap}
		found = true

		// Start from the existing room (update) or a fresh default room at the coord
		// (create), then apply only the provided fields. On create, JSON-array columns
		// default to "[]" (not "") so readers match imported rows and need not tolerate
		// the empty string.
		room := snap.Room
		if !snap.Existed {
			room = Room{MapSetID: mapSetID, Zone: zone, X: x, Y: y, L: l,
				EDirs: "[]", Doors: "[]", Automaps: "[]"}
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
			return tx.Model(&Room{}).Where("id = ?", room.ID).Save(&room).Error
		}
		room.ID = 0
		return tx.Create(&room).Error
	})
	if err != nil {
		return nil, false, err
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
func (s *Store) linkWithSnapshot(mapSetID int64, zone string, x, y, l int, dir string, add bool) (before []RoomSnapshot, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cell, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !cell.Existed {
			return nil // found stays false — cannot link from a nonexistent room
		}
		found = true
		before = []RoomSnapshot{cell}

		// Patch this cell's exit.
		if err := applyExitPatch(tx, cell.Room, dir, add); err != nil {
			return err
		}

		// Mirror the reverse on the grid neighbor, if a room is there.
		d, ok := dirDelta[dir]
		if !ok {
			return nil
		}
		nb, nerr := snapshotRoom(tx, mapSetID, zone, x+d.dx, y+d.dy, l+d.dl)
		if nerr != nil {
			return nerr
		}
		if !nb.Existed {
			return nil // one-sided edge — valid
		}
		before = append(before, nb)
		return applyExitPatch(tx, nb.Room, oppositeDir[dir], add)
	})
	if err != nil {
		return nil, false, err
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
func (s *Store) deleteRoomWithSnapshot(mapSetID int64, zone string, x, y, l int) (before []RoomSnapshot, found bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		snap, serr := snapshotRoom(tx, mapSetID, zone, x, y, l)
		if serr != nil {
			return serr
		}
		if !snap.Existed {
			return nil // found stays false — nothing to delete
		}
		found = true
		before = []RoomSnapshot{snap}
		return tx.Delete(&Room{}, snap.Room.ID).Error
	})
	if err != nil {
		return nil, false, err
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

// RestoreRoomSnapshots reverses an op by restoring each cell to its exact prior
// snapshot (the generalized undo, plan §8). For each snapshot: Existed=true →
// upsert the room back to its exact prior fields (preserving the surviving row's
// id, or re-creating it if a later write deleted it); Existed=false → delete any
// room now at that cell (undo of a create). Returns applied=true when at least one
// snapshot was restored, so undo can soft-report if every target cell vanished.
func (s *Store) RestoreRoomSnapshots(mapSetID int64, snaps []RoomSnapshot) (applied bool, err error) {
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for _, snap := range snaps {
			r := snap.Room
			var existing Room
			ferr := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
				mapSetID, r.Zone, r.X, r.Y, r.L).First(&existing).Error
			found := ferr == nil
			if ferr != nil && ferr != gorm.ErrRecordNotFound {
				return ferr
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
		return nil
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
