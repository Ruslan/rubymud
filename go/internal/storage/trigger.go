package storage

type TriggerRule struct {
	ID             int64
	SessionID      int64
	Name           string
	Pattern        string
	Command        string
	IsButton       bool
	Enabled        bool
	StopAfterMatch bool
	GroupName      string
}

func (s *Store) LoadTriggers(sessionID int64) ([]TriggerRule, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, name, pattern, command, is_button, enabled, stop_after_match, group_name
		FROM trigger_rules
		WHERE session_id = ? AND enabled = 1
		ORDER BY id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	triggers := make([]TriggerRule, 0)
	for rows.Next() {
		var t TriggerRule
		var isButton, enabled, stopAfter int
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Name, &t.Pattern, &t.Command, &isButton, &enabled, &stopAfter, &t.GroupName); err != nil {
			return nil, err
		}
		t.IsButton = isButton == 1
		t.Enabled = enabled == 1
		t.StopAfterMatch = stopAfter == 1
		triggers = append(triggers, t)
	}
	return triggers, rows.Err()
}

func (s *Store) SaveTrigger(sessionID int64, pattern, command string, isButton bool, group string) error {
	btn := 0
	if isButton {
		btn = 1
	}
	if group == "" {
		group = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO trigger_rules(session_id, name, pattern, command, is_button, enabled, group_name)
		VALUES(?, ?, ?, ?, ?, 1, ?)
	`, sessionID, pattern, pattern, command, btn, group)
	return err
}

func (s *Store) DeleteTrigger(sessionID int64, pattern string) error {
	_, err := s.db.Exec(`DELETE FROM trigger_rules WHERE session_id = ? AND pattern = ?`, sessionID, pattern)
	return err
}
