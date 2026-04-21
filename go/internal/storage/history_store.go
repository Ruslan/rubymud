package storage

import "strings"

func (h *HistoryEntry) TableName() string {
	return "history_entries"
}

func (s *Store) AppendHistoryEntry(sessionID int64, kind, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	entry := HistoryEntry{
		SessionID: sessionID,
		Kind:      kind,
		Line:      line,
		CreatedAt: nowSQLiteTime(),
	}
	return s.db.Create(&entry).Error
}

func (s *Store) RecentInputHistory(sessionID int64, limit int) ([]string, error) {
	var entries []HistoryEntry
	err := s.db.Where("session_id = ? AND kind = 'input'", sessionID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, err
	}

	history := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry.Line]; ok {
			continue
		}
		seen[entry.Line] = struct{}{}
		history = append(history, entry.Line)
	}

	// Reverse to get chronological order (oldest first) as expected by the client
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	return history, nil
}
