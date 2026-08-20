package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

const (
	InhabitantKindNPC     = "npc"
	InhabitantKindMonster = "monster"
	InhabitantKindOther   = "other"

	// Match the established room-annotation transport cap. Storage enforces it
	// too so REST, tests and future MCP callers cannot bypass the bound.
	InhabitantTextLimit        = 2048
	InhabitantQueryLimit       = 256
	InhabitantListDefaultLimit = 20
	InhabitantListMaxLimit     = 100
	inhabitantNameLimit        = 256
)

var (
	ErrInvalidInhabitant       = errors.New("invalid inhabitant")
	ErrInhabitantNotFound      = errors.New("inhabitant not found")
	ErrDuplicateRoomInhabitant = errors.New("inhabitant is already placed in this room")
)

// MapInhabitant is one normalized person/creature in a map set. Room-specific
// presence and residency live in RoomInhabitant, not here.
type MapInhabitant struct {
	ID         int64       `gorm:"primaryKey" json:"id"`
	MapSetID   int64       `gorm:"column:map_set_id" json:"map_set_id"`
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	LookTarget string      `gorm:"column:look_target" json:"look_target"`
	LookText   string      `gorm:"column:look_text" json:"look_text"`
	Note       string      `json:"note"`
	Strategy   string      `json:"strategy"`
	UpdatedAt  *SQLiteTime `gorm:"column:updated_at" json:"updated_at"`
}

func (MapInhabitant) TableName() string { return "map_inhabitants" }

// RoomInhabitant binds a normalized entity to one logical room coordinate. It
// intentionally has no relation to rooms.id: dangling knowledge is valid.
type RoomInhabitant struct {
	ID           int64       `gorm:"primaryKey" json:"placement_id"`
	MapSetID     int64       `gorm:"column:map_set_id" json:"map_set_id"`
	Zone         string      `json:"zone"`
	X            int         `json:"x"`
	Y            int         `json:"y"`
	L            int         `json:"l"`
	InhabitantID int64       `gorm:"column:inhabitant_id" json:"inhabitant_id"`
	PresenceText string      `gorm:"column:presence_text" json:"presence_text"`
	Resident     bool        `json:"resident"`
	UpdatedAt    *SQLiteTime `gorm:"column:updated_at" json:"updated_at"`
}

func (RoomInhabitant) TableName() string { return "room_inhabitants" }

type InhabitantFields struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	LookTarget string `json:"look_target"`
	LookText   string `json:"look_text"`
	Note       string `json:"note"`
	Strategy   string `json:"strategy"`
}

type RoomInhabitantFields struct {
	InhabitantFields
	PresenceText string `json:"presence_text"`
	Resident     bool   `json:"resident"`
}

// RoomInhabitantPatch distinguishes omitted fields from explicit empty strings
// and false. PatchRoomInhabitant applies only non-nil fields atomically.
type RoomInhabitantPatch struct {
	Name         *string
	Kind         *string
	LookTarget   *string
	LookText     *string
	Note         *string
	Strategy     *string
	PresenceText *string
	Resident     *bool
}

// RoomInhabitantView is the lazy room-read wire model: entity fields and the
// selected room's placement fields are flattened for a compact UI payload.
type RoomInhabitantView struct {
	ID                 int64       `json:"id"`
	PlacementID        int64       `json:"placement_id"`
	MapSetID           int64       `json:"map_set_id"`
	Zone               string      `json:"zone"`
	X                  int         `json:"x"`
	Y                  int         `json:"y"`
	L                  int         `json:"l"`
	Name               string      `json:"name"`
	Kind               string      `json:"kind"`
	LookTarget         string      `json:"look_target"`
	LookText           string      `json:"look_text"`
	Note               string      `json:"note"`
	Strategy           string      `json:"strategy"`
	PresenceText       string      `json:"presence_text"`
	Resident           bool        `json:"resident"`
	UpdatedAt          *SQLiteTime `json:"updated_at"`
	PlacementUpdatedAt *SQLiteTime `json:"placement_updated_at"`
}

