package vm

import (
	"strings"
	"testing"
	"unicode/utf8"

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

func TestApplyHighlightsMultipleMatches(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `yellow`, FG: "yellow", Enabled: true}}

	got := v.ApplyHighlights("yellow one and yellow two")

	if count := strings.Count(got, "\x1b[33myellow\x1b[0m"); count != 2 {
		t.Fatalf("expected 2 highlighted matches, got %d: %q", count, got)
	}
	if stripANSIFromVM(got) != "yellow one and yellow two" {
		t.Fatalf("plain text after multiple-match highlight = %q", stripANSIFromVM(got))
	}
}

func TestApplyHighlightsMultipleMatchesWithExistingANSI(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `yellow`, FG: "yellow", Enabled: true}}

	input := "\x1b[31myellow one yellow two\x1b[0m"
	got := v.ApplyHighlights(input)

	if count := strings.Count(got, "\x1b[33myellow\x1b[0m\x1b[31m"); count != 2 {
		t.Fatalf("expected 2 highlighted matches with restored outer ANSI, got %d: %q", count, got)
	}
	if stripANSIFromVM(got) != "yellow one yellow two" {
		t.Fatalf("plain text after ANSI-aware multiple-match highlight = %q", stripANSIFromVM(got))
	}
}

func TestApplyHighlightsMalformedUTF8KeepsRegexOffsetsSafe(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		match string
		want  string
	}{
		{
			name:  "invalid byte before match",
			text:  string([]byte{0xff, 'a'}),
			match: `a`,
			want:  string([]byte{0xff}) + "\x1b[31ma\x1b[0m",
		},
		{
			name:  "invalid byte between matches",
			text:  string([]byte{'a', 0xff, 'b'}),
			match: `b`,
			want:  "a" + string([]byte{0xff}) + "\x1b[31mb\x1b[0m",
		},
		{
			name:  "invalid bytes around match",
			text:  string([]byte{0xfe, 'a', 0xff, 'b'}),
			match: `a`,
			want:  string([]byte{0xfe}) + "\x1b[31ma\x1b[0m" + string([]byte{0xff, 'b'}),
		},
		{
			name:  "invalid byte with no match",
			text:  string([]byte{0xff, 'x'}),
			match: `a`,
			want:  string([]byte{0xff, 'x'}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New(nil, 1)
			v.highlights = []storage.HighlightRule{{Pattern: tt.match, FG: "red", Enabled: true}}
			if got := v.ApplyHighlights(tt.text); got != tt.want {
				t.Fatalf("malformed UTF-8 highlight = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyHighlightsInvalidColorDoesNotBlockLowerAttribute(t *testing.T) {
	tests := []struct {
		name  string
		rules []storage.HighlightRule
		at    int
		want  string
	}{
		{
			name: "invalid foreground",
			rules: []storage.HighlightRule{
				{Pattern: `abc`, FG: "yellow", Enabled: true},
				{Pattern: `b`, FG: "not-a-color", Bold: true, Enabled: true},
			},
			at:   1,
			want: "\x1b[1;33m",
		},
		{
			name: "invalid background",
			rules: []storage.HighlightRule{
				{Pattern: `abc`, BG: "blue", Enabled: true},
				{Pattern: `b`, BG: "not-a-color", Italic: true, Enabled: true},
			},
			at:   1,
			want: "\x1b[3;44m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New(nil, 1)
			v.highlights = tt.rules
			got := v.ApplyHighlights("abc")
			if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, tt.at)); style != tt.want {
				t.Fatalf("style at invalid-color overlap = %q, want %q; output %q", style, tt.want, got)
			}
		})
	}
}

func TestApplyHighlightsOverlappingRulesFollowRuleOrder(t *testing.T) {
	line := "Привет, Ринтар!"
	lineRule := storage.HighlightRule{Pattern: `^.*$`, FG: "yellow", Enabled: true}
	nameRule := storage.HighlightRule{Pattern: `Ринтар`, FG: "red", Enabled: true}

	tests := []struct {
		name  string
		rules []storage.HighlightRule
		want  string
	}{
		{
			name:  "whole line before name",
			rules: []storage.HighlightRule{lineRule, nameRule},
			want:  "\x1b[33mПривет, \x1b[0m\x1b[31mРинтар\x1b[0m\x1b[33m!\x1b[0m",
		},
		{
			name:  "name before whole line",
			rules: []storage.HighlightRule{nameRule, lineRule},
			want:  "\x1b[33mПривет, Ринтар!\x1b[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New(nil, 1)
			v.highlights = tt.rules

			got := v.ApplyHighlights(line)

			if got != tt.want {
				t.Fatalf("overlapping highlights = %q, want %q", got, tt.want)
			}
			if plain := stripANSIFromVM(got); plain != line {
				t.Fatalf("plain text after overlapping highlights = %q, want %q", plain, line)
			}
		})
	}
}

