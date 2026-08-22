package storage

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// Migration 018 — moving rooms.doors onto the edges.
//
// The column is added by SQL, but its VALUE is not: doors are derived from
// rooms.doors by the one Go rule (storage.deriveGridEdge), and restating that rule
// in the migration would create the second implementation materialization exists
// to remove. So Open re-materializes every set's derived rows exactly
// once, in the session that applies 018.
//
// That "exactly once, on an EXISTING database" is the part with no other owner: a
// fresh database derives doors directly and never needs it, so a test that only
// exercised the fresh path would pass with the backfill deleted. This one starts
// from a database whose edges were written before the column existed.
// ---------------------------------------------------------------------------

// TestMigration018BackfillsDoorsOntoExistingEdges builds a pre-018 database by
// hand — 001..017 applied, rooms and map_edges rows written the way the previous
// binary wrote them, with no door column anywhere — then opens it through the real
// Open() and checks the doors arrived.
func TestMigration018BackfillsDoorsOntoExistingEdges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre018.db")

	// --- the pre-018 database ------------------------------------------------
	func() {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatalf("gorm.Open: %v", err)
		}
		sqlDB, _ := db.DB()
		defer sqlDB.Close()

		if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY);`).Error; err != nil {
			t.Fatalf("create schema_migrations: %v", err)
		}
		// Fence 018/019 the same way runMigrations skips an applied file, so what we get
		// really is "the schema after 017".
		if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, "018_map_edge_attrs.sql").Error; err != nil {
			t.Fatalf("fence 018: %v", err)
		}
		if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, "019_map_edge_weight.sql").Error; err != nil {
			t.Fatalf("fence 019: %v", err)
		}
		if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, "026_drop_rooms_automaps.sql").Error; err != nil {
			t.Fatalf("fence 026: %v", err)
		}
		if err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, "025b_map_edge_open_delay.sql").Error; err != nil {
			t.Fatalf("fence 025b: %v", err)
		}
		if err := runMigrations(db); err != nil {
			t.Fatalf("migrate to 017: %v", err)
		}
		if edgeHasColumn(t, db, "door") {
			t.Fatal("fencing 018 did not work — map_edges already has the door column")
		}

		// Two rooms with a two-way passage; the north one records a door on the face
		// it shares with the other. Raw SQL, because the Go structs already know about
		// the 018 columns and could not write this shape.
		if err := db.Exec(`INSERT INTO map_sets (id, name) VALUES (1, 'pre-018')`).Error; err != nil {
			t.Fatalf("insert map set: %v", err)
		}
		if err := db.Exec(`INSERT INTO rooms (map_set_id, zone, x, y, l, hint, exits, edirs, doors, ch, automaps)
			VALUES (1,'Alpha',0,0,0,'a','(S)','["S"]','["S"]',2,'[]'),
			       (1,'Alpha',1,0,0,'b','N','["N"]','[]',1,'[]')`).Error; err != nil {
			t.Fatalf("insert rooms: %v", err)
		}
		if err := db.Exec(`INSERT INTO map_edges (map_set_id, kind, source, passable, from_zone, from_x, from_y, from_l, dir, to_zone, to_x, to_y, to_l)
			VALUES (1,'grid','parsed',1,'Alpha',0,0,0,'S','Alpha',1,0,0),
			       (1,'grid','parsed',1,'Alpha',1,0,0,'N','Alpha',0,0,0)`).Error; err != nil {
			t.Fatalf("insert edges: %v", err)
		}
		// Lift the fence so the next open really applies 018.
		if err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, "018_map_edge_attrs.sql").Error; err != nil {
			t.Fatalf("unfence 018: %v", err)
		}
		if err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, "019_map_edge_weight.sql").Error; err != nil {
			t.Fatalf("unfence 019: %v", err)
		}
		if err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, "026_drop_rooms_automaps.sql").Error; err != nil {
			t.Fatalf("unfence 026: %v", err)
		}
		if err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, "025b_map_edge_open_delay.sql").Error; err != nil {
			t.Fatalf("unfence 025b: %v", err)
		}
	}()

	// --- the upgrade ---------------------------------------------------------
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open (applying 018): %v", err)
	}
	defer store.Close()

	fwd := faceOf(t, store, 1, "Alpha", 0, 0, 0, "S")
	if fwd == nil {
		t.Fatal("the upgrade lost the a->b row")
	}
	if fwd.Weight != 1 || fwd.EffectiveWeight() != 1 {
		t.Errorf("019 did not preserve old row default weight: %+v", fwd.EdgeAttrs)
	}
	if !fwd.Door {
		t.Errorf("018 left the door in rooms.doors and not on the edge: %+v", fwd.EdgeAttrs)
	}
	back := faceOf(t, store, 1, "Alpha", 1, 0, 0, "N")
	if back == nil {
		t.Fatal("the upgrade lost the b->a row")
	}
	if back.Door {
		t.Errorf("the backfill invented a door on the side that records none: %+v", back.EdgeAttrs)
	}
}

func edgeHasColumn(t *testing.T, db *gorm.DB, col string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM pragma_table_info('map_edges') WHERE name = ?`, col).
		Scan(&n).Error; err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	return n > 0
}

// TestDoorBackfillRetriesAfterAnInterruptedRun: the migration commits in its own
// transaction and the data half runs after it, so a crash in between used to be
// PERMANENT — 018 stayed recorded, the backfill never ran again, and the faces it
// had not reached kept no door for ever (with `--rematerialize-edges` unable to
// repair the hand-drawn ones). The completion flag is therefore separate from the
// migration, and its absence is what schedules the work.
func TestDoorBackfillRetriesAfterAnInterruptedRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// A set whose edges predate the door column, created AFTER the first Open — i.e.
	// exactly the state a crash between migration and backfill would leave behind.
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := store.db.Exec(`UPDATE map_edges SET door = 0 WHERE map_set_id = ?`, id).Error; err != nil {
		t.Fatalf("clear doors: %v", err)
	}
	// Simulate the interrupted run: 018 is applied, the flag is not set.
	if err := store.db.Exec(`DELETE FROM app_settings WHERE "key" = ?`, edgeDoorsBackfilledKey).Error; err != nil {
		t.Fatalf("clear flag: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The next start must pick the work up again.
	store2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer store2.Close()
	if e := faceOf(t, store2, id, "Alpha", 0, 0, 0, "S"); e == nil || !e.Door {
		t.Errorf("an interrupted backfill was never retried: %+v", e)
	}
	if done, err := store2.GetSetting(edgeDoorsBackfilledKey); err != nil || done == "" {
		t.Errorf("the completion flag was not written: %q (%v)", done, err)
	}
}

// ...and once the flag is set it does NOT run again, so the cost is paid once.
func TestDoorBackfillDoesNotRepeatOnceComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := store.CreateMapSet(attrsWorldInput())
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	// Something the backfill WOULD undo if it ran: a door cleared by hand on a face
	// rooms.doors names. A second run would restore it; a skipped run leaves it.
	if err := store.db.Exec(
		`UPDATE map_edges SET door = 0 WHERE map_set_id = ? AND dir = 'S' AND from_x = 0`, id).Error; err != nil {
		t.Fatalf("clear one door: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	store2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer store2.Close()
	if e := faceOf(t, store2, id, "Alpha", 0, 0, 0, "S"); e == nil || e.Door {
		t.Errorf("the backfill ran a second time (the flag did not stop it): %+v", e)
	}
}
