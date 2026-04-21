PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY,
  name TEXT,
  mud_host TEXT NOT NULL,
  mud_port INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'new',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_connected_at TEXT,
  last_disconnected_at TEXT
);

CREATE INDEX IF NOT EXISTS sessions_status_idx ON sessions(status);
CREATE INDEX IF NOT EXISTS sessions_updated_at_idx ON sessions(updated_at);

CREATE TABLE IF NOT EXISTS clients (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  client_uid TEXT NOT NULL,
  display_name TEXT,
  mode TEXT NOT NULL DEFAULT 'unknown',
  user_agent TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS clients_session_id_idx ON clients(session_id);
CREATE UNIQUE INDEX IF NOT EXISTS clients_session_uid_idx ON clients(session_id, client_uid);

CREATE TABLE IF NOT EXISTS log_entries (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  stream TEXT NOT NULL,
  window_name TEXT,
  raw_text TEXT NOT NULL,
  plain_text TEXT NOT NULL,
  raw_bytes BLOB,
  encoding TEXT,
  source_type TEXT NOT NULL,
  source_id TEXT,
  received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS log_entries_session_id_idx ON log_entries(session_id);
CREATE INDEX IF NOT EXISTS log_entries_session_created_idx ON log_entries(session_id, created_at);
CREATE INDEX IF NOT EXISTS log_entries_session_window_created_idx ON log_entries(session_id, window_name, created_at);
CREATE INDEX IF NOT EXISTS log_entries_source_idx ON log_entries(session_id, source_type, created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS log_entries_fts USING fts5(
  plain_text,
  content='log_entries',
  content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS log_entries_ai AFTER INSERT ON log_entries BEGIN
  INSERT INTO log_entries_fts(rowid, plain_text) VALUES (new.id, new.plain_text);
END;

CREATE TRIGGER IF NOT EXISTS log_entries_ad AFTER DELETE ON log_entries BEGIN
  INSERT INTO log_entries_fts(log_entries_fts, rowid, plain_text) VALUES('delete', old.id, old.plain_text);
END;

CREATE TRIGGER IF NOT EXISTS log_entries_au AFTER UPDATE ON log_entries BEGIN
  INSERT INTO log_entries_fts(log_entries_fts, rowid, plain_text) VALUES('delete', old.id, old.plain_text);
  INSERT INTO log_entries_fts(rowid, plain_text) VALUES (new.id, new.plain_text);
END;

CREATE TABLE IF NOT EXISTS log_overlays (
  id INTEGER PRIMARY KEY,
  log_entry_id INTEGER NOT NULL,
  overlay_type TEXT NOT NULL,
  layer INTEGER NOT NULL DEFAULT 0,
  start_offset INTEGER,
  end_offset INTEGER,
  payload_json TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (log_entry_id) REFERENCES log_entries(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS log_overlays_log_entry_id_idx ON log_overlays(log_entry_id);
CREATE INDEX IF NOT EXISTS log_overlays_type_idx ON log_overlays(overlay_type);
CREATE INDEX IF NOT EXISTS log_overlays_source_idx ON log_overlays(source_type, source_id);

CREATE TABLE IF NOT EXISTS history_entries (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  client_id INTEGER,
  kind TEXT NOT NULL,
  line TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
  FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS history_entries_session_created_idx ON history_entries(session_id, created_at);
CREATE INDEX IF NOT EXISTS history_entries_session_kind_created_idx ON history_entries(session_id, kind, created_at);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT,
  related_log_entry_id INTEGER,
  related_client_id INTEGER,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
  FOREIGN KEY (related_log_entry_id) REFERENCES log_entries(id) ON DELETE SET NULL,
  FOREIGN KEY (related_client_id) REFERENCES clients(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS events_session_created_idx ON events(session_id, created_at);
CREATE INDEX IF NOT EXISTS events_session_kind_created_idx ON events(session_id, kind, created_at);
CREATE INDEX IF NOT EXISTS events_related_log_idx ON events(related_log_entry_id);

CREATE TABLE IF NOT EXISTS snapshots (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS snapshots_session_kind_created_idx ON snapshots(session_id, kind, created_at);

CREATE TABLE IF NOT EXISTS plugins (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  runtime TEXT NOT NULL,
  status TEXT NOT NULL,
  capabilities_json TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS plugins_session_name_idx ON plugins(session_id, name);
