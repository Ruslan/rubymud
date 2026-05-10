package vm

import (
	"reflect"
	"testing"

	"rubymud/go/internal/storage"
)

func TestParseColorSpec(t *testing.T) {
	tests := []struct {
		spec     string
		expected storage.HighlightRule
	}{
		{"red bold", storage.HighlightRule{FG: "red", Bold: true}},
		{"light red b light blue", storage.HighlightRule{FG: "light red", BG: "light blue"}},
		{"default b default", storage.HighlightRule{FG: "", BG: ""}},
		{"bold faint italic underline strikethrough blink reverse", storage.HighlightRule{
			Bold: true, Faint: true, Italic: true, Underline: true, Strikethrough: true, Blink: true, Reverse: true,
		}},
		{"COAL b charcoal", storage.HighlightRule{FG: "black", BG: "black"}},
		{"light brown b yellow", storage.HighlightRule{FG: "brown", BG: "brown"}},
		{"256:196 b #ff0000", storage.HighlightRule{FG: "256:196", BG: "#ff0000"}},
	}

	for _, tt := range tests {
		got := parseColorSpec(tt.spec)
		if !reflect.DeepEqual(got, tt.expected) {
			t.Errorf("parseColorSpec(%q) = %+v, want %+v", tt.spec, got, tt.expected)
		}
	}
}

func TestFormatColorSpec(t *testing.T) {
	tests := []struct {
		h        storage.HighlightRule
		expected string
	}{
		{storage.HighlightRule{FG: "red", Bold: true}, "red bold"},
		{storage.HighlightRule{FG: "256:196", BG: "256:21"}, "red b blue"},
		{storage.HighlightRule{FG: "light red", BG: "light blue"}, "light red b light blue"},
		{storage.HighlightRule{FG: "", BG: ""}, ""},
	}

	for _, tt := range tests {
		got := formatColorSpec(tt.h)
		if got != tt.expected {
			t.Errorf("formatColorSpec(%+v) = %q, want %q", tt.h, got, tt.expected)
		}
	}
}
