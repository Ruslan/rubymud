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
