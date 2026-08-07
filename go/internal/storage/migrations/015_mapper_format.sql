-- Mapper room FORMAT reference.
--
-- 014 gave a session an INTEGER FK into mapper_profiles. That shape can only
-- name a ROW, and the formats a session most often wants are deliberately not
-- rows: the builtin declarative profiles (rmud, diku-en) and the contributed Go
-- detectors are compiled into the binary, so there is nothing for an FK to point
-- at. One TEXT column with a scheme prefix names all three:
--
--     NULL           -> the default builtin ("rmud"). This is the normal state
--                       of every existing session and MUST keep parsing exactly
--                       as it did before this migration.
--     'builtin:<key>' -> a registered detector: a declarative builtin OR a
--                       contributed Go detector. <key> is mapper.DetectorInfo.Key.
--     'db:<id>'      -> a user-written row in mapper_profiles.
--
-- mapper.ParseFormatRef is the one parser for this column; an unrecognized value
-- degrades to the default with a visible warning rather than failing a session.
--
-- WHY THIS IS A NEW FILE AND 014 WAS NOT REWRITTEN
-- Migrations are recorded by FILENAME in schema_migrations. 014 is committed and
-- may already have been applied on a live database; a rewritten 014 would be
-- skipped there forever, so the change would silently never happen on exactly
-- the installations that already have data. Hence 015. It is written to be
-- correct in both orders, and since the runner sorts by filename there is only
-- one order that can occur:
--   * 014 applied earlier, 015 applied now  -> mapper_profile_id exists, may hold
--     assignments; they are carried over below.
--   * fresh DB, 014 and 015 run back to back -> mapper_profile_id exists and is
--     empty; the UPDATE below matches no rows and the DROP is a no-op cleanup.
-- Either way 015 runs after 014, so the column it reads is always present.

ALTER TABLE sessions ADD COLUMN mapper_format TEXT;

-- Carry over any existing assignment. NULL stays NULL: NULL is the DEFAULT, not
-- "unset pending setup", and rewriting it to an explicit 'builtin:rmud' would
-- pin every existing session to today's default forever instead of letting it
-- follow the default.
UPDATE sessions
   SET mapper_format = 'db:' || mapper_profile_id
 WHERE mapper_profile_id IS NOT NULL;

-- Drop the old column. Losing the FK costs nothing that was real: SQLite's
-- foreign_keys pragma is per-connection and this project sets it on a pooled
-- *gorm.DB, so ON DELETE SET NULL was never guaranteed to fire. The actual
-- mechanism was always Store.DeleteMapperProfile clearing referencing sessions
-- EXPLICITLY inside the delete transaction, and that is kept (it now clears the
-- TEXT column instead).
--
-- DROP COLUMN needs SQLite >= 3.35; the pinned driver (glebarez/go-sqlite ->
-- modernc) bundles 3.50.3, verified, and no index, view or trigger references
-- this column.
ALTER TABLE sessions DROP COLUMN mapper_profile_id;
