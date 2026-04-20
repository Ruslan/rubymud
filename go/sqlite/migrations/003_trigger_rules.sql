CREATE TABLE IF NOT EXISTS trigger_rules (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  name TEXT,
  pattern TEXT NOT NULL,
  command TEXT NOT NULL,
  is_button INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  stop_after_match INTEGER NOT NULL DEFAULT 0,
  group_name TEXT NOT NULL DEFAULT 'default',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS trigger_rules_session_id_idx ON trigger_rules(session_id);
CREATE INDEX IF NOT EXISTS trigger_rules_session_group_idx ON trigger_rules(session_id, group_name);
