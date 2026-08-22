package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// ZoneCoord identifies a room without exposing its surrogate database id.
type ZoneCoord struct {
	X int `json:"x"`
	Y int `json:"y"`
	L int `json:"l"`
}

func (c *ZoneCoord) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("invalid room coordinate: %w", err)
	}
	if len(fields) != 3 {
		return fmt.Errorf("room coordinate requires exactly x, y, and l")
	}
	for name, dst := range map[string]*int{"x": &c.X, "y": &c.Y, "l": &c.L} {
		raw, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("room coordinate %s is required", name)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("room coordinate %s must be an integer", name)
		}
	}
	return nil
}

// ZoneMoveInput is an atomic namespace move. AllSource is used for a rename and
// intentionally resolves the set server-side; callers never need to send a map.
type ZoneMoveInput struct {
	SourceZone       string      `json:"source_zone"`
	TargetZone       string      `json:"target_zone"`
	Rooms            []ZoneCoord `json:"rooms,omitempty"`
	AllSource        bool        `json:"all_source,omitempty"`
	ExpectedRevision int64       `json:"expected_revision"`
}

// ZoneMovePreview is deliberately aggregate and bounded. It is valid both for a
// dry run and the report returned by a successful apply.
type ZoneMovePreview struct {
	Revision            int64       `json:"revision"`
	Rooms               int         `json:"rooms"`
	InternalEdges       int         `json:"internal_edges"`
	BoundaryGridEdges   int         `json:"boundary_grid_edges"`
	Portals             int         `json:"portals"`
	Incoming            int         `json:"incoming"`
	Outgoing            int         `json:"outgoing"`
	CrossZoneContacts   int         `json:"cross_zone_contacts"`
	Components          int         `json:"components"`
	Disconnected        bool        `json:"disconnected"`
	AnnotationsMoved    int         `json:"annotations_moved"`
	InhabitantsMoved    int         `json:"inhabitants_moved"`
	CollisionCount      int         `json:"collision_count"`
	Collisions          []ZoneCoord `json:"collisions,omitempty"`
	CollisionsTruncated bool        `json:"collisions_truncated,omitempty"`
	SourceEmpties       bool        `json:"source_empties"`
	TargetExists        bool        `json:"target_exists"`
}

// ZoneMoveBefore is one exact undo payload. Room IDs are deliberately retained,
// so attached room_images travel without rewrites in either direction.
type ZoneMoveBefore struct {
	Input       ZoneMoveInput
	Rooms       []Room
	Edges       []MapEdge
	Annotations []RoomAnnotation
	Inhabitants []RoomInhabitant
	Layouts     []RoomLayout
	Revision    int64
}

const (
	zoneCollisionCap = 100
	// Keeps request memory, undo snapshots and SQLite tuple predicates bounded.
	// The production corpus's largest supported operation is an 8k-room zone.
	zoneSelectionCap = 8000
)

// ErrInvalidZoneName marks a malformed zone key at a transport boundary. Callers
// can return a 4xx/domain refusal instead of misreporting client input as a 500.
var ErrInvalidZoneName = errors.New("invalid zone name")

// NormalizeZoneName is the single write-boundary representation of a mapper zone
// name. Zone names are logical keys across rooms, edges and annotations, so every
// writer must trim and NFC-normalize before looking up or persisting one.
func NormalizeZoneName(name string) (string, error) {
	name = norm.NFC.String(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("%w: zone name is required", ErrInvalidZoneName)
	}
	if utf8.RuneCountInString(name) > 256 {
		return "", fmt.Errorf("%w: zone name exceeds 256 characters", ErrInvalidZoneName)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: zone name contains a control character", ErrInvalidZoneName)
		}
	}
	return name, nil
}

func canonicalZoneName(name string) (string, error) { return NormalizeZoneName(name) }

