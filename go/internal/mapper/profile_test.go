package mapper

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// englishSpec is a SYNTHETIC profile for a hypothetical English server. It
// shares nothing with RMUD: different exits grammar and none-token, a prompt as
// block boundary, min_run(2) strip, header = first line. Its only job is to
// prove the parameterization is real — that the detector follows the profile
// rather than the constants it used to hardcode.
func englishSpec() ProfileSpec {
	return ProfileSpec{
		Name: "test-english",
		Exits: ExitsSpec{
			Guard:     "Obvious exits:",
			Pattern:   `^Obvious exits:\s*(.*?)\s*\.?$`,
			NoneToken: "None",
		},
		Block: BlockSpec{
			Stops:    []BlockStop{StopPrompt, StopBlank, StopMaxLines},
			MaxLines: 16,
		},
		Prompt: `^<\d+hp \d+m>$`,
		Strip: StripSpec{
			Mode:   StripMinRun,
			Glyphs: RMUDGlyphs,
			MinRun: 2,
		},
		Header: HeaderSpec{Rule: HeaderFirst},
	}
}

// feedLines runs a block through a fresh accumulator for p.
func feedLines(t *testing.T, p Profile, lines []string) (RoomEvent, bool) {
	t.Helper()
	return feedLinesAcc(NewAccumulator(p), lines)
}

// feedLinesAcc runs lines through an EXISTING accumulator, so tests can assert
// on state carried between blocks (e.g. the ring reset).
func feedLinesAcc(acc *Accumulator, lines []string) (RoomEvent, bool) {
	var ev RoomEvent
	var got bool
	for _, l := range lines {
		if e, ok := acc.Feed(l); ok {
			ev, got = e, true
		}
	}
	return ev, got
}

func TestEnglishProfileParsesEnglishBlock(t *testing.T) {
	p := MustCompile(englishSpec())
	lines := []string{
		"You walk north along the road.",
		"<100hp 50m>",
		"The Old Cobbled Road",
		"  [ ]-[ ]   Ancient stones pave this road, worn smooth by",
		"   |        centuries of cart traffic.",
		// No left column at all, and it starts with a glyph char. Under RMUD's
		// greedy strip this would silently become "ery old cobblestones...".
		"very old cobblestones line the verge.",
		"Obvious exits: north, east.",
	}
	ev, ok := feedLines(t, p, lines)
	if !ok {
		t.Fatal("english profile emitted no room event")
	}
	if ev.Hint != "The Old Cobbled Road" {
		t.Errorf("hint = %q, want The Old Cobbled Road", ev.Hint)
	}
	want := "Ancient stones pave this road, worn smooth by centuries of cart traffic. very old cobblestones line the verge."
	if ev.Desc != want {
		t.Errorf("desc =\n %q\nwant\n %q", ev.Desc, want)
	}
	if ev.Exits != "north, east" {
		t.Errorf("exits = %q, want north, east", ev.Exits)
	}
}

func TestEnglishProfileNoneToken(t *testing.T) {
	p := MustCompile(englishSpec())
	ev, ok := feedLines(t, p, []string{"A Dead End", "Nothing here.", "Obvious exits: None."})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Exits != "None" {
		t.Errorf("exits = %q, want None (profile none-token, not RMUD's lowercase)", ev.Exits)
	}
	// An empty capture also canonicalizes to the profile's none-token.
	ev2, ok := feedLines(t, p, []string{"A Dead End", "Nothing here.", "Obvious exits:"})
	if !ok {
		t.Fatal("no event for empty exits")
	}
	if ev2.Exits != "None" {
		t.Errorf("empty exits = %q, want None", ev2.Exits)
	}
}

func ansiHeaderSpec() ProfileSpec {
	s := englishSpec()
	s.Name = "ansi-header"
	s.Header = HeaderSpec{Rule: HeaderANSIDelimited, ANSIStart: "1;36"}
	return s
}

func TestANSIDelimitedHeaderExtractsOnlyColoredText(t *testing.T) {
	det := NewSpecDetector(MustCompile(ansiHeaderSpec()), DetectorInfo{})
	lines := []Line{
		StrippedLine("<100hp 50m>"),
		{Stripped: "Notice: The Glass Bridge (safe)", Raw: "Notice: \x1b[1;36mThe Glass Bridge\x1b[0m (safe)"},
		StrippedLine("A narrow span crosses the chasm."),
		StrippedLine("Obvious exits: north, south."),
	}
	var got RoomEvent
	var ok bool
	for _, line := range lines {
		if ev, emitted := det.Feed(line); emitted {
			got, ok = ev, true
		}
	}
	if !ok {
		t.Fatal("ANSI profile emitted no room")
	}
	if got.Hint != "The Glass Bridge" || got.Desc != "A narrow span crosses the chasm." || got.Exits != "north, south" {
		t.Errorf("ANSI room = %+v", got)
	}
}

