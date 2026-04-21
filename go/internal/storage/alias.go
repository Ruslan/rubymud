package storage

type AliasRule struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	SessionID int64      `json:"session_id"`
	Name      string     `json:"name"`
	Template  string     `json:"template"`
	Enabled   bool       `json:"enabled"`
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

func (s *Store) SaveAlias(sessionID int64, name, template string) error {
	var a AliasRule
	err := s.db.Where("session_id = ? AND name = ?", sessionID, name).First(&a).Error
	if err == nil {
		a.Template = template
		a.UpdatedAt = nowSQLiteTime()
		return s.db.Save(&a).Error
	}
	a = AliasRule{
		SessionID: sessionID,
		Name:      name,
		Template:  template,
		Enabled:   true,
		UpdatedAt: nowSQLiteTime(),
	}
	return s.db.Create(&a).Error
}

func (s *Store) UpdateAlias(a AliasRule) error {
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
