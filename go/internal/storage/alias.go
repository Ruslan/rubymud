package storage

import "errors"

type AliasRule struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	ProfileID int64      `json:"profile_id"`
	Position  int        `json:"position"`
	Name      string     `json:"name"`
	Template  string     `json:"template"`
	Enabled   bool       `json:"enabled"`
	GroupName string     `json:"group_name"`
	UpdatedAt SQLiteTime `json:"updated_at"`
}

func (s *AliasRule) TableName() string {
	return "alias_rules"
}

func (s *Store) ListAliases(profileID int64) ([]AliasRule, error) {
	var aliases []AliasRule
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC").Find(&aliases).Error
	return aliases, err
}

func (s *Store) GetAlias(id int64) (*AliasRule, error) {
	var a AliasRule
	err := s.db.First(&a, id).Error
	return &a, err
}

func (s *Store) SaveAlias(profileID int64, name, template string, enabled bool, group string) error {
	if group == "" {
		group = "default"
	}
	var a AliasRule
	err := s.db.Where("profile_id = ? AND name = ?", profileID, name).First(&a).Error
	if err == nil {
		a.Template = template
		a.Enabled = enabled
		a.GroupName = group
		a.UpdatedAt = nowSQLiteTime()
		return s.db.Save(&a).Error
	}
	var maxPos int
	s.db.Model(&AliasRule{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)

	a = AliasRule{
		ProfileID: profileID,
		Position:  maxPos + 1,
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
		Where("id = ? AND profile_id = ?", a.ID, a.ProfileID).
		Updates(map[string]interface{}{
			"position":   a.Position,
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

func (s *Store) DeleteAliasByID(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&AliasRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("alias not found")
	}
	return nil
}

func (s *Store) LoadAliasesForProfiles(profileIDs []int64) ([]AliasRule, error) {
	var aliases []AliasRule
	if len(profileIDs) == 0 {
		return aliases, nil
	}
	err := s.db.Where("profile_id IN ? AND enabled = ?", profileIDs, true).Order("position ASC").Find(&aliases).Error
	return aliases, err
}

func (s *Store) DeleteAlias(profileID int64, name string) error {
	return s.db.Where("profile_id = ? AND name = ?", profileID, name).Delete(&AliasRule{}).Error
}
