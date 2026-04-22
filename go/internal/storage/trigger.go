package storage

import "errors"

type TriggerRule struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	ProfileID      int64      `json:"profile_id"`
	Position       int        `json:"position"`
	Name           string     `json:"name"`
	Pattern        string     `json:"pattern"`
	Command        string     `json:"command"`
	IsButton       bool       `json:"is_button"`
	Enabled        bool       `json:"enabled"`
	StopAfterMatch bool       `json:"stop_after_match"`
	GroupName      string     `json:"group_name"`
	UpdatedAt      SQLiteTime `json:"updated_at"`
}

func (s *TriggerRule) TableName() string {
	return "trigger_rules"
}

func (s *Store) ListTriggers(profileID int64) ([]TriggerRule, error) {
	var triggers []TriggerRule
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC").Find(&triggers).Error
	return triggers, err
}

func (s *Store) GetTrigger(id int64) (*TriggerRule, error) {
	var t TriggerRule
	err := s.db.First(&t, id).Error
	return &t, err
}

func (s *Store) CreateTrigger(t TriggerRule) error {
	if t.GroupName == "" {
		t.GroupName = "default"
	}
	t.UpdatedAt = nowSQLiteTime()
	if t.Position == 0 {
		var maxPos int
		s.db.Model(&TriggerRule{}).Where("profile_id = ?", t.ProfileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
		t.Position = maxPos + 1
	}
	return s.db.Create(&t).Error
}

func (s *Store) UpdateTrigger(t TriggerRule) error {
	if t.GroupName == "" {
		t.GroupName = "default"
	}
	t.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&TriggerRule{}).
		Where("id = ? AND profile_id = ?", t.ID, t.ProfileID).
		Updates(map[string]interface{}{
			"position":         t.Position,
			"name":             t.Name,
			"pattern":          t.Pattern,
			"command":          t.Command,
			"enabled":          t.Enabled,
			"is_button":        t.IsButton,
			"stop_after_match": t.StopAfterMatch,
			"group_name":       t.GroupName,
			"updated_at":       t.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("trigger not found")
	}
	return nil
}

func (s *Store) DeleteTriggerByID(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&TriggerRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("trigger not found")
	}
	return nil
}

func (s *Store) LoadTriggersForProfiles(profileIDs []int64) ([]TriggerRule, error) {
	var triggers []TriggerRule
	if len(profileIDs) == 0 {
		return triggers, nil
	}
	err := s.db.Where("profile_id IN ? AND enabled = ?", profileIDs, true).Order("position ASC").Find(&triggers).Error
	return triggers, err
}

func (s *Store) SaveTrigger(profileID int64, pattern, command string, isButton bool, group string) error {
	if group == "" {
		group = "default"
	}
	var maxPos int
	s.db.Model(&TriggerRule{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)

	t := TriggerRule{
		ProfileID: profileID,
		Position:  maxPos + 1,
		Name:      pattern,
		Pattern:   pattern,
		Command:   command,
		IsButton:  isButton,
		Enabled:   true,
		GroupName: group,
		UpdatedAt: nowSQLiteTime(),
	}
	return s.db.Create(&t).Error
}

func (s *Store) DeleteTrigger(profileID int64, pattern string) error {
	return s.db.Where("profile_id = ? AND pattern = ?", profileID, pattern).Delete(&TriggerRule{}).Error
}
