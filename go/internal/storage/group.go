package storage

type UnifiedGroupSummary struct {
	GroupName     string `json:"group_name"`
	TotalCount    int64  `json:"total_count"`
	EnabledCount  int64  `json:"enabled_count"`
	DisabledCount int64  `json:"disabled_count"`
}

// ListUnifiedGroups aggregates rules from all domains by group name.
func (s *Store) ListUnifiedGroups(profileID int64) ([]UnifiedGroupSummary, error) {
	// We use a UNION to collect all group names and their counts from rule tables.
	query := `
		SELECT group_name, SUM(total) as total_count, SUM(enabled) as enabled_count, SUM(disabled) as disabled_count
		FROM (
			SELECT COALESCE(group_name, 'default') as group_name, COUNT(*) as total, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) as enabled, SUM(CASE WHEN enabled THEN 0 ELSE 1 END) as disabled FROM alias_rules WHERE profile_id = ? GROUP BY group_name
			UNION ALL
			SELECT COALESCE(group_name, 'default') as group_name, COUNT(*) as total, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) as enabled, SUM(CASE WHEN enabled THEN 0 ELSE 1 END) as disabled FROM trigger_rules WHERE profile_id = ? GROUP BY group_name
			UNION ALL
			SELECT COALESCE(group_name, 'default') as group_name, COUNT(*) as total, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) as enabled, SUM(CASE WHEN enabled THEN 0 ELSE 1 END) as disabled FROM highlight_rules WHERE profile_id = ? GROUP BY group_name
			UNION ALL
			SELECT COALESCE(group_name, 'default') as group_name, COUNT(*) as total, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) as enabled, SUM(CASE WHEN enabled THEN 0 ELSE 1 END) as disabled FROM substitute_rules WHERE profile_id = ? GROUP BY group_name
		) 
		GROUP BY group_name
		ORDER BY group_name ASC
	`
	var results []UnifiedGroupSummary
	err := s.db.Raw(query, profileID, profileID, profileID, profileID).Scan(&results).Error
	return results, err
}

// SetUnifiedGroupEnabled toggles a group across all rule domains at once.
func (s *Store) SetUnifiedGroupEnabled(profileID int64, group string, enabled bool) error {
	if group == "" {
		group = "default"
	}

	domains := []string{"alias_rules", "trigger_rules", "highlight_rules", "substitute_rules"}
	for _, table := range domains {
		err := s.db.Table(table).
			Where("profile_id = ? AND COALESCE(group_name, 'default') = ?", profileID, group).
			Updates(map[string]any{"enabled": enabled, "updated_at": nowSQLiteTime()}).Error
		if err != nil {
			return err
		}
	}
	return nil
}
