package storage

import "strings"

func (s *Store) AppendHistoryEntry(sessionID int64, kind, line string) error {
	_, err := s.db.Exec(`
		INSERT INTO history_entries(session_id, kind, line)
		VALUES(?, ?, ?)
	`, sessionID, kind, strings.TrimSpace(line))
	return err
}

func (s *Store) RecentInputHistory(sessionID int64, limit int) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT line
		FROM history_entries
		WHERE session_id = ? AND kind = 'input'
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		history = append(history, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	return history, nil
}
