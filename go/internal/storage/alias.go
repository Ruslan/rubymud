package storage

import "errors"

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
	a.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&AliasRule{}).
		Where("id = ? AND session_id = ?", a.ID, a.SessionID).
		Updates(map[string]interface{}{
			"name":       a.Name,
			"template":   a.Template,
			"enabled":    a.Enabled,
			"group_name": a.GroupName,
			"updated_at": a.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("alias not found")
	}
	return nil
}

func (s *Store) DeleteAliasByID(id, sessionID int64) error {
	result := s.db.Where("id = ? AND session_id = ?", id, sessionID).Delete(&AliasRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("alias not found")
	}
	return nil
}

func (s *Store) LoadAliases(sessionID int64) ([]AliasRule, error) {
	var aliases []AliasRule
	err := s.db.Where("session_id = ? AND enabled = ?", sessionID, true).Order("name").Find(&aliases).Error
	return aliases, err
}

func (s *Store) DeleteAlias(sessionID int64, name string) error {
	return s.db.Where("session_id = ? AND name = ?", sessionID, name).Delete(&AliasRule{}).Error
}
