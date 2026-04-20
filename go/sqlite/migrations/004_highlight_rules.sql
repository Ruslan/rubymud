CREATE TABLE IF NOT EXISTS highlight_rules (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  pattern TEXT NOT NULL,
  fg TEXT NOT NULL DEFAULT '',
  bg TEXT NOT NULL DEFAULT '',
  bold INTEGER NOT NULL DEFAULT 0,
  faint INTEGER NOT NULL DEFAULT 0,
  italic INTEGER NOT NULL DEFAULT 0,
  underline INTEGER NOT NULL DEFAULT 0,
  strikethrough INTEGER NOT NULL DEFAULT 0,
  blink INTEGER NOT NULL DEFAULT 0,
  reverse INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  group_name TEXT NOT NULL DEFAULT 'default',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS highlight_rules_session_id_idx ON highlight_rules(session_id);
CREATE UNIQUE INDEX IF NOT EXISTS highlight_rules_session_pattern_idx ON highlight_rules(session_id, pattern);
