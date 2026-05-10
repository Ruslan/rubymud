package colorreg

import (
	"testing"
)

func TestCanonicalName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"red", "red", true},
		{"RED", "red", true},
		{" coal ", "black", true},
		{"charcoal", "black", true},
		{"light red", "light red", true},
		{"light brown", "brown", true},
		{"purple", "magenta", true},
		{"grey", "white", true},
		{"unknown", "unknown", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got, ok := CanonicalName(tt.input)
		if got != tt.expected || ok != tt.ok {
			t.Errorf("CanonicalName(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.expected, tt.ok)
		}
	}
}

func TestNormalizeStoredColor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  RED  ", "red"},
		{"default", ""},
		{"", ""},
		{"COAL", "black"},
		{"256:196", "256:196"},
		{"#ff0000", "#ff0000"},
		{"light brown", "brown"},
	}

	for _, tt := range tests {
		got := NormalizeStoredColor(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeStoredColor(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeExportColor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"256:196", "red"},
		{"256:21", "blue"},
		{"#ff0000", "red"},
		{"#0000ff", "blue"},
		{"256:17", "256:17"}, // unknown to name
		{"#123456", "#123456"},
		{"red", "red"},
		{"coal", "black"},
	}

	for _, tt := range tests {
		got := NormalizeExportColor(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeExportColor(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
