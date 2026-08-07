package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"

	"rubymud/go/internal/mapref"
)

// ---------------------------------------------------------------------------
// Room-format profiles ("mapper profiles") — persistence.
//
// NAMING: `profiles` in this project already means TinTin-style alias/trigger
// layering. THIS entity is a description of one server's ROOM TEXT FORMAT (the
// exits regex, the block boundaries, the left-column strip, the header rule).
// The two are unrelated; everything here carries the mapper_ prefix so no
// caller can confuse them.
//
// THE BUILTINS ARE NOT ROWS. A session with sessions.mapper_format NULL uses
// the default compiled-in detector. That keeps one source of truth for the
// builtins (Go), makes them undeletable and un-corruptible, and gives a
// deleted-profile a safe automatic fallback.
//
// The TEXT sessions.mapper_format reference replaced the INTEGER FK
// sessions.mapper_profile_id (migration 015), because the reference can name
// a compiled-in detector as well as a row. See the migration
// for the scheme and mapper.ParseFormatRef for the parser.
// ---------------------------------------------------------------------------

// MapperSpecVersion is the current ProfileSpec SHAPE version written to new
// rows. It is not a revision counter — it exists so a future change to the spec
// layout has something to migrate on.
const MapperSpecVersion = 1

// MapperProfile is one stored room-format description. Spec holds
// mapper.ProfileSpec as canonical JSON; it is validated through the registered
// compile gate on every write, so a row here always loads.
type MapperProfile struct {
	ID        int64       `gorm:"primaryKey" json:"id"`
	Name      string      `json:"name"`
	Spec      string      `json:"spec"`
	Version   int         `gorm:"default:1" json:"version"`
	CreatedAt *SQLiteTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt *SQLiteTime `gorm:"column:updated_at" json:"updated_at"`
}

func (MapperProfile) TableName() string { return "mapper_profiles" }

// Errors the write path returns to callers that must render them to a human.
var (
	// ErrMapperProfileNameRequired — the name is the row's unique key and the UI's
	// only handle on a profile; an empty one is rejected rather than defaulted.
	ErrMapperProfileNameRequired = errors.New("mapper profile: name is required")
	// ErrMapperProfileNameTaken is returned instead of a raw UNIQUE constraint
	// error so the settings form can show something a user can act on.
	ErrMapperProfileNameTaken = errors.New("mapper profile: a profile with this name already exists")
	// ErrNoMapperSpecValidator means the compile gate was never registered. It can
	// only happen in a binary that links storage WITHOUT linking mapper. Writes
	// fail closed on it: storing an unvalidated spec is exactly the state this
	// table must never reach.
	ErrNoMapperSpecValidator = errors.New("mapper profile: no spec validator registered (the mapper package is not linked in)")
	// ErrMapperProfileNewerVersion guards against an older binary silently
	// downgrading a row a newer one wrote. See UpdateMapperProfile.
	ErrMapperProfileNewerVersion = errors.New("mapper profile: stored spec is from a newer version of this app")
)

// --- the compile gate ------------------------------------------------------

// MapperSpecValidator compile-checks a ProfileSpec JSON blob, returning the
// compiler's verbatim error. See mapper.ValidateSpecJSON.
type MapperSpecValidator func(specJSON string) error

var (
	mapperSpecMu        sync.RWMutex
	mapperSpecValidator MapperSpecValidator
)

// RegisterMapperSpecValidator installs the compile gate every mapper_profiles
// write runs the spec through. The mapper package calls this from its init(),
// because storage cannot import mapper (mapper imports storage). It is
// mutex-guarded rather than a plain assignment only so a test that swaps in a
// stub cannot trip the race detector against a concurrent read.
func RegisterMapperSpecValidator(v MapperSpecValidator) {
	mapperSpecMu.Lock()
	mapperSpecValidator = v
	mapperSpecMu.Unlock()
}

// ValidateMapperSpec runs the registered compile gate. Exported so the
// validate-only REST endpoint checks a draft with EXACTLY the gate the writes
// use — one validation truth, never a reimplementation.
func ValidateMapperSpec(specJSON string) error {
	mapperSpecMu.RLock()
	v := mapperSpecValidator
	mapperSpecMu.RUnlock()
	if v == nil {
		return ErrNoMapperSpecValidator
	}
	return v(specJSON)
}

// --- CRUD ------------------------------------------------------------------

// ListMapperProfiles returns the stored profiles ordered by name (stable UI
// order). The builtin is NOT included — it is not a row; the REST layer prepends
// it as a synthetic read-only entry.
func (s *Store) ListMapperProfiles() ([]MapperProfile, error) {
	var rows []MapperProfile
	// COLLATE NOCASE, not a bare ORDER BY: SQLite's default BINARY collation sorts
	// by code point, so "Zebra" lands before "apple". NOCASE only case-folds
	// ASCII, so Cyrillic names still sort after Latin ones by code point — fixing
	// that properly needs an ICU collation this build does not carry. This is the
	// improvement available without one.
	err := s.db.Order("name COLLATE NOCASE ASC").Find(&rows).Error
	if rows == nil {
		rows = []MapperProfile{}
	}
	return rows, err
}