// MapInhabitantSummary is the bounded catalogue wire model. Deliberately omit
// look_text and note: callers use GetMapInhabitant for the potentially large
// detailed knowledge body.
type MapInhabitantSummary struct {
	ID             int64  `json:"id"`
	MapSetID       int64  `json:"map_set_id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	LookTarget     string `json:"look_target"`
	Strategy       string `json:"strategy"`
	PlacementCount int    `json:"placement_count"`
}

// RoomInhabitantSummary is the compact exact-room catalogue projection used by
// bounded list callers. It includes the selected placement fields without the
// entity's potentially large look_text or note bodies.
type RoomInhabitantSummary struct {
	MapInhabitantSummary
	PresenceText string `json:"presence_text"`
	Resident     bool   `json:"resident"`
}

type InhabitantListOptions struct {
	Query       string
	Zone        string
	Cell        *EdgeCell
	ExcludeCell *EdgeCell
	Limit       int
}

func normalizeInhabitantName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return value, fmt.Errorf("%w: name is required", ErrInvalidInhabitant)
	}
	if utf8.RuneCountInString(value) > inhabitantNameLimit {
		return value, fmt.Errorf("%w: name exceeds %d characters", ErrInvalidInhabitant, inhabitantNameLimit)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return value, fmt.Errorf("%w: name contains a control character", ErrInvalidInhabitant)
		}
	}
	return value, nil
}

func normalizeInhabitantKind(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case InhabitantKindNPC, InhabitantKindMonster, InhabitantKindOther:
	default:
		return value, fmt.Errorf("%w: kind must be npc, monster, or other", ErrInvalidInhabitant)
	}
	return value, nil
}

func normalizeInhabitantText(field, value string, trim bool) (string, error) {
	if trim {
		value = strings.TrimSpace(value)
	}
	if utf8.RuneCountInString(value) > InhabitantTextLimit {
		return value, fmt.Errorf("%w: %s exceeds %d characters", ErrInvalidInhabitant, field, InhabitantTextLimit)
	}
	return value, nil
}

func normalizeInhabitantFields(fields InhabitantFields) (InhabitantFields, error) {
	var err error
	if fields.Name, err = normalizeInhabitantName(fields.Name); err != nil {
		return fields, err
	}
	if fields.Kind, err = normalizeInhabitantKind(fields.Kind); err != nil {
		return fields, err
	}
	if fields.LookTarget, err = normalizeInhabitantText("look_target", fields.LookTarget, true); err != nil {
		return fields, err
	}
	for field, value := range map[string]*string{
		"look_text": &fields.LookText,
		"note":      &fields.Note,
		"strategy":  &fields.Strategy,
	} {
		if *value, err = normalizeInhabitantText(field, *value, false); err != nil {
			return fields, err
		}
	}
	return fields, nil
}

func normalizePresenceText(value string) (string, error) {
	if utf8.RuneCountInString(value) > InhabitantTextLimit {
		return value, fmt.Errorf("%w: presence_text exceeds %d characters", ErrInvalidInhabitant, InhabitantTextLimit)
	}
	return value, nil
}

func normalizeInhabitantCell(cell EdgeCell) (EdgeCell, error) {
	zone, err := NormalizeZoneName(cell.Zone)
	if err != nil {
		return cell, fmt.Errorf("%w: %v", ErrInvalidInhabitant, err)
	}
	cell.Zone = zone
	return cell, nil
}

func mapSetExistsTx(tx *gorm.DB, mapSetID int64) error {
	if mapSetID <= 0 {
		return ErrMapSetNotFound
	}
	var count int64
	if err := tx.Model(&MapSet{}).Where("id = ?", mapSetID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrMapSetNotFound
	}
	return nil
}

func createMapInhabitantTx(tx *gorm.DB, mapSetID int64, fields InhabitantFields) (MapInhabitant, error) {
	fields, err := normalizeInhabitantFields(fields)
	if err != nil {
		return MapInhabitant{}, err
	}
	if err := mapSetExistsTx(tx, mapSetID); err != nil {
		return MapInhabitant{}, err
	}
	row := MapInhabitant{
		MapSetID: mapSetID, Name: fields.Name, Kind: fields.Kind,
		LookTarget: fields.LookTarget, LookText: fields.LookText,
		Note: fields.Note, Strategy: fields.Strategy, UpdatedAt: nowSQLiteTimePtr(),
	}
	if err := tx.Create(&row).Error; err != nil {
		return MapInhabitant{}, err
	}
	return row, nil
}

func (s *Store) CreateMapInhabitant(mapSetID int64, fields InhabitantFields) (MapInhabitant, error) {
	var out MapInhabitant
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = createMapInhabitantTx(tx, mapSetID, fields)
		return err
	})
	return out, err
}

func (s *Store) GetMapInhabitant(mapSetID, inhabitantID int64) (MapInhabitant, error) {
	var row MapInhabitant
	err := s.db.Where("map_set_id = ? AND id = ?", mapSetID, inhabitantID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MapInhabitant{}, ErrInhabitantNotFound
	}
	return row, err
}

// ListMapInhabitants returns unique catalogue entities in stable name/id order.
// It always applies a hard entity limit and reports whether another row exists.
// Zone/Cell filters mean "has at least one placement here"; ExcludeCell is used
// by the UI placement picker to hide entities already attached to the room.
func (s *Store) ListMapInhabitants(mapSetID int64, options InhabitantListOptions) ([]MapInhabitantSummary, bool, error) {
	if err := mapSetExistsTx(s.db, mapSetID); err != nil {
		return nil, false, err
	}
	if utf8.RuneCountInString(options.Query) > InhabitantQueryLimit {
		return nil, false, fmt.Errorf("%w: query exceeds %d characters", ErrInvalidInhabitant, InhabitantQueryLimit)
	}
	limit := options.Limit
	if limit <= 0 {
		limit = InhabitantListDefaultLimit
	}
	if limit > InhabitantListMaxLimit {
		limit = InhabitantListMaxLimit
	}
	query := s.db.Table("map_inhabitants AS mi").
		Select(`mi.id, mi.map_set_id, mi.name, mi.kind, mi.look_target, mi.strategy,
			COUNT(ri.id) AS placement_count`).
		Joins("LEFT JOIN room_inhabitants AS ri ON ri.map_set_id=mi.map_set_id AND ri.inhabitant_id=mi.id").
		Where("mi.map_set_id=?", mapSetID)
	searchNeedle := strings.TrimSpace(options.Query)
	unicodeNeedle := ""
	if needle := searchNeedle; needle != "" && isASCII(needle) {
		needle = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(needle))
		like := "%" + needle + "%"
		query = query.Where(`(LOWER(mi.name) LIKE ? ESCAPE '\' OR LOWER(mi.look_target) LIKE ? ESCAPE '\')`, like, like)
	} else if needle != "" {
		// SQLite's built-in LOWER/NOCASE only handles ASCII. Keep the common
		// ASCII search in SQL, but stream non-ASCII candidates through Go's
		// Unicode case folder so Cyrillic and other scripts work correctly.
		unicodeNeedle = foldInhabitantSearch(needle)
	}
	if options.Cell != nil {
		cell, err := normalizeInhabitantCell(*options.Cell)
		if err != nil {
			return nil, false, err
		}
		query = query.Where(`EXISTS (SELECT 1 FROM room_inhabitants f
			WHERE f.map_set_id=mi.map_set_id AND f.inhabitant_id=mi.id
			AND f.zone=? AND f.x=? AND f.y=? AND f.l=?)`, cell.Zone, cell.X, cell.Y, cell.L)
	} else if strings.TrimSpace(options.Zone) != "" {
		zone, err := NormalizeZoneName(options.Zone)
		if err != nil {
			return nil, false, err
		}
		query = query.Where(`EXISTS (SELECT 1 FROM room_inhabitants f
			WHERE f.map_set_id=mi.map_set_id AND f.inhabitant_id=mi.id AND f.zone=?)`, zone)
	}
	if options.ExcludeCell != nil {
		cell, err := normalizeInhabitantCell(*options.ExcludeCell)
		if err != nil {
			return nil, false, err
		}
		query = query.Where(`NOT EXISTS (SELECT 1 FROM room_inhabitants e
			WHERE e.map_set_id=mi.map_set_id AND e.inhabitant_id=mi.id
			AND e.zone=? AND e.x=? AND e.y=? AND e.l=?)`, cell.Zone, cell.X, cell.Y, cell.L)
	}
	ordered := query.Group("mi.id, mi.map_set_id, mi.name, mi.kind, mi.look_target, mi.strategy").
		Order("mi.name COLLATE NOCASE ASC, mi.id ASC")
	var rows []MapInhabitantSummary
	var err error
	if unicodeNeedle == "" {
		err = ordered.Limit(limit + 1).Scan(&rows).Error
	} else {
		var sqlRows *sql.Rows
		sqlRows, err = ordered.Rows()
		if err == nil {
			defer sqlRows.Close()
			for sqlRows.Next() {
				var row MapInhabitantSummary
				if err = s.db.ScanRows(sqlRows, &row); err != nil {
					break
				}
				if !strings.Contains(foldInhabitantSearch(row.Name), unicodeNeedle) &&
					!strings.Contains(foldInhabitantSearch(row.LookTarget), unicodeNeedle) {
					continue
				}
				rows = append(rows, row)
				if len(rows) > limit {
					break
				}
			}
			if err == nil {
				err = sqlRows.Err()
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []MapInhabitantSummary{}
	}
	return rows, truncated, nil
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func foldInhabitantSearch(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

func updateMapInhabitantTx(tx *gorm.DB, mapSetID, inhabitantID int64, fields InhabitantFields) (MapInhabitant, bool, error) {
	fields, err := normalizeInhabitantFields(fields)
	if err != nil {
		return MapInhabitant{}, false, err
	}
	var row MapInhabitant
	err = tx.Where("map_set_id = ? AND id = ?", mapSetID, inhabitantID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MapInhabitant{}, false, nil
	}
	if err != nil {
		return MapInhabitant{}, false, err
	}
	row.Name = fields.Name
	row.Kind = fields.Kind
	row.LookTarget = fields.LookTarget
	row.LookText = fields.LookText
	row.Note = fields.Note
	row.Strategy = fields.Strategy
	row.UpdatedAt = nowSQLiteTimePtr()
	if err := tx.Save(&row).Error; err != nil {
		return MapInhabitant{}, false, err
	}
	return row, true, nil
}

func (s *Store) UpdateMapInhabitant(mapSetID, inhabitantID int64, fields InhabitantFields) (MapInhabitant, bool, error) {
	lock := s.inhabitantPatchLock(mapSetID, inhabitantID)
	lock.Lock()
	defer lock.Unlock()
	var out MapInhabitant
	found := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, found, err = updateMapInhabitantTx(tx, mapSetID, inhabitantID, fields)
		return err
	})
	return out, found, err
}

func (s *Store) DeleteMapInhabitant(mapSetID, inhabitantID int64) (bool, error) {
	deleted := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&MapInhabitant{}).
			Where("map_set_id=? AND id=?", mapSetID, inhabitantID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		if err := tx.Where("map_set_id=? AND inhabitant_id=?", mapSetID, inhabitantID).
			Delete(&RoomInhabitant{}).Error; err != nil {
			return err
		}
		result := tx.Where("map_set_id=? AND id=?", mapSetID, inhabitantID).Delete(&MapInhabitant{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected > 0
		return nil
	})
	return deleted, err
}

func placeRoomInhabitantTx(tx *gorm.DB, mapSetID int64, cell EdgeCell, inhabitantID int64, presenceText string, resident bool) (RoomInhabitant, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return RoomInhabitant{}, err
	}
	presenceText, err = normalizePresenceText(presenceText)
	if err != nil {
		return RoomInhabitant{}, err
	}
	var entityCount int64
	if err := tx.Model(&MapInhabitant{}).
		Where("map_set_id = ? AND id = ?", mapSetID, inhabitantID).Count(&entityCount).Error; err != nil {
		return RoomInhabitant{}, err
	}
	if entityCount == 0 {
		return RoomInhabitant{}, ErrInhabitantNotFound
	}
	var existing int64
	if err := tx.Model(&RoomInhabitant{}).
		Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=? AND inhabitant_id=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L, inhabitantID).
		Count(&existing).Error; err != nil {
		return RoomInhabitant{}, err
	}
	if existing > 0 {
		return RoomInhabitant{}, ErrDuplicateRoomInhabitant
	}
	row := RoomInhabitant{
		MapSetID: mapSetID, Zone: cell.Zone, X: cell.X, Y: cell.Y, L: cell.L,
		InhabitantID: inhabitantID, PresenceText: presenceText, Resident: resident,
		UpdatedAt: nowSQLiteTimePtr(),
	}
	if err := tx.Create(&row).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return RoomInhabitant{}, ErrDuplicateRoomInhabitant
		}
		return RoomInhabitant{}, err
	}
	return row, nil
}

