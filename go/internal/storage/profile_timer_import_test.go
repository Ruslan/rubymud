package storage

import (
	"strings"
	"testing"
)

func TestProfileTimer_ImportExport(t *testing.T) {
	s := newProfileTestStore(t)

	// 1. Create a profile with timers
	p, err := s.CreateProfile("TimerProfile", "")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Named repeating timer
	t1 := ProfileTimer{
		ProfileID:  p.ID,
		Name:       "rep",
		Icon:       "🔄",
		CycleMS:    30000,
		RepeatMode: "repeating",
	}
	s.SaveProfileTimer(t1)
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{
		ProfileID: p.ID, TimerName: "rep", Second: 0, Command: "rep cmd 0",
	})
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{
		ProfileID: p.ID, TimerName: "rep", Second: 10, Command: "rep cmd 10",
	})

	// Named one_shot timer
	t2 := ProfileTimer{
		ProfileID:  p.ID,
		Name:       "once",
		Icon:       "🎯",
		CycleMS:    10000,
		RepeatMode: "one_shot",
	}
	s.SaveProfileTimer(t2)
	s.SaveProfileTimerSubscription(ProfileTimerSubscription{
		ProfileID: p.ID, TimerName: "once", Second: 0, Command: "once cmd 0",
	})

	// Add default ticker (should NOT be exported)
	s.db.Create(&TimerRecord{SessionID: 1, Name: "ticker", CycleMS: 60000, Enabled: true})

	// 2. Export
	exported, err := s.ExportProfileScript(p.ID)
	if err != nil {
		t.Fatalf("ExportProfileScript: %v", err)
	}

	// Verify export content
	if !strings.Contains(exported, "#tickicon {rep} {🔄}") {
		t.Errorf("Export missing rep icon: %s", exported)
	}
	if !strings.Contains(exported, "#ticksize {rep} {30}") {
		t.Errorf("Export missing rep size: %s", exported)
	}
	if strings.Contains(exported, "#tickmode {rep} {repeating}") {
		t.Error("Export should NOT contain default repeating mode line")
	}
	if !strings.Contains(exported, "#tickat {rep} {0} {rep cmd 0}") {
		t.Errorf("Export missing rep sub 0: %s", exported)
	}
	if !strings.Contains(exported, "#tickat {rep} {10} {rep cmd 10}") {
		t.Errorf("Export missing rep sub 10: %s", exported)
	}

	if !strings.Contains(exported, "#tickmode {once} {one_shot}") {
		t.Errorf("Export missing one_shot mode: %s", exported)
	}
	if !strings.Contains(exported, "#tickicon {once} {🎯}") {
		t.Errorf("Export missing once icon: %s", exported)
	}

	if strings.Contains(exported, "{ticker}") {
		t.Error("Export should NOT contain default ticker")
	}

	// 3. Parse back
	ps, err := ParseProfileScript(exported)
	if err != nil {
		t.Fatalf("ParseProfileScript: %v", err)
	}

	if len(ps.Timers) != 2 {
		t.Fatalf("Expected 2 timers parsed, got %d", len(ps.Timers))
	}
	if len(ps.Subscriptions) != 3 {
		t.Fatalf("Expected 3 subscriptions parsed, got %d", len(ps.Subscriptions))
	}

	// Verify order in ps.Subscriptions (alphabetical timers: once then rep)
	if ps.Subscriptions[0].TimerName != "once" || ps.Subscriptions[0].Second != 0 || ps.Subscriptions[0].Command != "once cmd 0" {
		t.Errorf("Parsed sub 0 mismatch: %+v", ps.Subscriptions[0])
	}
	if ps.Subscriptions[1].TimerName != "rep" || ps.Subscriptions[1].Second != 0 || ps.Subscriptions[1].Command != "rep cmd 0" {
		t.Errorf("Parsed sub 1 mismatch: %+v", ps.Subscriptions[1])
	}
	if ps.Subscriptions[2].TimerName != "rep" || ps.Subscriptions[2].Second != 10 || ps.Subscriptions[2].Command != "rep cmd 10" {
		t.Errorf("Parsed sub 2 mismatch: %+v", ps.Subscriptions[2])
	}

	// 4. Import into new profile
	ps.Name = "ImportedTimerProfile"
	p2, err := s.ImportProfileScript(ps)
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}

	// 5. Verify DB content for imported profile
	timers, _ := s.GetProfileTimers(p2.ID)
	if len(timers) != 2 {
		t.Errorf("Imported DB should have 2 timers, got %d", len(timers))
	}

	for _, timer := range timers {
		if timer.Name == "once" {
			if timer.RepeatMode != "one_shot" || timer.Icon != "🎯" || timer.CycleMS != 10000 {
				t.Errorf("Imported 'once' timer mismatch: %+v", timer)
			}
		}
	}

	subs, _ := s.GetProfileTimerSubscriptions(p2.ID, "rep")
	if len(subs) != 2 {
		t.Errorf("Imported 'rep' should have 2 subs, got %d", len(subs))
	}
	// Verify order: (0, order0), (10, order10)
	if subs[0].Second != 0 || subs[0].Command != "rep cmd 0" {
		t.Errorf("Imported sub 0 mismatch: %+v", subs[0])
	}
	if subs[1].Second != 10 || subs[1].Command != "rep cmd 10" {
		t.Errorf("Imported sub 1 mismatch: %+v", subs[1])
	}
}