// GetMapperProfile returns one profile, or (zero,false,nil) when it does not
// exist. A missing profile is NOT an error: an assignment can dangle (a row
// deleted while a session held it) and every caller must degrade to the builtin
// rather than fail.
func (s *Store) GetMapperProfile(id int64) (MapperProfile, bool, error) {
	var row MapperProfile
	err := s.db.First(&row, id).Error
	if err == gorm.ErrRecordNotFound {
		return MapperProfile{}, false, nil
	}
	if err != nil {
		return MapperProfile{}, false, err
	}
	return row, true, nil
}

// CreateMapperProfile stores a new room format. The spec is compile-checked
// first: the DB must never hold a spec that cannot be loaded, because a session
// that meets one silently stops mapping.
func (s *Store) CreateMapperProfile(name, specJSON string) (MapperProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MapperProfile{}, ErrMapperProfileNameRequired
	}
	if err := ValidateMapperSpec(specJSON); err != nil {
		return MapperProfile{}, err
	}
	now := nowSQLiteTimePtr()
	row := MapperProfile{
		Name:      name,
		Spec:      specJSON,
		Version:   MapperSpecVersion,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.db.Create(&row).Error; err != nil {
		return MapperProfile{}, mapperNameConflict(err)
	}
	return row, nil
}

// UpdateMapperProfile rewrites a profile's name and spec, validating the spec
// exactly as create does. Returns found=false (no error) when the id is gone, so
// a stale settings tab soft-fails instead of 500ing.
func (s *Store) UpdateMapperProfile(id int64, name, specJSON string) (MapperProfile, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MapperProfile{}, false, ErrMapperProfileNameRequired
	}
	if err := ValidateMapperSpec(specJSON); err != nil {
		return MapperProfile{}, false, err
	}
	row, found, err := s.GetMapperProfile(id)
	if err != nil || !found {
		return MapperProfile{}, found, err
	}
	if row.Version > MapperSpecVersion {
		// The row was written by a NEWER build whose ProfileSpec has fields this
		// binary does not know. ParseSpecJSON ignores unknown keys, so saving here
		// would silently strip them AND stamp version back down to ours, erasing the
		// only marker a future data migration keys on. Refuse instead.
		return MapperProfile{}, true, fmt.Errorf(
			"%w: profile %q is version %d, this build understands up to %d — editing it here would silently drop the newer fields",
			ErrMapperProfileNewerVersion, row.Name, row.Version, MapperSpecVersion)
	}
	row.Name = name
	row.Spec = specJSON
	row.Version = MapperSpecVersion
	row.UpdatedAt = nowSQLiteTimePtr()
	if err := s.db.Save(&row).Error; err != nil {
		return MapperProfile{}, true, mapperNameConflict(err)
	}
	return row, true, nil
}

// DeleteMapperProfile removes a profile and CLEARS it off every session that
// referenced it, in one transaction, so those sessions fall back to the builtin.
// It returns the ids of the sessions it cleared, so the caller can push a live
// re-apply to exactly those.
//
// The affected ids are collected INSIDE the transaction, immediately before the
// clear. Collecting them beforehand (as a first cut did) loses any session
// assigned in the window between the two reads: it gets its column nulled but
// never gets the live re-apply, so it keeps parsing with a profile that no
// longer exists until it reconnects.
//
// 014 declared ON DELETE SET NULL, but that was a backstop only: SQLite's
// foreign_keys pragma is PER CONNECTION and this project sets it once on a
// pooled *gorm.DB, so FK actions were not guaranteed to fire on whichever
// connection served this statement. The explicit UPDATE was always the real
// mechanism. Migration 015 replaced the FK column with a TEXT reference and so
// removed the backstop entirely; this UPDATE (now clearing `mapper_format`) is
// therefore the ONLY thing standing between a delete and a session pointing at a
// vanished id.
//
// Returns found=false when the id was already gone.
func (s *Store) DeleteMapperProfile(id int64) (bool, []int64, error) {
	var found bool
	var affected []int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var row MapperProfile
		err := tx.First(&row, id).Error
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		ref := MapperFormatDBRef(id)
		if err := tx.Model(&SessionRecord{}).
			Where("mapper_format = ?", ref).
			Order("id ASC").
			Pluck("id", &affected).Error; err != nil {
			return err
		}
		if err := tx.Model(&SessionRecord{}).
			Where("mapper_format = ?", ref).
			Update("mapper_format", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&MapperProfile{}, id).Error
	})
	return found, affected, err
}

