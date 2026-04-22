package storage

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
	if t.SessionID == 0 && t.ID != 0 {
		var existing TriggerRule
		if err := s.db.First(&existing, t.ID).Error; err != nil {
			return err
		}
		t.SessionID = existing.SessionID
	}
	t.UpdatedAt = nowSQLiteTime()
	return s.db.Save(&t).Error
}

func (s *Store) DeleteTriggerByID(id int64) error {
	return s.db.Delete(&TriggerRule{}, id).Error
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
