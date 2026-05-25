package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

func resultTexts(results []Result) []string {
	texts := make([]string, 0, len(results))
	for _, result := range results {
		texts = append(texts, result.Text)
	}
	return texts
}

func inputVisibleTexts(results []Result) []string {
	texts := make([]string, 0, len(results))
	for _, result := range results {
		if result.IsInternal && result.Depth > 0 && !result.ShowOnInput {
			continue
		}
		texts = append(texts, result.Text)
	}
	return texts
}

func TestSubstituteVars(t *testing.T) {
	v := New(nil, 1)
	v.variables["двуруч"] = "фламберг"
	v.variables["сумка"] = "сумк"

	tests := []struct {
		input    string
		expected string
	}{
		{"взя $двуруч $сумка", "взя фламберг сумк"},
		{"у $t1", "у $t1"},
		{"нет переменных тут", "нет переменных тут"},
		{"$двуруч и $сумка", "фламберг и сумк"},
	}

	for _, tt := range tests {
		result := v.substituteVars(tt.input)
		if result != tt.expected {
			t.Errorf("substituteVars(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestBuiltinVars(t *testing.T) {
	_, ok := builtinVar("DATE")
	if !ok {
		t.Error("builtinVar(DATE) should exist")
	}
	_, ok = builtinVar("TIME")
	if !ok {
		t.Error("builtinVar(TIME) should exist")
	}
	_, ok = builtinVar("NONEXISTENT")
	if ok {
		t.Error("builtinVar(NONEXISTENT) should not exist")
	}
}

func TestCmdVariableBracesStripped(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#var {weapon} {фламберг}", 0, nil)

	if v.variables["weapon"] != "фламберг" {
		t.Errorf("variable value should be 'фламберг', got %q", v.variables["weapon"])
	}

	result := v.substituteVars("взя $weapon")
	if result != "взя фламберг" {
		t.Errorf("$weapon substitution should be 'взя фламберг', got %q", result)
	}
}

func TestCmdVariableListsVariablesSortedByName(t *testing.T) {
	v := New(nil, 1)
	v.variables["zeta"] = "last"
	v.variables["alpha"] = "first"
	v.variables["middle"] = "mid"

	results := v.dispatchCommand("#var", 0, nil)
	got := resultTexts(results)
	want := []string{
		"#variable {alpha} = {first}",
		"#variable {middle} = {mid}",
		"#variable {zeta} = {last}",
	}

	if len(got) != len(want) {
		t.Fatalf("#var returned %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("#var line %d = %q, want %q (all lines: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCmdVariableGetterShowsCurrentValue(t *testing.T) {
	v := New(nil, 1)
	v.variables["kast1"] = "Тартис"

	results := v.dispatchCommand("#var {kast1}", 0, nil)
	got := resultTexts(results)
	want := []string{"#variable {kast1} = {Тартис}"}

	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("#var getter = %v, want %v", got, want)
	}
}

func TestCmdVariableAliasGetterSetterWithEmptyPercentZero(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {каст1} {#var {kast1} {%0}}", 0, nil)

	setResults := v.ProcessInputDetailed("каст1 Тартис")
	if got := v.variables["kast1"]; got != "Тартис" {
		t.Fatalf("alias setter stored %q, want %q", got, "Тартис")
	}
	if got := resultTexts(setResults); len(got) != 1 || got[0] != "#variable {kast1} = {Тартис}" {
		t.Fatalf("alias setter results = %v, want variable echo", got)
	}
	if got := inputVisibleTexts(setResults); len(got) != 0 {
		t.Fatalf("alias setter input-visible results = %v, want hidden nested setter echo", got)
	}

	getResults := v.ProcessInputDetailed("каст1")
	if got := v.variables["kast1"]; got != "Тартис" {
		t.Fatalf("alias getter with empty %%0 changed value to %q, want %q", got, "Тартис")
	}
	if got := resultTexts(getResults); len(got) != 1 || got[0] != "#variable {kast1} = {Тартис}" {
		t.Fatalf("alias getter results = %v, want current variable echo", got)
	}
	if got := inputVisibleTexts(getResults); len(got) != 1 || got[0] != "#variable {kast1} = {Тартис}" {
		t.Fatalf("alias getter input-visible results = %v, want current variable echo", got)
	}
}

func TestCmdVariableDoesNotSetEmptyExpandedValue(t *testing.T) {
	v := New(nil, 1)
	v.variables["name"] = "existing"

	results := v.dispatchCommand("#var {name} {}", 0, nil)
	if got := v.variables["name"]; got != "existing" {
		t.Fatalf("#var empty value stored %q, want existing value unchanged", got)
	}
	if got := resultTexts(results); len(got) != 1 || got[0] != "#variable {name} = {existing}" {
		t.Fatalf("#var empty value results = %v, want getter echo", got)
	}
	if !results[0].IsInternal || !results[0].ShowOnInput {
		t.Fatalf("#var empty value getter should remain internal but input-visible, got: %+v", results[0])
	}
}

func TestCmdVariableNormalNonEmptyAssignmentStillWorks(t *testing.T) {
	v := New(nil, 1)

	results := v.dispatchCommand("#var {name} {value}", 0, nil)
	if got := v.variables["name"]; got != "value" {
		t.Fatalf("#var non-empty assignment stored %q, want %q", got, "value")
	}
	if got := resultTexts(results); len(got) != 1 || got[0] != "#variable {name} = {value}" {
		t.Fatalf("#var non-empty assignment results = %v, want assignment echo", got)
	}
}

func TestVariableInAliasTemplate(t *testing.T) {
	v := New(nil, 1)
	v.variables["двуруч"] = "фламберг"
	v.dispatchCommand("#alias {моддву} {взя $двуруч;дву $двуруч}", 0, nil)

	result := v.ProcessInput("моддву")
	if len(result) != 2 {
		t.Fatalf("моддву = %d commands, want 2: %v", len(result), result)
	}
	if result[0] != "взя фламберг" {
		t.Errorf("моддву[0] = %q, want 'взя фламберг'", result[0])
	}
	if result[1] != "дву фламберг" {
		t.Errorf("моддву[1] = %q, want 'дву фламберг'", result[1])
	}
}

func TestVariableInTriggerCommand(t *testing.T) {
	v := New(nil, 1)
	v.variables["таргет"] = "крыса"
	v.triggers = []storage.TriggerRule{
		{Pattern: `^Вы упали`, Command: "у $таргет", Enabled: true},
	}

	effects, _ := v.MatchTriggers("Вы упали на землю!")
	if len(effects) != 1 {
		t.Fatalf("var in trigger = %d effects, want 1", len(effects))
	}

	commands := v.ProcessInput(effects[0].Command)
	if len(commands) != 1 || commands[0] != "у крыса" {
		t.Errorf("$таргет in trigger command = %v, want [у крыса]", commands)
	}
}

func TestCmdVariableRejectsDollarPrefixedNameOnSet(t *testing.T) {
	v := New(nil, 1)
	results := v.dispatchCommand("#var {$bad} {1}", 0, nil)

	if len(results) == 0 {
		t.Fatalf("expected error result from #var set")
	}
	if results[0].Kind != ResultEcho {
		t.Fatalf("expected echo result, got %q", results[0].Kind)
	}
	if results[0].Text == "" {
		t.Fatalf("expected non-empty error message")
	}
	if _, ok := v.variables["$bad"]; ok {
		t.Fatalf("$bad variable should not be created")
	}
}

func TestCmdVariableRejectsWhitespaceNameOnSet(t *testing.T) {
	v := New(nil, 1)
	results := v.dispatchCommand("#var {   } {1}", 0, nil)

	if len(results) == 0 {
		t.Fatalf("expected error result from #var set")
	}
	if results[0].Kind != ResultEcho {
		t.Fatalf("expected echo result, got %q", results[0].Kind)
	}
	if _, ok := v.variables["   "]; ok {
		t.Fatalf("whitespace-only variable should not be created")
	}
}
