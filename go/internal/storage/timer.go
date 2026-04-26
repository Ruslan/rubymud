package storage

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) SaveTimer(timer TimerRecord) error {
	return s.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&timer).Error
}

func (s *Store) GetTimers(sessionID int64) ([]TimerRecord, error) {
	var timers []TimerRecord
	err := s.db.Where("session_id = ?", sessionID).Find(&timers).Error
	return timers, err
}

func (s *Store) SaveSubscription(sub TimerSubscriptionRecord) error {
	return s.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&sub).Error
}

func (s *Store) GetSubscriptions(sessionID int64) ([]TimerSubscriptionRecord, error) {
	var subs []TimerSubscriptionRecord
	err := s.db.Where("session_id = ?", sessionID).Order("second, sort_order").Find(&subs).Error
	return subs, err
}

func (s *Store) ClearSubscriptions(sessionID int64, timerName string, second int) error {
	return s.db.Where("session_id = ? AND timer_name = ? AND second = ?", sessionID, timerName, second).
		Delete(&TimerSubscriptionRecord{}).Error
}

func (s *Store) ClearAllSubscriptions(sessionID int64, timerName string) error {
	return s.db.Where("session_id = ? AND timer_name = ?", sessionID, timerName).
		Delete(&TimerSubscriptionRecord{}).Error
}

func (s *Store) Transaction(fn func(store *Store) error) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		return fn(&Store{db: tx})
	})
}
