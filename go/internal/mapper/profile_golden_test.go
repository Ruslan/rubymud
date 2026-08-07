package mapper

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The regression gate for the profile refactor.
//
// The wanted values below were captured from the detector BEFORE the Profile
// type existed (hardcoded exits regex, hardcoded glyph switch, hardcoded
// "scan back to blank, cap 32"). They are full-string, not substring: a single
// changed byte in Desc changes mapimport.Fingerprint(hint, desc, exits), which
// is the tracker's ONLY re-localization path (auto-resync / reconcile step 3).
// Fingerprint drift breaks resync across every imported room while ordinary
// walking still looks fine, so this must fail loudly rather than subtly.
//
// If you are here because this test failed: BuiltinRMUD's rules changed. That
// is a behaviour change, not a refactor — do not edit the wanted values to make
// it pass.
// ---------------------------------------------------------------------------

func TestGoldenBuiltinRMUDByteIdentical(t *testing.T) {
	cases := []struct {
		name  string
		block string
		hint  string
		desc  string
		exits string
	}{
		{
			name:  "fixture1_doors_and_down_with_following_preamble",
			block: fixture1,
			hint:  "Кают-компания",
			desc:  "Вы стоите в большой, ярко освещенной каюте. Большой круглый стол на двенадцать персон приколочен к полу в центре комнаты, несколько столов поменьше расставлены по углам. Густой запах спирта мешает Вам дышать.",
			exits: "(N) (E) S (W) (D)",
		},
		{
			name:  "fixture2_dead_end_none",
			block: fixture2,
			hint:  "Шалаш",
			desc:  "Это легкое сооружение из веток - место отдыха свободных от стражи воинов. Ветви искусно сплетены между собой, так что этому шалашу нипочем и довольно сильный дождь. Несколько охапок сена на полу приглашают Вас отдохнуть на них.",
			exits: "none",
		},
		{
			name:  "fixture3_stairs_up",
			block: fixture3,
			hint:  `Постоялый двор "У Аджойса"`,
			desc:  "В помещении постоялого двора довольно многолюдно. В просторном зале установлено несколько больших деревянных столов, за которыми многочисленные путешественники едят и пьют, обсуждая свершившиеся и будущие сделки. Несколько людей, по виду - наемников, играют в кости в углу. Хозяин заведения, сам старик Аджойс, обозревает зал из-за барной стойки. С кухни за его спиной доносятся вкусные запахи. Крутая лестница ведет на второй этаж, где расположены комнаты постояльцев.",
			exits: "S U",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			acc := NewAccumulator(BuiltinRMUD())
			ev := feedBlock(t, acc, c.block)
			if ev.Hint != c.hint {
				t.Errorf("hint drift:\n got %q\nwant %q", ev.Hint, c.hint)
			}
			if ev.Desc != c.desc {
				t.Errorf("desc drift (FINGERPRINT-BREAKING):\n got %q\nwant %q", ev.Desc, c.desc)
			}
			if ev.Exits != c.exits {
				t.Errorf("exits drift:\n got %q\nwant %q", ev.Exits, c.exits)
			}
		})
	}
}

// The synthetic blocks the session wiring tests feed through the real pipeline,
// pinned here at the parser level so a profile change that only shows up in the
// short "header / one desc line / exits" shape is caught in this package too.
func TestGoldenBuiltinRMUDShortBlocks(t *testing.T) {
	cases := []struct {
		name              string
		lines             []string
		hint, desc, exits string
	}{
		{
			name:  "short_block",
			lines: []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"},
			hint:  "Вторая", desc: "two", exits: "N",
		},
		{
			name:  "english_prose_body",
			lines: []string{"Тюрьма", ".[ ].....      a far away jail", "[ Exits: N ]"},
			hint:  "Тюрьма", desc: "a far away jail", exits: "N",
		},
		{
			name:  "exits_only_after_blank",
			lines: []string{"", "[ Exits: S ]"},
			hint:  "", desc: "", exits: "S",
		},
		{
			name:  "no_column_line_header_is_first",
			lines: []string{"Тихая комната", "Простая комната.", "[ Exits: N E S W ]"},
			hint:  "Тихая комната", desc: "Простая комната.", exits: "N E S W",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			acc := NewAccumulator(BuiltinRMUD())
			var ev RoomEvent
			var got bool
			for _, l := range c.lines {
				if e, ok := acc.Feed(l); ok {
					ev, got = e, true
				}
			}
			if !got {
				t.Fatalf("no room event emitted for %v", c.lines)
			}
			if ev.Hint != c.hint || ev.Desc != c.desc || ev.Exits != c.exits {
				t.Errorf("drift: got {%q %q %q}, want {%q %q %q}", ev.Hint, ev.Desc, ev.Exits, c.hint, c.desc, c.exits)
			}
		})
	}
}

