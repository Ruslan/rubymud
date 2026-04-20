package storage

type HighlightRule struct {
	ID            int64
	SessionID     int64
	Pattern       string
	FG            string
	BG            string
	Bold          bool
	Faint         bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	Blink         bool
	Reverse       bool
	Enabled       bool
	GroupName     string
}

func (s *Store) LoadHighlights(sessionID int64) ([]HighlightRule, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, pattern, fg, bg, bold, faint, italic, underline, strikethrough, blink, reverse, enabled, group_name
		FROM highlight_rules
		WHERE session_id = ? AND enabled = 1
		ORDER BY id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	highlights := make([]HighlightRule, 0)
	for rows.Next() {
		var h HighlightRule
		var bold, faint, italic, underline, strikethrough, blink, reverse, enabled int
		if err := rows.Scan(&h.ID, &h.SessionID, &h.Pattern, &h.FG, &h.BG, &bold, &faint, &italic, &underline, &strikethrough, &blink, &reverse, &enabled, &h.GroupName); err != nil {
			return nil, err
		}
		h.Bold = bold == 1
		h.Faint = faint == 1
		h.Italic = italic == 1
		h.Underline = underline == 1
		h.Strikethrough = strikethrough == 1
		h.Blink = blink == 1
		h.Reverse = reverse == 1
		h.Enabled = enabled == 1
		highlights = append(highlights, h)
	}
	return highlights, rows.Err()
}

func (s *Store) SaveHighlight(sessionID int64, h HighlightRule) error {
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO highlight_rules(session_id, pattern, fg, bg, bold, faint, italic, underline, strikethrough, blink, reverse, enabled, group_name)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(session_id, pattern) DO UPDATE SET fg = excluded.fg, bg = excluded.bg, bold = excluded.bold, faint = excluded.faint, italic = excluded.italic, underline = excluded.underline, strikethrough = excluded.strikethrough, blink = excluded.blink, reverse = excluded.reverse, group_name = excluded.group_name, updated_at = CURRENT_TIMESTAMP
	`, sessionID, h.Pattern, h.FG, h.BG, boolToInt(h.Bold), boolToInt(h.Faint), boolToInt(h.Italic), boolToInt(h.Underline), boolToInt(h.Strikethrough), boolToInt(h.Blink), boolToInt(h.Reverse), h.GroupName)
	return err
}

func (s *Store) DeleteHighlight(sessionID int64, pattern string) error {
	_, err := s.db.Exec(`DELETE FROM highlight_rules WHERE session_id = ? AND pattern = ?`, sessionID, pattern)
	return err
}
