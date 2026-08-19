// Package mapperfixture creates an isolated five-room mapper database for the
// live reconciliation E2E. It is test infrastructure, not a production seed.
package mapperfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rubymud/go/internal/storage"
)

const Zone = "Five Room Loop"

type Options struct {
	RunDir  string
	DBPath  string
	MUDHost string
	MUDPort int
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

	rooms := fiveRooms()
	importedID, err := store.CreateMapSet(storage.MapSetInput{
		Name:          "Five Room Loop E2E source",
		SourceArchive: "e2e-five-room-loop",
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

	return Result{
		SessionID: session.ID,
		MapSetID:  mapSetID,
		Rooms: map[int]storage.EdgeCell{
			1: {Zone: Zone, X: 0, Y: 0, L: 0},
			2: {Zone: Zone, X: 0, Y: 1, L: 0},
			3: {Zone: Zone, X: 0, Y: 2, L: 0},
			4: {Zone: Zone, X: 1, Y: 2, L: 0},
			5: {Zone: Zone, X: 1, Y: 1, L: 0},
		},
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

func room(tag, x, y int, hint, desc, exits string, edirs []string, ch int) storage.Room {
	return storage.Room{
		Zone: Zone, X: x, Y: y, L: 0,
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
