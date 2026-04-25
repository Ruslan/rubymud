package vm

import (
	"testing"
	"rubymud/go/internal/storage"
)

func TestDisabledAliasFallback(t *testing.T) {
	v := New(nil, 1)
	
	// Senior profile has 'foo' disabled
	// Junior profile has 'foo' enabled with template 'bar'
	v.aliases = []storage.AliasRule{
		{Name: "foo", Template: "wrong", Enabled: false},
		{Name: "foo", Template: "bar", Enabled: true},
	}

	results := v.ProcessInputDetailed("foo")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Text != "bar" {
		t.Errorf("expected expansion 'bar', got %q", results[0].Text)
	}
	if results[0].Kind != ResultCommand {
		t.Errorf("expected Kind ResultCommand, got %v", results[0].Kind)
	}
}

func TestUnvariableWithoutStore(t *testing.T) {
	v := New(nil, 1) // store is nil
	v.variables["target"] = "orc"

	// Should not panic
	results := v.ProcessInputDetailed("#unvar target")
	
	if val, ok := v.variables["target"]; ok {
		t.Errorf("variable 'target' should be deleted, but still exists with value %q", val)
	}
	
	if len(results) != 1 || results[0].Kind != ResultEcho {
		t.Errorf("expected 1 echo result, got %v", results)
	}
}

func TestSplitBraceArgFallback(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"unterminated brace", "{foo bar", "foo bar"},
		{"unterminated single quote", "'hello world", "hello world"},
		{"unterminated double quote", "\"quoted text", "quoted text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arg, rest := splitBraceArg(tt.input)
			if arg != tt.expected {
				t.Errorf("splitBraceArg(%q) arg = %q, want %q", tt.input, arg, tt.expected)
			}
			if rest != "" {
				t.Errorf("splitBraceArg(%q) rest = %q, want empty", tt.input, rest)
			}
		})
	}
}
