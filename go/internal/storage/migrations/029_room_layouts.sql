-- Sparse presentation coordinates. Logical room coordinates remain authoritative
-- for identity, routing and tracker state; absence means display == logical.
CREATE TABLE room_layouts (
    map_set_id INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    zone TEXT NOT NULL,
    x INTEGER NOT NULL,
    y INTEGER NOT NULL,
    l INTEGER NOT NULL DEFAULT 0,
    display_x INTEGER NOT NULL,
    display_y INTEGER NOT NULL,
    display_l INTEGER NOT NULL DEFAULT 0,
    pinned INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (map_set_id, zone, x, y, l)
);
CREATE UNIQUE INDEX idx_room_layouts_display
    ON room_layouts(map_set_id, zone, display_x, display_y, display_l);
CREATE INDEX idx_room_layouts_zone ON room_layouts(map_set_id, zone);
