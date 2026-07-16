-- Mapper feature — read-only auto-map storage (Phase 1).
-- A "map set" is the result of importing one archive of .mm2 map files.
-- Sets are global entities (a session references one via active_map_set_id);
-- this is a deliberate exception to the per-profile layering rule — world maps
-- do not depend on the active profile.

CREATE TABLE IF NOT EXISTS map_sets (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL,
    source_archive TEXT NOT NULL DEFAULT '',
    imported_at    DATETIME,
    zone_count     INTEGER NOT NULL DEFAULT 0,
    room_count     INTEGER NOT NULL DEFAULT 0,
    seam_count     INTEGER NOT NULL DEFAULT 0,
    note           TEXT NOT NULL DEFAULT ''
);

-- rooms — one row per parsed .mm2 room. Surrogate id for clean child FKs.
--   (map_set_id, zone, x, y, l) is the logical room key (unique in the corpus).
--   x/y/l are SIGNED int8 grid coords (negatives kept). dx/dy/dl are the logical
--   "home" coords used for displaced rooms.
--   ch is a 6-bit connectivity bitmask (ChN..ChD) — authoritative over `exits`.
--   edirs/doors/automaps are JSON arrays of text.
--   fingerprint = hash of normalized hint+desc+sorted exits (door markers stripped).
CREATE TABLE IF NOT EXISTS rooms (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    map_set_id  INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    zone        TEXT NOT NULL,
    x           INTEGER NOT NULL,
    y           INTEGER NOT NULL,
    l           INTEGER NOT NULL DEFAULT 0,
    dx          INTEGER NOT NULL DEFAULT 0,
    dy          INTEGER NOT NULL DEFAULT 0,
    dl          INTEGER NOT NULL DEFAULT 0,
    tag         INTEGER,
    hint        TEXT NOT NULL DEFAULT '',
    desc        TEXT NOT NULL DEFAULT '',
    exits       TEXT NOT NULL DEFAULT '',
    edirs       TEXT NOT NULL DEFAULT '[]',
    doors       TEXT NOT NULL DEFAULT '[]',
    ch          INTEGER NOT NULL DEFAULT 0,
    imageindex  INTEGER,
    note        TEXT NOT NULL DEFAULT '',
    is_dt       INTEGER NOT NULL DEFAULT 0,
    pipe        INTEGER NOT NULL DEFAULT 0,
    bcolor      INTEGER,
    automaps    TEXT NOT NULL DEFAULT '[]',
    fingerprint TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_rooms_coord ON rooms (map_set_id, zone, x, y, l);
CREATE INDEX IF NOT EXISTS idx_rooms_fingerprint ON rooms (map_set_id, fingerprint);
CREATE INDEX IF NOT EXISTS idx_rooms_zone ON rooms (map_set_id, zone);

-- room_images — optional "vibe" image per room from an external LLM pipeline.
-- Schema only this round (no images imported yet). thumb is an inline BLOB (<=128px);
-- full-size images live on disk under data/room_images/<map_set_id>/... (full_path).
CREATE TABLE IF NOT EXISTS room_images (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id      INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    thumb        BLOB,
    full_path    TEXT,
    prompt       TEXT,
    model        TEXT,
    seed         INTEGER,
    generated_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_room_images_room ON room_images (room_id);

-- room_annotations — crowdsourced overlay (phase 5 writes this; created now to
-- avoid migration churn). Keyed by logical room coords so it survives on frozen
-- sets without forking. Independent of the topology write-path.
CREATE TABLE IF NOT EXISTS room_annotations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    map_set_id  INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    zone        TEXT NOT NULL,
    x           INTEGER NOT NULL,
    y           INTEGER NOT NULL,
    l           INTEGER NOT NULL DEFAULT 0,
    dt          INTEGER NOT NULL DEFAULT 0,
    hazard      TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    battle_log  TEXT NOT NULL DEFAULT '',
    author      TEXT NOT NULL DEFAULT '',
    updated_at  DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_room_annotations_coord ON room_annotations (map_set_id, zone, x, y, l);

-- Which map set is active for a session. Nullable — a NULL / dangling reference
-- must degrade gracefully (empty map, no crash) everywhere.
ALTER TABLE sessions ADD COLUMN active_map_set_id INTEGER;
