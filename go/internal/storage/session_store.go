package storage

import "gorm.io/gorm"

func (s *SessionRecord) TableName() string {
	return "sessions"
}

func (s *Store) EnsureDefaultSession(host string, port int) (SessionRecord, error) {
	var record SessionRecord
	err := s.db.Where("name = ?", "default").Order("id ASC").First(&record).Error

	now := nowSQLiteTimePtr()
	if err == gorm.ErrRecordNotFound {
		record = SessionRecord{
			Name:            "default",
			MudHost:         host,
			MudPort:         port,
			Status:          "disconnected",
			MCCPEnabled:     1,
			LastConnectedAt: now,
		}
		if err := s.db.Create(&record).Error; err != nil {
			return SessionRecord{}, err
		}
		if err := s.EnsureSessionProfiles(record.ID, record.Name); err != nil {
			return SessionRecord{}, err
		}
		return record, nil
	}
	if err != nil {
		return SessionRecord{}, err
	}
	if err := s.EnsureSessionProfiles(record.ID, record.Name); err != nil {
		return SessionRecord{}, err
	}

	return record, nil
}

func (s *Store) GetSession(id int64) (SessionRecord, error) {
	var record SessionRecord
	err := s.db.First(&record, id).Error
	return record, err
}

func (s *Store) CreateSession(name, host string, port int) (SessionRecord, error) {
	record := SessionRecord{
		Name:        name,
		MudHost:     host,
		MudPort:     port,
		Status:      "disconnected",
		MCCPEnabled: 1,
	}
	err := s.db.Create(&record).Error
	if err != nil {
		return record, err
	}
	err = s.EnsureSessionProfiles(record.ID, record.Name)
	return record, err
}

func (s *Store) UpdateSession(record SessionRecord) error {
	return s.db.Save(&record).Error
}

func (s *Store) DeleteSession(id int64) error {
	return s.db.Delete(&SessionRecord{}, id).Error
}

func (s *Store) ListSessions() ([]SessionRecord, error) {
	var sessions []SessionRecord
	err := s.db.Order("id ASC").Find(&sessions).Error
	return sessions, err
}

func (s *Store) MarkSessionConnected(sessionID int64) error {
	now := nowSQLiteTimePtr()
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Updates(map[string]any{
		"status":          "connected",
		"last_connected_at": now,
	}).Error
}

func (s *Store) MarkSessionDisconnected(sessionID int64) error {
	now := nowSQLiteTimePtr()
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Updates(map[string]any{
		"status":               "disconnected",
		"last_disconnected_at": now,
	}).Error
}