func TestANSIDelimitedHeaderDoesNotGuessWithoutClosedMarker(t *testing.T) {
	for _, rawTitle := range []string{
		"The Glass Bridge",
		"\x1b[1;36mThe Glass Bridge",
	} {
		det := NewSpecDetector(MustCompile(ansiHeaderSpec()), DetectorInfo{})
		det.Feed(Line{Stripped: "The Glass Bridge", Raw: rawTitle})
		det.Feed(StrippedLine("A narrow span crosses the chasm."))
		ev, ok := det.Feed(StrippedLine("Obvious exits: north."))
		if !ok {
			t.Fatalf("raw %q: exits line emitted no event", rawTitle)
		}
		if ev.Hint != "" || ev.Desc != "" || ev.Exits != "north" {
			t.Errorf("raw %q: event = %+v, want exits-only", rawTitle, ev)
		}
	}
}

func TestANSIDelimitedHeaderValidation(t *testing.T) {
	for _, tc := range []struct {
		start string
		want  string
	}{
		{"", "ansi_start"},
		{"\x1b[1;36m", "parameters"},
		{"red", "parameters"},
	} {
		s := ansiHeaderSpec()
		s.Header.ANSIStart = tc.start
		if _, err := Compile(s); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ansi_start %q: err = %v, want %q", tc.start, err, tc.want)
		}
	}
}

func TestReplayProfileReportsCandidatesMatchesAndLatestSamples(t *testing.T) {
	p := MustCompile(englishSpec())
	lines := []ReplayLine{
		{ID: 1, Plain: "noise", Raw: "noise"},
		{ID: 2, Plain: "", Raw: ""},
		{ID: 3, Plain: "First Room", Raw: "First Room"},
		{ID: 4, Plain: "description one", Raw: "description one"},
		{ID: 5, Plain: "Obvious exits: north.", Raw: "Obvious exits: north."},
		{ID: 6, Plain: "Second Room", Raw: "Second Room"},
		{ID: 7, Plain: "description two", Raw: "description two"},
		{ID: 8, Plain: "Obvious exits: south.", Raw: "Obvious exits: south."},
	}

	got := ReplayProfile(p, lines, 1)
	if got.ScannedLines != 8 || got.CandidateLines != 2 || got.MatchedBlocks != 2 {
		t.Fatalf("replay counts = %+v", got)
	}
	if len(got.Samples) != 1 {
		t.Fatalf("samples = %d, want latest one", len(got.Samples))
	}
	sample := got.Samples[0]
	if sample.LogID != 8 || sample.Hint != "Second Room" || sample.Desc != "description two" || sample.Exits != "south" {
		t.Errorf("latest sample = %+v", sample)
	}
}

func TestReplayProfilePreservesRawANSIForHeaderDetection(t *testing.T) {
	p := MustCompile(ansiHeaderSpec())
	got := ReplayProfile(p, []ReplayLine{
		{ID: 1, Plain: "The Glass Bridge", Raw: "\x1b[1;36mThe Glass Bridge\x1b[0m"},
		{ID: 2, Plain: "A narrow span.", Raw: "A narrow span."},
		{ID: 3, Plain: "Obvious exits: north.", Raw: "Obvious exits: north."},
	}, 20)
	if got.MatchedBlocks != 1 || len(got.Samples) != 1 || got.Samples[0].Hint != "The Glass Bridge" {
		t.Fatalf("ANSI replay = %+v", got)
	}
}

func TestExitsAlternativesUseIndependentGuards(t *testing.T) {
	s := englishSpec()
	s.Exits.Alt = []ExitsRule{
		{Guard: "Ways:", Pattern: `^Ways:\s*(.*?)\s*$`},
		{Guard: "No obvious exits", Pattern: `^No obvious exits()$`},
	}
	p := MustCompile(s)

	for _, tc := range []struct {
		line string
		want string
	}{
		{"Obvious exits: north, east.", "north, east"},
		{"Ways: west down", "west down"},
		{"No obvious exits", "None"},
	} {
		if !p.LooksLikeExits(tc.line) {
			t.Errorf("LooksLikeExits(%q)=false", tc.line)
		}
		got, ok := p.ParseExits(tc.line)
		if !ok || got != tc.want {
			t.Errorf("ParseExits(%q)=(%q,%v), want (%q,true)", tc.line, got, ok, tc.want)
		}
	}
	if p.LooksLikeExits("The large orc attacks.") {
		t.Fatal("unrelated prose passed every exits guard")
	}
}

func TestExitsAlternativesValidateEachRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule ExitsRule
		want string
	}{
		{"empty guard", ExitsRule{Pattern: `^Ways:(.*)$`}, "alternative 1: empty exits guard"},
		{"empty pattern", ExitsRule{Guard: "Ways:"}, "alternative 1: empty exits pattern"},
		{"bad capture count", ExitsRule{Guard: "Ways:", Pattern: `^Ways:$`}, "alternative 1: exits pattern must have exactly 1 capture group"},
		{"guard mismatch", ExitsRule{Guard: "Doors:", Pattern: `^Ways:(.*)$`}, "alternative 1: exits guard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := englishSpec()
			s.Exits.Alt = []ExitsRule{tc.rule}
			_, err := Compile(s)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile error=%v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestExitsAlternativesJSONIsBackwardCompatibleAndDeepCopied(t *testing.T) {
	legacy, err := json.Marshal(RMUDSpec())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), `"alt"`) {
		t.Fatalf("legacy spec gained an empty alt field: %s", legacy)
	}

	s := englishSpec()
	s.Exits.Alt = []ExitsRule{{Guard: "Ways:", Pattern: `^Ways:(.*)$`}}
	p := MustCompile(s)
	copySpec := p.Spec()
	copySpec.Exits.Alt[0].Guard = "Broken:"
	if got := p.Spec().Exits.Alt[0].Guard; got != "Ways:" {
		t.Fatalf("Spec returned shared exits alternatives: %q", got)
	}
}

func TestDikuAlternativesPreservePreviousGrammar(t *testing.T) {
	p := BuiltinDikuEN()
	for _, line := range []string{
		"There is no obvious exits.",
		"There is two obvious exits: north, south.",
		"There are one obvious exit: west.",
		"The only obvious exits are east, down.",
		"The obvious exits is up.",
	} {
		if _, ok := p.ParseExits(line); !ok {
			t.Errorf("previously accepted Diku wording no longer parses: %q", line)
		}
	}
	if p.LooksLikeExits("The large orc attacks.") {
		t.Fatal("ordinary The-prefixed prose still passes an exits guard")
	}
}

// The RMUD profile must NOT recognize the English exits line, and vice versa —
// otherwise "parameterized" would only mean "same rules, new names".
func TestProfilesDoNotCrossMatch(t *testing.T) {
	rmud := BuiltinRMUD()
	eng := MustCompile(englishSpec())

	if _, ok := rmud.ParseExits("Obvious exits: north, east."); ok {
		t.Error("RMUD profile matched an English exits line")
	}
	if _, ok := eng.ParseExits("[ Exits: N E S W ]"); ok {
		t.Error("English profile matched an RMUD exits line")
	}
	if rmud.LooksLikeExits("Obvious exits: north.") {
		t.Error("RMUD guard accepted an English exits line")
	}
	if eng.LooksLikeExits("[ Exits: N ]") {
		t.Error("English guard accepted an RMUD exits line")
	}
}

// ---------------------------------------------------------------------------
// Strip modes — including the `v` hazard the greedy mode carries.
// ---------------------------------------------------------------------------

func TestStripModes(t *testing.T) {
	greedy := BuiltinRMUD()
	minRun2 := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "min_run2"
		s.Strip.Mode, s.Strip.MinRun = StripMinRun, 2
		return s
	}())
	fixed21 := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "fixed21"
		s.Strip.Mode, s.Strip.Width = StripFixedWidth, 21
		return s
	}())

	cases := []struct {
		line                     string
		greedy, minRun2, fixed21 string
	}{
		// The bug: one leading glyph char is eaten with no minimum run. Harmless
		// on RMUD (Russian prose starts with none of the glyphs) and fatal on an
		// English server — and it fails SILENTLY, because hint+exits matching
		// still works and only the fingerprint (auto-resync) breaks.
		// (fixed_width(21) is no help either: this line is exactly 21 runes, so
		// the whole thing is treated as column and vanishes.)
		{"very old cobblestones", "ery old cobblestones", "very old cobblestones", ""},
		// min_run(2) does NOT save a leading ellipsis — the run is 3. Only a
		// narrower glyph set would.
		{"...an ellipsis opens this line", "an ellipsis opens this line", "an ellipsis opens this line", "this line"},
		// Capital V survives: the glyph set holds lowercase v only.
		{"Very old cobblestones", "Very old cobblestones", "Very old cobblestones", ""},
		// Run of 1 ('-'), so min_run(2) declines to cut.
		{"-a dash line", "a dash line", "-a dash line", ""},
		// A run of one SPACE is a no-op either way: the trailing TrimSpace does it.
		{" leading space", "leading space", "leading space", ""},
		// A real fixture body line (21-rune minimap column): identical under all
		// three modes, which is why the corpus shows no delta.
		{".[ ]-[ ]-[ ]......... круглый стол", "круглый стол", "круглый стол", "круглый стол"},
		// A wholly-minimap line collapses to "" under all modes.
		{".....................", "", "", ""},
		{"", "", "", ""},
	}

	for _, c := range cases {
		if got := greedy.StripColumn(c.line); got != c.greedy {
			t.Errorf("greedy(%q) = %q, want %q", c.line, got, c.greedy)
		}
		if got := minRun2.StripColumn(c.line); got != c.minRun2 {
			t.Errorf("min_run2(%q) = %q, want %q", c.line, got, c.minRun2)
		}
		if got := fixed21.StripColumn(c.line); got != c.fixed21 {
			t.Errorf("fixed21(%q) = %q, want %q", c.line, got, c.fixed21)
		}
	}
}

