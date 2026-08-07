-- A manually annotated seam owns its semantic transition, preventing the parsed
-- re-derivation from producing a duplicate route around no_route.
CREATE UNIQUE INDEX IF NOT EXISTS idx_map_edges_manual_seam_unique
    ON map_edges (map_set_id, from_zone, from_x, from_y, from_l, target_zone, target_tag, command)
    WHERE kind = 'seam' AND source = 'manual';
