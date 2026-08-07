-- map_edges — materialized topology.
--
-- Before this migration connectivity existed nowhere in the schema: it was
-- re-derived on every index build from grid adjacency + rooms.ch/rooms.edirs
-- (mapper.IndexRoom.ConnectsTo), and inter-zone seams lived as "Zone|command|tag"
-- strings in rooms.automaps resolved at walk time. This table makes both first
-- class, so an edge gets an IDENTITY that future topology attributes can use for
-- on (hidden exits, trigger phrases, locked doors).
--
-- ONE ROW = ONE TRAVERSAL IN ONE DIRECTION. A two-way passage is two rows, one
-- per side. That is not bookkeeping overhead: every future edge attribute
-- is a property of the SIDE you approach from, not of the passage — the trigger
-- phrase differs per direction ("двигать ящик" goes down, "лезть ковёр" goes up
-- through the same hole), a door can be locked from one side and open from the
-- other, and a hidden exit can be hidden one way only. A single row would carry
-- every one of those as an fwd/rev pair, i.e. the same two directed edges stitched
-- together by hand. Directedness also buys what the model cannot express today at
-- all: ONE-WAY passages. (Today it derives none — ConnectsTo is symmetric by
-- construction, and the live corpus has zero asymmetric pairs — but nothing in the
-- schema now stands in the way.) Seams were already one-way and need no exception.
--
-- Grouping the two sides back into "one physical object" (a nullable passage_id,
-- or mutual references) is deliberately NOT modelled: no consumer needs it yet.
--
-- ENDPOINTS ARE LOGICAL COORDS, NOT rooms.id. rooms.id is a surrogate that does
-- not survive the operations edges must survive: ForkMapSet re-keys every room,
-- undo of a delete re-creates a room with a fresh id, and re-import renumbers
-- everything. Every relation in this schema that must outlive those (room_annota-
-- tions) is already keyed by (map_set_id, zone, x, y, l), and the whole topology
-- write-path (TopologyOp, RoomSnapshot, undo) addresses cells by coord. Keying
-- edges the same way makes a fork a plain map_set_id re-key and undo a plain
-- re-derivation, with no id remapping anywhere.
--
--   kind   'grid' — a step between two grid-adjacent cells of one zone.
--          'seam' — an inter-zone jump: `command` is the MUD command, the target
--                   is kept SYMBOLICALLY (target_zone + target_tag, which survives
--                   a re-import of the target zone with different geometry) AND
--                   resolved into to_* at materialization time.
--   source 'parsed' — derived from the room's own exit data (ch/edirs) or from an
--                     automaps entry;
--          'grid'   — the permissive grid fallback (BOTH endpoints carry no exit
--                     data at all, so adjacency alone connects them);
--          'manual' — hand-drawn. Never produced or destroyed by the
--                     re-derivation path, which only touches non-manual rows.
--   to_zone/to_x/to_y/to_l  the RESOLVED target cell; NULL means the edge leads
--          nowhere known — a dangling exit (rooms.edirs points at an unmapped
--          cell) or a dead seam (its zone/tag does not exist). Such a row is not
--          traversable and changes no route; it exists so those cases stop being
--          silently dropped and become a query instead of a walk.
--
-- FK ON DELETE CASCADE mirrors rooms: PRAGMA foreign_keys is per-connection on a
-- pooled *gorm.DB and is not guaranteed here, so the cascade documents intent
-- while DeleteMapSet deletes edges explicitly, exactly as it already does rooms.
CREATE TABLE IF NOT EXISTS map_edges (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    map_set_id  INTEGER NOT NULL REFERENCES map_sets(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL DEFAULT 'grid',
    source      TEXT NOT NULL DEFAULT 'parsed',
    from_zone   TEXT NOT NULL,
    from_x      INTEGER NOT NULL,
    from_y      INTEGER NOT NULL,
    from_l      INTEGER NOT NULL DEFAULT 0,
    dir         TEXT NOT NULL DEFAULT '',
    to_zone     TEXT,
    to_x        INTEGER,
    to_y        INTEGER,
    to_l        INTEGER,
    command     TEXT NOT NULL DEFAULT '',
    target_zone TEXT NOT NULL DEFAULT '',
    target_tag  INTEGER
);

-- The index build reads a WHOLE set in materialization order (id), on every
-- reconnect and after every topology write. Without this index that read is a
-- filtered scan plus a temp b-tree sorting ~19k rows.
CREATE INDEX IF NOT EXISTS idx_map_edges_set_id ON map_edges (map_set_id, id);

-- "Every edge leaving this cell" — the write-path recompute deletes and re-derives
-- by from-cell, and it is the natural shape of "what are this room's exits" in SQL.
-- There is deliberately NO index on the to_* side: nothing queries edges by target
-- yet, and an unused index is write cost on every materialization. The dead-seam /
-- dangling-exit queries (to_zone IS NULL) scan a set, which is cheap and rare; if
-- an arrival-side lookup ever appears, it comes back with it.
CREATE INDEX IF NOT EXISTS idx_map_edges_from ON map_edges (map_set_id, from_zone, from_x, from_y, from_l);

-- One grid row per (set, from-cell, direction) — a cell cannot leave twice the same
-- way. Partial, because seams legitimately repeat: a room may carry several
-- automaps entries, all with dir ''.
CREATE UNIQUE INDEX IF NOT EXISTS idx_map_edges_grid_unique
    ON map_edges (map_set_id, from_zone, from_x, from_y, from_l, dir)
    WHERE kind = 'grid';
