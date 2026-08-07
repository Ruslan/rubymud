-- Mapper room-format profiles.
--
-- A "mapper profile" is a ROOM TEXT FORMAT description — the exits regex, the
-- block boundaries, the left-column strip and the header rule that the room
-- block detector uses. It has NOTHING to do with the TinTin-style `profiles`
-- table (alias/trigger layering); the two are unrelated entities that merely
-- share an English word, hence the `mapper_` prefix everywhere.
--
-- `spec` holds mapper.ProfileSpec as JSON, exactly as the Go type marshals it.
-- The store re-validates it through mapper.Compile on every write, so a row that
-- cannot be loaded must never exist here.
--
-- `version` is the SHAPE version of that JSON (not a row revision counter): if a
-- a future schema change updates ProfileSpec's field layout, this is what a data migration
-- keys on.
CREATE TABLE IF NOT EXISTS mapper_profiles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    spec       TEXT NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Which room format a session parses with. NULL is NOT "unset pending setup" —
-- it is the DEFAULT and the normal state: it means "use the builtin RMUD profile
-- compiled into the Go binary". The builtin is deliberately NOT a row here, so
-- it cannot be deleted, renamed or half-edited, and there is exactly one source
-- of truth for it (mapper.RMUDSpec).
--
-- ON DELETE SET NULL is a backstop only. SQLite's foreign_keys pragma is
-- per-connection and this project sets it on a pooled *gorm.DB, so enforcement
-- is not guaranteed on every connection — Store.DeleteMapperProfile therefore
-- clears the referencing sessions EXPLICITLY, in the same transaction as the
-- delete, rather than trusting this clause.
ALTER TABLE sessions ADD COLUMN mapper_profile_id INTEGER
    REFERENCES mapper_profiles(id) ON DELETE SET NULL;
