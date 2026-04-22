package storage

import "errors"

type HighlightRule struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	ProfileID     int64      `json:"profile_id"`
	Position      int        `json:"position"`
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

func (s *Store) ListHighlights(profileID int64) ([]HighlightRule, error) {
	var highlights []HighlightRule
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC").Find(&highlights).Error
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
	if h.Position == 0 {
		var maxPos int
		s.db.Model(&HighlightRule{}).Where("profile_id = ?", h.ProfileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
		h.Position = maxPos + 1
	}
	return s.db.Create(&h).Error
}

func (s *Store) UpdateHighlight(h HighlightRule) error {
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	h.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&HighlightRule{}).
		Where("id = ? AND profile_id = ?", h.ID, h.ProfileID).
		Updates(map[string]interface{}{
			"position":      h.Position,
			"pattern":       h.Pattern,
			"fg":            h.FG,
			"bg":            h.BG,
			"bold":          h.Bold,
			"faint":         h.Faint,
			"italic":        h.Italic,
			"underline":     h.Underline,
			"strikethrough": h.Strikethrough,
			"blink":         h.Blink,
			"reverse":       h.Reverse,
			"enabled":       h.Enabled,
			"group_name":    h.GroupName,
			"updated_at":    h.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("highlight not found")
	}
	return nil
}

func (s *Store) DeleteHighlightByID(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&HighlightRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("highlight not found")
	}
	return nil
}

func (s *Store) LoadHighlightsForProfiles(profileIDs []int64) ([]HighlightRule, error) {
	var highlights []HighlightRule
	if len(profileIDs) == 0 {
		return highlights, nil
	}
	err := s.db.Where("profile_id IN ? AND enabled = ?", profileIDs, true).Order("position ASC").Find(&highlights).Error
	return highlights, err
}

func (s *Store) SaveHighlight(profileID int64, h HighlightRule) error {
	h.ProfileID = profileID
	if h.GroupName == "" {
		h.GroupName = "default"
	}
	h.UpdatedAt = nowSQLiteTime()
	// UPSERT by pattern
	var existing HighlightRule
	err := s.db.Where("profile_id = ? AND pattern = ?", profileID, h.Pattern).First(&existing).Error
	if err == nil {
		h.ID = existing.ID
		h.Position = existing.Position
		return s.db.Save(&h).Error
	}

	var maxPos int
	s.db.Model(&HighlightRule{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
	h.Position = maxPos + 1

	return s.db.Create(&h).Error
}

func (s *Store) DeleteHighlight(profileID int64, pattern string) error {
	return s.db.Where("profile_id = ? AND pattern = ?", profileID, pattern).Delete(&HighlightRule{}).Error
}