func canonicalZoneMove(in ZoneMoveInput) (ZoneMoveInput, error) {
	var err error
	if in.SourceZone, err = canonicalZoneName(in.SourceZone); err != nil {
		return in, err
	}
	if in.TargetZone, err = canonicalZoneName(in.TargetZone); err != nil {
		return in, err
	}
	if in.SourceZone == in.TargetZone {
		return in, fmt.Errorf("source and target zone names must differ")
	}
	if in.ExpectedRevision < 0 {
		return in, fmt.Errorf("expected_revision must be non-negative")
	}
	seen := make(map[ZoneCoord]struct{}, len(in.Rooms))
	out := make([]ZoneCoord, 0, len(in.Rooms))
	for _, c := range in.Rooms {
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	in.Rooms = out
	if len(in.Rooms) > zoneSelectionCap {
		return in, fmt.Errorf("room selection exceeds %d rooms", zoneSelectionCap)
	}
	if !in.AllSource && len(in.Rooms) == 0 {
		return in, fmt.Errorf("select at least one room")
	}
	if in.AllSource && len(in.Rooms) != 0 {
		return in, fmt.Errorf("rename must not include an explicit room list")
	}
	return in, nil
}

func normalizeMapSetInputZones(in MapSetInput) (MapSetInput, error) {
	if len(in.Rooms) > 0 {
		in.Rooms = append([]Room(nil), in.Rooms...)
		for i := range in.Rooms {
			zone, err := NormalizeZoneName(in.Rooms[i].Zone)
			if err != nil {
				return in, fmt.Errorf("room %d: %w", i+1, err)
			}
			in.Rooms[i].Zone = zone
		}
	}
	if len(in.LegacySeams) > 0 {
		in.LegacySeams = append([]LegacySeamDraft(nil), in.LegacySeams...)
		for i := range in.LegacySeams {
			zone, err := NormalizeZoneName(in.LegacySeams[i].From.Zone)
			if err != nil {
				return in, fmt.Errorf("legacy seam %d source: %w", i+1, err)
			}
			in.LegacySeams[i].From.Zone = zone
		}
	}
	return in, nil
}

// canonicalizePersistedZoneKeys migrates pre-027 mapper data as one transaction.
// It refuses only real post-normalization UNIQUE-key collisions. A symbolic seam
// target and the room it names are a definition/reference pair, not two competing
// zone definitions, so they must be allowed to use different old spellings.
func canonicalizePersistedZoneKeys(tx *gorm.DB) error {
	var rooms []Room
	var annotations []RoomAnnotation
	var edges []MapEdge
	if err := tx.Find(&rooms).Error; err != nil {
		return err
	}
	if err := tx.Find(&annotations).Error; err != nil {
		return err
	}
	if err := tx.Find(&edges).Error; err != nil {
		return err
	}
	canonical := func(mapSetID int64, field, raw string) (string, error) {
		zone, err := NormalizeZoneName(raw)
		if err != nil {
			return "", fmt.Errorf("zone canonicalization: map set %d %s %q: %w", mapSetID, field, raw, err)
		}
		return zone, nil
	}
	collision := func(seen map[string]int64, key string, id int64, label string) error {
		if prior, ok := seen[key]; ok && prior != id {
			return fmt.Errorf("zone canonicalization: zone collision in %s between rows %d and %d", label, prior, id)
		}
		seen[key] = id
		return nil
	}

	roomZones := make([]string, len(rooms))
	roomKeys := make(map[string]int64, len(rooms))
	for i := range rooms {
		zone, err := canonical(rooms[i].MapSetID, "room zone", rooms[i].Zone)
		if err != nil {
			return err
		}
		roomZones[i] = zone
		key := fmt.Sprintf("%d|%q|%d|%d|%d", rooms[i].MapSetID, zone, rooms[i].X, rooms[i].Y, rooms[i].L)
		if err := collision(roomKeys, key, rooms[i].ID, "room coordinate"); err != nil {
			return err
		}
	}
	annotationZones := make([]string, len(annotations))
	annotationKeys := make(map[string]int64, len(annotations))
	for i := range annotations {
		zone, err := canonical(annotations[i].MapSetID, "annotation zone", annotations[i].Zone)
		if err != nil {
			return err
		}
		annotationZones[i] = zone
		key := fmt.Sprintf("%d|%q|%d|%d|%d", annotations[i].MapSetID, zone, annotations[i].X, annotations[i].Y, annotations[i].L)
		if err := collision(annotationKeys, key, annotations[i].ID, "annotation coordinate"); err != nil {
			return err
		}
	}
	type edgeZones struct{ from, to, target string }
	edgeZoneValues := make([]edgeZones, len(edges))
	gridKeys := make(map[string]int64)
	seamKeys := make(map[string]int64)
	manualSeamKeys := make(map[string]int64)
	touchedSets := make(map[int64]bool)
	for i := range edges {
		e := &edges[i]
		from, err := canonical(e.MapSetID, "edge source zone", e.FromZone)
		if err != nil {
			return err
		}
		values := edgeZones{from: from}
		if e.ToZone != nil {
			if values.to, err = canonical(e.MapSetID, "edge target zone", *e.ToZone); err != nil {
				return err
			}
		}
		if e.TargetZone != "" {
			if values.target, err = canonical(e.MapSetID, "symbolic target zone", e.TargetZone); err != nil {
				return err
			}
		}
		edgeZoneValues[i] = values
		touchedSets[e.MapSetID] = true
		switch e.Kind {
		case EdgeKindGrid:
			key := fmt.Sprintf("%d|%q|%d|%d|%d|%q", e.MapSetID, from, e.FromX, e.FromY, e.FromL, e.Dir)
			if err := collision(gridKeys, key, e.ID, "grid edge identity"); err != nil {
				return err
			}
		case EdgeKindSeam:
			key := fmt.Sprintf("%d|%q|%d|%d|%d|%d", e.MapSetID, from, e.FromX, e.FromY, e.FromL, e.SeamOrder)
			if err := collision(seamKeys, key, e.ID, "seam occurrence"); err != nil {
				return err
			}
			if e.Source == EdgeSourceManual {
				tag := "nil"
				if e.TargetTag != nil {
					tag = fmt.Sprintf("%d", *e.TargetTag)
				}
				manualKey := fmt.Sprintf("%d|%q|%d|%d|%d|%q|%s|%q|%d", e.MapSetID, from, e.FromX, e.FromY, e.FromL, values.target, tag, e.Command, e.SeamOrder)
				if err := collision(manualSeamKeys, manualKey, e.ID, "manual seam selector"); err != nil {
					return err
				}
			}
		}
	}

	for i := range rooms {
		if rooms[i].Zone != roomZones[i] {
			if err := tx.Model(&Room{}).Where("id=?", rooms[i].ID).Update("zone", roomZones[i]).Error; err != nil {
				return err
			}
		}
	}
	for i := range annotations {
		if annotations[i].Zone != annotationZones[i] {
			if err := tx.Model(&RoomAnnotation{}).Where("id=?", annotations[i].ID).Update("zone", annotationZones[i]).Error; err != nil {
				return err
			}
		}
	}
	for i := range edges {
		e, zones := &edges[i], edgeZoneValues[i]
		updates := map[string]any{}
		if e.FromZone != zones.from {
			updates["from_zone"] = zones.from
		}
		if e.ToZone != nil && *e.ToZone != zones.to {
			updates["to_zone"] = zones.to
		}
		if e.TargetZone != "" && e.TargetZone != zones.target {
			updates["target_zone"] = zones.target
		}
		if len(updates) > 0 {
			if err := tx.Model(&MapEdge{}).Where("id=?", e.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	for mapSetID := range touchedSets {
		if err := reresolveSeamsTx(tx, mapSetID); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeZoneMove exposes the write-boundary normalization to transport and
// tracker callers. Storage repeats it defensively, so direct callers cannot
// bypass validation.
func NormalizeZoneMove(in ZoneMoveInput) (ZoneMoveInput, error) { return canonicalZoneMove(in) }

func zoneCoordKey(zone string, x, y, l int) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%d", zone, x, y, l)
}

func selectedZoneRooms(db *gorm.DB, mapSetID int64, in ZoneMoveInput) ([]Room, error) {
	var rooms []Room
	if in.AllSource {
		var count int64
		if err := db.Model(&Room{}).Where("map_set_id=? AND zone=?", mapSetID, in.SourceZone).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > zoneSelectionCap {
			return nil, fmt.Errorf("source zone exceeds %d rooms", zoneSelectionCap)
		}
		if err := db.Where("map_set_id=? AND zone=?", mapSetID, in.SourceZone).Find(&rooms).Error; err != nil {
			return nil, err
		}
		return rooms, nil
	}
	selected := make([]Room, len(in.Rooms))
	for i, c := range in.Rooms {
		selected[i] = Room{X: c.X, Y: c.Y, L: c.L}
	}
	where, args := zoneTupleWhere("", selected, []any{mapSetID, in.SourceZone})
	if err := db.Where("map_set_id=? AND zone=? AND "+where, args...).Find(&rooms).Error; err != nil {
		return nil, err
	}
	if len(rooms) != len(in.Rooms) {
		return nil, fmt.Errorf("one or more selected rooms are missing from source zone")
	}
	return rooms, nil
}

func zoneMovePreviewTx(tx *gorm.DB, mapSetID int64, in ZoneMoveInput) (ZoneMovePreview, []Room, []MapEdge, []RoomAnnotation, []RoomInhabitant, error) {
	var set MapSet
	if err := tx.First(&set, mapSetID).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	rooms, err := selectedZoneRooms(tx, mapSetID, in)
	if err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	if len(rooms) == 0 {
		return ZoneMovePreview{}, nil, nil, nil, nil, fmt.Errorf("source zone has no rooms")
	}
	if len(rooms) > zoneSelectionCap {
		return ZoneMovePreview{}, nil, nil, nil, nil, fmt.Errorf("source zone exceeds %d rooms", zoneSelectionCap)
	}
	selected := make(map[string]bool, len(rooms))
	for _, r := range rooms {
		selected[zoneCoordKey(in.SourceZone, r.X, r.Y, r.L)] = true
	}
	var targetCount int64
	if err := tx.Model(&Room{}).Where("map_set_id=? AND zone=?", mapSetID, in.TargetZone).Count(&targetCount).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	p := ZoneMovePreview{Revision: set.TopologyRevision, Rooms: len(rooms), TargetExists: targetCount > 0}
	collisionKeys := make(map[string]bool)
	addCollision := func(x, y, l int) {
		key := fmt.Sprintf("%d\x00%d\x00%d", x, y, l)
		if collisionKeys[key] {
			return
		}
		collisionKeys[key] = true
		p.CollisionCount++
		if len(p.Collisions) < zoneCollisionCap {
			p.Collisions = append(p.Collisions, ZoneCoord{x, y, l})
		} else {
			p.CollisionsTruncated = true
		}
	}
	// Presentation coordinates share the destination namespace after the move.
	// Validate the complete prospective effective layout here, under the same
	// transaction and revision snapshot as topology collision checks. Sparse rooms
	// without an override display at their logical coordinate.
	var allRooms []Room
	if err := tx.Where("map_set_id=?", mapSetID).Find(&allRooms).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	var allLayouts []RoomLayout
	if err := tx.Where("map_set_id=?", mapSetID).Find(&allLayouts).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	layoutByLogical := make(map[string]RoomLayout, len(allLayouts))
	for _, layout := range allLayouts {
		layoutByLogical[zoneCoordKey(layout.Zone, layout.X, layout.Y, layout.L)] = layout
	}
	displayOwner := make(map[string]string, len(allRooms))
	for _, room := range allRooms {
		logical := zoneCoordKey(room.Zone, room.X, room.Y, room.L)
		finalZone := room.Zone
		if selected[logical] {
			finalZone = in.TargetZone
		}
		dx, dy, dl := room.X, room.Y, room.L
		if layout, ok := layoutByLogical[logical]; ok {
			dx, dy, dl = layout.DisplayX, layout.DisplayY, layout.DisplayL
		}
		display := zoneCoordKey(finalZone, dx, dy, dl)
		if prior, exists := displayOwner[display]; exists && prior != logical {
			addCollision(dx, dy, dl)
		}
		displayOwner[display] = logical
	}
	var collisions []Room
	where, args := zoneTupleWhere("", rooms, []any{mapSetID, in.TargetZone})
	if err := tx.Where("map_set_id=? AND zone=? AND "+where, args...).Find(&collisions).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	for _, r := range collisions {
		addCollision(r.X, r.Y, r.L)
	}
	var edges []MapEdge
	if err := tx.Where("map_set_id = ?", mapSetID).Find(&edges).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	affected := make([]MapEdge, 0)
	adj := make(map[string][]string)
	for _, e := range edges {
		from := selected[zoneCoordKey(e.FromZone, e.FromX, e.FromY, e.FromL)]
		toCell, hasTo := e.To()
		to := hasTo && selected[zoneCoordKey(toCell.Zone, toCell.X, toCell.Y, toCell.L)]
		if from || to {
			affected = append(affected, e)
		}
		if from && to {
			p.InternalEdges++
			if e.Passable && hasTo {
				a := zoneCoordKey(e.FromZone, e.FromX, e.FromY, e.FromL)
				b := zoneCoordKey(toCell.Zone, toCell.X, toCell.Y, toCell.L)
				adj[a] = append(adj[a], b)
				adj[b] = append(adj[b], a)
			}
		}
		if e.Kind == EdgeKindGrid && from != to && (from || to) {
			p.BoundaryGridEdges++
			p.CrossZoneContacts++
		}
		if e.Kind == EdgeKindSeam {
			if from {
				p.Outgoing++
				p.Portals++
			}
			if to {
				p.Incoming++
				if !from {
					p.Portals++
				}
			}
		}
	}
	seen := map[string]bool{}
	for k := range selected {
		if seen[k] {
			continue
		}
		p.Components++
		stack := []string{k}
		seen[k] = true
		for len(stack) > 0 {
			n := len(stack) - 1
			cur := stack[n]
			stack = stack[:n]
			for _, next := range adj[cur] {
				if !seen[next] {
					seen[next] = true
					stack = append(stack, next)
				}
			}
		}
	}
	p.Disconnected = p.Components > 1
	var annos []RoomAnnotation
	where, args = zoneTupleWhere("", rooms, []any{mapSetID, in.SourceZone})
	if err := tx.Where("map_set_id=? AND zone=? AND "+where, args...).Find(&annos).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	p.AnnotationsMoved = len(annos)
	var inhabitants []RoomInhabitant
	if err := tx.Where("map_set_id=? AND zone=? AND "+where, args...).Find(&inhabitants).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	p.InhabitantsMoved = len(inhabitants)
	if len(inhabitants) > 0 {
		sourcePlacements := make(map[string]bool, len(inhabitants))
		for _, placement := range inhabitants {
			key := fmt.Sprintf("%d\x00%d\x00%d\x00%d", placement.X, placement.Y, placement.L, placement.InhabitantID)
			sourcePlacements[key] = true
		}
		var targetPlacements []RoomInhabitant
		where, args = zoneTupleWhere("", rooms, []any{mapSetID, in.TargetZone})
		if err := tx.Where("map_set_id=? AND zone=? AND "+where, args...).Find(&targetPlacements).Error; err != nil {
			return ZoneMovePreview{}, nil, nil, nil, nil, err
		}
		for _, placement := range targetPlacements {
			key := fmt.Sprintf("%d\x00%d\x00%d\x00%d", placement.X, placement.Y, placement.L, placement.InhabitantID)
			if sourcePlacements[key] {
				addCollision(placement.X, placement.Y, placement.L)
			}
		}
	}
	var sourceCount int64
	if err := tx.Model(&Room{}).Where("map_set_id=? AND zone=?", mapSetID, in.SourceZone).Count(&sourceCount).Error; err != nil {
		return ZoneMovePreview{}, nil, nil, nil, nil, err
	}
	p.SourceEmpties = int64(len(rooms)) == sourceCount
	return p, rooms, affected, annos, inhabitants, nil
}

// PreviewZoneMove is read-only; it deliberately does not fork frozen sets.
func (s *Store) PreviewZoneMove(mapSetID int64, input ZoneMoveInput) (ZoneMovePreview, error) {
	in, err := canonicalZoneMove(input)
	if err != nil {
		return ZoneMovePreview{}, err
	}
	var out ZoneMovePreview
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, _, _, _, _, e = zoneMovePreviewTx(tx, mapSetID, in)
		return e
	})
	return out, err
}

func topologyRevisionTx(tx *gorm.DB, mapSetID int64) (int64, error) {
	var set MapSet
	if err := tx.Select("id", "topology_revision").First(&set, mapSetID).Error; err != nil {
		return 0, err
	}
	return set.TopologyRevision, nil
}

// advanceTopologyRevisionTx is the process-independent fence. The mutation and
// this compare-and-swap share one SQLite transaction, so a separate Store (or a
// separate process) cannot commit topology between them.
func advanceTopologyRevisionTx(tx *gorm.DB, mapSetID, expected int64) error {
	if expected == math.MaxInt64 {
		return fmt.Errorf("topology revision overflow")
	}
	res := tx.Exec(`UPDATE map_sets
		SET topology_revision=topology_revision+1
		WHERE id=? AND topology_revision=? AND topology_revision<?`, mapSetID, expected, int64(math.MaxInt64))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	current, err := topologyRevisionTx(tx, mapSetID)
	if err != nil {
		return err
	}
	return fmt.Errorf("stale topology revision: expected %d, current %d", expected, current)
}

func zoneTupleWhere(prefix string, rooms []Room, args []any) (string, []any) {
	parts := make([]string, 0, len(rooms))
	for _, r := range rooms {
		parts = append(parts, "(?,?,?)")
		args = append(args, r.X, r.Y, r.L)
	}
	columns := "x,y,l"
	if prefix != "" {
		columns = prefix + "_x," + prefix + "_y," + prefix + "_l"
	}
	return "(" + columns + ") IN (" + strings.Join(parts, ",") + ")", args
}

type zoneTempCoord struct {
	X int `gorm:"column:x"`
	Y int `gorm:"column:y"`
	L int `gorm:"column:l"`
}

func prepareZoneSelectionTx(tx *gorm.DB, rooms []Room) error {
	if err := tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS zone_move_selected (
		x INTEGER NOT NULL, y INTEGER NOT NULL, l INTEGER NOT NULL,
		PRIMARY KEY (x,y,l)
	) WITHOUT ROWID`).Error; err != nil {
		return err
	}
	if err := tx.Exec("DELETE FROM zone_move_selected").Error; err != nil {
		return err
	}
	coords := make([]zoneTempCoord, len(rooms))
	for i, room := range rooms {
		coords[i] = zoneTempCoord{X: room.X, Y: room.Y, L: room.L}
	}
	return tx.Table("zone_move_selected").CreateInBatches(coords, 500).Error
}

// ApplyZoneMove performs every room, edge, annotation and count change in one
// SQLite transaction. Edges are updated by endpoint set, never reconstructed.
func (s *Store) ApplyZoneMove(mapSetID int64, input ZoneMoveInput) (TopologyBefore, ZoneMovePreview, bool, error) {
	in, err := canonicalZoneMove(input)
	if err != nil {
		return TopologyBefore{}, ZoneMovePreview{}, false, err
	}
	var before TopologyBefore
	var report ZoneMovePreview
	found := false
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var e error
		before, report, found, e = applyZoneMoveTx(tx, mapSetID, in)
		return e
	})
	if err != nil {
		return TopologyBefore{}, ZoneMovePreview{}, false, err
	}
	return before, report, found, nil
}

func applyZoneMoveTx(tx *gorm.DB, mapSetID int64, in ZoneMoveInput) (before TopologyBefore, report ZoneMovePreview, found bool, err error) {
	in, err = canonicalZoneMove(in)
	if err != nil {
		return before, report, false, err
	}
	err = tx.Transaction(func(tx *gorm.DB) error {
		var e error
		var rooms []Room
		var edges []MapEdge
		var annos []RoomAnnotation
		var inhabitants []RoomInhabitant
		report, rooms, edges, annos, inhabitants, e = zoneMovePreviewTx(tx, mapSetID, in)
		if e != nil {
			return e
		}
		if report.CollisionCount > 0 {
			return fmt.Errorf("target zone has %d coordinate collision(s)", report.CollisionCount)
		}
		if report.Revision != in.ExpectedRevision {
			return fmt.Errorf("stale topology revision: expected %d, current %d", in.ExpectedRevision, report.Revision)
		}
		if err := advanceTopologyRevisionTx(tx, mapSetID, in.ExpectedRevision); err != nil {
			return err
		}
		before.Revision = report.Revision
		var layouts []RoomLayout
		where, layoutArgs := zoneTupleWhere("", rooms, []any{mapSetID, in.SourceZone})
		if e := tx.Where("map_set_id=? AND zone=? AND "+where, layoutArgs...).Find(&layouts).Error; e != nil {
			return e
		}
		before.ZoneMove = &ZoneMoveBefore{Input: in, Rooms: append([]Room(nil), rooms...), Edges: append([]MapEdge(nil), edges...), Annotations: append([]RoomAnnotation(nil), annos...), Inhabitants: append([]RoomInhabitant(nil), inhabitants...), Layouts: append([]RoomLayout(nil), layouts...), Revision: report.Revision}
		if err := prepareZoneSelectionTx(tx, rooms); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE rooms SET zone=?
			WHERE map_set_id=? AND zone=? AND EXISTS (
				SELECT 1 FROM zone_move_selected s WHERE s.x=rooms.x AND s.y=rooms.y AND s.l=rooms.l
			)`, in.TargetZone, mapSetID, in.SourceZone).Error; err != nil {
			return err
		}
		fromSelected := `(from_zone=? AND EXISTS (SELECT 1 FROM zone_move_selected s WHERE s.x=map_edges.from_x AND s.y=map_edges.from_y AND s.l=map_edges.from_l))`
		toSelected := `(to_zone=? AND EXISTS (SELECT 1 FROM zone_move_selected s WHERE s.x=map_edges.to_x AND s.y=map_edges.to_y AND s.l=map_edges.to_l))`
		edgeSQL := `UPDATE map_edges SET
			from_zone=CASE WHEN ` + fromSelected + ` THEN ? ELSE from_zone END,
			to_zone=CASE WHEN ` + toSelected + ` THEN ? ELSE to_zone END,
			source=CASE WHEN kind=? AND source IN (?,?) AND to_zone IS NOT NULL AND (` + fromSelected + ` <> ` + toSelected + `) THEN ? ELSE source END,
			zone_guard=CASE WHEN kind=? AND source IN (?,?) AND to_zone IS NOT NULL AND (` + fromSelected + ` <> ` + toSelected + `) THEN 1 ELSE zone_guard END
			WHERE map_set_id=? AND (` + fromSelected + ` OR ` + toSelected + `)`
		args := []any{
			in.SourceZone, in.TargetZone,
			in.SourceZone, in.TargetZone,
			EdgeKindGrid, EdgeSourceParsed, EdgeSourceGrid, in.SourceZone, in.SourceZone, EdgeSourceManual,
			EdgeKindGrid, EdgeSourceParsed, EdgeSourceGrid, in.SourceZone, in.SourceZone,
			mapSetID, in.SourceZone, in.SourceZone,
		}
		if err := tx.Exec(edgeSQL, args...).Error; err != nil {
			return err
		}
		if err := demoteZoneGuardedNaturalFacesTx(tx, mapSetID, in.SourceZone, in.TargetZone); err != nil {
			return err
		}
		if len(annos) > 0 {
			if err := tx.Exec(`UPDATE room_annotations SET zone=?
				WHERE map_set_id=? AND zone=? AND EXISTS (
					SELECT 1 FROM zone_move_selected s
					WHERE s.x=room_annotations.x AND s.y=room_annotations.y AND s.l=room_annotations.l
				)`, in.TargetZone, mapSetID, in.SourceZone).Error; err != nil {
				return err
			}
		}
		if len(layouts) > 0 {
			if err := tx.Exec(`UPDATE room_layouts SET zone=? WHERE map_set_id=? AND zone=? AND EXISTS (
				SELECT 1 FROM zone_move_selected s WHERE s.x=room_layouts.x AND s.y=room_layouts.y AND s.l=room_layouts.l
			)`, in.TargetZone, mapSetID, in.SourceZone).Error; err != nil {
				return err
			}
		}
		if len(inhabitants) > 0 {
			ids := make([]int64, len(inhabitants))
			for i := range inhabitants {
				ids[i] = inhabitants[i].ID
			}
			if err := tx.Model(&RoomInhabitant{}).
				Where("map_set_id=? AND zone=? AND id IN ?", mapSetID, in.SourceZone, ids).
				Update("zone", in.TargetZone).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE map_sets SET room_count=(SELECT COUNT(*) FROM rooms WHERE map_set_id=?), zone_count=(SELECT COUNT(DISTINCT zone) FROM rooms WHERE map_set_id=?), seam_count=(SELECT COUNT(*) FROM map_edges WHERE map_set_id=? AND kind='seam') WHERE id=?`, mapSetID, mapSetID, mapSetID, mapSetID).Error; err != nil {
			return err
		}
		report.Revision++
		found = true
		return nil
	})
	return before, report, found, err
}

// demoteZoneGuardedNaturalFacesTx gives a zone-move-owned boundary back to the
// derivation once its endpoints are again in the same namespace. The comparison
// includes the derived door, so a normal imported door does not turn a face into
// permanent manual markup. Rows without ZoneGuard are user-owned by definition
// and are never considered here.
func demoteZoneGuardedNaturalFacesTx(tx *gorm.DB, mapSetID int64, zones ...string) error {
	seen := make(map[string]bool, len(zones))
	unique := make([]string, 0, len(zones))
	for _, zone := range zones {
		if !seen[zone] {
			seen[zone] = true
			unique = append(unique, zone)
		}
	}
	if len(unique) == 0 {
		return nil
	}
	var guarded []MapEdge
	if err := tx.Where(`map_set_id=? AND kind=? AND source=? AND zone_guard=1
		AND from_zone=to_zone AND from_zone IN ?`, mapSetID, EdgeKindGrid, EdgeSourceManual, unique).
		Find(&guarded).Error; err != nil {
		return err
	}
	if len(guarded) == 0 {
		return nil
	}
	var rooms []Room
	if err := tx.Where("map_set_id=? AND zone IN ?", mapSetID, unique).Find(&rooms).Error; err != nil {
		return err
	}
	natural := make(map[edgeKey]MapEdge, len(rooms)*2)
	for _, edge := range DeriveGridEdges(mapSetID, rooms) {
		natural[edgeKey{cell: edge.From(), dir: edge.Dir}] = edge
	}
	for _, edge := range guarded {
		derived, ok := natural[edgeKey{cell: edge.From(), dir: edge.Dir}]
		if !ok || !sameDerivedGridSemantics(edge, derived) {
			continue
		}
		if err := tx.Model(&MapEdge{}).Where("id=?", edge.ID).
			Updates(map[string]any{"source": derived.Source, "zone_guard": false}).Error; err != nil {
			return err
		}
	}
	return nil
}

func sameDerivedGridSemantics(actual, derived MapEdge) bool {
	if actual.Kind != EdgeKindGrid || derived.Kind != EdgeKindGrid ||
		!actual.Passable || actual.Passable != derived.Passable || actual.Dir != derived.Dir ||
		actual.From() != derived.From() ||
		actual.EffectiveWeight() != derived.EffectiveWeight() ||
		actual.Phrase != derived.Phrase || actual.Door != derived.Door ||
		actual.Locked != derived.Locked || actual.OpenCmd != derived.OpenCmd || actual.OpenDelayMS != derived.OpenDelayMS || actual.NoRoute != derived.NoRoute {
		return false
	}
	actualTo, actualHasTo := actual.To()
	derivedTo, derivedHasTo := derived.To()
	return actualHasTo == derivedHasTo && (!actualHasTo || actualTo == derivedTo)
}

func (s *Store) restoreZoneMoveBeforeUnfenced(mapSetID int64, b ZoneMoveBefore) (bool, error) {
	if len(b.Rooms) == 0 {
		return false, nil
	}
	return true, s.db.Transaction(func(tx *gorm.DB) error {
		if err := prepareZoneSelectionTx(tx, b.Rooms); err != nil {
			return err
		}
		// Remove exact affected rows before reinserting their immutable snapshot.
		if err := tx.Exec(`DELETE FROM map_edges WHERE map_set_id=? AND (
			(from_zone=? AND EXISTS (SELECT 1 FROM zone_move_selected s WHERE s.x=map_edges.from_x AND s.y=map_edges.from_y AND s.l=map_edges.from_l))
			OR
			(to_zone=? AND EXISTS (SELECT 1 FROM zone_move_selected s WHERE s.x=map_edges.to_x AND s.y=map_edges.to_y AND s.l=map_edges.to_l))
		)`, mapSetID, b.Input.TargetZone, b.Input.TargetZone).Error; err != nil {
			return err
		}
		if len(b.Edges) > 0 {
			if err := tx.CreateInBatches(b.Edges, 500).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE rooms SET zone=?
			WHERE map_set_id=? AND zone=? AND EXISTS (
				SELECT 1 FROM zone_move_selected s WHERE s.x=rooms.x AND s.y=rooms.y AND s.l=rooms.l
			)`, b.Input.SourceZone, mapSetID, b.Input.TargetZone).Error; err != nil {
			return err
		}
		if len(b.Annotations) > 0 {
			if err := tx.Exec(`UPDATE room_annotations SET zone=?
				WHERE map_set_id=? AND zone=? AND EXISTS (
					SELECT 1 FROM zone_move_selected s
					WHERE s.x=room_annotations.x AND s.y=room_annotations.y AND s.l=room_annotations.l
				)`, b.Input.SourceZone, mapSetID, b.Input.TargetZone).Error; err != nil {
				return err
			}
		}
		if len(b.Layouts) > 0 {
			if err := tx.Exec(`DELETE FROM room_layouts WHERE map_set_id=? AND zone=? AND EXISTS (
				SELECT 1 FROM zone_move_selected s WHERE s.x=room_layouts.x AND s.y=room_layouts.y AND s.l=room_layouts.l
			)`, mapSetID, b.Input.TargetZone).Error; err != nil {
				return err
			}
			for i := range b.Layouts {
				b.Layouts[i].MapSetID = mapSetID
			}
			if err := tx.CreateInBatches(b.Layouts, 500).Error; err != nil {
				return err
			}
		}
		if len(b.Inhabitants) > 0 {
			ids := make([]int64, len(b.Inhabitants))
			for i := range b.Inhabitants {
				ids[i] = b.Inhabitants[i].ID
			}
			if err := tx.Model(&RoomInhabitant{}).
				Where("map_set_id=? AND zone=? AND id IN ?", mapSetID, b.Input.TargetZone, ids).
				Update("zone", b.Input.SourceZone).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE map_sets SET room_count=(SELECT COUNT(*) FROM rooms WHERE map_set_id=?), zone_count=(SELECT COUNT(DISTINCT zone) FROM rooms WHERE map_set_id=?), seam_count=(SELECT COUNT(*) FROM map_edges WHERE map_set_id=? AND kind='seam') WHERE id=?`, mapSetID, mapSetID, mapSetID, mapSetID).Error
	})
}
