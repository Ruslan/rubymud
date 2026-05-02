package storage

import (
	"strings"
	"testing"
)

func TestProfileTimer_ReImportReplacement(t *testing.T) {
	s := newProfileTestStore(t)

	// 1. Create a profile with an existing timer
	p, _ := s.CreateProfile("ReplaceMe", "")
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "old", CycleMS: 5000})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "old", Second: 0, Command: "old cmd"})

	// 2. Prepare a script with a DIFFERENT timer but same profile name
	script := `
#nop Profile: ReplaceMe
#ticksize {new} {10}
#tickat {new} {1} {new cmd}
`
	ps, _ := ParseProfileScript(script)
	
	// 3. Import (should replace)
	p2, err := s.ImportProfileScript(ps)
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}
	if p2.ID != p.ID {
		t.Fatalf("Expected same profile ID, got %d and %d", p.ID, p2.ID)
	}

	// 4. Verify old timer is GONE
	timers, _ := s.GetProfileTimers(p.ID)
	if len(timers) != 1 || timers[0].Name != "new" {
		t.Errorf("Expected only 'new' timer, got %v", timers)
	}

	subs, _ := s.GetProfileTimerSubscriptions(p.ID, "old")
	if len(subs) != 0 {
		t.Error("Expected old subscriptions to be cleared")
	}
}

func TestProfileTimer_RoundTripStability(t *testing.T) {
	s := newProfileTestStore(t)
	p, _ := s.CreateProfile("Stability", "")

	// Create diverse declarations, including a fractional cycle
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "a", Icon: "💡", CycleMS: 30000, RepeatMode: "repeating"})
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "b", CycleMS: 1500, RepeatMode: "one_shot"}) // 1.5s
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "a", Second: 5, Command: "cmd1", SortOrder: 0})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "a", Second: 5, Command: "cmd2", SortOrder: 1}) // Multi-sub

	normalize := func(script string) string {
		lines := strings.Split(script, "\n")
		var filtered []string
		for _, line := range lines {
			// Remove #nop rubymud:profile line which contains the "exported" timestamp
			if strings.HasPrefix(line, "#nop rubymud:profile ") {
				continue
			}
			filtered = append(filtered, line)
		}
		return strings.Join(filtered, "\n")
	}

	// Round-trip 1
	exported1, _ := s.ExportProfileScript(p.ID)
	ps1, _ := ParseProfileScript(exported1)
	p2, _ := s.ImportProfileScript(ps1)

	// Round-trip 2
	exported2, _ := s.ExportProfileScript(p2.ID)
	
	n1 := normalize(exported1)
	n2 := normalize(exported2)

	// They should be identical in content (because of stable alphabetical ordering and normalized metadata)
	if n1 != n2 {
		t.Errorf("Export not stable!\nN1:\n%s\nN2:\n%s", n1, n2)
	}

	// Verify fractional cycle precision was preserved
	timers, _ := s.GetProfileTimers(p2.ID)
	foundB := false
	for _, timer := range timers {
		if timer.Name == "b" {
			foundB = true
			if timer.CycleMS != 1500 {
				t.Errorf("Fractional cycle lost precision: got %dms, want 1500ms", timer.CycleMS)
			}
		}
	}
	if !foundB {
		t.Error("Timer 'b' with fractional cycle missing after import")
	}
}

func TestProfileTimer_MultiSubSameSecond(t *testing.T) {
	s := newProfileTestStore(t)
	p, _ := s.CreateProfile("MultiSub", "")

	// Three commands on the same second
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "m", CycleMS: 60000})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "m", Second: 0, Command: "first", SortOrder: 0})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "m", Second: 0, Command: "second", SortOrder: 1})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "m", Second: 0, Command: "third", SortOrder: 2})

	exported, _ := s.ExportProfileScript(p.ID)
	ps, _ := ParseProfileScript(exported)
	p2, _ := s.ImportProfileScript(ps)

	subs, _ := s.GetProfileTimerSubscriptions(p2.ID, "m")
	if len(subs) != 3 {
		t.Fatalf("Expected 3 subs, got %d", len(subs))
	}

	if subs[0].Command != "first" || subs[1].Command != "second" || subs[2].Command != "third" {
		t.Errorf("Order not preserved for multi-sub: %v", subs)
	}
}

func TestProfileTimer_PartialDeclarations(t *testing.T) {
	s := newProfileTestStore(t)
	p, _ := s.CreateProfile("Partial", "")

	// 1. Icon but no subs
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "icon_only", Icon: "🖼️", CycleMS: 60000})
	
	// 2. Subs but empty icon
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "subs_only", Icon: "", CycleMS: 60000})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{ProfileID: p.ID, TimerName: "subs_only", Second: 1, Command: "ping"})

	exported, _ := s.ExportProfileScript(p.ID)
	
	if !strings.Contains(exported, "#tickicon {icon_only} {🖼️}") {
		t.Error("Missing icon_only export")
	}
	if strings.Contains(exported, "#tickat {icon_only}") {
		t.Error("icon_only should not have #tickat lines")
	}
	if strings.Contains(exported, "#tickicon {subs_only}") {
		t.Error("subs_only should not have #tickicon line (empty)")
	}
	if !strings.Contains(exported, "#tickat {subs_only} {1} {ping}") {
		t.Error("Missing subs_only sub")
	}
}

func TestProfileTimer_NoRuntimeStateExport(t *testing.T) {
	s := newProfileTestStore(t)
	p, _ := s.CreateProfile("RuntimeExclusion", "")

	// Create declaration
	s.SaveProfileTimer(ProfileTimer{ProfileID: p.ID, Name: "t1", CycleMS: 60000})
	
	// Create runtime/session state (which should be IGNORED by profile script export)
	past := nowSQLiteTime()
	s.db.Create(&TimerRecord{
		SessionID: 1, 
		Name: "t1", 
		Enabled: true, 
		RemainingMS: 12345, 
		NextTickAt: &past,
		RepeatMode: "one_shot", // Different from declaration to be extra sure
	})

	exported, _ := s.ExportProfileScript(p.ID)
	
	// These strings would only appear if we accidentally leaked runtime state fields
	if strings.Contains(exported, "12345") {
		t.Error("Export leaked RemainingMS")
	}
	if strings.Contains(exported, "one_shot") {
		// Declaration was default 'repeating', runtime was 'one_shot'
		// If 'one_shot' appears, it means we took it from the session record
		t.Error("Export leaked runtime RepeatMode")
	}
}
