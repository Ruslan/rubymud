package vm

import (
	"strings"
	"testing"
)

func TestEvalExpression(t *testing.T) {
	variables := map[string]string{
		"a":      "10",
		"b":      "20",
		"target": "orc",
		"hp":     "40",
		"таргет": "орк",
	}

	tests := []struct {
		expr string
		want any
	}{
		{"$a == 10", true},
		{"$a == 5", false},
		{"$target == \"orc\"", true},
		{"$таргет == \"орк\"", true},
		{"$target == \"\"", false},
		{"$missing == \"\"", true},
		{"$hp + 10 == 50", true},
		{"$hp / 2 == 20", true},
		{"($a + $b) / 2 == 15", true},
		{"$a * 2 == $b", true},
		{"'string with $a' == \"string with $a\"", true},     // Preprocessing should ignore $ in quotes
		{"\"escaped \\\" $a\" == \"escaped \\\" $a\"", true}, // Preprocessing should handle escaped quotes
		{"'dont split $var' == \"dont split $var\"", true},   // Preprocessing should handle single quotes
		{"1.5 + 0.5 == 2.0", true},                           // Decimal literals
		{"40 / 2.5 == 16.0", true},                           // Decimal literals
		{"1 + 1", 2},
		{"not true", false},
		{"$hp > 10", true},
		{"$hp != 10", true},
		{"$hp <= 40", true},
		{"$hp == 40 && $hp == 40", true},
		{"$hp % 2 == 0", true},
	}

	for _, tt := range tests {
		got, err := EvalExpression(tt.expr, variables, nil)
		if err != nil {
			t.Errorf("EvalExpression(%q) error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("EvalExpression(%q) = %v, want %v", tt.expr, got, tt.want)
		}
	}
}

func TestEvalExpressionErrors(t *testing.T) {
	variables := map[string]string{"hp": "40"}

	tests := []struct {
		expr         string
		wantContains string
	}{
		{"invalid++", "unsupported word operator"},
		{"len(\"abc\") == 3", "unsupported word operator"},
		{"[1, 2][0] == 1", "not supported"},
		{"\"abc\" matches \"a.*\"", "unsupported word operator"},
		{"\"abc\" contains \"b\"", "unsupported word operator"},
		{"\"abc\" startsWith \"a\"", "unsupported word operator"},
		{"\"abc\" endsWith \"c\"", "unsupported word operator"},
		{"\"a\" in \"abc\"", "unsupported word operator"},
		{"$hp . variable", "not supported"}, // invalid decimal/member access
	}

	for _, tt := range tests {
		_, err := EvalExpression(tt.expr, variables, nil)
		if err == nil {
			t.Errorf("EvalExpression(%q) expected error, got nil", tt.expr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantContains) {
			t.Errorf("EvalExpression(%q) error %q does not contain %q", tt.expr, err.Error(), tt.wantContains)
		}
	}
}
