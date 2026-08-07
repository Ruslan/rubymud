// Package mapref holds the room-format REFERENCE scheme: the vocabulary of the
// sessions.mapper_format column, and nothing else.
//
// WHY IT IS ITS OWN PACKAGE. The reference has three consumers that cannot all
// import one another: `mapper` (resolves a reference to a detector), `storage`
// (matches on it in SQL, and cannot import `mapper` because `mapper` imports
// `storage`), and the web layer. Earlier code solved that by duplicating the
// "db:" literal into storage with a comment claiming a test pinned the two
// together. That test did not exist, and the prefix ended up in three places.
//
// This package is the fix: it has NO dependencies beyond strconv/strings, so
// everything can import it, and there is exactly one definition of each prefix.
// `mapper` re-exports the whole surface, so callers that already say
// mapper.ParseFormatRef keep working.
//
// The fourth copy is the SQL literal in migration 015 ('db:' || mapper_profile_id).
// That one is frozen history — an applied migration must never be rewritten — so
// it cannot import anything. TestMigration015LiteralMatchesFormatRefForDB pins it
// instead.
package mapref

import (
	"strconv"
	"strings"
)

// DefaultDetectorKey is the detector a session with no stored reference uses.
//
// It MUST stay "rmud": a NULL sessions.mapper_format is the normal state of
// every existing installation, and changing this would silently re-parse every
// one of them with different rules.
const DefaultDetectorKey = "rmud"

// Reference schemes.
const (
	// RefBuiltinPrefix names a registered detector: `builtin:rmud`.
	RefBuiltinPrefix = "builtin:"
	// RefDBPrefix names a user row in mapper_profiles: `db:7`.
	RefDBPrefix = "db:"
)

// RefKind is what a parsed reference points at.
type RefKind int

const (
	// RefDefault is the empty/NULL reference: use DefaultDetectorKey. This is the
	// normal state of every existing session.
	RefDefault RefKind = iota
	// RefBuiltin names a registered detector by key.
	RefBuiltin
	// RefDB names a mapper_profiles row by id.
	RefDB
)

// ParseFormatRef splits a stored reference into its kind and payload.
//
// It is DELIBERATELY LENIENT — it accepts " db:007 " and "builtin: rmud " —
// because the column can be hand-edited and a session that refuses to parse
// rooms is worse than one parsing them with the default rules.
//
// That leniency makes canonicalization mandatory on the WRITE path. `storage`
// matches this column by exact string (delete-clears-sessions, and the live
// re-apply after an edit), so storing a non-canonical "db:007" while
// FormatRefForDB renders "db:7" silently breaks both: editing the profile never
// reaches the live session, and deleting it leaves the session pointing at a row
// that is gone. Every writer must re-render through BuiltinRef/FormatRefForDB
// after parsing rather than storing the raw input.
//
// An unrecognized or malformed reference returns ok=false. Callers treat that as
// "fall back to the default and say so" — never as a hard failure.
func ParseFormatRef(ref string) (kind RefKind, key string, id int64, ok bool) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return RefDefault, DefaultDetectorKey, 0, true
	case strings.HasPrefix(ref, RefBuiltinPrefix):
		key = strings.TrimSpace(strings.TrimPrefix(ref, RefBuiltinPrefix))
		if key == "" {
			return RefDefault, "", 0, false
		}
		return RefBuiltin, key, 0, true
	case strings.HasPrefix(ref, RefDBPrefix):
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(ref, RefDBPrefix)), 10, 64)
		if err != nil || n <= 0 {
			return RefDefault, "", 0, false
		}
		return RefDB, "", n, true
	}
	return RefDefault, "", 0, false
}

// BuiltinRef renders the CANONICAL reference for a registered detector key.
func BuiltinRef(key string) string { return RefBuiltinPrefix + key }

// FormatRefForDB renders the CANONICAL reference for a mapper_profiles row id.
// This is the single definition of the form migration 015 writes as
// `'db:' || mapper_profile_id`.
func FormatRefForDB(id int64) string {
	return RefDBPrefix + strconv.FormatInt(id, 10)
}

// Canonical re-renders a parsed reference in its canonical form. It is the ONE
// call a write path needs: parse, check, then store Canonical's result.
// Returns "" for the default (which is stored as SQL NULL).
func Canonical(kind RefKind, key string, id int64) string {
	switch kind {
	case RefBuiltin:
		return BuiltinRef(key)
	case RefDB:
		return FormatRefForDB(id)
	default:
		return ""
	}
}
