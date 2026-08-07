package mapper

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Repro for the fixed_width + last_before_stripped + empty-glyphs hole.
//
// Strip.Glyphs is consumed by the STRIP (greedy/min_run size the run they cut)
// AND by the HEADER rule (HasLeftColumn -> HeaderIndex finds the first
// column-prefixed line, in every strip mode). Compile used to require it only
// for the strip, so a fixed_width profile could compile with no glyphs at all —
// and then silently name every room after the movement preamble above it.
// ---------------------------------------------------------------------------

func fixedWidthHeaderSpec(glyphs string) ProfileSpec {
	return ProfileSpec{
		Name:   "fixed-width-header",
		Exits:  ExitsSpec{Guard: "Obvious exits:", Pattern: `^Obvious exits:\s*(.*?)\s*\.?$`, NoneToken: "None"},
		Block:  BlockSpec{Stops: []BlockStop{StopBlank, StopMaxLines}, MaxLines: 16},
		Strip:  StripSpec{Mode: StripFixedWidth, Glyphs: glyphs, Width: 12},
		Header: HeaderSpec{Rule: HeaderLastBeforeStripped, ColumnRun: 2},
	}
}

// TestFixedWidthHeaderRuleRequiresGlyphs is the gate: the combination that
// silently mis-names every room must not compile.
func TestFixedWidthHeaderRuleRequiresGlyphs(t *testing.T) {
	_, err := Compile(fixedWidthHeaderSpec(""))
	if err == nil {
		t.Fatal("Compile accepted last_before_stripped with an empty glyph set — every room name would silently become the movement preamble above it")
	}
	if !strings.Contains(err.Error(), "non-empty glyph set") {
		t.Errorf("error = %v, want it to name the glyph set", err)
	}

	// The same spec WITH glyphs is legitimate and must still compile: fixed_width
	// ignores glyphs when cutting, but the header rule needs them.
	if _, err := Compile(fixedWidthHeaderSpec(RMUDGlyphs)); err != nil {
		t.Fatalf("fixed_width + glyphs must remain valid: %v", err)
	}
}

// TestHeaderFirstDoesNotNeedGlyphs: the requirement is scoped to the rule that
// actually consults the glyph set. header:first never calls HasLeftColumn, so a
// glyph-less fixed_width profile is fine there and must not be rejected.
func TestHeaderFirstDoesNotNeedGlyphs(t *testing.T) {
	spec := fixedWidthHeaderSpec("")
	spec.Header = HeaderSpec{Rule: HeaderFirst}
	if _, err := Compile(spec); err != nil {
		t.Fatalf("header:first has no use for glyphs and must still compile: %v", err)
	}
}

// ANSI header selection reads Raw and is independent of the left-column glyph
// set. This is the useful fixed_width combination for a MUD whose room name is
// colour-marked and whose body column is positional rather than glyph-shaped.
func TestFixedWidthWithANSIHeaderDoesNotNeedGlyphs(t *testing.T) {
	spec := fixedWidthHeaderSpec("")
	spec.Header = HeaderSpec{Rule: HeaderANSIDelimited, ANSIStart: "1;36"}
	spec.Strip.Width = 4
	det := NewSpecDetector(MustCompile(spec), DetectorInfo{})

	det.Feed(Line{Stripped: "The Glass Bridge", Raw: "\x1b[1;36mThe Glass Bridge\x1b[0m"})
	det.Feed(StrippedLine("....A narrow span."))
	ev, ok := det.Feed(StrippedLine("Obvious exits: north."))
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "The Glass Bridge" || ev.Desc != "A narrow span." || ev.Exits != "north" {
		t.Errorf("fixed_width + ANSI event = %+v", ev)
	}
}

// TestGlyphsDriveTheHeaderUnderFixedWidth is the behavioural half: with glyphs
// the room name is found, and the movement preamble above it is excluded.
func TestGlyphsDriveTheHeaderUnderFixedWidth(t *testing.T) {
	p := MustCompile(fixedWidthHeaderSpec(RMUDGlyphs))
	ev, ok := feedLines(t, p, []string{
		"Вы последовали за Гномом на север.",
		"Тронный зал",
		"  [ ]-[ ]   Огромный зал с троном.",
		"Obvious exits: north.",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Тронный зал" {
		t.Errorf("hint = %q, want the room name (not the movement preamble)", ev.Hint)
	}
}