func (s *Store) PlaceRoomInhabitant(mapSetID int64, cell EdgeCell, inhabitantID int64, presenceText string, resident bool) (RoomInhabitant, error) {
	var out RoomInhabitant
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = placeRoomInhabitantTx(tx, mapSetID, cell, inhabitantID, presenceText, resident)
		return err
	})
	return out, err
}

func roomInhabitantViewTx(tx *gorm.DB, mapSetID int64, cell EdgeCell, inhabitantID int64) (RoomInhabitantView, bool, error) {
	var out RoomInhabitantView
	err := tx.Table("room_inhabitants AS ri").
		Select(`mi.id, ri.id AS placement_id, mi.map_set_id, ri.zone, ri.x, ri.y, ri.l,
			mi.name, mi.kind, mi.look_target, mi.look_text, mi.note, mi.strategy,
			ri.presence_text, ri.resident, mi.updated_at, ri.updated_at AS placement_updated_at`).
		Joins("JOIN map_inhabitants AS mi ON mi.id=ri.inhabitant_id AND mi.map_set_id=ri.map_set_id").
		Where("ri.map_set_id=? AND ri.zone=? AND ri.x=? AND ri.y=? AND ri.l=? AND ri.inhabitant_id=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L, inhabitantID).
		Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoomInhabitantView{}, false, nil
	}
	return out, err == nil, err
}

