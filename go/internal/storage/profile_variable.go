package storage

import "errors"

func (s *Store) ListProfileVariables(profileID int64) ([]ProfileVariable, error) {
	var vars []ProfileVariable
	err := s.db.Where("profile_id = ?", profileID).Order("position ASC").Find(&vars).Error
	return vars, err
}

func (s *Store) CreateProfileVariable(profileID int64, name, defaultValue, description string) (ProfileVariable, error) {
	var maxPos int
	s.db.Model(&ProfileVariable{}).Where("profile_id = ?", profileID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)

	pv := ProfileVariable{
		ProfileID:    profileID,
		Position:     maxPos + 1,
		Name:         name,
		DefaultValue: defaultValue,
		Description:  description,
		UpdatedAt:    nowSQLiteTime(),
	}
	err := s.db.Create(&pv).Error
	return pv, err
}

func (s *Store) UpdateProfileVariable(pv ProfileVariable) error {
	pv.UpdatedAt = nowSQLiteTime()
	result := s.db.Model(&ProfileVariable{}).
		Where("id = ? AND profile_id = ?", pv.ID, pv.ProfileID).
		Updates(map[string]interface{}{
			"position":      pv.Position,
			"name":          pv.Name,
			"default_value": pv.DefaultValue,
			"description":   pv.Description,
			"updated_at":    pv.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("profile variable not found")
	}
	return nil
}

func (s *Store) DeleteProfileVariable(id, profileID int64) error {
	result := s.db.Where("id = ? AND profile_id = ?", id, profileID).Delete(&ProfileVariable{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("profile variable not found")
	}
	return nil
}

func (s *Store) LoadProfileVariablesForProfiles(profileIDs []int64) ([]ProfileVariable, error) {
	var vars []ProfileVariable
	if len(profileIDs) == 0 {
		return vars, nil
	}
	// We want to load all variables from all profiles, but for merging, first wins.
	// However, we load all and then merge in the caller (VM/Server) for better control.
	err := s.db.Where("profile_id IN ?", profileIDs).Order("position ASC").Find(&vars).Error
	return vars, err
}

type ResolvedVariable struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	DefaultValue string `json:"default_value"`
	Declared     bool   `json:"declared"`
	HasValue     bool   `json:"has_value"`
	UsesDefault  bool   `json:"uses_default"`
}

func (s *Store) ListResolvedVariablesForSession(sessionID int64) ([]ResolvedVariable, error) {
	// 1. Get ordered profile IDs
	profileIDs, err := s.GetOrderedProfileIDs(sessionID)
	if err != nil {
		return nil, err
	}

	// 2. Load declared variables from profiles
	declaredVars, err := s.LoadProfileVariablesForProfiles(profileIDs)
	if err != nil {
		return nil, err
	}

	// 3. Load actual session values
	sessionVars, err := s.ListVariables(sessionID)
	if err != nil {
		return nil, err
	}

	sessionValueMap := make(map[string]string)
	for _, sv := range sessionVars {
		sessionValueMap[sv.Key] = sv.Value
	}

	// 4. Merge
	// We use a map to handle overrides across profiles (higher priority first)
	// and a slice to keep the order.
	resolvedMap := make(map[string]*ResolvedVariable)
	var orderedNames []string

	// profileIDs is ordered DESC by order_index: highest priority first.
	// First declaration by name wins — matches alias merge semantics.
	for _, pid := range profileIDs {
		for _, dv := range declaredVars {
			if dv.ProfileID != pid {
				continue
			}
			if _, exists := resolvedMap[dv.Name]; !exists {
				rv := &ResolvedVariable{
					Name:         dv.Name,
					DefaultValue: dv.DefaultValue,
					Declared:     true,
				}
				resolvedMap[dv.Name] = rv
				orderedNames = append(orderedNames, dv.Name)
			}
		}
	}

	// Add any session variables that are not declared
	for _, sv := range sessionVars {
		if _, exists := resolvedMap[sv.Key]; !exists {
			rv := &ResolvedVariable{
				Name:     sv.Key,
				Declared: false,
			}
			resolvedMap[sv.Key] = rv
			orderedNames = append(orderedNames, sv.Key)
		}
	}

	// Apply values and defaults
	var result []ResolvedVariable
	for _, name := range orderedNames {
		rv := resolvedMap[name]
		if val, ok := sessionValueMap[name]; ok {
			rv.Value = val
			rv.HasValue = true
			rv.UsesDefault = false
		} else if rv.Declared && rv.DefaultValue != "" {
			rv.Value = rv.DefaultValue
			rv.HasValue = false
			rv.UsesDefault = true
		} else {
			rv.Value = ""
			rv.HasValue = false
			rv.UsesDefault = false
		}
		result = append(result, *rv)
	}

	return result, nil
}