// mapperNameConflict maps SQLite's UNIQUE violation on mapper_profiles.name to a
// message a user can act on, leaving every other error untouched.
func mapperNameConflict(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") && strings.Contains(msg, "name") {
		return ErrMapperProfileNameTaken
	}
	return err
}

// --- per-session assignment ------------------------------------------------

// MapperFormatDBRef renders the sessions.mapper_format value for a profile row.
//
// It delegates to `mapref`, which is a dependency-free leaf package precisely so
// this file does not have to re-declare the "db:" prefix. `mapper` imports
// `storage`, so `storage` can never import `mapper`; before mapref existed the
// prefix was copied here with a comment claiming a cross-package test pinned the
// two — that test never existed. Now there is one definition and nothing to pin.
func MapperFormatDBRef(id int64) string { return mapref.FormatRefForDB(id) }

// GetSessionMapperFormat returns a session's mapper_format reference, or "" when
// it is NULL or the session does not exist. "" is the normal default and means
// "use the default builtin detector" — never an error.
func (s *Store) GetSessionMapperFormat(sessionID int64) (string, error) {
	var val sql.NullString
	row := s.db.Model(&SessionRecord{}).
		Where("id = ?", sessionID).
		Select("mapper_format").Row()
	if err := row.Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	if !val.Valid {
		return "", nil
	}
	return val.String, nil
}

// SetSessionMapperFormat assigns (or clears, when ref is empty) a session's room
// format reference. Only the one column is written so a concurrent
// status/connection update is not clobbered — same reason as SetActiveMapSetID.
//
// It does NOT validate the reference: it is the low-level setter used by tests
// and by callers that have already resolved what they are writing. The
// validating path is AssignSessionMapperFormat.
func (s *Store) SetSessionMapperFormat(sessionID int64, ref string) error {
	var val any
	if strings.TrimSpace(ref) != "" {
		val = ref
	} else {
		val = nil
	}
	return s.db.Model(&SessionRecord{}).
		Where("id = ?", sessionID).
		Update("mapper_format", val).Error
}

// AssignSessionMapperFormat sets a session's room format reference, checking
// that the session exists and — for a `db:<id>` reference — that the row exists,
// atomically with the write.
//
// dbID is the row id the caller already parsed out of ref (0 for a builtin
// reference or a clear). Passing it in rather than re-parsing here is what keeps
// mapper.ParseFormatRef the single parser for the column: storage never has to
// know the scheme beyond the "db:" prefix it renders.
//
// The existence check must not be a separate statement from the write: a DELETE
// landing between "does profile 7 exist?" and "set mapper_format = 'db:7'"
// leaves the column pointing at a vanished row permanently. With the FK now gone
// (migration 015) nothing else would catch it at all — and the FK never reliably
// did, since the pragma is per-connection on a pooled DB. So the existence test
// is an EXISTS subquery inside the UPDATE itself, evaluated by SQLite as one
// statement, and RowsAffected is the authority on what happened.
//
// Pass an empty ref to clear the assignment (= use the default builtin), which
// needs no existence check.
//
// Returns (sessionFound, refFound): the caller renders a 404 for the first and a
// 400 for the second.
func (s *Store) AssignSessionMapperFormat(sessionID int64, ref string, dbID int64) (bool, bool, error) {
	var sessionFound, refFound bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&SessionRecord{}).Where("id = ?", sessionID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		sessionFound = true

		if strings.TrimSpace(ref) == "" {
			refFound = true
			return tx.Model(&SessionRecord{}).
				Where("id = ?", sessionID).
				Update("mapper_format", nil).Error
		}

		if dbID <= 0 {
			// A builtin reference. Its existence was checked by the caller against the
			// in-process registry (the only place that knows), not by the DB.
			refFound = true
			return tx.Model(&SessionRecord{}).
				Where("id = ?", sessionID).
				Update("mapper_format", ref).Error
		}

		res := tx.Exec(`UPDATE sessions SET mapper_format = ?
			WHERE id = ? AND EXISTS (SELECT 1 FROM mapper_profiles WHERE id = ?)`,
			ref, sessionID, dbID)
		if res.Error != nil {
			return res.Error
		}
		// 0 rows with a known-present session means the EXISTS failed: the profile
		// is gone (possibly deleted a microsecond ago). Nothing was written.
		refFound = res.RowsAffected > 0
		return nil
	})
	return sessionFound, refFound, err
}

// SessionIDsUsingMapperProfile lists the sessions assigned to a profile ROW. The
// web layer uses it to push a LIVE re-apply to exactly those sessions when the
// profile is edited or deleted (AGENTS #2) instead of restarting anything.
func (s *Store) SessionIDsUsingMapperProfile(id int64) ([]int64, error) {
	var ids []int64
	err := s.db.Model(&SessionRecord{}).
		Where("mapper_format = ?", MapperFormatDBRef(id)).
		Order("id ASC").
		Pluck("id", &ids).Error
	return ids, err
}
