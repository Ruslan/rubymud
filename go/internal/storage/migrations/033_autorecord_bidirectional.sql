-- Early OBS builds encoded the unobserved reverse side of every newly recorded
-- passage as a reconciled no-passage tombstone. Autorecord is two-way by
-- default; one-way is an explicit later edge edit. Repair only the distinctive
-- generated pair: a blocked grid face whose exact reverse is an autorecord
-- corridor. Manual negations and reconciliation tombstones without that pair
-- are untouched.
UPDATE map_edges AS blocked
SET kind = 'corridor',
    source = 'autorecord',
    passable = 1,
    command = lower(blocked.dir)
WHERE blocked.kind = 'grid'
  AND blocked.source = 'reconciled'
  AND blocked.passable = 0
  AND blocked.to_zone IS NOT NULL
  AND blocked.to_x IS NOT NULL
  AND blocked.to_y IS NOT NULL
  AND blocked.to_l IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM map_edges AS reverse
    WHERE reverse.map_set_id = blocked.map_set_id
      AND reverse.kind = 'corridor'
      AND reverse.source = 'autorecord'
      AND reverse.passable = 1
      AND reverse.from_zone = blocked.to_zone
      AND reverse.from_x = blocked.to_x
      AND reverse.from_y = blocked.to_y
      AND reverse.from_l = blocked.to_l
      AND reverse.to_zone = blocked.from_zone
      AND reverse.to_x = blocked.from_x
      AND reverse.to_y = blocked.from_y
      AND reverse.to_l = blocked.from_l
      AND reverse.dir = CASE blocked.dir
        WHEN 'N' THEN 'S' WHEN 'S' THEN 'N'
        WHEN 'E' THEN 'W' WHEN 'W' THEN 'E'
        WHEN 'U' THEN 'D' WHEN 'D' THEN 'U'
      END
  );
