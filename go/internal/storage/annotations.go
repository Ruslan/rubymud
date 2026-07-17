package storage

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoomAnnotation is a crowdsourced/LLM overlay on ONE logical room cell of a map
// set. It is DELIBERATELY separate from the topology write-path: it is keyed by
// the logical coordinate (map_set_id, zone, x, y, l) — never by rooms.id — so it
// survives on a frozen/imported set WITHOUT forking (an annotation never touches
// edges, so no fork/undo machinery is needed). Edit-in-place by updated_at, NOT
// an append-only log.
type RoomAnnotation struct {
	ID        int64       `gorm:"primaryKey" json:"id"`
	MapSetID  int64       `gorm:"column:map_set_id" json:"map_set_id"`
	Zone      string      `json:"zone"`
	X         int         `json:"x"`
	Y         int         `json:"y"`
	L         int         `json:"l"`
	DT        bool        `gorm:"column:dt" json:"dt"`
	Hazard    string      `json:"hazard"`
	Note      string      `json:"note"`
	BattleLog string      `gorm:"column:battle_log" json:"battle_log"`
	Author    string      `json:"author"`
	UpdatedAt *SQLiteTime `gorm:"column:updated_at" json:"updated_at"`
}

func (RoomAnnotation) TableName() string { return "room_annotations" }

// AnnotationFields carries the writable fields of an annotation for a partial
// update. Every field is a pointer: nil means "leave unchanged" on an existing
// row (and "use the column default" on insert). To CLEAR a field, pass an
// explicit zero value (e.g. a pointer to "" or to false) — nil never clears.
// This is the partial-update contract for UpsertRoomAnnotation / mud_room_annotate.
type AnnotationFields struct {
	DT        *bool
	Hazard    *string
	Note      *string
	BattleLog *string
	Author    *string
}

// UpsertRoomAnnotation inserts-or-updates the annotation for one logical cell of
// a map set, keyed by (map_set_id, zone, x, y, l). It is edit-in-place: an
// existing row is patched with only the provided (non-nil) fields, and
// updated_at is always bumped. Fields left nil are preserved on an update and
// defaulted (empty/false) on an insert. Works regardless of the set's
// editable/frozen state — annotations never fork. Returns the resulting row.
func (s *Store) UpsertRoomAnnotation(mapSetID int64, zone string, x, y, l int, f AnnotationFields) (RoomAnnotation, error) {
	var out RoomAnnotation
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var existing RoomAnnotation
		err := tx.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
			mapSetID, zone, x, y, l).
			// Lock the row for the read-modify-write so a concurrent upsert on the
			// same cell can't interleave and lose a partial patch.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&existing).Error
		switch err {
		case nil:
			// Edit-in-place: patch only provided fields, always bump updated_at.
			if f.DT != nil {
				existing.DT = *f.DT
			}
			if f.Hazard != nil {
				existing.Hazard = *f.Hazard
			}
			if f.Note != nil {
				existing.Note = *f.Note
			}
			if f.BattleLog != nil {
				existing.BattleLog = *f.BattleLog
			}
			if f.Author != nil {
				existing.Author = *f.Author
			}
			existing.UpdatedAt = nowSQLiteTimePtr()
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			out = existing
			return nil
		case gorm.ErrRecordNotFound:
			row := RoomAnnotation{
				MapSetID:  mapSetID,
				Zone:      zone,
				X:         x,
				Y:         y,
				L:         l,
				UpdatedAt: nowSQLiteTimePtr(),
			}
			if f.DT != nil {
				row.DT = *f.DT
			}
			if f.Hazard != nil {
				row.Hazard = *f.Hazard
			}
			if f.Note != nil {
				row.Note = *f.Note
			}
			if f.BattleLog != nil {
				row.BattleLog = *f.BattleLog
			}
			if f.Author != nil {
				row.Author = *f.Author
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out = row
			return nil
		default:
			return err
		}
	})
	return out, err
}

// GetRoomAnnotation returns the annotation for one cell, or (row, false, nil)
// when absent. A missing annotation is not an error.
func (s *Store) GetRoomAnnotation(mapSetID int64, zone string, x, y, l int) (RoomAnnotation, bool, error) {
	var row RoomAnnotation
	err := s.db.Where("map_set_id = ? AND zone = ? AND x = ? AND y = ? AND l = ?",
		mapSetID, zone, x, y, l).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return RoomAnnotation{}, false, nil
	}
	if err != nil {
		return RoomAnnotation{}, false, err
	}
	return row, true, nil
}

// ListRoomAnnotations returns the annotations of a set, optionally filtered to
// one zone (pass "" for all zones), ordered by coordinate for stable output.
func (s *Store) ListRoomAnnotations(mapSetID int64, zone string) ([]RoomAnnotation, error) {
	q := s.db.Where("map_set_id = ?", mapSetID)
	if zone != "" {
		q = q.Where("zone = ?", zone)
	}
	var rows []RoomAnnotation
	err := q.Order("zone ASC, l ASC, x ASC, y ASC").Find(&rows).Error
	if rows == nil {
		rows = []RoomAnnotation{}
	}
	return rows, err
}
