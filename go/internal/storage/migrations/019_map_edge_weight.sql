-- Directed routing weight. Existing rows retain the old one-hop cost.
ALTER TABLE map_edges ADD COLUMN weight REAL NOT NULL DEFAULT 1
    CHECK (
        typeof(weight) IN ('integer', 'real')
        AND weight > 0
        AND weight <= 1.7976931348623157e308
    );

-- The filtered attribute reads in ListMapEdges/ListSlimRooms repeat this predicate
-- verbatim, so non-default weights stay off their hot full-edge scans.
DROP INDEX IF EXISTS idx_map_edges_attrs;
CREATE INDEX idx_map_edges_attrs ON map_edges (map_set_id, from_zone, id)
    WHERE weight <> 1 OR door = 1 OR locked = 1 OR no_route = 1 OR phrase <> '' OR open_cmd <> '';