func TestRMUDDescriptionStripsLiveMinimapColumn(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	lines := []string{
		"Еле заметная тропинка",
		".....................      Карниз становится все уже, и уже вряд ли два",
		"..................... человека иогли бы разойтись на нем. А впереди какие-то",
		"..................... зловещие развалины преградили дорогу...",
		"[ Exits: E W ]",
	}
	var got RoomEvent
	var ok bool
	for _, line := range lines {
		if ev, emitted := acc.Feed(line); emitted {
			got, ok = ev, true
		}
	}
	if !ok {
		t.Fatal("live RMUD room block was not emitted")
	}
	want := "Карниз становится все уже, и уже вряд ли два человека иогли бы разойтись на нем. А впереди какие-то зловещие развалины преградили дорогу..."
	if got.Hint != "Еле заметная тропинка" || got.Desc != want || got.Exits != "E W" {
		t.Fatalf("room event=%+v, want cleaned description %q", got, want)
	}
}

// fixed_width must cut on RUNE column, not byte offset.
func TestStripFixedWidthIsRuneColumn(t *testing.T) {
	p := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "fixed3"
		s.Strip.Mode, s.Strip.Width = StripFixedWidth, 3
		return s
	}())
	if got := p.StripColumn("абвгде"); got != "где" {
		t.Errorf("StripColumn = %q, want где", got)
	}
	if got := p.StripColumn("аб"); got != "" {
		t.Errorf("short line = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Block boundaries: prompt-stop vs blank-stop vs max_lines.
// ---------------------------------------------------------------------------

func TestBlockStopPrompt(t *testing.T) {
	withPrompt := MustCompile(englishSpec())
	blankOnly := MustCompile(func() ProfileSpec {
		s := englishSpec()
		s.Name = "english-blank-only"
		s.Block.Stops = []BlockStop{StopBlank, StopMaxLines}
		s.Prompt = ""
		return s
	}())

	lines := []string{
		"<100hp 50m>",
		"The Old Cobbled Road",
		"Ancient stones pave this road.",
		"Obvious exits: north.",
	}

	ev, ok := feedLines(t, withPrompt, lines)
	if !ok {
		t.Fatal("no event with prompt stop")
	}
	if ev.Hint != "The Old Cobbled Road" {
		t.Errorf("prompt-stop hint = %q, want The Old Cobbled Road", ev.Hint)
	}

	// Without the prompt stop nothing bounds the block, so header=first picks up
	// the prompt line. This is what proves the prompt stop is actually load-bearing.
	ev2, ok := feedLines(t, blankOnly, lines)
	if !ok {
		t.Fatal("no event without prompt stop")
	}
	if ev2.Hint != "<100hp 50m>" {
		t.Errorf("blank-only hint = %q, want the prompt line (proving the stop mattered)", ev2.Hint)
	}
}

func TestBlockStopBlank(t *testing.T) {
	p := BuiltinRMUD()
	ev, ok := feedLines(t, p, []string{
		"Ты убил крысу.",
		"", // blank separator: everything above is not part of the block
		"Кают-компания",
		".[ ].....      Простая комната.",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want Кают-компания (blank must bound the block)", ev.Hint)
	}
	if strings.Contains(ev.Desc, "крысу") {
		t.Errorf("desc leaked past the blank stop: %q", ev.Desc)
	}
}

// The preamble case the RMUD header rule exists for: a "following" line sits
// directly above the room name with NO blank line between, so only the header
// rule (not a block stop) can exclude it.
func TestBlockPreambleAboveHeaderExcluded(t *testing.T) {
	p := BuiltinRMUD()
	ev, ok := feedLines(t, p, []string{
		"Вы последовали за Ринтаом на север.",
		"Кают-компания",
		".[ ]-[ ]....... Большой круглый стол.",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want Кают-компания (preamble must be excluded)", ev.Hint)
	}
	if strings.Contains(ev.Desc, "последовали") {
		t.Errorf("preamble leaked into desc: %q", ev.Desc)
	}
	// header=first would (wrongly, for RMUD) take the preamble — confirming the
	// two header rules really differ.
	first := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "rmud-header-first"
		s.Header.Rule = HeaderFirst
		return s
	}())
	ev2, _ := feedLines(t, first, []string{
		"Вы последовали за Ринтаом на север.",
		"Кают-компания",
		".[ ]-[ ]....... Большой круглый стол.",
		"[ Exits: N ]",
	})
	if ev2.Hint != "Вы последовали за Ринтаом на север." {
		t.Errorf("header=first hint = %q, want the preamble line", ev2.Hint)
	}
}

func TestBlockStopMaxLines(t *testing.T) {
	p := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "rmud-max3"
		s.Block.MaxLines = 3
		s.Header.Rule = HeaderFirst // isolate the bound from the header rule
		return s
	}())
	// 6 body lines, but only the last 3 may survive the bound.
	ev, ok := feedLines(t, p, []string{
		"line one", "line two", "line three",
		"line four", "line five", "line six",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "line four" {
		t.Errorf("hint = %q, want line four (max_lines=3 keeps the last 3)", ev.Hint)
	}
	if ev.Desc != "line five line six" {
		t.Errorf("desc = %q, want \"line five line six\"", ev.Desc)
	}
	if strings.Contains(ev.Desc, "line one") || strings.Contains(ev.Desc, "line three") {
		t.Errorf("desc exceeded max_lines: %q", ev.Desc)
	}
}

// ---------------------------------------------------------------------------
// Prompt preset + compile validation.
// ---------------------------------------------------------------------------

func TestTortillaPromptPresetMatchesRealPrompts(t *testing.T) {
	p := MustCompile(func() ProfileSpec {
		s := RMUDSpec()
		s.Name = "rmud-tortilla-prompt"
		s.Block.Stops = []BlockStop{StopBlank, StopPrompt, StopMaxLines}
		s.Prompt = TortillaPromptPattern
		return s
	}())
	for _, prompt := range []string{
		"220ж 86б 511510о 1546м Зап:3 Выходы:ВЗ>",
		"100H 100M 100V>",
	} {
		if !p.promptRE.MatchString(prompt) {
			t.Errorf("tortilla preset did not match %q", prompt)
		}
	}
	for _, notPrompt := range []string{
		"Кают-компания",
		"[ Exits: N E S W ]",
	} {
		if p.promptRE.MatchString(notPrompt) {
			t.Errorf("tortilla preset wrongly matched %q", notPrompt)
		}
	}
	// And it works as a block stop.
	ev, ok := feedLines(t, p, []string{
		"Ты убил крысу.",
		"220ж 86б 511510о 1546м Зап:3 Выходы:ВЗ>",
		"Кают-компания",
		".[ ].....      Простая комната.",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want Кают-компания", ev.Hint)
	}
	if strings.Contains(ev.Desc, "крысу") {
		t.Errorf("desc leaked past the prompt stop: %q", ev.Desc)
	}
}

func TestCompileRejectsBadSpecs(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*ProfileSpec)
		wants string
	}{
		{"empty exits pattern", func(s *ProfileSpec) { s.Exits.Pattern = "" }, "empty exits pattern"},
		{"bad exits regex", func(s *ProfileSpec) { s.Exits.Pattern = `^[ Exits(` }, "exits pattern"},
		{"no capture group", func(s *ProfileSpec) { s.Exits.Pattern = `^\[ Exits:.*\]$` }, "exactly 1 capture group"},
		{"two capture groups", func(s *ProfileSpec) { s.Exits.Pattern = `^\[ (Exits):\s*(.*?)\s*\]$` }, "exactly 1 capture group"},
		{"unknown stop", func(s *ProfileSpec) { s.Block.Stops = []BlockStop{"sometimes"} }, "unknown block stop"},
		{"prompt stop without pattern", func(s *ProfileSpec) { s.Block.Stops = []BlockStop{StopPrompt} }, "needs a prompt pattern"},
		{"bad prompt regex", func(s *ProfileSpec) {
			s.Block.Stops = []BlockStop{StopPrompt}
			s.Prompt = `(`
		}, "prompt pattern"},
		// A bad prompt regex must be rejected even when the stop is NOT enabled,
		// or it lies dormant and explodes when someone turns the stop on.
		{"bad prompt regex, stop not enabled", func(s *ProfileSpec) { s.Prompt = `(` }, "prompt pattern"},
		{"zero max lines", func(s *ProfileSpec) { s.Block.MaxLines = 0 }, "max_lines must be > 0"},
		{"no stops at all", func(s *ProfileSpec) { s.Block.Stops = nil }, "no stop conditions"},
		{"unknown strip mode", func(s *ProfileSpec) { s.Strip.Mode = "shave" }, "unknown strip mode"},
		{"min_run without n", func(s *ProfileSpec) { s.Strip.Mode = StripMinRun }, "MinRun >= 1"},
		// An empty glyph set silently no-ops the strip AND silently degrades
		// last_before_stripped to first.
		{"greedy without glyphs", func(s *ProfileSpec) { s.Strip.Glyphs = "" }, "non-empty glyph set"},
		{"min_run without glyphs", func(s *ProfileSpec) {
			s.Strip.Mode, s.Strip.MinRun, s.Strip.Glyphs = StripMinRun, 2, ""
		}, "non-empty glyph set"},
		// An omitted Width is the likeliest JSON/LLM mistake: it would mean "no strip".
		{"fixed_width without width", func(s *ProfileSpec) { s.Strip.Mode = StripFixedWidth }, "Width >= 1"},
		{"unknown header rule", func(s *ProfileSpec) { s.Header.Rule = "vibes" }, "unknown header rule"},
		{"header without column run", func(s *ProfileSpec) { s.Header.ColumnRun = 0 }, "ColumnRun >= 1"},
		// An empty guard puts the exits regex on EVERY incoming line (AGENTS #3).
		{"empty guard", func(s *ProfileSpec) { s.Exits.Guard = "" }, "empty exits guard"},
		// A guard the pattern can never match kills the detector silently, forever.
		{"guard cannot match pattern", func(s *ProfileSpec) { s.Exits.Guard = "Obvious exits:" }, "can never match"},
		{"guard subtly wrong", func(s *ProfileSpec) { s.Exits.Guard = "[Exits:" }, "can never match"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := RMUDSpec()
			c.mut(&s)
			_, err := Compile(s)
			if err == nil {
				t.Fatalf("Compile accepted a bad spec (%s)", c.name)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %v, want mention of %q", err, c.wants)
			}
		})
	}
}

