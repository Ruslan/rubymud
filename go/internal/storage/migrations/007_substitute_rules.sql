CREATE TABLE IF NOT EXISTS substitute_rules (
  id INTEGER PRIMARY KEY,
  profile_id INTEGER NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  pattern TEXT NOT NULL,
  replacement TEXT NOT NULL,
  is_gag INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  group_name TEXT DEFAULT 'default',
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS substitute_rules_profile_position_idx ON substitute_rules(profile_id, position);
CREATE INDEX IF NOT EXISTS substitute_rules_profile_group_idx ON substitute_rules(profile_id, group_name);
