package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

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
