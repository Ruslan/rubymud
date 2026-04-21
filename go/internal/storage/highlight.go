package storage

type HighlightRule struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	SessionID     int64      `json:"session_id"`
	Pattern       string     `json:"pattern"`
	FG            string     `json:"fg"`
	BG            string     `json:"bg"`
	Bold          bool       `json:"bold"`
	Faint         bool       `json:"faint"`
	Italic        bool       `json:"italic"`
	Underline     bool       `json:"underline"`
	Strikethrough bool       `json:"strikethrough"`
	Blink         bool       `json:"blink"`
	Reverse       bool       `json:"reverse"`
	Enabled       bool       `json:"enabled"`
	GroupName     string     `json:"group_name"`
	UpdatedAt     SQLiteTime `json:"updated_at"`
}

func (s *HighlightRule) TableName() string {
	return "highlight_rules"
}

func (s *Store) ListHighlights(sessionID int64) ([]HighlightRule, error) {
	var highlights []HighlightRule
	err := s.db.Where("session_id = ?", sessionID).Order("id").Find(&highlights).Error
	return highlights, err
}

func (s *Store) GetHighlight(id int64) (*HighlightRule, error) {
	var h HighlightRule
	err := s.db.First(&h, id).Error
	return &h, err
}

func (s *Store) CreateHighlight(h HighlightRule) error {
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	h.UpdatedAt = nowSQLiteTime()
	return s.db.Create(&h).Error
}

func (s *Store) UpdateHighlight(h HighlightRule) error {
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	h.UpdatedAt = nowSQLiteTime()
	return s.db.Save(&h).Error
}

func (s *Store) DeleteHighlightByID(id int64) error {
	return s.db.Delete(&HighlightRule{}, id).Error
}

func (s *Store) LoadHighlights(sessionID int64) ([]HighlightRule, error) {
	var highlights []HighlightRule
	err := s.db.Where("session_id = ? AND enabled = ?", sessionID, true).Order("id").Find(&highlights).Error
	return highlights, err
}

func (s *Store) SaveHighlight(sessionID int64, h HighlightRule) error {
	h.SessionID = sessionID
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	h.UpdatedAt = nowSQLiteTime()
	// UPSERT by pattern
	var existing HighlightRule
	err := s.db.Where("session_id = ? AND pattern = ?", sessionID, h.Pattern).First(&existing).Error
	if err == nil {
		h.ID = existing.ID
		return s.db.Save(&h).Error
	}
	return s.db.Create(&h).Error
}

func (s *Store) DeleteHighlight(sessionID int64, pattern string) error {
	return s.db.Where("session_id = ? AND pattern = ?", sessionID, pattern).Delete(&HighlightRule{}).Error
}
