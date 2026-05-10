ALTER TABLE sessions ADD COLUMN initial_commands TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN mccp_enabled INTEGER NOT NULL DEFAULT 1 CHECK (mccp_enabled IN (0, 1));
