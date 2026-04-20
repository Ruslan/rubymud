package storage

type AliasRule struct {
	ID        int64
	SessionID int64
	Name      string
	Template  string
	Enabled   bool
}

func (s *Store) LoadAliases(sessionID int64) ([]AliasRule, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, name, template, enabled
		FROM alias_rules
		WHERE session_id = ? AND enabled = 1
		ORDER BY name
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aliases := make([]AliasRule, 0)
	for rows.Next() {
		var a AliasRule
		var enabled int
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Name, &a.Template, &enabled); err != nil {
			return nil, err
		}
		a.Enabled = enabled == 1
		aliases = append(aliases, a)
	}
	return aliases, rows.Err()
}

func (s *Store) SaveAlias(sessionID int64, name, template string) error {
	_, err := s.db.Exec(`
		INSERT INTO alias_rules(session_id, name, template, enabled)
		VALUES(?, ?, ?, 1)
		ON CONFLICT(session_id, name) DO UPDATE SET template = excluded.template, updated_at = CURRENT_TIMESTAMP
	`, sessionID, name, template)
	return err
}

func (s *Store) DeleteAlias(sessionID int64, name string) error {
	_, err := s.db.Exec(`DELETE FROM alias_rules WHERE session_id = ? AND name = ?`, sessionID, name)
	return err
}
