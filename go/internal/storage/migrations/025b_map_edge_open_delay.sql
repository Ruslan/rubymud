-- A bounded, directed, non-blocking pause after an edge's opening action and
-- before its movement command. Zero preserves the historical immediate write.
--
-- This sorts before 026 because that migration re-materializes legacy edges
-- through the current Go model and therefore needs every current edge column.
ALTER TABLE map_edges ADD COLUMN open_delay_ms INTEGER NOT NULL DEFAULT 0
    CHECK (typeof(open_delay_ms) = 'integer' AND open_delay_ms BETWEEN 0 AND 30000);

-- Keep the hot attribute scan sparse. The read predicates repeat this WHERE.
DROP INDEX IF EXISTS idx_map_edges_attrs;
CREATE INDEX idx_map_edges_attrs ON map_edges (map_set_id, from_zone, id)
    WHERE weight <> 1 OR door = 1 OR locked = 1 OR no_route = 1
       OR phrase <> '' OR open_cmd <> '' OR open_delay_ms <> 0;
