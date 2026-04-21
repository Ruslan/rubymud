package vm

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func TestApplyHighlightsWithExistingANSI(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `bar`, FG: "blue", Enabled: true}}

	input := "\x1b[31mfoo\x1b[0m bar"
	got := v.ApplyHighlights(input)

	if !strings.Contains(got, "\x1b[31mfoo\x1b[0m ") {
		t.Fatalf("existing ANSI prefix was corrupted: %q", got)
	}
	if !strings.Contains(got, "\x1b[34mbar\x1b[0m") {
		t.Fatalf("highlighted segment missing around target: %q", got)
	}
	if stripANSIFromVM(got) != "foo bar" {
		t.Fatalf("plain text after highlight = %q, want %q", stripANSIFromVM(got), "foo bar")
	}
}

func TestApplyHighlightsSupportsHexColors(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `danger`, FG: "#ff0000", BG: "#003366", Enabled: true}}

	got := v.ApplyHighlights("danger zone")

	if !strings.Contains(got, "\x1b[38;2;255;0;0;48;2;0;51;102mdanger\x1b[0m") {
		t.Fatalf("hex highlight missing expected ANSI: %q", got)
	}
}

func TestApplyHighlightsRestoresOuterANSIContext(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `девушк`, FG: "blue", BG: "magenta", Enabled: true}}

	input := "\x1b[1;31mПриветливая девушка готова провести Вас\x1b[0m"
	got := v.ApplyHighlights(input)

	if !strings.Contains(got, "\x1b[34;45mдевушк\x1b[0m\x1b[1;31mа готова") {
		t.Fatalf("outer ANSI context was not restored after highlight: %q", got)
	}
	if stripANSIFromVM(got) != "Приветливая девушка готова провести Вас" {
		t.Fatalf("plain text after restored highlight = %q", stripANSIFromVM(got))
	}
}
