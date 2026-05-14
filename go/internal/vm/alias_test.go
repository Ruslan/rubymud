package vm

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func TestSubstituteTemplate(t *testing.T) {
	tests := []struct {
		template string
		args     []string
		expected string
	}{
		{"у %1;пари", []string{"крыса"}, "у крыса;пари"},
		{"вар t1 %1", []string{"крыса"}, "вар t1 крыса"},
		{"сня %1;пол %1 $сумка", []string{"кольцо"}, "сня кольцо;пол кольцо $сумка"},
		{"нет параметров", nil, "нет параметров"},
		{"%1 %2 %3", []string{"a", "b"}, "a b %3"},
		{"%0", []string{"крыса", "большая"}, "крыса большая"},
		{"%0 ударил %1", []string{"он", "крыса"}, "он крыса ударил он"},
	}

	for _, tt := range tests {
		result := ExpandCaptures(tt.template, append([]string{strings.Join(tt.args, " ")}, tt.args...))
		if result != tt.expected {
			t.Errorf("substituteTemplate(%q, %v) = %q, want %q", tt.template, tt.args, result, tt.expected)
		}
	}
}

func TestSplitStatements(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"у крыса;пари", []string{"у крыса", "пари"}},
		{"один", []string{"один"}},
		{"", nil},
		{"a;b;c", []string{"a", "b", "c"}},
		{" a ; b ", []string{"a", "b"}},
		{"one\ntwo", []string{"one", "two"}},
		{"one\r\ntwo", []string{"one", "two"}},
		{"#alias {a} {b;c}; d", []string{"#alias {a} {b;c}", "d"}},
		{"say 'hello;world'; e", []string{"say 'hello;world'", "e"}},
		{"say \"hello;world\"; f", []string{"say \"hello;world\"", "f"}},
	}

	for _, tt := range tests {
		result := splitStatements(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("splitStatements(%q) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("splitStatements(%q)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestExpandNoInfiniteLoop(t *testing.T) {
	v := New(nil, 1)
	v.aliases = []storage.AliasRule{
		{Name: "a", Template: "b", Enabled: true},
		{Name: "b", Template: "a", Enabled: true},
	}

	result := v.ProcessInput("a")
	if len(result) == 0 {
		t.Error("ProcessInput should not return empty result on recursive alias")
	}
}

func TestExpandSemicolonInInput(t *testing.T) {
	v := New(nil, 1)
	v.aliases = []storage.AliasRule{
		{Name: "ц1", Template: "вар t1 %1", Enabled: true},
	}
	v.variables["t1"] = "крыса"

	result := v.ProcessInput("ц1 босс;отд")
	expected := []string{"вар t1 босс", "отд"}

	if len(result) != len(expected) {
		t.Errorf("ProcessInput(%q) = %v, want %v", "ц1 босс;отд", result, expected)
		return
	}
	for i := range result {
		if result[i] != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestAliasWithPercentZeroNoArgs(t *testing.T) {
	v := New(nil, 1)
	v.dispatchCommand("#alias {вздох} {вздохнуть %0}", 0, nil)

	result := v.ProcessInput("вздох")
	if len(result) != 1 {
		t.Fatalf("alias %%0 no args = %v, want 1 result", result)
	}
	if result[0] != "вздохнуть" {
		t.Errorf("alias %%0 no args = %q, want 'вздохнуть'", result[0])
	}
}
