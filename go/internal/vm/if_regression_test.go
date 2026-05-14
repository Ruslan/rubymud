package vm

import (
	"strings"
	"testing"
)

func TestIfWithAliasCaptures(t *testing.T) {
	v := New(nil, 1)
	v.variables["bag"] = "рюкзак"
	v.variables["money"] = "деньги"

	// Exact `ден` use-case from plan
	v.dispatchCommand("#alias {ден} {#if {%0 > 0} {положить %0 монет $bag} {положить $money монет $bag}}", 0, nil)

	tests := []struct {
		input    string
		expected string
	}{
		{"ден 10", "положить 10 монет рюкзак"},
		{"ден 0", "положить деньги монет рюкзак"},
		{"ден", "положить деньги монет рюкзак"},
	}

	for _, tt := range tests {
		result := v.ProcessInput(tt.input)
		if len(result) != 1 {
			t.Fatalf("input %q expected 1 result, got %d", tt.input, len(result))
		}
		if result[0] != tt.expected {
			t.Errorf("input %q = %q, want %q", tt.input, result[0], tt.expected)
		}
	}
}

func TestIfWithTriggerCaptures(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#action {^A (.*) attacks you!$} {#if {%1 == \"goblin\"} {flee} {kill %1}}", 0, nil)

	tests := []struct {
		input    string
		expected string
	}{
		{"A goblin attacks you!", "flee"},
		{"A dragon attacks you!", "kill dragon"},
	}

	for _, tt := range tests {
		effects, _ := v.MatchTriggers(tt.input)
		var cmds []string
		v.ApplyEffects(effects, 0, "main", func(cmd string, id int64, buf string) error {
			cmds = append(cmds, cmd)
			return nil
		}, func(r Result) {})

		if len(cmds) != 1 {
			t.Fatalf("input %q expected 1 command, got %v", tt.input, cmds)
		}
		if cmds[0] != tt.expected {
			t.Errorf("input %q = %q, want %q", tt.input, cmds[0], tt.expected)
		}
	}
}

func TestIfModuloParsing(t *testing.T) {
	v := New(nil, 1)
	v.variables["hp"] = "40"

	tests := []struct {
		input    string
		expected string
	}{
		{"#if {$hp % 2 == 0} {even} {odd}", "even"},
		{"#if {$hp%2 == 0} {even} {odd}", "even"},
	}

	for _, tt := range tests {
		results := v.ProcessInput(tt.input)
		if len(results) != 1 || results[0] != tt.expected {
			t.Errorf("input %q = %v, want [%q]", tt.input, results, tt.expected)
		}
	}
}

func TestIfCaptureGuards(t *testing.T) {
	v := New(nil, 1)

	tests := []struct {
		input    string
		expected string
	}{
		{"#if {%1 != \"\"} {yes} {no}", "no"},
		{"#if {%1 == \"\"} {yes} {no}", "yes"},
		{"#if {%1 == 0} {yes} {no}", "yes"},
		{"#if {%1 > 0} {yes} {no}", "no"},
		{"#if {%1 < 0} {yes} {no}", "no"},
	}

	for _, tt := range tests {
		result := v.ProcessInput(tt.input)
		if len(result) != 1 {
			t.Fatalf("input %q expected 1 result, got %d", tt.input, len(result))
		}
		if result[0] != tt.expected {
			t.Errorf("input %q = %q, want %q", tt.input, result[0], tt.expected)
		}
	}

	v.dispatchCommand("#alias {guard} {#if {%1 != \"\"} {yes} {no}}", 0, nil)
	aliasTests := []struct {
		input    string
		expected string
	}{
		{"guard", "no"},
		{"guard goblin", "yes"},
		{"guard 5", "yes"},
	}

	for _, tt := range aliasTests {
		result := v.ProcessInput(tt.input)
		if len(result) != 1 {
			t.Fatalf("input %q expected 1 result, got %d", tt.input, len(result))
		}
		if result[0] != tt.expected {
			t.Errorf("input %q = %q, want %q", tt.input, result[0], tt.expected)
		}
	}
}

func TestIfTypeError(t *testing.T) {
	v := New(nil, 1)

	// pass a non-empty non-numeric string as %1
	v.dispatchCommand("#alias {t} {#if {%1 > 0} {then} {else}}", 0, nil)

	got := v.ProcessInputDetailed("t goblin")
	found := false
	for _, r := range got {
		if r.Kind == ResultEcho && strings.Contains(r.Text, "mismatched types") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ProcessInputDetailed did not return expected type mismatch error. Got: %v", got)
	}
}

// TestIfModuloWithSpace verifies that "$hp %2 == 0" (space before %) is treated as modulo.
func TestIfModuloWithSpace(t *testing.T) {
	v := New(nil, 1)
	v.variables["hp"] = "40"

	results := v.ProcessInput("#if {$hp %2 == 0} {even} {odd}")
	if len(results) != 1 || results[0] != "even" {
		t.Errorf("#if {$hp %%2 == 0} = %v, want [even]", results)
	}
}

// TestTriggerSendExpandsCapture verifies that trigger send command is expanded
// in ApplyEffects (via ProcessInputWithCaptures), not at MatchTriggers time.
func TestTriggerSendExpandsCapture(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#action {^There were (\\d+) coins\\.$} {split %1}", 0, nil)

	effects, _ := v.MatchTriggers("There were 42 coins.")
	if len(effects) != 1 {
		t.Fatalf("expected 1 effect, got %d", len(effects))
	}
	// Raw command must be preserved for #if evaluation
	if effects[0].Command != "split %1" {
		t.Errorf("send Effect.Command = %q, want \"split %%1\"", effects[0].Command)
	}

	// After ApplyEffects the expanded command is sent
	var sent []string
	v.ApplyEffects(effects, 0, "main", func(cmd string, id int64, buf string) error {
		sent = append(sent, cmd)
		return nil
	}, func(r Result) {})

	if len(sent) != 1 || sent[0] != "split 42" {
		t.Errorf("ApplyEffects sent %v, want [split 42]", sent)
	}
}

// TestTriggerButtonExpandsCapture verifies that button Command is expanded at
// MatchTriggers time so it is clickable without a live capture context.
func TestTriggerButtonExpandsCapture(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#action {^(.+) R\\.I\\.P\\.$} {loot %1}", 0, nil)
	// Mark the action as a button by patching triggers directly after compilation
	v.ensureFresh()
	if len(v.compiledTriggers) > 0 {
		v.compiledTriggers[len(v.compiledTriggers)-1].rule.IsButton = true
	}

	effects, _ := v.MatchTriggers("Крыса R.I.P.")
	if len(effects) != 1 || effects[0].Type != "button" {
		t.Fatalf("expected 1 button effect, got %v", effects)
	}
	// Button command must be expanded (loot Крыса), not raw
	if effects[0].Command != "loot Крыса" {
		t.Errorf("button Effect.Command = %q, want \"loot Крыса\"", effects[0].Command)
	}
}
