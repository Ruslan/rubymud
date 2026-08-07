CREATE INDEX IF NOT EXISTS log_entries_session_window_id_idx
ON log_entries(session_id, window_name, id);