func (s *Store) GetRoomInhabitant(mapSetID int64, cell EdgeCell, inhabitantID int64) (RoomInhabitantView, bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return RoomInhabitantView{}, false, err
	}
	return roomInhabitantViewTx(s.db, mapSetID, cell, inhabitantID)
}

func (s *Store) CreateRoomInhabitant(mapSetID int64, cell EdgeCell, fields RoomInhabitantFields) (RoomInhabitantView, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return RoomInhabitantView{}, err
	}
	fields.InhabitantFields, err = normalizeInhabitantFields(fields.InhabitantFields)
	if err != nil {
		return RoomInhabitantView{}, err
	}
	fields.PresenceText, err = normalizePresenceText(fields.PresenceText)
	if err != nil {
		return RoomInhabitantView{}, err
	}
	var out RoomInhabitantView
	err = s.db.Transaction(func(tx *gorm.DB) error {
		entity, err := createMapInhabitantTx(tx, mapSetID, fields.InhabitantFields)
		if err != nil {
			return err
		}
		if _, err := placeRoomInhabitantTx(tx, mapSetID, cell, entity.ID, fields.PresenceText, fields.Resident); err != nil {
			return err
		}
		var ok bool
		out, ok, err = roomInhabitantViewTx(tx, mapSetID, cell, entity.ID)
		if err == nil && !ok {
			return errors.New("created room inhabitant disappeared")
		}
		return err
	})
	return out, err
}

