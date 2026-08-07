-- `map_edges` is the authoritative topology.  A seam occurrence needs its own
-- stable source position because duplicate selectors are legal and ids change
-- when derived rows are rebuilt.  Grid rows leave this at zero.
ALTER TABLE map_edges ADD COLUMN seam_order INTEGER NOT NULL DEFAULT 0;

-- Give pre-023 rows a temporary deterministic occurrence before adding the
-- uniqueness constraint. The Go data half later restores exact raw positions
-- (including gaps occupied by formerly discarded malformed entries).
UPDATE map_edges AS edge
SET seam_order = (
    SELECT COUNT(*) - 1
    FROM map_edges AS earlier
    WHERE earlier.kind = 'seam'
      AND earlier.map_set_id = edge.map_set_id
      AND earlier.from_zone = edge.from_zone
      AND earlier.from_x = edge.from_x
      AND earlier.from_y = edge.from_y
      AND earlier.from_l = edge.from_l
      AND earlier.id <= edge.id
)
WHERE edge.kind = 'seam';

CREATE UNIQUE INDEX idx_map_edges_seam_occurrence
    ON map_edges (map_set_id, from_zone, from_x, from_y, from_l, seam_order)
    WHERE kind = 'seam';

-- Migration 020 predates occurrence identity and therefore prevents two identical
-- raw seam occurrences from carrying independent manual attributes. Replace it;
-- the data backfill in storage.Open assigns legacy rows their exact occurrence
-- before normal runtime reads begin.
DROP INDEX IF EXISTS idx_map_edges_manual_seam_unique;
CREATE UNIQUE INDEX idx_map_edges_manual_seam_unique
    ON map_edges (map_set_id, from_zone, from_x, from_y, from_l,
                  target_zone, target_tag, command, seam_order)
    WHERE kind = 'seam' AND source = 'manual';

CREATE INDEX IF NOT EXISTS idx_map_edges_seam_order
    ON map_edges (map_set_id, from_zone, kind, from_l, from_x, from_y, seam_order, id);
