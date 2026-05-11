package storage

import "errors"

type SubstituteRule struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	ProfileID   int64      `json:"profile_id"`
	Position    int        `json:"position"`
	Pattern     string     `json:"pattern"`
	Replacement string     `json:"replacement"`
	IsGag       bool       `json:"is_gag"`
	Enabled     bool       `json:"enabled"`
	GroupName   string     `json:"group_name"`
	UpdatedAt   SQLiteTime `json:"updated_at"`
}

func (s *SubstituteRule) TableName() string {
	return "substitute_rules"
}

func (s *Store) ListSubstitutes(profileID int64) ([]SubstituteRule, error) {
	var rules []SubstituteRule
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC, id ASC").Find(&rules).Error
	return rules, err
}

func (s *Store) GetSubstitute(id int64) (*SubstituteRule, error) {
	var rule SubstituteRule
	err := s.db.First(&rule, id).Error
	return &rule, err
}

func (s *Store) CreateSubstitute(rule SubstituteRule) error {
	if rule.GroupName == "" {
		rule.GroupName = "default"
	}
	if rule.IsGag {
		rule.Replacement = ""
	}
	rule.UpdatedAt = nowSQLiteTime()
	if rule.Position == 0 {
		var maxPos int
		s.db.Model(&SubstituteRule{}).Where("profile_id = ?", rule.ProfileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
		rule.Position = maxPos + 1
	}
	return s.db.Create(&rule).Error
}

func (s *Store) UpdateSubstitute(rule SubstituteRule) error {
	if rule.GroupName == "" {
		rule.GroupName = "default"
	}
	if rule.IsGag {
		rule.Replacement = ""
	}
	rule.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&SubstituteRule{}).
		Where("id = ? AND profile_id = ?", rule.ID, rule.ProfileID).
		Updates(map[string]interface{}{
			"position":    rule.Position,
			"pattern":     rule.Pattern,
			"replacement": rule.Replacement,
			"is_gag":      rule.IsGag,
			"enabled":     rule.Enabled,
			"group_name":  rule.GroupName,
			"updated_at":  rule.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("substitute rule not found")
	}
	return nil
}

func (s *Store) DeleteSubstituteByID(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&SubstituteRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("substitute rule not found")
	}
	return nil
}

func (s *Store) LoadSubstitutesForProfiles(profileIDs []int64) ([]SubstituteRule, error) {
	var rules []SubstituteRule
	if len(profileIDs) == 0 {
		return rules, nil
	}
	err := s.db.Where("profile_id IN ? AND enabled = ?", profileIDs, true).Order("position ASC, id ASC").Find(&rules).Error
	return rules, err
}

func (s *Store) SaveSubstitute(profileID int64, pattern, replacement string, isGag bool, group string) error {
	if group == "" {
		group = "default"
	}
	var existing SubstituteRule
	err := s.db.Where("profile_id = ? AND pattern = ?", profileID, pattern).First(&existing).Error
	if err == nil {
		existing.Replacement = replacement
		existing.IsGag = isGag
		existing.Enabled = true
		existing.GroupName = group
		existing.UpdatedAt = nowSQLiteTime()
		return s.db.Save(&existing).Error
	}

	var maxPos int
	s.db.Model(&SubstituteRule{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
	rule := SubstituteRule{
		ProfileID:   profileID,
		Position:    maxPos + 1,
		Pattern:     pattern,
		Replacement: replacement,
		IsGag:       isGag,
		Enabled:     true,
		GroupName:   group,
		UpdatedAt:   nowSQLiteTime(),
	}
	return s.db.Create(&rule).Error
}

func (s *Store) DeleteSubstitute(profileID int64, pattern string) error {
	return s.db.Where("profile_id = ? AND pattern = ?", profileID, pattern).Delete(&SubstituteRule{}).Error
}