func (s *Store) ListRoomInhabitants(mapSetID int64, cell EdgeCell) ([]RoomInhabitantView, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return nil, err
	}
	var rows []RoomInhabitantView
	err = s.db.Table("room_inhabitants AS ri").
		Select(`mi.id, ri.id AS placement_id, mi.map_set_id, ri.zone, ri.x, ri.y, ri.l,
			mi.name, mi.kind, mi.look_target, mi.look_text, mi.note, mi.strategy,
			ri.presence_text, ri.resident, mi.updated_at, ri.updated_at AS placement_updated_at`).
		Joins("JOIN map_inhabitants AS mi ON mi.id=ri.inhabitant_id AND mi.map_set_id=ri.map_set_id").
		Where("ri.map_set_id=? AND ri.zone=? AND ri.x=? AND ri.y=? AND ri.l=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L).
		Order("mi.name COLLATE NOCASE ASC, mi.id ASC").Scan(&rows).Error
	if rows == nil {
		rows = []RoomInhabitantView{}
	}
	return rows, err
}

func (s *Store) ListRoomInhabitantsBounded(mapSetID int64, cell EdgeCell, limit int) ([]RoomInhabitantView, bool, error) {
	return s.ListRoomInhabitantsBoundedAfter(mapSetID, cell, limit, "", 0)
}