func TestCompileBoundsExitsAlternatives(t *testing.T) {
	base := RMUDSpec()
	rule := ExitsRule{Guard: base.Exits.Guard, Pattern: base.Exits.Pattern}
	base.Exits.Alt = make([]ExitsRule, maxExitsAlternatives)
	for i := range base.Exits.Alt {
		base.Exits.Alt[i] = rule
	}
	if _, err := Compile(base); err != nil {
		t.Fatalf("Compile accepted %d alternative exits rules: %v", maxExitsAlternatives, err)
	}

	base.Exits.Alt = append(base.Exits.Alt, rule)
	if _, err := Compile(base); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("Compile with %d alternatives = %v, want a clear upper-bound error", len(base.Exits.Alt), err)
	}
}

// The guard/pattern check must not reject patterns whose literal prefix is
// legitimately undeterminable (alternation, character class) — that is
// unprovable, not wrong.
func TestGuardPatternCheckAllowsUndeterminablePrefixes(t *testing.T) {
	for _, c := range []struct{ guard, pattern string }{
		{"Exits", `^(?:Exits|Выходы): (.*)$`},
		{"exits", `^[Ee]xits: (.*)$`},
		{"anything", `(.*)`},
	} {
		if err := checkGuardMatchesPattern(c.guard, c.pattern); err != nil {
			t.Errorf("guard %q / pattern %q wrongly rejected: %v", c.guard, c.pattern, err)
		}
	}
	// And it DOES catch a determinable mismatch.
	if err := checkGuardMatchesPattern("[ Выходы:", `^\[ Exits:\s*(.*?)\s*\]$`); err == nil {
		t.Error("a guard the pattern can never match was accepted")
	}
}

