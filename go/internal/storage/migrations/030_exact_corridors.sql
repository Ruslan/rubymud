-- Exact directed corridors own the same directional face as ordinary grid
-- edges.  Refuse an upgrade with legacy duplicate faces before replacing the
-- narrower partial unique index; silently choosing a winner would corrupt the
-- literal before-state required by reconciliation undo.
CREATE TEMP TABLE corridor_face_preflight (
    duplicate_grid_or_corridor_face INTEGER
        CONSTRAINT duplicate_grid_or_corridor_face_must_be_resolved
        CHECK (duplicate_grid_or_corridor_face = 0)
);
INSERT INTO corridor_face_preflight
SELECT 1
WHERE EXISTS (
    SELECT 1 FROM map_edges
    WHERE kind IN ('grid', 'corridor')
    GROUP BY map_set_id, from_zone, from_x, from_y, from_l, dir
    HAVING COUNT(*) > 1
);
DROP TABLE corridor_face_preflight;

DROP INDEX IF EXISTS idx_map_edges_grid_unique;
CREATE UNIQUE INDEX idx_map_edges_directional_face_unique
    ON map_edges (map_set_id, from_zone, from_x, from_y, from_l, dir)
    WHERE kind IN ('grid', 'corridor');
