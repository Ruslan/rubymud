package storage

import (
	"testing"
)

func TestUnifiedGroups(t *testing.T) {
	s := newProfileTestStore(t)

	// 1. Create a profile
	p, _ := s.CreateProfile("Test Groups", "")

	// 2. Add rules to different domains but the same group "combat"
	// Alias in group "combat"
	_ = s.db.Create(&AliasRule{
		ProfileID: p.ID, Name: "k", Template: "kill %1", GroupName: "combat", Enabled: true,
	}).Error

	// Trigger in group "combat"
	_ = s.db.Create(&TriggerRule{
		ProfileID: p.ID, Pattern: "You are dead", Command: "quit", GroupName: "combat", Enabled: true,
	}).Error

	// Highlight in group "combat" (but DISABLED)
	_ = s.db.Create(&HighlightRule{
		ProfileID: p.ID, Pattern: "Dragon", FG: "red", GroupName: "combat", Enabled: false,
	}).Error

	// Add a rule in a different group "util"
	_ = s.db.Create(&AliasRule{
		ProfileID: p.ID, Name: "i", Template: "inventory", GroupName: "util", Enabled: true,
	}).Error

	// 3. Test ListUnifiedGroups
	groups, err := s.ListUnifiedGroups(p.ID)
	if err != nil {
		t.Fatalf("ListUnifiedGroups failed: %v", err)
	}

	// We expect 2 groups: combat and util
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}

	// Check "combat" counts: 3 total (alias + trigger + highlight), 2 enabled, 1 disabled
	var combat *UnifiedGroupSummary
	for i := range groups {
		if groups[i].GroupName == "combat" {
			combat = &groups[i]
		}
	}

	if combat == nil {
		t.Fatal("Group 'combat' not found")
	}
	if combat.TotalCount != 3 || combat.EnabledCount != 2 || combat.DisabledCount != 1 {
		t.Errorf("Incorrect combat counts: %+v", combat)
	}

	// 4. Test Toggle
	err = s.SetUnifiedGroupEnabled(p.ID, "combat", false)
	if err != nil {
		t.Fatalf("SetUnifiedGroupEnabled failed: %v", err)
	}

	// Verify all are disabled now
	groups2, _ := s.ListUnifiedGroups(p.ID)
	for _, g := range groups2 {
		if g.GroupName == "combat" {
			if g.EnabledCount != 0 || g.DisabledCount != 3 {
				t.Errorf("Expected all rules in combat to be disabled, got %+v", g)
			}
		}
	}
}
