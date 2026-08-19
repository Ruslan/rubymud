package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// RoomLayout is a sparse presentation override keyed by a room's immutable
// logical coordinate. It is never consulted by routing or tracker code.
type RoomLayout struct {
	MapSetID  int64      `gorm:"column:map_set_id;primaryKey" json:"map_set_id"`
	Zone      string     `gorm:"primaryKey" json:"zone"`
	X         int        `gorm:"primaryKey" json:"x"`
	Y         int        `gorm:"primaryKey" json:"y"`
	L         int        `gorm:"primaryKey" json:"l"`
	DisplayX  int        `gorm:"column:display_x" json:"display_x"`
	DisplayY  int        `gorm:"column:display_y" json:"display_y"`
	DisplayL  int        `gorm:"column:display_l" json:"display_l"`
	Pinned    bool       `json:"pinned"`
	UpdatedAt SQLiteTime `gorm:"column:updated_at" json:"updated_at"`
}

func (RoomLayout) TableName() string { return "room_layouts" }

func (s *Store) ListRoomLayouts(mapSetID int64, zone string) ([]RoomLayout, error) {
	var out []RoomLayout
	q := s.db.Where("map_set_id = ?", mapSetID)
	if zone != "" {
		var err error
		zone, err = NormalizeZoneName(zone)
		if err != nil {
			return nil, err
		}
		q = q.Where("zone = ?", zone)
	}
	err := q.Order("zone,l,x,y").Find(&out).Error
	if out == nil {
		out = []RoomLayout{}
	}
	return out, err
}

// UpsertRoomLayouts validates and writes a bounded preview as one transaction.
// A collision or missing logical room rolls the whole batch back.
func (s *Store) UpsertRoomLayouts(mapSetID int64, layouts []RoomLayout) error {
	if len(layouts) > 8000 {
		return fmt.Errorf("too many layout changes")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		logicalKey := func(zone string, x, y, l int) string { return fmt.Sprintf("%s\x00%d\x00%d\x00%d", zone, x, y, l) }
		var rooms []Room
		if err := tx.Where("map_set_id=?", mapSetID).Find(&rooms).Error; err != nil {
			return err
		}
		roomKeys := make(map[string]struct{}, len(rooms))
		for _, room := range rooms {
			roomKeys[logicalKey(room.Zone, room.X, room.Y, room.L)] = struct{}{}
		}
		proposed := make(map[string]RoomLayout, len(layouts))
		for i := range layouts {
			l := &layouts[i]
			var err error
			l.Zone, err = NormalizeZoneName(l.Zone)
			if err != nil {
				return err
			}
			if l.MapSetID != 0 && l.MapSetID != mapSetID {
				return fmt.Errorf("layout belongs to another map set")
			}
			l.MapSetID = mapSetID
			key := logicalKey(l.Zone, l.X, l.Y, l.L)
			if _, ok := proposed[key]; ok {
				return fmt.Errorf("duplicate logical room")
			}
			proposed[key] = *l
			if _, ok := roomKeys[key]; !ok {
				return fmt.Errorf("logical room not found")
			}
			l.UpdatedAt = *nowSQLiteTimePtr()
			proposed[key] = *l
		}
		var existing []RoomLayout
		if err := tx.Where("map_set_id=?", mapSetID).Find(&existing).Error; err != nil {
			return err
		}
		effective := make(map[string]RoomLayout, len(existing)+len(proposed))
		for _, l := range existing {
			effective[logicalKey(l.Zone, l.X, l.Y, l.L)] = l
		}
		for key, l := range proposed {
			effective[key] = l
		}
		displays := make(map[string]string, len(rooms))
		for _, room := range rooms {
			logical := logicalKey(room.Zone, room.X, room.Y, room.L)
			dx, dy, dl := room.X, room.Y, room.L
			if l, ok := effective[logical]; ok {
				dx, dy, dl = l.DisplayX, l.DisplayY, l.DisplayL
			}
			display := logicalKey(room.Zone, dx, dy, dl)
			if prior, ok := displays[display]; ok && prior != logical {
				return fmt.Errorf("display collision")
			}
			displays[display] = logical
		}
		for start := 0; start < len(layouts); start += 500 {
			end := start + 500
			if end > len(layouts) {
				end = len(layouts)
			}
			parts := make([]string, 0, end-start)
			args := make([]any, 0, 1+(end-start)*4)
			args = append(args, mapSetID)
			for _, l := range layouts[start:end] {
				parts = append(parts, "(?,?,?,?)")
				args = append(args, l.Zone, l.X, l.Y, l.L)
			}
			if err := tx.Exec("DELETE FROM room_layouts WHERE map_set_id=? AND (zone,x,y,l) IN ("+strings.Join(parts, ",")+")", args...).Error; err != nil {
				return err
			}
		}
		if len(layouts) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(layouts, 500).Error; err != nil {
			if err == gorm.ErrDuplicatedKey {
				return fmt.Errorf("display collision")
			}
			return err
		}
		return nil
	})
}
