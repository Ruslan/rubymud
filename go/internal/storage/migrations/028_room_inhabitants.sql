-- Normalized room-knowledge catalogue. This is an overlay: placements are keyed
-- by logical room coordinates and deliberately have no FK to rooms, so knowledge
-- survives a temporary topology delete/undo. Map-set ownership remains strict.

CREATE TABLE IF NOT EXISTS map_inhabitants (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    map_set_id   INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('npc', 'monster', 'other')),
    look_target  TEXT NOT NULL DEFAULT '',
    look_text    TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    strategy     TEXT NOT NULL DEFAULT '',
    updated_at   DATETIME NOT NULL,
    UNIQUE (id, map_set_id)
);

CREATE INDEX IF NOT EXISTS idx_map_inhabitants_set_name
    ON map_inhabitants (map_set_id, name, id);

CREATE TABLE IF NOT EXISTS room_inhabitants (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    map_set_id     INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    zone           TEXT NOT NULL,
    x              INTEGER NOT NULL,
    y              INTEGER NOT NULL,
    l              INTEGER NOT NULL DEFAULT 0,
    inhabitant_id  INTEGER NOT NULL,
    presence_text  TEXT NOT NULL DEFAULT '',
    resident       INTEGER NOT NULL DEFAULT 0 CHECK (resident IN (0, 1)),
    updated_at     DATETIME NOT NULL,
    FOREIGN KEY (inhabitant_id, map_set_id)
        REFERENCES map_inhabitants(id, map_set_id) ON DELETE CASCADE,
    UNIQUE (map_set_id, zone, x, y, l, inhabitant_id)
);

CREATE INDEX IF NOT EXISTS idx_room_inhabitants_entity
    ON room_inhabitants (map_set_id, inhabitant_id);
