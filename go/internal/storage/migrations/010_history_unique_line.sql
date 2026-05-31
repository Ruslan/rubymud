WITH ranked AS (
  SELECT
    id,
    session_id,
    line,
    kind,
    ROW_NUMBER() OVER (
      PARTITION BY session_id, line
      ORDER BY created_at DESC, id DESC
    ) AS rn,
    MAX(CASE WHEN kind = 'input' THEN 1 ELSE 0 END) OVER (
      PARTITION BY session_id, line
    ) AS has_input
  FROM history_entries
)
UPDATE history_entries
SET kind = 'input'
WHERE id IN (
  SELECT id FROM ranked WHERE rn = 1 AND has_input = 1
);

WITH ranked AS (
  SELECT
    id,
    ROW_NUMBER() OVER (
      PARTITION BY session_id, line
      ORDER BY created_at DESC, id DESC
    ) AS rn
  FROM history_entries
)
DELETE FROM history_entries
WHERE id IN (
  SELECT id FROM ranked WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS history_entries_session_line_idx
  ON history_entries(session_id, line);
