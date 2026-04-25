package vm

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "braced args with spaces",
			input:    `{<<< -ЗОЗ>>>} {-ЗОЗ}`,
			expected: []string{"<<< -ЗОЗ>>>", "-ЗОЗ"},
		},
		{
			name:     "braced and normal word mix",
			input:    `{brace} normal`,
			expected: []string{"brace", "normal"},
		},
		{
			name:     "quoted and braced mix",
			input:    `"quoted" {braced}`,
			expected: []string{"quoted", "braced"},
		},
		{
			name:     "nested braces",
			input:    `{{inner}} {outer}`,
			expected: []string{"{inner}", "outer"},
		},
		{
			name:     "empty input",
			input:    ``,
			expected: nil,
		},
		{
			name:     "single word",
			input:    `word`,
			expected: []string{"word"},
		},
		{
			name:     "multiple words",
			input:    `word1 word2`,
			expected: []string{"word1", "word2"},
		},
		{
			name:     "trailing and leading spaces",
			input:    `   {  spaces  }   word   `,
			expected: []string{"  spaces  ", "word"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseArgs(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseArgs(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
