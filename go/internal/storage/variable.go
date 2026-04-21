package storage

type Variable struct {
	SessionID int64      `gorm:"primaryKey" json:"session_id"`
	Scope     string     `gorm:"primaryKey;default:'session'" json:"scope"`
	Key       string     `gorm:"primaryKey" json:"key"`
	Value     string     `json:"value"`
	UpdatedAt SQLiteTime `json:"updated_at"`
}

func (s *Store) ListVariables(sessionID int64) ([]Variable, error) {
	var variables []Variable
	err := s.db.Where("session_id = ? AND scope = 'session'", sessionID).Order("key").Find(&variables).Error
	return variables, err
}

func (s *Store) LoadVariables(sessionID int64) (map[string]string, error) {
	var variables []Variable
	err := s.db.Where("session_id = ? AND scope = 'session'", sessionID).Find(&variables).Error
	if err != nil {
		return nil, err
	}
	vars := make(map[string]string)
	for _, v := range variables {
		vars[v.Key] = v.Value
	}
	return vars, nil
}

func (s *Store) SetVariable(sessionID int64, key, value string) error {
	v := Variable{
		SessionID: sessionID,
		Scope:     "session",
		Key:       key,
		Value:     value,
		UpdatedAt: nowSQLiteTime(),
	}
	return s.db.Save(&v).Error
}

func (s *Store) DeleteVariable(sessionID int64, key string) error {
	return s.db.Where("session_id = ? AND scope = 'session' AND key = ?", sessionID, key).Delete(&Variable{}).Error
}