func TestCompileAcceptsBuiltinAndEnglish(t *testing.T) {
	if _, err := Compile(RMUDSpec()); err != nil {
		t.Fatalf("RMUDSpec does not compile: %v", err)
	}
	if _, err := Compile(englishSpec()); err != nil {
		t.Fatalf("englishSpec does not compile: %v", err)
	}
	if BuiltinRMUD().Name() != "rmud" {
		t.Errorf("builtin name = %q", BuiltinRMUD().Name())
	}
	if BuiltinRMUD().MaxLines() != 32 {
		t.Errorf("builtin max lines = %d", BuiltinRMUD().MaxLines())
	}
	// The accumulator sizes its ring from the profile.
	acc := NewAccumulator(MustCompile(englishSpec()))
	if got := cap(acc.ring); got != 16 {
		t.Errorf("ring cap = %d, want 16 (from the profile)", got)
	}
	if acc.Profile().Name() != "test-english" {
		t.Errorf("accumulator profile = %q", acc.Profile().Name())
	}
}

// A Profile is handed around by value; copies must stay usable and must not
// share mutable state.
func TestProfileValueSemantics(t *testing.T) {
	a := BuiltinRMUD()
	b := a
	if _, ok := b.ParseExits("[ Exits: N ]"); !ok {
		t.Error("copied profile lost its compiled regex")
	}
	if a.Spec().Strip.Glyphs != b.Spec().Strip.Glyphs {
		t.Error("copied profile spec differs")
	}
}

