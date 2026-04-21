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
			Status:          "connected",
			LastConnectedAt: now,
		}
		if err := s.db.Create(&record).Error; err != nil {
			return SessionRecord{}, err
		}
		return record, nil
	}
	if err != nil {
		return SessionRecord{}, err
	}

	record.MudHost = host
	record.MudPort = port
	record.Status = "connected"
	record.LastConnectedAt = now
	if err := s.db.Save(&record).Error; err != nil {
		return SessionRecord{}, err
	}

	return record, nil
}

func (s *Store) MarkSessionDisconnected(sessionID int64) error {
	now := nowSQLiteTimePtr()
	return s.db.Model(&SessionRecord{}).Where("id = ?", sessionID).Updates(map[string]any{
		"status":               "disconnected",
		"last_disconnected_at": now,
	}).Error
}
