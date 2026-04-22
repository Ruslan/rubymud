package storage

import "errors"

type TriggerRule struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	SessionID      int64      `json:"session_id"`
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

func (s *Store) ListTriggers(sessionID int64) ([]TriggerRule, error) {
	var triggers []TriggerRule
	err := s.db.Where("session_id = ?", sessionID).Order("id").Find(&triggers).Error
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
	return s.db.Create(&t).Error
}

func (s *Store) UpdateTrigger(t TriggerRule) error {
	if t.GroupName == "" {
		t.GroupName = "default"
	}
	t.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&TriggerRule{}).
		Where("id = ? AND session_id = ?", t.ID, t.SessionID).
		Updates(map[string]interface{}{
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

func (s *Store) DeleteTriggerByID(id, sessionID int64) error {
	result := s.db.Where("id = ? AND session_id = ?", id, sessionID).Delete(&TriggerRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("trigger not found")
	}
	return nil
}

func (s *Store) LoadTriggers(sessionID int64) ([]TriggerRule, error) {
	var triggers []TriggerRule
	err := s.db.Where("session_id = ? AND enabled = ?", sessionID, true).Order("id").Find(&triggers).Error
	return triggers, err
}

func (s *Store) SaveTrigger(sessionID int64, pattern, command string, isButton bool, group string) error {
	if group == "" {
		group = "default"
	}
	t := TriggerRule{
		SessionID: sessionID,
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

func (s *Store) DeleteTrigger(sessionID int64, pattern string) error {
	return s.db.Where("session_id = ? AND pattern = ?", sessionID, pattern).Delete(&TriggerRule{}).Error
}
