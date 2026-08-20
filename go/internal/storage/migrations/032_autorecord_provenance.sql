-- Continuous autorecord keeps its evidence in structured room columns rather
-- than user-facing notes. Existing imported rooms are verified/imported; new
-- rooms written by the recording worker are provisional/autorecord rows.
ALTER TABLE rooms ADD COLUMN verification_state TEXT NOT NULL DEFAULT 'verified';
ALTER TABLE rooms ADD COLUMN record_source TEXT NOT NULL DEFAULT 'import';
ALTER TABLE rooms ADD COLUMN record_batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE rooms ADD COLUMN recorded_at DATETIME;
ALTER TABLE rooms ADD COLUMN record_origin TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_rooms_record_batch
    ON rooms (map_set_id, record_batch_id)
    WHERE record_batch_id <> '';