// The builtin must stay exactly the pre-refactor constant set. This is a
// tripwire on the spec values themselves, so a drive-by edit to RMUDSpec is
// caught even if no fixture happens to cover it.
func TestBuiltinRMUDSpecPinned(t *testing.T) {
	s := RMUDSpec()
	if s.Exits.Guard != "[ Exits:" {
		t.Errorf("exits guard = %q", s.Exits.Guard)
	}
	if s.Exits.Pattern != `^\[ Exits:\s*(.*?)\s*\]$` {
		t.Errorf("exits pattern = %q", s.Exits.Pattern)
	}
	if s.Exits.NoneToken != "none" {
		t.Errorf("none token = %q", s.Exits.NoneToken)
	}
	if s.Block.MaxLines != 32 {
		t.Errorf("max lines = %d, want 32", s.Block.MaxLines)
	}
	if s.Strip.Mode != StripGreedy {
		t.Errorf("strip mode = %q, want greedy (flipping this is the owner's call)", s.Strip.Mode)
	}
	if s.Strip.Glyphs != `[]*^v.|_- ` {
		t.Errorf("glyphs = %q", s.Strip.Glyphs)
	}
	if s.Header.Rule != HeaderLastBeforeStripped || s.Header.ColumnRun != 2 {
		t.Errorf("header = %q/%d", s.Header.Rule, s.Header.ColumnRun)
	}
	// The prompt is NOT enabled on RMUD: enabling it would add a second block
	// boundary and could truncate blocks (fingerprint drift).
	if s.Prompt != "" {
		t.Errorf("RMUD prompt should be empty, got %q", s.Prompt)
	}
	// Pin the WHOLE stop set, not just the absence of StopPrompt. Asserting only
	// the absence let StopBlank be deleted with the entire suite still green,
	// while reconstruction reached back through blank separators — see
	// TestGoldenBlankStopIsLoadBearing for what that costs.
	wantStops := []BlockStop{StopBlank, StopMaxLines}
	if !reflect.DeepEqual(s.Block.Stops, wantStops) {
		t.Errorf("block stops = %v, want exactly %v", s.Block.Stops, wantStops)
	}
}

