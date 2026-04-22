package storage

import "errors"

func (s *HotkeyRule) TableName() string {
	return "hotkey_rules"
}

func (s *Store) ListHotkeys(profileID int64) ([]HotkeyRule, error) {
	var hotkeys []HotkeyRule
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC").Find(&hotkeys).Error
	return hotkeys, err
}

func (s *Store) CreateHotkey(profileID int64, shortcut, command string) (HotkeyRule, error) {
	var maxPos int
	s.db.Model(&HotkeyRule{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)

	h := HotkeyRule{
		ProfileID: profileID,
		Position:  maxPos + 1,
		Shortcut:  shortcut,
		Command:   command,
	}
	err := s.db.Create(&h).Error
	return h, err
}

func (s *Store) UpdateHotkey(h HotkeyRule) error {
	result := s.db.Model(&HotkeyRule{}).
		Where("id = ? AND profile_id = ?", h.ID, h.ProfileID).
		Updates(map[string]interface{}{
			"position": h.Position,
			"shortcut": h.Shortcut,
			"command":  h.Command,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("hotkey not found")
	}
	return nil
}

func (s *Store) DeleteHotkeyByID(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&HotkeyRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("hotkey not found")
	}
	return nil
}

func (s *Store) LoadHotkeysForProfiles(profileIDs []int64) ([]HotkeyRule, error) {
	var hotkeys []HotkeyRule
	if len(profileIDs) == 0 {
		return hotkeys, nil
	}
	err := s.db.Where("profile_id IN ?", profileIDs).Order("position ASC").Find(&hotkeys).Error
	return hotkeys, err
}
