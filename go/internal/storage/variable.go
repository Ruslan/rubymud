package storage

func (s *Store) ListVariables(sessionID int64) ([]Variable, error) {
	rows, err := s.db.Query(`
		SELECT key, value
		FROM variables
		WHERE session_id = ? AND scope = 'session'
		ORDER BY key ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variables := make([]Variable, 0)
	for rows.Next() {
		var variable Variable
		if err := rows.Scan(&variable.Key, &variable.Value); err != nil {
			return nil, err
		}
		variables = append(variables, variable)
	}

	return variables, rows.Err()
}

func (s *Store) LoadVariables(sessionID int64) (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT key, value
		FROM variables
		WHERE session_id = ? AND scope = 'session'
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vars := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		vars[k] = v
	}
	return vars, rows.Err()
}

func (s *Store) SetVariable(sessionID int64, key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO variables(session_id, scope, key, value, updated_at)
		VALUES(?, 'session', ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(session_id, scope, key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, sessionID, key, value)
	return err
}

func (s *Store) DeleteVariable(sessionID int64, key string) error {
	_, err := s.db.Exec(`
		DELETE FROM variables WHERE session_id = ? AND scope = 'session' AND key = ?
	`, sessionID, key)
	return err
}
