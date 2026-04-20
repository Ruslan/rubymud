package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

func TestArcticTriggerAnchoredCaret(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are thirsty\.`, Command: "drink all", Enabled: true},
	}

	effects := v.MatchTriggers("You are thirsty.", 1)
	if len(effects) != 1 {
		t.Fatalf("anchored trigger match = %d, want 1", len(effects))
	}

	effects = v.MatchTriggers("Someone says: You are thirsty.", 2)
	if len(effects) != 0 {
		t.Errorf("anchored trigger should NOT match mid-line, got %d matches", len(effects))
	}
}

func TestArcticTriggerWithCapture(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) is dead!`, Command: "get coins corpse", Enabled: true},
	}

	effects := v.MatchTriggers("The Dragon is dead!", 1)
	if len(effects) != 1 || effects[0].Command != "get coins corpse" {
		t.Errorf("is dead trigger = %v, want send{get coins corpse}", effects)
	}
}

func TestArcticTriggerSplitCoins(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^There were (\d+) coins\.`, Command: "split %1", Enabled: true},
	}

	effects := v.MatchTriggers("There were 42 coins.", 1)
	if len(effects) != 1 || effects[0].Command != "split 42" {
		t.Errorf("split coins trigger = %v, want send{split 42}", effects)
	}
}

func TestArcticTriggerTwoCaptures(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) swings madly at you with (.+), knocking you to the ground\.`, Command: "stand", Enabled: true},
	}

	effects := v.MatchTriggers("Гоблин swings madly at you with дубина, knocking you to the ground.", 1)
	if len(effects) != 1 || effects[0].Command != "stand" {
		t.Errorf("two-capture trigger = %v, want send{stand}", effects)
	}
}

func TestArcticTriggerFlyLoss(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You feel much heavier\.`, Command: "fly;fly", Enabled: true},
	}

	effects := v.MatchTriggers("You feel much heavier.", 1)
	if len(effects) != 1 {
		t.Fatalf("fly loss trigger = %d effects, want 1", len(effects))
	}

	commands := v.ExpandInput(effects[0].Command)
	if len(commands) != 2 || commands[0] != "fly" || commands[1] != "fly" {
		t.Errorf("fly;fly expansion = %v, want [fly, fly]", commands)
	}
}

func TestArcticTriggerSummonWithMulticmd(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) has summoned you!`, Command: "wake;stand;fly", Enabled: true},
	}

	effects := v.MatchTriggers("Маг has summoned you!", 1)
	if len(effects) != 1 {
		t.Fatalf("summon trigger = %d effects, want 1", len(effects))
	}

	commands := v.ExpandInput(effects[0].Command)
	expected := []string{"wake", "stand", "fly"}
	if len(commands) != len(expected) {
		t.Fatalf("summon expansion = %v, want %v", commands, expected)
	}
	for i := range expected {
		if commands[i] != expected[i] {
			t.Errorf("summon[%d] = %q, want %q", i, commands[i], expected[i])
		}
	}
}

func TestArcticRipButtonTrigger(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `R\.I\.P\.$`, Command: "взя все *.тру", IsButton: true, Enabled: true},
	}

	effects := v.MatchTriggers("Крыса R.I.P.", 42)
	if len(effects) != 1 || effects[0].Type != "button" {
		t.Fatalf("RIP button trigger = %v, want button", effects)
	}
	if effects[0].Command != "взя все *.тру" {
		t.Errorf("button command = %q, want %q", effects[0].Command, "взя все *.тру")
	}
}

func TestMultipleTriggersMatch(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are hungry\.`, Command: "eat all", Enabled: true},
		{Pattern: `^You are`, Command: "look", Enabled: true},
	}

	effects := v.MatchTriggers("You are hungry.", 1)
	if len(effects) != 2 {
		t.Errorf("two triggers matching same line = %d effects, want 2", len(effects))
	}
}

func TestTriggerNoMatch(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You are thirsty\.`, Command: "drink all", Enabled: true},
	}

	effects := v.MatchTriggers("You are hungry.", 1)
	if len(effects) != 0 {
		t.Errorf("non-matching trigger = %d effects, want 0", len(effects))
	}
}

func TestDisabledTrigger(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^test`, Command: "cmd", Enabled: false},
	}

	effects := v.MatchTriggers("test line", 1)
	if len(effects) != 0 {
		t.Errorf("disabled trigger should not match, got %d effects", len(effects))
	}
}

func TestArcticTriggerCaptureInCommand(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^(.+) pants heavily\.`, Command: "cast 'refresh' %1", Enabled: true},
	}

	effects := v.MatchTriggers("Воин pants heavily.", 1)
	if len(effects) != 1 {
		t.Fatalf("refresh trigger = %d, want 1", len(effects))
	}
	if effects[0].Command != "cast 'refresh' Воин" {
		t.Errorf("capture in command = %q, want %q", effects[0].Command, "cast 'refresh' Воин")
	}
}

func TestArcticTriggerCancelStand(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^You should probably stand up!`, Command: "cancel;stand", Enabled: true},
	}

	effects := v.MatchTriggers("You should probably stand up!", 1)
	if len(effects) != 1 {
		t.Fatalf("cancel+stand trigger = %d, want 1", len(effects))
	}

	commands := v.ExpandInput(effects[0].Command)
	if len(commands) != 2 || commands[0] != "cancel" || commands[1] != "stand" {
		t.Errorf("cancel;stand expansion = %v, want [cancel, stand]", commands)
	}
}
