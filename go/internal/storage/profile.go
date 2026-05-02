package storage

import (
	"errors"

	"gorm.io/gorm"
)

func (s *Store) ListProfiles() ([]Profile, error) {
	var profiles []Profile
	err := s.db.Order("name ASC").Find(&profiles).Error
	return profiles, err
}

func (s *Store) CreateProfile(name, description string) (Profile, error) {
	p := Profile{
		Name:        name,
		Description: description,
		CreatedAt:   nowSQLiteTime(),
	}
	err := s.db.Create(&p).Error
	return p, err
}

func (s *Store) GetProfile(id int64) (*Profile, error) {
	var p Profile
	err := s.db.First(&p, id).Error
	return &p, err
}

func (s *Store) GetProfileByName(name string) (*Profile, error) {
	var p Profile
	err := s.db.Where("name = ?", name).First(&p).Error
	return &p, err
}

func (s *Store) UpdateProfile(p Profile) error {
	result := s.db.Model(&Profile{}).Where("id = ?", p.ID).Updates(map[string]interface{}{
		"name":        p.Name,
		"description": p.Description,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("profile not found")
	}
	return nil
}

func (s *Store) DeleteProfile(id int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("profile_id = ?", id).Delete(&AliasRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&TriggerRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&HighlightRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&HotkeyRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&ProfileTimerSubscription{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&ProfileTimer{}).Error; err != nil {
			return err
		}
		if err := tx.Where("profile_id = ?", id).Delete(&SessionProfile{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&Profile{}).Error
	})
}

type SessionProfileEntry struct {
	SessionProfile
	ProfileName string `json:"profile_name"`
}

func (s *Store) GetSessionProfiles(sessionID int64) ([]SessionProfileEntry, error) {
	var entries []SessionProfileEntry
	err := s.db.Table("session_profiles").
		Select("session_profiles.*, profiles.name as profile_name").
		Joins("JOIN profiles ON profiles.id = session_profiles.profile_id").
		Where("session_profiles.session_id = ?", sessionID).
		Order("session_profiles.order_index ASC").
		Find(&entries).Error
	return entries, err
}

func (s *Store) GetOrderedProfileIDs(sessionID int64) ([]int64, error) {
	var ids []int64
	err := s.db.Model(&SessionProfile{}).
		Where("session_id = ?", sessionID).
		Order("order_index DESC"). // order_index DESC means higher priority is processed first in Reload
		Pluck("profile_id", &ids).Error
	return ids, err
}

func (s *Store) GetPrimaryProfileID(sessionID int64) (int64, error) {
	var id int64
	err := s.db.Model(&SessionProfile{}).
		Where("session_id = ?", sessionID).
		Order("order_index DESC").
		Limit(1).
		Pluck("profile_id", &id).Error
	return id, err
}

func (s *Store) AddProfileToSession(sessionID, profileID int64, orderIndex int) error {
	sp := SessionProfile{
		SessionID:  sessionID,
		ProfileID:  profileID,
		OrderIndex: orderIndex,
	}
	return s.db.Create(&sp).Error
}

func (s *Store) EnsureProfileInSession(sessionID, profileID int64) error {
	var count int64
	if err := s.db.Model(&SessionProfile{}).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	var maxOrder int
	if err := s.db.Model(&SessionProfile{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(order_index), -1)").
		Scan(&maxOrder).Error; err != nil {
		return err
	}

	return s.AddProfileToSession(sessionID, profileID, maxOrder+1)
}

func (s *Store) EnsureSessionProfiles(sessionID int64, sessionName string) error {
	var count int64
	if err := s.db.Model(&SessionProfile{}).Where("session_id = ?", sessionID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		primaryName := sessionName
		if primaryName == "" {
			primaryName = "Default"
		}

		var primary Profile
		if err := tx.Where("name = ?", primaryName).First(&primary).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			primary = Profile{Name: primaryName, CreatedAt: nowSQLiteTime()}
			if err := tx.Create(&primary).Error; err != nil {
				return err
			}
		}

		return tx.Create(&SessionProfile{SessionID: sessionID, ProfileID: primary.ID, OrderIndex: 0}).Error
	})
}

func (s *Store) BackfillMissingSessionProfiles() error {
	var sessions []SessionRecord
	if err := s.db.Order("id ASC").Find(&sessions).Error; err != nil {
		return err
	}
	for _, session := range sessions {
		if err := s.EnsureSessionProfiles(session.ID, session.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RemoveProfileFromSession(sessionID, profileID int64) error {
	return s.db.Where("session_id = ? AND profile_id = ?", sessionID, profileID).Delete(&SessionProfile{}).Error
}

type ProfileOrder struct {
	ProfileID  int64 `json:"profile_id"`
	OrderIndex int   `json:"order_index"`
}

func (s *Store) ReorderSessionProfiles(sessionID int64, ordered []ProfileOrder) error {
	tx := s.db.Begin()
	for _, po := range ordered {
		if err := tx.Model(&SessionProfile{}).
			Where("session_id = ? AND profile_id = ?", sessionID, po.ProfileID).
			Update("order_index", po.OrderIndex).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

func (s *Store) GetSessionIDsForProfile(profileID int64) ([]int64, error) {
	var ids []int64
	err := s.db.Model(&SessionProfile{}).Where("profile_id = ?", profileID).Pluck("session_id", &ids).Error
	return ids, err
}