func TestApplyHighlightsComposesIntervalShapes(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		rules []storage.HighlightRule
		at    map[int]string
	}{
		{
			name: "nested same start",
			text: "abcdef",
			rules: []storage.HighlightRule{
				{Pattern: `^abcdef`, FG: "yellow", Enabled: true},
				{Pattern: `^abc`, FG: "red", Enabled: true},
			},
			at: map[int]string{0: "\x1b[31m", 3: "\x1b[33m", 6: ""},
		},
		{
			name: "nested different start",
			text: "abcdef",
			rules: []storage.HighlightRule{
				{Pattern: `^abcdef`, FG: "yellow", Enabled: true},
				{Pattern: `bc`, FG: "red", Enabled: true},
			},
			at: map[int]string{0: "\x1b[33m", 1: "\x1b[31m", 3: "\x1b[33m", 6: ""},
		},
		{
			name: "partially crossing",
			text: "abcdef",
			rules: []storage.HighlightRule{
				{Pattern: `^abcd`, FG: "yellow", Enabled: true},
				{Pattern: `cdef`, FG: "red", Enabled: true},
			},
			at: map[int]string{0: "\x1b[33m", 2: "\x1b[31m", 4: "\x1b[31m", 6: ""},
		},
		{
			name: "equal ranges preserve lower attributes",
			text: "abcd",
			rules: []storage.HighlightRule{
				{Pattern: `^abcd`, FG: "yellow", BG: "blue", Enabled: true},
				{Pattern: `^abcd`, FG: "red", Enabled: true},
			},
			at: map[int]string{0: "\x1b[31;44m", 4: ""},
		},
		{
			name: "disjoint",
			text: "ab cd",
			rules: []storage.HighlightRule{
				{Pattern: `ab`, FG: "yellow", Enabled: true},
				{Pattern: `cd`, FG: "red", Enabled: true},
			},
			at: map[int]string{0: "\x1b[33m", 2: "", 3: "\x1b[31m", 5: ""},
		},
		{
			name:  "multiple matches of one rule",
			text:  "x-x-x",
			rules: []storage.HighlightRule{{Pattern: `x`, FG: "yellow", Enabled: true}},
			at:    map[int]string{0: "\x1b[33m", 1: "", 2: "\x1b[33m", 3: "", 4: "\x1b[33m", 5: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New(nil, 1)
			v.highlights = tt.rules
			got := v.ApplyHighlights(tt.text)
			if stripANSIFromVM(got) != tt.text {
				t.Fatalf("plain text changed: %q", got)
			}
			for plainOffset, want := range tt.at {
				if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, plainOffset)); style != want {
					t.Errorf("style at plain offset %d = %q, want %q; output %q", plainOffset, style, want, got)
				}
			}
		})
	}
}

func TestApplyHighlightsLaterRuleOnlyOverridesSpecifiedAttributes(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{
		{Pattern: `^whole line$`, FG: "yellow", Bold: true, Enabled: true},
		{Pattern: `line`, FG: "red", Enabled: true},
	}
	got := v.ApplyHighlights("whole line")
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, len("whole "))); style != "\x1b[1;31m" {
		t.Fatalf("later rule erased lower bold attribute: %q", style)
	}
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 0)); style != "\x1b[1;33m" {
		t.Fatalf("lower rule style was not restored: %q", style)
	}
}

