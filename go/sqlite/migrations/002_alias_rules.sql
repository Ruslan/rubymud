CREATE TABLE IF NOT EXISTS alias_rules (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  template TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS alias_rules_session_name_idx ON alias_rules(session_id, name);