// Spec() must hand out a DEEP copy. Block.Stops is a slice; if it aliased the
// profile's own backing array, a caller editing what Spec() returned — which is
// exactly what the future settings UI will do — would silently rewrite the
// package-level builtin for every session in the process, permanently.
func TestSpecIsDeepCopy(t *testing.T) {
	s := BuiltinRMUD().Spec()
	if len(s.Block.Stops) == 0 {
		t.Fatal("builtin has no stops")
	}
	s.Block.Stops[0] = StopMaxLines
	s.Block.MaxLines = 999

	if got := BuiltinRMUD().Spec().Block.Stops[0]; got != StopBlank {
		t.Errorf("mutating a Spec() copy corrupted the builtin: stops[0] = %q, want %q", got, StopBlank)
	}
	if got := BuiltinRMUD().MaxLines(); got != 32 {
		t.Errorf("builtin max lines = %d, want 32", got)
	}
	// Compile must likewise not retain the caller's slice.
	spec := RMUDSpec()
	p := MustCompile(spec)
	spec.Block.Stops[0] = StopMaxLines
	if got := p.Spec().Block.Stops[0]; got != StopBlank {
		t.Errorf("mutating the spec after Compile changed the profile: stops[0] = %q", got)
	}
}

// An ignored Compile error yields a zero Profile. Once specs come from JSON
// that is a live possibility, and it must degrade to "no mapper" rather than
// panicking inside the session's read loop.
func TestZeroProfileIsInertNotExplosive(t *testing.T) {
	var zero Profile
	if _, ok := zero.ParseExits("[ Exits: N ]"); ok {
		t.Error("zero profile recognized an exits line")
	}
	acc := NewAccumulator(zero)
	// Feeding it must neither panic nor emit, and the ring must stay bounded.
	for i := 0; i < 200; i++ {
		if _, ok := acc.Feed("[ Exits: N ]"); ok {
			t.Fatal("zero profile emitted a room event")
		}
		if _, ok := acc.Feed("some line"); ok {
			t.Fatal("zero profile emitted a room event")
		}
	}
	if len(acc.ring) > 1 {
		t.Errorf("zero profile ring grew to %d, want <= 1", len(acc.ring))
	}
}

// ---------------------------------------------------------------------------
// Rules that were silently removable: each of these fails only if the specific
// line of logic it names is deleted.
// ---------------------------------------------------------------------------

// Without the `a.ring = a.ring[:0]` reset in Feed, a second exits line re-uses
// the previous block's lines and emits a STALE hint instead of the empty-hint
// pipe event the tracker relies on to stay pipe-safe.
func TestRingResetAfterEmit(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	first, ok := feedLinesAcc(acc, []string{"Кают-компания", ".[ ].....      Простая комната.", "[ Exits: N ]"})
	if !ok || first.Hint != "Кают-компания" {
		t.Fatalf("first block = %+v", first)
	}
	second, ok := acc.Feed("[ Exits: S ]")
	if !ok {
		t.Fatal("second exits line emitted nothing")
	}
	if second.Hint != "" || second.Desc != "" {
		t.Errorf("second event reused the previous block: %+v (want empty hint/desc pipe event)", second)
	}
	if second.Exits != "S" {
		t.Errorf("second exits = %q, want S", second.Exits)
	}
}

// The guard check and ParseExits both TrimSpace. Drop either and an indented
// exits line stops being detected.
func TestIndentedExitsLineStillDetected(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	ev, ok := feedLinesAcc(acc, []string{"Кают-компания", ".[ ].....      Простая комната.", "   [ Exits: N E ]  "})
	if !ok {
		t.Fatal("indented exits line not detected")
	}
	if ev.Exits != "N E" {
		t.Errorf("exits = %q, want N E", ev.Exits)
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q", ev.Hint)
	}
}

