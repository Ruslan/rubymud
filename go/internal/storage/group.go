package storage

import "fmt"

type RuleGroupSummary struct {
	Domain        string `json:"domain"`
	GroupName     string `json:"group_name"`
	TotalCount    int64  `json:"total_count"`
	EnabledCount  int64  `json:"enabled_count"`
	DisabledCount int64  `json:"disabled_count"`
}

func (s *Store) ListRuleGroups(profileID int64) ([]RuleGroupSummary, error) {
	type domainSpec struct {
		domain string
		table  string
	}

	var groups []RuleGroupSummary
	for _, spec := range []domainSpec{
		{domain: "aliases", table: "alias_rules"},
		{domain: "triggers", table: "trigger_rules"},
		{domain: "highlights", table: "highlight_rules"},
	} {
		var rows []RuleGroupSummary
		err := s.db.Table(spec.table).
			Select("? AS domain, COALESCE(group_name, 'default') AS group_name, COUNT(*) AS total_count, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) AS enabled_count, SUM(CASE WHEN enabled THEN 0 ELSE 1 END) AS disabled_count", spec.domain).
			Where("profile_id = ?", profileID).
			Group("COALESCE(group_name, 'default')").
			Order("group_name").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		groups = append(groups, rows...)
	}
	return groups, nil
}

func (s *Store) SetGroupEnabled(profileID int64, domain, group string, enabled bool) error {
	if group == "" {
		group = "default"
	}

	table, err := ruleTable(domain)
	if err != nil {
		return err
	}

	return s.db.Table(table).
		Where("profile_id = ? AND COALESCE(group_name, 'default') = ?", profileID, group).
		Updates(map[string]any{"enabled": enabled, "updated_at": nowSQLiteTime()}).Error
}

func ruleTable(domain string) (string, error) {
	switch domain {
	case "aliases":
		return "alias_rules", nil
	case "triggers":
		return "trigger_rules", nil
	case "highlights":
		return "highlight_rules", nil
	default:
		return "", fmt.Errorf("unsupported domain: %s", domain)
	}
}