// The golden half of the StopBlank pin: this must fail on behaviour, not just
// on the spec value, if StopBlank is removed from RMUDSpec.
//
// The trap it closes: a SPACE is a glyph, so any line above the blank separator
// that is indented 2+ spaces looks column-prefixed. With no blank stop the scan
// swallows it, firstMap becomes 0, HeaderIndex returns 0, and the hint becomes
// the unrelated preceding line while the real name is demoted into the desc —
// total hint+desc loss and exactly the fingerprint drift this regression exists to
// prevent. The previous fixtures all happened to have unindented preceding
// lines, so none of them caught it.
func TestGoldenBlankStopIsLoadBearing(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{
			name: "indented_line_above_the_blank",
			lines: []string{
				"  Ты получил 250 опыта за убийство крысы.",
				"",
				"Кают-компания",
				".[ ]-[ ]......... Большой круглый стол.",
				"[ Exits: N ]",
			},
		},
		{
			name: "dash_line_above_the_blank",
			lines: []string{
				"-- Ты потерял 3 хита. --",
				"",
				"Кают-компания",
				".[ ]-[ ]......... Большой круглый стол.",
				"[ Exits: N ]",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			acc := NewAccumulator(BuiltinRMUD())
			var ev RoomEvent
			var ok bool
			for _, l := range c.lines {
				if e, got := acc.Feed(l); got {
					ev, ok = e, true
				}
			}
			if !ok {
				t.Fatal("no event")
			}
			if ev.Hint != "Кают-компания" {
				t.Errorf("hint = %q, want Кают-компания — the blank stop is not bounding the block", ev.Hint)
			}
			if ev.Desc != "Большой круглый стол." {
				t.Errorf("desc = %q, want just the room prose", ev.Desc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The strip-mode measurement is kept as an executable test so the answer can be re-derived
// rather than trusted: does flipping the RMUD builtin greedy -> min_run(2)
// change ANY output over the in-repo block corpus?
// ---------------------------------------------------------------------------

func TestStripModeDeltaOverFixtureCorpus(t *testing.T) {
	minRun2 := RMUDSpec()
	minRun2.Name = "rmud-min_run2"
	minRun2.Strip.Mode = StripMinRun
	minRun2.Strip.MinRun = 2
	alt := MustCompile(minRun2)

	corpus := map[string][]string{
		"fixture1":     strings.Split(fixture1, "\n"),
		"fixture2":     strings.Split(fixture2, "\n"),
		"fixture3":     strings.Split(fixture3, "\n"),
		"wiring_two":   {"Вторая", ".[ ].....      two", "[ Exits: N ]"},
		"wiring_one":   {"Первая", ".[ ].....      one", "[ Exits: S ]"},
		"wiring_jail":  {"Тюрьма", ".[ ].....      a far away jail", "[ Exits: N ]"},
		"wiring_new":   {"Новая", ".[ ].....      brand new", "[ Exits: W ]"},
		"wiring_blank": {"", "[ Exits: S ]"},
		"ansi_block":   {"Тихая комната", ".[ ].....      Простая комната.", "[ Exits: N E S W ]"},
	}

	run := func(p Profile, lines []string) (RoomEvent, bool) {
		acc := NewAccumulator(p)
		var ev RoomEvent
		var got bool
		for _, l := range lines {
			if e, ok := acc.Feed(l); ok {
				ev, got = e, true
			}
		}
		return ev, got
	}

	blocks, diffs := 0, 0
	for name, lines := range corpus {
		a, okA := run(BuiltinRMUD(), lines)
		b, okB := run(alt, lines)
		if okA != okB {
			t.Errorf("%s: emission differs greedy=%v min_run2=%v", name, okA, okB)
			continue
		}
		if !okA {
			continue
		}
		blocks++
		if a != b {
			diffs++
			t.Logf("%s DIFFERS:\n greedy   %+v\n min_run2 %+v", name, a, b)
		}
	}
	t.Logf("strip-mode delta over in-repo corpus: %d blocks compared, %d differ", blocks, diffs)
	if diffs != 0 {
		t.Errorf("expected greedy and min_run(2) to agree on the in-repo corpus, %d/%d blocks differ", diffs, blocks)
	}
}

// loadCorpusDescs returns the raw room descriptions from the reference
// rmud_index.json artifact. The shared helper keeps this private corpus
// explicitly opt-in and skips ordinary runs when it is unavailable.
func loadCorpusDescs(t *testing.T) []corpusRoom {
	t.Helper()
	data := readMapperCorpus(t)
	var rooms []corpusRoom
	if err := json.Unmarshal(data, &rooms); err != nil {
		t.Fatalf("corpus json: %v", err)
	}
	return rooms
}

// The same measurement at scale. greedy and min_run(2) can only disagree on a
// line whose LEADING GLYPH RUN IS EXACTLY 1 — greedy eats that char, min_run(2)
// keeps it. On a real wire line the minimap column is ~21 glyphs, so the risk
// is confined to prose lines that carry no column at all (a description that
// outruns the map, or a server with no minimap). This runs every description
// line of all ~8034 imported rooms through both modes and reports the delta.
//
// It asserts nothing about the count: it exists so the number can be re-derived
// on demand rather than taken on trust. The builtin stays on greedy either way.
func TestStripModeDeltaOverRMUDIndexCorpus(t *testing.T) {
	rooms := loadCorpusDescs(t)

	greedy := BuiltinRMUD()
	alt := RMUDSpec()
	alt.Name = "rmud-min_run2"
	alt.Strip.Mode, alt.Strip.MinRun = StripMinRun, 2
	minRun2 := MustCompile(alt)

	// Self-check: a zero delta below is only meaningful if the comparison CAN
	// detect one. This line is the known-differing case from the spec.
	if greedy.StripColumn("very old cobblestones") == minRun2.StripColumn("very old cobblestones") {
		t.Fatal("comparison is broken: the two profiles agree on the known-differing line")
	}

	var totalLines, diffLines, diffRooms, totalRooms int
	runHist := map[int]int{} // leading-glyph-run length -> line count
	examples := map[string]int{}
	for _, r := range rooms {
		if r.Desc == "" {
			continue
		}
		totalRooms++
		roomDiffers := false
		for _, line := range strings.Split(strings.ReplaceAll(r.Desc, "\r\n", "\n"), "\n") {
			totalLines++
			_, runLen := greedy.glyphRun(line)
			if runLen > 3 {
				runLen = 4 // bucket "4+"
			}
			runHist[runLen]++
			g := greedy.StripColumn(line)
			m := minRun2.StripColumn(line)
			if g != m {
				diffLines++
				roomDiffers = true
				if len(examples) < 40 {
					examples[line] = examples[line] + 1
				}
			}
		}
		if roomDiffers {
			diffRooms++
		}
	}

	keys := make([]string, 0, len(examples))
	for k := range examples {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  differing line (x%d): %q -> greedy %q | min_run2 %q",
			examples[k], k, greedy.StripColumn(k), minRun2.StripColumn(k))
	}
	t.Logf("leading-glyph-run histogram: run=0 %d, run=1 %d, run=2 %d, run=3 %d, run>=4 %d",
		runHist[0], runHist[1], runHist[2], runHist[3], runHist[4])
	t.Logf("RMUD index corpus: %d rooms with a desc, %d description lines; "+
		"greedy vs min_run(2) differ on %d lines across %d rooms",
		totalRooms, totalLines, diffLines, diffRooms)
}
