// Package mapperfixture creates an isolated five-room mapper database for the
// live reconciliation E2E. It is test infrastructure, not a production seed.
package mapperfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rubymud/go/internal/e2e/fiveroom"
	"rubymud/go/internal/storage"
)

const Zone = "Five Room Loop"

type Options struct {
	RunDir       string
	DBPath       string
	MUDHost      string
	MUDPort      int
	Autorecord   bool
	OneWayHillo  bool
	DynamicCrate bool
	HiddenHatch  bool
}

type Result struct {
	SessionID int64                    `json:"session_id"`
	MapSetID  int64                    `json:"map_set_id"`
	Rooms     map[int]storage.EdgeCell `json:"rooms"`
}

func Seed(opts Options) (Result, error) {
	runDir, err := filepath.Abs(opts.RunDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve run directory: %w", err)
	}
	dbPath, err := filepath.Abs(opts.DBPath)
	if err != nil {
		return Result{}, fmt.Errorf("resolve database path: %w", err)
	}
	rel, err := filepath.Rel(runDir, dbPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Result{}, fmt.Errorf("database must be a file inside the E2E run directory")
	}
	if opts.MUDHost != "127.0.0.1" && opts.MUDHost != "localhost" {
		return Result{}, fmt.Errorf("test MUD must use loopback")
	}
	if opts.MUDPort <= 0 || opts.MUDPort > 65535 {
		return Result{}, fmt.Errorf("invalid test MUD port")
	}
	if _, err := os.Stat(dbPath); err == nil {
		return Result{}, fmt.Errorf("database already exists: refusing to reuse E2E state")
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect database path: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create run directory: %w", err)
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		return Result{}, fmt.Errorf("open isolated storage: %w", err)
	}
	defer store.Close()

	session, err := store.EnsureDefaultSession(opts.MUDHost, opts.MUDPort)
	if err != nil {
		return Result{}, fmt.Errorf("create default session: %w", err)
	}
	session.MCCPEnabled = 0
	if err := store.UpdateSession(session); err != nil {
		return Result{}, fmt.Errorf("disable MCCP for fixture: %w", err)
	}

	allRooms := fiveRooms()
	rooms := allRooms
	archive := "e2e-five-room-loop"
	if opts.OneWayHillo {
		rooms = hilloRooms()
		archive = "e2e-hillo-one-way"
	} else if opts.DynamicCrate {
		rooms = crateRooms()
		archive = "e2e-hillo-dynamic-crate"
	} else if opts.HiddenHatch {
		rooms = hatchRooms()
		archive = "e2e-hillo-hidden-delayed-hatch"
	} else if opts.Autorecord {
		// Keep the physical world unchanged but seed only the confirmed anchor.
		// The first E movement therefore lands in a real, complete room that is
		// deliberately absent from the active map set.
		rooms = allRooms[:1]
		archive = "e2e-autorecord-unseeded-target"
	}
	importedID, err := store.CreateMapSet(storage.MapSetInput{
		Name:          "Five Room Loop E2E source",
		SourceArchive: archive,
		ZoneCount:     1,
		RoomCount:     len(rooms),
		Rooms:         rooms,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create five-room source: %w", err)
	}
	mapSetID, err := store.ForkMapSet(importedID)
	if err != nil {
		return Result{}, fmt.Errorf("fork editable fixture: %w", err)
	}
	if err := store.SetActiveMapSetID(session.ID, mapSetID); err != nil {
		return Result{}, fmt.Errorf("activate fixture map: %w", err)
	}
	if opts.HiddenHatch {
		door, delay := true, 2000
		for _, edge := range []struct {
			cell storage.EdgeCell
			dir  string
			cmd  string
		}{
			{storage.EdgeCell{Zone: fiveroom.HatchZone, X: 0, Y: -1, L: 0}, "D", "повернуть рычаг"},
			{storage.EdgeCell{Zone: fiveroom.HatchZone, X: 0, Y: -1, L: -1}, "U", "нажать кнопку"},
		} {
			cmd := edge.cmd
			_, found, applyErr := store.ApplyTopologyOp(mapSetID, storage.TopologyOp{
				Kind: storage.TopoSetEdgeAttrs, Zone: edge.cell.Zone, X: edge.cell.X, Y: edge.cell.Y, L: edge.cell.L, Dir: edge.dir,
				Attrs: storage.EdgeAttrPatch{Door: &door, OpenCmd: &cmd, OpenDelayMS: &delay},
			})
			if applyErr != nil || !found {
				return Result{}, fmt.Errorf("seed delayed hatch %s: found=%t: %w", edge.dir, found, applyErr)
			}
		}
	}

	resultRooms := map[int]storage.EdgeCell{
		1: {Zone: Zone, X: 0, Y: 0, L: 0},
		2: {Zone: Zone, X: 0, Y: 1, L: 0},
		3: {Zone: Zone, X: 0, Y: 2, L: 0},
		4: {Zone: Zone, X: 1, Y: 2, L: 0},
		5: {Zone: Zone, X: 1, Y: 1, L: 0},
	}
	if opts.OneWayHillo {
		resultRooms = map[int]storage.EdgeCell{
			1: {Zone: fiveroom.HilloZone, X: 0, Y: 0, L: 0},
			2: {Zone: fiveroom.HilloZone, X: 0, Y: -1, L: 0},
		}
	}
	if opts.DynamicCrate {
		resultRooms = map[int]storage.EdgeCell{
			1: {Zone: fiveroom.CrateZone, X: 0, Y: 0, L: 0},
			2: {Zone: fiveroom.CrateZone, X: -1, Y: 0, L: 0},
			3: {Zone: fiveroom.CrateZone, X: 0, Y: 0, L: -1},
		}
	}
	if opts.HiddenHatch {
		resultRooms = map[int]storage.EdgeCell{
			1: {Zone: fiveroom.HatchZone, X: 0, Y: 0, L: 0},
			2: {Zone: fiveroom.HatchZone, X: 0, Y: -1, L: 0},
			3: {Zone: fiveroom.HatchZone, X: 0, Y: -1, L: -1},
			4: {Zone: fiveroom.HatchZone, X: -1, Y: 0, L: 0},
			5: {Zone: fiveroom.HatchZone, X: -2, Y: 0, L: 0},
		}
	}

	return Result{
		SessionID: session.ID,
		MapSetID:  mapSetID,
		Rooms:     resultRooms,
	}, nil
}

func fiveRooms() []storage.Room {
	return []storage.Room{
		room(1, 0, 0, "Loop Room 1", "The first chamber has a brass compass rose.", "E", []string{"E"}, 1<<2),
		room(2, 0, 1, "Loop Room 2", "The second chamber is lined with blue tiles.", "E W", []string{"E", "W"}, (1<<2)|(1<<3)),
		room(3, 0, 2, "Loop Room 3", "The third chamber has a narrow southern arch.", "S W", []string{"S", "W"}, (1<<1)|(1<<3)),
		room(4, 1, 2, "Loop Room 4", "The fourth chamber begins a long western tunnel.", "N W", []string{"N", "W"}, (1<<0)|(1<<3)),
		room(5, 1, 1, "Loop Room 5", "The fifth chamber lies below the first chamber.", "E N", []string{"E", "N"}, (1<<0)|(1<<2)),
	}
}

func hilloRooms() []storage.Room {
	return []storage.Room{
		roomInZone(fiveroom.HilloZone, 1, 0, 0, fiveroom.HilloSourceHint, fiveroom.HilloSourceDesc, fiveroom.HilloSourceExits, []string{"N", "W"}, (1<<0)|(1<<3)),
		roomInZone(fiveroom.HilloZone, 2, 0, -1, fiveroom.HilloTargetHint, fiveroom.HilloTargetDesc, fiveroom.HilloTargetExits, []string{"N", "S", "W"}, (1<<0)|(1<<1)|(1<<3)),
	}
}

func crateRooms() []storage.Room {
	return []storage.Room{
		roomInZone(fiveroom.CrateZone, 1, 0, 0, fiveroom.CrateBoxHint, fiveroom.CrateBoxDesc, "N", []string{"N"}, 1<<0),
		roomInZone(fiveroom.CrateZone, 2, -1, 0, fiveroom.CratePassageHint, fiveroom.CratePassageDesc, "S", []string{"S"}, 1<<1),
	}
}

func hatchRooms() []storage.Room {
	return []storage.Room{
		roomAt(fiveroom.HatchZone, 1, 0, 0, 0, fiveroom.HatchTurnHint, "Тропа огибает склон; скрытая дорожка уходит к беседке.", "N E S", []string{"N", "E", "S"}, (1<<0)|(1<<1)|(1<<2)|(1<<3)),
		roomAt(fiveroom.HatchZone, 2, 0, -1, 0, fiveroom.HatchGazeboHint, "Старая беседка скрывает люк с рычагом.", "", nil, (1<<2)|(1<<5)),
		roomAt(fiveroom.HatchZone, 3, 0, -1, -1, fiveroom.HatchDeadEndHint, "Подземный тупик; рядом с люком спрятана кнопка.", "", nil, 1<<4),
		pipeRoomAt(fiveroom.HatchZone, 4, -1, 0, 0, "Узкий тоннель", "Прямой тесный тоннель тянется с севера на юг.", "N S", (1<<0)|(1<<1)),
		roomAt(fiveroom.HatchZone, 5, -2, 0, 0, "Конец тоннеля", "Тоннель заканчивается небольшой площадкой.", "S", []string{"S"}, 1<<1),
	}
}

func pipeRoomAt(zone string, tag, x, y, l int, hint, desc, exits string, ch int) storage.Room {
	room := roomAt(zone, tag, x, y, l, hint, desc, exits, nil, ch)
	room.Pipe = true
	return room
}

func room(tag, x, y int, hint, desc, exits string, edirs []string, ch int) storage.Room {
	return roomInZone(Zone, tag, x, y, hint, desc, exits, edirs, ch)
}

func roomInZone(zone string, tag, x, y int, hint, desc, exits string, edirs []string, ch int) storage.Room {
	return roomAt(zone, tag, x, y, 0, hint, desc, exits, edirs, ch)
}

func roomAt(zone string, tag, x, y, l int, hint, desc, exits string, edirs []string, ch int) storage.Room {
	return storage.Room{
		Zone: zone, X: x, Y: y, L: l,
		Tag: &tag, Hint: hint, Desc: desc, Exits: exits,
		EDirs: mustJSONStrings(edirs), Doors: "[]", Ch: ch,
	}
}

func mustJSONStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return `["` + strings.Join(values, `","`) + `"]`
}