// ListRoomInhabitantsBoundedAfter returns one stable keyset page. The cursor is
// the last returned entity id; unlike OFFSET it uses the room's covering key
// and stays bounded without a name sort/temp B-tree. afterName is accepted for
// compatibility with the original name+id cursor but the numeric id is the
// authoritative continuation key. Callers may sort each returned page for
// display without changing pagination order.
func (s *Store) ListRoomInhabitantsBoundedAfter(mapSetID int64, cell EdgeCell, limit int, afterName string, afterID int64) ([]RoomInhabitantView, bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return nil, false, err
	}
	if afterID < 0 || (afterName != "" && afterID == 0) {
		return nil, false, fmt.Errorf("%w: invalid continuation cursor", ErrInvalidInhabitant)
	}
	if limit <= 0 {
		limit = InhabitantListDefaultLimit
	}
	if limit > InhabitantListMaxLimit {
		limit = InhabitantListMaxLimit
	}
	query := s.db.Table("room_inhabitants AS ri").
		Select(`mi.id, ri.id AS placement_id, mi.map_set_id, ri.zone, ri.x, ri.y, ri.l,
			mi.name, mi.kind, mi.look_target, mi.look_text, mi.note, mi.strategy,
			ri.presence_text, ri.resident, mi.updated_at, ri.updated_at AS placement_updated_at`).
		Joins("JOIN map_inhabitants AS mi ON mi.id=ri.inhabitant_id AND mi.map_set_id=ri.map_set_id").
		Where("ri.map_set_id=? AND ri.zone=? AND ri.x=? AND ri.y=? AND ri.l=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L)
	if afterID > 0 {
		query = query.Where("ri.inhabitant_id > ?", afterID)
	}
	var rows []RoomInhabitantView
	err = query.Order("ri.inhabitant_id ASC").Limit(limit + 1).Scan(&rows).Error
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []RoomInhabitantView{}
	}
	return rows, truncated, nil
}

