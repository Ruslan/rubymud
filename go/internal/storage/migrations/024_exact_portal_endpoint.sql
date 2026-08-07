-- Exact portals are deleted with either endpoint room. Room snapshots therefore
-- look up manual portals arriving at a coordinate on every topology write. Keep
-- that lookup proportional to the handful of incoming portals, not to every edge
-- in a large map set.
CREATE INDEX IF NOT EXISTS idx_map_edges_exact_portal_to
    ON map_edges (map_set_id, to_zone, to_x, to_y, to_l)
    WHERE kind = 'seam' AND source = 'manual' AND target_tag IS NULL;
