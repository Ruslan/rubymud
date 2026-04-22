package storage

type AliasRule struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	SessionID int64      `json:"session_id"`
	Name      string     `json:"name"`
	Template  string     `json:"template"`
	Enabled   bool       `json:"enabled"`
	GroupName string     `json:"group_name"`
	UpdatedAt SQLiteTime `json:"updated_at"`
}

func (s *AliasRule) TableName() string {
	return "alias_rules"
}

func (s *Store) ListAliases(sessionID int64) ([]AliasRule, error) {
	var aliases []AliasRule
	err := s.db.Where("session_id = ?", sessionID).Order("name").Find(&aliases).Error
	return aliases, err
}

func (s *Store) GetAlias(id int64) (*AliasRule, error) {
	var a AliasRule
	err := s.db.First(&a, id).Error
	return &a, err
}

func (s *Store) SaveAlias(sessionID int64, name, template string, enabled bool, group string) error {
	if group == "" {
		group = "default"
	}
	var a AliasRule
	err := s.db.Where("session_id = ? AND name = ?", sessionID, name).First(&a).Error
	if err == nil {
		a.Template = template
		a.Enabled = enabled
		a.GroupName = group
		a.UpdatedAt = nowSQLiteTime()
		return s.db.Save(&a).Error
	}
	a = AliasRule{
		SessionID: sessionID,
		Name:      name,
		Template:  template,
		Enabled:   enabled,
		GroupName: group,
		UpdatedAt: nowSQLiteTime(),
	}
	return s.db.Create(&a).Error
}

func (s *Store) UpdateAlias(a AliasRule) error {
	if a.GroupName == "" {
		a.GroupName = "default"
	}
	if a.SessionID == 0 && a.ID != 0 {
		var existing AliasRule
		if err := s.db.First(&existing, a.ID).Error; err != nil {
			return err
		}
		a.SessionID = existing.SessionID
	}
	a.UpdatedAt = nowSQLiteTime()
	return s.db.Save(&a).Error
}

func (s *Store) DeleteAliasByID(id int64) error {
	return s.db.Delete(&AliasRule{}, id).Error
}

func (s *Store) LoadAliases(sessionID int64) ([]AliasRule, error) {
	var aliases []AliasRule
	err := s.db.Where("session_id = ? AND enabled = ?", sessionID, true).Order("name").Find(&aliases).Error
	return aliases, err
}

func (s *Store) DeleteAlias(sessionID int64, name string) error {
	return s.db.Where("session_id = ? AND name = ?", sessionID, name).Delete(&AliasRule{}).Error
}