func (s *Store) ListRoomInhabitantSummariesBounded(mapSetID int64, cell EdgeCell, search string, limit int) ([]RoomInhabitantSummary, bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return nil, false, err
	}
	if utf8.RuneCountInString(search) > InhabitantQueryLimit {
		return nil, false, fmt.Errorf("%w: query exceeds %d characters", ErrInvalidInhabitant, InhabitantQueryLimit)
	}
	if limit <= 0 {
		limit = InhabitantListDefaultLimit
	}
	if limit > InhabitantListMaxLimit {
		limit = InhabitantListMaxLimit
	}
	query := s.db.Table("room_inhabitants AS selected").
		Select(`mi.id, mi.map_set_id, mi.name, mi.kind, mi.look_target, mi.strategy,
			(SELECT COUNT(*) FROM room_inhabitants placements
			 WHERE placements.map_set_id=mi.map_set_id AND placements.inhabitant_id=mi.id) AS placement_count,
			selected.presence_text, selected.resident`).
		Joins("JOIN map_inhabitants AS mi ON mi.id=selected.inhabitant_id AND mi.map_set_id=selected.map_set_id").
		Where("selected.map_set_id=? AND selected.zone=? AND selected.x=? AND selected.y=? AND selected.l=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L)
	searchNeedle := strings.TrimSpace(search)
	unicodeNeedle := ""
	if searchNeedle != "" && isASCII(searchNeedle) {
		needle := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(searchNeedle))
		like := "%" + needle + "%"
		query = query.Where(`(LOWER(mi.name) LIKE ? ESCAPE '\' OR LOWER(mi.look_target) LIKE ? ESCAPE '\')`, like, like)
	} else if searchNeedle != "" {
		unicodeNeedle = foldInhabitantSearch(searchNeedle)
	}
	ordered := query.Order("mi.name COLLATE NOCASE ASC, mi.id ASC")
	var rows []RoomInhabitantSummary
	if unicodeNeedle == "" {
		err = ordered.Limit(limit + 1).Scan(&rows).Error
	} else {
		var sqlRows *sql.Rows
		sqlRows, err = ordered.Rows()
		if err == nil {
			defer sqlRows.Close()
			for sqlRows.Next() {
				var row RoomInhabitantSummary
				if err = s.db.ScanRows(sqlRows, &row); err != nil {
					break
				}
				if !strings.Contains(foldInhabitantSearch(row.Name), unicodeNeedle) &&
					!strings.Contains(foldInhabitantSearch(row.LookTarget), unicodeNeedle) {
					continue
				}
				rows = append(rows, row)
				if len(rows) > limit {
					break
				}
			}
			if err == nil {
				err = sqlRows.Err()
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []RoomInhabitantSummary{}
	}
	return rows, truncated, nil
}

// ListInhabitantPlacements returns every room binding for controlled internal
// uses. User-facing reads use ListInhabitantPlacementsBounded; shared-field
// invalidation is map-set scoped and must not enumerate this result.
func (s *Store) ListInhabitantPlacements(mapSetID, inhabitantID int64) ([]RoomInhabitant, error) {
	var rows []RoomInhabitant
	err := s.db.Where("map_set_id=? AND inhabitant_id=?", mapSetID, inhabitantID).
		Order("zone ASC, l ASC, x ASC, y ASC, id ASC").Find(&rows).Error
	if rows == nil {
		rows = []RoomInhabitant{}
	}
	return rows, err
}

func (s *Store) ListInhabitantPlacementsBounded(mapSetID, inhabitantID int64, limit int) ([]RoomInhabitant, bool, error) {
	if limit <= 0 {
		limit = InhabitantListDefaultLimit
	}
	if limit > InhabitantListMaxLimit {
		limit = InhabitantListMaxLimit
	}
	var rows []RoomInhabitant
	err := s.db.Where("map_set_id=? AND inhabitant_id=?", mapSetID, inhabitantID).
		Order("zone ASC, l ASC, x ASC, y ASC, id ASC").Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []RoomInhabitant{}
	}
	return rows, truncated, nil
}

func (s *Store) UpdateRoomInhabitant(mapSetID int64, cell EdgeCell, inhabitantID int64, fields RoomInhabitantFields) (RoomInhabitantView, bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return RoomInhabitantView{}, false, err
	}
	fields.InhabitantFields, err = normalizeInhabitantFields(fields.InhabitantFields)
	if err != nil {
		return RoomInhabitantView{}, false, err
	}
	fields.PresenceText, err = normalizePresenceText(fields.PresenceText)
	if err != nil {
		return RoomInhabitantView{}, false, err
	}
	lock := s.inhabitantPatchLock(mapSetID, inhabitantID)
	lock.Lock()
	defer lock.Unlock()
	var out RoomInhabitantView
	found := false
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var placement RoomInhabitant
		err := tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=? AND inhabitant_id=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L, inhabitantID).First(&placement).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok, err := updateMapInhabitantTx(tx, mapSetID, inhabitantID, fields.InhabitantFields); err != nil {
			return err
		} else if !ok {
			return ErrInhabitantNotFound
		}
		placement.PresenceText = fields.PresenceText
		placement.Resident = fields.Resident
		placement.UpdatedAt = nowSQLiteTimePtr()
		if err := tx.Save(&placement).Error; err != nil {
			return err
		}
		out, found, err = roomInhabitantViewTx(tx, mapSetID, cell, inhabitantID)
		return err
	})
	return out, found, err
}

