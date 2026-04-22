package storage

import "gorm.io/gorm"

func (s *Store) GetSetting(key string) (string, error) {
	var setting AppSetting
	err := s.db.Where("key = ?", key).First(&setting).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return setting.Value, err
}

func (s *Store) SetSetting(key, value string) error {
	setting := AppSetting{Key: key, Value: value}
	return s.db.Save(&setting).Error
}