func TestApplyHighlightsPreservesExistingANSIAndBaseANSIAcrossIntervals(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `b`, FG: "red", Enabled: true}}

	input := "\x1b[1;34mabc\x1b[0m"
	got := v.ApplyHighlights(input)
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 0)); style != "\x1b[1;34m" {
		t.Fatalf("existing ANSI style changed before highlight: %q", style)
	}
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 1)); style != "\x1b[1;31m" {
		t.Fatalf("highlight did not retain existing bold attribute: %q", style)
	}
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 2)); style != "\x1b[1;34m" {
		t.Fatalf("existing ANSI style was not restored: %q", style)
	}

	baseGot := v.ApplyHighlightsWithBase("abc", "\x1b[1;34m")
	if baseGot != "a\x1b[31mb\x1b[0m\x1b[1;34mc" {
		t.Fatalf("base ANSI context was not preserved: %q", baseGot)
	}
}

func TestApplyHighlightsANSIResetsInsideCrossingInterval(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `bcde`, FG: "red", Enabled: true}}

	// The source style changes and is then partially reset while the highlight
	// remains active. The overlay must keep its red foreground, while the
	// source's bold/underline/foreground/background state must be restored from
	// the partial reset once the crossing interval ends.
	input := "\x1b[1;34;4;44mabc\x1b[39;22;24;49mde!"
	got := v.ApplyHighlightsWithBase(input, "\x1b[32m")
	if stripANSIFromVM(got) != "abcde!" {
		t.Fatalf("plain text changed after ANSI reset inside highlight: %q", got)
	}

	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 1)); style != "\x1b[1;4;31;44m" {
		t.Fatalf("source style was not combined with highlight before reset: %q (output %q)", style, got)
	}
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 3)); style != "\x1b[31m" {
		t.Fatalf("partial ANSI reset did not preserve active highlight: %q (output %q)", style, got)
	}
	if style := activeANSIAt(got, rawBoundaryForPlainOffset(got, 5)); style != "" {
		t.Fatalf("source state was not restored after crossing interval: %q (output %q)", style, got)
	}
}

func rawBoundaryForPlainOffset(raw string, target int) int {
	plainOffset := 0
	for rawOffset := 0; rawOffset < len(raw); {
		if raw[rawOffset] == '\x1b' {
			if end := ansiSequenceEnd(raw, rawOffset); end > rawOffset {
				rawOffset = end
				continue
			}
		}
		if plainOffset == target {
			return rawOffset
		}
		_, size := utf8.DecodeRuneInString(raw[rawOffset:])
		if size <= 0 {
			break
		}
		rawOffset += size
		plainOffset += size
	}
	return len(raw)
}

func TestApplyHighlightsAfterSubWithUnclosedANSI(t *testing.T) {
	v := New(nil, 1)
	v.substitutes = []storage.SubstituteRule{{Pattern: `danger`, Replacement: "[31mdanger", Enabled: true}}
	v.highlights = []storage.HighlightRule{{Pattern: `danger`, FG: "blue", Enabled: true}}

	raw, plain, _ := v.ApplySubsAndCollectOverlays("danger zone", "danger zone")
	if plain != "danger zone" {
		t.Fatalf("plain text changed unexpectedly: %q", plain)
	}

	highlighted := v.ApplyHighlights(raw)

	if !strings.Contains(highlighted, "\x1b[34mdanger\x1b[0m\x1b[31m") {
		t.Fatalf("expected highlight reset+restore sequence, got %q", highlighted)
	}
	if !strings.HasSuffix(highlighted, "\x1b[31m zone") {
		t.Fatalf("expected unclosed substitute color to remain active after highlight, got %q", highlighted)
	}
}

func TestSubAndHighlightChangeTerminalTailStateRepro(t *testing.T) {
	v := New(nil, 1)
	v.substitutes = []storage.SubstituteRule{{Pattern: `danger`, Replacement: "[31mdanger", Enabled: true}}
	v.highlights = []storage.HighlightRule{{Pattern: `danger`, FG: "blue", Enabled: true}}

	original := "danger zone"
	raw, _, _ := v.ApplySubsAndCollectOverlays(original, original)
	highlighted := v.ApplyHighlights(raw)

	originalTail := activeANSIAt(original, len(original))
	highlightedTail := activeANSIAt(highlighted, len(highlighted))

	if originalTail != "" {
		t.Fatalf("expected original tail ANSI state to be empty, got %q", originalTail)
	}
	if highlightedTail == "" {
		t.Fatalf("expected highlighted tail ANSI state to stay active (bug repro), got empty in %q", highlighted)
	}
	if highlightedTail != "\x1b[31m" {
		t.Fatalf("expected highlighted tail ANSI state to be red, got %q", highlightedTail)
	}
}