func (s *Store) inhabitantPatchLock(mapSetID, inhabitantID int64) *sync.Mutex {
	key := uint64(mapSetID)*11400714819323198485 ^ uint64(inhabitantID)
	return &s.inhabitantPatchLocks[key%uint64(len(s.inhabitantPatchLocks))]
}

// PatchRoomInhabitant updates only fields present in patch. The first UPDATE
// acquires SQLite's write lock and verifies the selected placement before any
// shared entity field changes, while the bounded stripe serializes same-entity
// full and partial writes within this Store.
func (s *Store) PatchRoomInhabitant(mapSetID int64, cell EdgeCell, inhabitantID int64, patch RoomInhabitantPatch) (RoomInhabitantView, bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return RoomInhabitantView{}, false, err
	}
	entityUpdates := map[string]any{"updated_at": nowSQLiteTimePtr()}
	placementUpdates := map[string]any{"updated_at": nowSQLiteTimePtr()}
	if patch.Name != nil {
		value, err := normalizeInhabitantName(*patch.Name)
		if err != nil {
			return RoomInhabitantView{}, false, err
		}
		entityUpdates["name"] = value
	}
	if patch.Kind != nil {
		value, err := normalizeInhabitantKind(*patch.Kind)
		if err != nil {
			return RoomInhabitantView{}, false, err
		}
		entityUpdates["kind"] = value
	}
	for field, input := range map[string]*string{
		"look_target": patch.LookTarget, "look_text": patch.LookText,
		"note": patch.Note, "strategy": patch.Strategy,
	} {
		if input == nil {
			continue
		}
		value, err := normalizeInhabitantText(field, *input, field == "look_target")
		if err != nil {
			return RoomInhabitantView{}, false, err
		}
		entityUpdates[field] = value
	}
	if patch.PresenceText != nil {
		value, err := normalizePresenceText(*patch.PresenceText)
		if err != nil {
			return RoomInhabitantView{}, false, err
		}
		placementUpdates["presence_text"] = value
	}
	if patch.Resident != nil {
		placementUpdates["resident"] = *patch.Resident
	}

	lock := s.inhabitantPatchLock(mapSetID, inhabitantID)
	lock.Lock()
	defer lock.Unlock()
	var out RoomInhabitantView
	found := false
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&RoomInhabitant{}).
			Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=? AND inhabitant_id=?",
				mapSetID, cell.Zone, cell.X, cell.Y, cell.L, inhabitantID).
			Updates(placementUpdates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		result = tx.Model(&MapInhabitant{}).
			Where("map_set_id=? AND id=?", mapSetID, inhabitantID).
			Updates(entityUpdates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrInhabitantNotFound
		}
		var err error
		out, found, err = roomInhabitantViewTx(tx, mapSetID, cell, inhabitantID)
		return err
	})
	return out, found, err
}

// DeleteRoomInhabitant removes only this room placement. The normalized entity
// remains while referenced elsewhere; an unreferenced entity is removed in the
// same transaction so UI-created catalogue rows do not accumulate as orphans.
func (s *Store) DeleteRoomInhabitant(mapSetID int64, cell EdgeCell, inhabitantID int64) (bool, error) {
	cell, err := normalizeInhabitantCell(cell)
	if err != nil {
		return false, err
	}
	deleted := false
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("map_set_id=? AND zone=? AND x=? AND y=? AND l=? AND inhabitant_id=?",
			mapSetID, cell.Zone, cell.X, cell.Y, cell.L, inhabitantID).Delete(&RoomInhabitant{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		deleted = true
		var remaining int64
		if err := tx.Model(&RoomInhabitant{}).
			Where("map_set_id=? AND inhabitant_id=?", mapSetID, inhabitantID).Count(&remaining).Error; err != nil {
			return err
		}
		if remaining == 0 {
			return tx.Where("map_set_id=? AND id=?", mapSetID, inhabitantID).Delete(&MapInhabitant{}).Error
		}
		return nil
	})
	return deleted, err
}