// StopBlank tests TrimSpace(l) == "", not l == "": a separator line of spaces
// must still bound the block.
func TestWhitespaceOnlyLineIsBlankStop(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	ev, ok := feedLinesAcc(acc, []string{
		"Ты убил крысу.",
		"    ", // whitespace-only separator
		"Кают-компания",
		".[ ].....      Простая комната.",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want Кают-компания (whitespace-only line must stop the scan)", ev.Hint)
	}
	if strings.Contains(ev.Desc, "крысу") {
		t.Errorf("desc leaked past the whitespace-only stop: %q", ev.Desc)
	}
}

// The none-token comparison is case-INSENSITIVE and canonicalizes to the
// profile's spelling.
func TestNoneTokenIsCaseInsensitive(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	for _, line := range []string{"[ Exits: none ]", "[ Exits: None ]", "[ Exits: NONE ]", "[ Exits:  ]"} {
		ev, ok := feedLinesAcc(NewAccumulator(BuiltinRMUD()), []string{"Шалаш", ".[ ].....      x", line})
		if !ok {
			t.Fatalf("%q not detected", line)
		}
		if ev.Exits != "none" {
			t.Errorf("%q -> exits %q, want none", line, ev.Exits)
		}
	}
	_ = acc
}

// The header and each desc line are TrimSpace'd.
func TestHeaderAndDescAreTrimmed(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	ev, ok := feedLinesAcc(acc, []string{
		"   Кают-компания   ",
		"Простая комната.   ",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "Кают-компания" {
		t.Errorf("hint = %q, want trimmed", ev.Hint)
	}
	if ev.Desc != "Простая комната." {
		t.Errorf("desc = %q, want trimmed", ev.Desc)
	}
}

// firstMap == 0: the block's FIRST line already carries a left column, so there
// is no preamble and no separate header line. The `firstMap > 0` guard in
// HeaderIndex must fall through to index 0 — mutating it to `>= 0` yields
// block[-1] and panics in the session read loop.
func TestHeaderBlockStartsWithColumnLine(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	ev, ok := feedLinesAcc(acc, []string{
		".[ ]-[ ]......... первая строка",
		".[ ]-[ ]......... вторая строка",
		"[ Exits: N ]",
	})
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != ".[ ]-[ ]......... первая строка" {
		t.Errorf("hint = %q, want the first (column) line verbatim", ev.Hint)
	}
	if ev.Desc != "вторая строка" {
		t.Errorf("desc = %q, want вторая строка", ev.Desc)
	}
}

// ---------------------------------------------------------------------------
// End-to-end through the accumulator with fixed_width: nothing else proves
// StripFixedWidth works outside a direct StripColumn call.
// ---------------------------------------------------------------------------

func TestEnglishFixedWidthProfileEndToEnd(t *testing.T) {
	spec := englishSpec()
	spec.Name = "test-english-fixed"
	spec.Strip = StripSpec{Mode: StripFixedWidth, Width: 12}
	p := MustCompile(spec)

	//          |<-- 12 -->|
	lines := []string{
		"<100hp 50m>",
		"The Old Cobbled Road",
		"  [ ]-[ ]   Ancient stones pave this road, worn smooth by",
		"   |        centuries of cart traffic.",
		"            very old cobblestones line the verge.",
		"Obvious exits: north, east.",
	}
	ev, ok := feedLines(t, p, lines)
	if !ok {
		t.Fatal("no event")
	}
	if ev.Hint != "The Old Cobbled Road" {
		t.Errorf("hint = %q", ev.Hint)
	}
	want := "Ancient stones pave this road, worn smooth by centuries of cart traffic. very old cobblestones line the verge."
	if ev.Desc != want {
		t.Errorf("desc =\n %q\nwant\n %q", ev.Desc, want)
	}
	if ev.Exits != "north, east" {
		t.Errorf("exits = %q", ev.Exits)
	}
}

// ---------------------------------------------------------------------------
// ProfileSpec JSON round-trip: persistence freezes these keys.
// ---------------------------------------------------------------------------

func TestProfileSpecJSONRoundTrip(t *testing.T) {
	for _, orig := range []ProfileSpec{RMUDSpec(), englishSpec(), ansiHeaderSpec()} {
		blob, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal %s: %v", orig.Name, err)
		}
		var back ProfileSpec
		if err := json.Unmarshal(blob, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", orig.Name, err)
		}
		if !reflect.DeepEqual(orig, back) {
			t.Errorf("%s did not round-trip:\n orig %+v\n back %+v", orig.Name, orig, back)
		}
		if _, err := Compile(back); err != nil {
			t.Errorf("%s does not compile after round-trip: %v", orig.Name, err)
		}
	}
	// Keys are snake_case, matching the enum values.
	blob, _ := json.Marshal(RMUDSpec())
	for _, key := range []string{`"none_token"`, `"max_lines"`, `"column_run"`, `"guard"`, `"pattern"`, `"stops"`} {
		if !strings.Contains(string(blob), key) {
			t.Errorf("marshalled spec missing %s: %s", key, blob)
		}
	}
}

// ---------------------------------------------------------------------------
// Hot path: a non-exits line must not allocate.
// ---------------------------------------------------------------------------

func TestFeedNonExitsLineDoesNotAllocate(t *testing.T) {
	acc := NewAccumulator(BuiltinRMUD())
	// Prime the ring to steady state so append never grows it.
	for i := 0; i < BuiltinRMUD().MaxLines(); i++ {
		acc.Feed(".[ ]-[ ]......... круглый стол на двенадцать персон")
	}
	got := testing.AllocsPerRun(200, func() {
		acc.Feed(".[ ]-[ ]......... круглый стол на двенадцать персон")
	})
	if got != 0 {
		t.Errorf("Feed allocated %v times per non-exits line, want 0", got)
	}
}

func BenchmarkFeedNonExitsLine(b *testing.B) {
	acc := NewAccumulator(BuiltinRMUD())
	line := ".[ ]-[ ]......... круглый стол на двенадцать персон приколочен к полу"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		acc.Feed(line)
	}
}
