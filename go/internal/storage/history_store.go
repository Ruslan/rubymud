package storage

import (
	"errors"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

type HistoryListOptions struct {
	Kind     string
	Query    string
	BeforeID int64
	Limit    int
}

func (h *HistoryEntry) TableName() string {
	return "history_entries"
}

func (s *Store) AppendHistoryEntry(sessionID int64, kind, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "input"
	}

	createdAt := nowSQLiteTime()
	return s.db.Exec(`
		INSERT INTO history_entries (session_id, kind, line, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, line) DO UPDATE SET
			kind = CASE
				WHEN excluded.kind = 'input' OR history_entries.kind = 'input' THEN 'input'
				ELSE excluded.kind
			END,
			created_at = excluded.created_at
	`, sessionID, kind, line, createdAt).Error
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

func (s *Store) ListHistory(sessionID int64, limit int) ([]HistoryEntry, error) {
	entries, _, err := s.ListHistoryPage(sessionID, HistoryListOptions{Limit: limit})
	return entries, err
}

func historyQueryGlob(query string) string {
	escaped := escapeGlob(query)
	var globPattern strings.Builder
	globPattern.WriteByte('*')
	for _, r := range escaped {
		lower := unicode.ToLower(r)
		upper := unicode.ToUpper(r)
		if lower == upper {
			globPattern.WriteRune(lower)
		} else {
			globPattern.WriteByte('[')
			globPattern.WriteRune(lower)
			globPattern.WriteRune(upper)
			globPattern.WriteByte(']')
		}
	}
	globPattern.WriteByte('*')
	return globPattern.String()
}

func (s *Store) ListHistoryPage(sessionID int64, opts HistoryListOptions) ([]HistoryEntry, bool, error) {
	limit := opts.Limit
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	var entries []HistoryEntry
	query := s.db.Where("session_id = ?", sessionID)
	if kind := strings.TrimSpace(opts.Kind); kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if search := strings.TrimSpace(opts.Query); search != "" {
		query = query.Where("line GLOB ?", historyQueryGlob(search))
	}
	if opts.BeforeID > 0 {
		var cursor HistoryEntry
		err := s.db.Where("session_id = ? AND id = ?", sessionID, opts.BeforeID).First(&cursor).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []HistoryEntry{}, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		query = query.Where("(created_at < ? OR (created_at = ? AND id < ?))", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}

	err := query.Order("created_at DESC, id DESC").
		Limit(limit + 1).
		Find(&entries).Error
	if err != nil {
		return nil, false, err
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

func (s *Store) DeleteHistoryEntry(sessionID int64, id int64) error {
	return s.db.Where("session_id = ? AND id = ?", sessionID, id).Delete(&HistoryEntry{}).Error
}
