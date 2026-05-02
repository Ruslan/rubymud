package storage

import (
	"gorm.io/gorm/clause"
)

func (s *Store) SaveProfileTimer(timer ProfileTimer) error {
	return s.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&timer).Error
}

func (s *Store) GetProfileTimers(profileID int64) ([]ProfileTimer, error) {
	var timers []ProfileTimer
	err := s.db.Where("profile_id = ?", profileID).Find(&timers).Error
	return timers, err
}

func (s *Store) GetProfileTimer(profileID int64, name string) (*ProfileTimer, error) {
	var timer ProfileTimer
	err := s.db.Where("profile_id = ? AND name = ?", profileID, name).First(&timer).Error
	if err != nil {
		return nil, err
	}
	return &timer, nil
}

func (s *Store) DeleteProfileTimer(profileID int64, name string) error {
	return s.db.Where("profile_id = ? AND name = ?", profileID, name).Delete(&ProfileTimer{}).Error
}

func (s *Store) SaveProfileTimerSubscription(sub ProfileTimerSubscription) error {
	return s.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&sub).Error
}

func (s *Store) GetProfileTimerSubscriptions(profileID int64, timerName string) ([]ProfileTimerSubscription, error) {
	var subs []ProfileTimerSubscription
	err := s.db.Where("profile_id = ? AND timer_name = ?", profileID, timerName).
		Order("second, sort_order").Find(&subs).Error
	return subs, err
}

func (s *Store) ClearProfileTimerSubscriptions(profileID int64, timerName string) error {
	return s.db.Where("profile_id = ? AND timer_name = ?", profileID, timerName).
		Delete(&ProfileTimerSubscription{}).Error
}
