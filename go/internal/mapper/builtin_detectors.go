package mapper

// ---------------------------------------------------------------------------
// The builtin detector library.
//
// Two entries, deliberately: one entry proves nothing about a library
// mechanism. The second one is what forces the registry, the reference scheme,
// the grouped selector and the conformance suite to actually be general.
//
//   rmud     — Verified.  Today's RMUDSpec, byte-identical to the original
//              hardcoded detector. This is the DEFAULT (DefaultDetectorKey).
//   diku-en  — Starter.   A plausible English Diku-family spec that NOBODY has
//              run against a live server. Its fixtures are synthetic.
//
// Registration happens in this package's init(), so any binary that links
// `mapper` at all has both — there is no wiring line in main() to forget, and
// the session's default resolution can rely on "rmud" existing.
// ---------------------------------------------------------------------------

func init() {
	RegisterDetector(newRMUDDetector)
	RegisterDetector(newDikuENDetector)
}

// --- rmud ------------------------------------------------------------------

// RMUDDetectorInfo is the builtin RMUD detector's self-description. Verified is
// claimed here and nowhere else: this is the only format that has been run
// against a live server (RMUD, 8034 rooms imported and walked).
func RMUDDetectorInfo() DetectorInfo {
	return DetectorInfo{
		Key:        DefaultDetectorKey,
		Title:      "RMUD",
		MUD:        "RMUD (rmud.ru)",
		Provenance: ProvenanceVerified,
		Description: "The format rubymud's mapper was written against. A room block is " +
			"the room name, then description lines each prefixed by the ASCII minimap " +
			"column, closed by a `[ Exits: ... ]` line; blocks are separated by blank " +
			"lines. Door exits keep their parentheses. This is the default for a session " +
			"with no room format chosen.",
	}
}

func newRMUDDetector() RoomDetector {
	return NewSpecDetector(BuiltinRMUD(), RMUDDetectorInfo()).
		WithConformance(
			StrippedLine(".[ ]-[ ]......... круглый стол на двенадцать персон"),
			ConformanceCase{
				Name: "block_with_minimap_column",
				Feed: strippedLines(
					"",
					"Вторая",
					".[ ].....      two",
					"[ Exits: N ]",
				),
				Want: []RoomEvent{{Hint: "Вторая", Desc: "two", Exits: "N"}},
			},
			ConformanceCase{
				Name: "dead_end_reports_none",
				Feed: strippedLines(
					"",
					"Тихая комната",
					"Простая комната.",
					"[ Exits: ]",
				),
				Want: []RoomEvent{{Hint: "Тихая комната", Desc: "Простая комната.", Exits: "none"}},
			},
			ConformanceCase{
				Name: "doors_are_preserved_verbatim",
				Feed: strippedLines(
					"",
					"Кают-компания",
					".[ ]-[ ]....   Вы стоите в большой каюте.",
					"[ Exits: (N) (E) S (W) (D) ]",
				),
				Want: []RoomEvent{{
					Hint:  "Кают-компания",
					Desc:  "Вы стоите в большой каюте.",
					Exits: "(N) (E) S (W) (D)",
				}},
			},
			ConformanceCase{
				Name: "ordinary_output_emits_nothing",
				Feed: strippedLines(
					"Вы говорите: 'привет'",
					"Кто-то machет вам рукой.",
					"Вы получаете 12 монет.",
				),
			},
		)
}

// --- diku-en ---------------------------------------------------------------

// DikuENDetectorInfo describes the English Diku starter. Read the Description:
// it is written for the person choosing it in the settings UI, and it says
// plainly that it is unverified and what it does not cover.
func DikuENDetectorInfo() DetectorInfo {
	return DetectorInfo{
		Key:        "diku-en",
		Title:      "Diku-family (English) — starter",
		MUD:        "Generic English Diku/Merc/ROM/Circle",
		Provenance: ProvenanceStarter,
		Description: "STARTER — nobody has run this against a live server; its test " +
			"fixtures are synthetic. It recognizes the two most common English Diku " +
			"exits lines (`There is/are N obvious exit(s): ...`, `The (only) obvious " +
			"exit(s) is/are: ...`, and `There are no obvious exits.`) and takes the " +
			"first line of the block as the room name. The block boundary is the " +
			"universal stat prompt (e.g. `100H 100M 100V >`). It does NOT cover the " +
			"`You see ... exit(s) leading ...` wording — the cheap prefix guard that " +
			"keeps the parser off the hot path is a single literal, and that wording " +
			"shares no prefix with the others; it needs its own format. Copy this and " +
			"adjust it to your server.",
	}
}

// DikuENSpec is the pure-data English Diku-family starter spec.
//
// The exits wordings and prompt fallback are deliberately plausible Diku-family
// defaults, not a claim of live compatibility. They remain Starter until a
// running server verifies the complete profile.
// Neither has been confirmed against a running server BY US — hence Starter.
func DikuENSpec() ProfileSpec {
	count := `(?:no|one|two|three|four|five|six|seven|eight|nine|ten|[0-9]+)`
	return ProfileSpec{
		Name: "diku-en",
		Exits: ExitsSpec{
			Guard:   "There is ",
			Pattern: `^There is ` + count + ` obvious exits?[:.]?\s*(.*?)\s*\.?$`,
			Alt: []ExitsRule{
				{Guard: "There are ", Pattern: `^There are ` + count + ` obvious exits?[:.]?\s*(.*?)\s*\.?$`},
				{Guard: "The only obvious exit", Pattern: `^The only obvious exits? (?:is|are)[:.]?\s*(.*?)\s*\.?$`},
				{Guard: "The obvious exit", Pattern: `^The obvious exits? (?:is|are)[:.]?\s*(.*?)\s*\.?$`},
			},
			NoneToken: "none",
		},
		Block: BlockSpec{
			// The prompt is the boundary here, NOT a blank line: Diku servers routinely
			// print a blank line between the room name and the description, so StopBlank
			// would cut the block in half and lose the name.
			Stops:    []BlockStop{StopPrompt, StopMaxLines},
			MaxLines: 32,
		},
		Prompt: TortillaPromptPattern,
		Strip: StripSpec{
			// There is no left column on a Diku server — no minimap, no gutter. The
			// glyph set is therefore just the space, which makes the strip equivalent to
			// a trim. It is not omitted because Compile (correctly) refuses an empty
			// glyph set for a run-based strip mode, and "trim only" is the honest
			// description of what this format needs.
			Mode:   StripGreedy,
			Glyphs: " ",
		},
		Header: HeaderSpec{
			// No column line exists, so last_before_stripped would degrade to "first"
			// anyway. Saying `first` states the intent instead of relying on that.
			Rule: HeaderFirst,
		},
	}
}

var builtinDikuEN = MustCompile(DikuENSpec())

// BuiltinDikuEN returns the compiled English Diku starter profile.
func BuiltinDikuEN() Profile { return builtinDikuEN }

func newDikuENDetector() RoomDetector {
	return NewSpecDetector(BuiltinDikuEN(), DikuENDetectorInfo()).
		WithConformance(
			// The filler is the WORST case on purpose, not the average one.
			//
			// Ordinary prose beginning with "The" used to pay the combined exits regex.
			// Independent exact guards keep this worst-case filler on the prefix-only
			// path while preserving both Diku wording families.
			StrippedLine("The large orc hits you very hard."),
			ConformanceCase{
				Name: "there_are_n_obvious_exits",
				Feed: strippedLines(
					"100H 100M 100V > ",
					"The Temple Square",
					"You are standing on the temple square.",
					"There are 3 obvious exits: north, south, east.",
				),
				Want: []RoomEvent{{
					Hint:  "The Temple Square",
					Desc:  "You are standing on the temple square.",
					Exits: "north, south, east",
				}},
			},
			ConformanceCase{
				Name: "the_only_obvious_exit_is",
				Feed: strippedLines(
					"100H 100M 100V > ",
					"A Dead End",
					"The passage stops here.",
					"The only obvious exit is west.",
				),
				Want: []RoomEvent{{
					Hint:  "A Dead End",
					Desc:  "The passage stops here.",
					Exits: "west",
				}},
			},
			ConformanceCase{
				Name: "no_obvious_exits_reports_none",
				Feed: strippedLines(
					"100H 100M 100V > ",
					"A Sealed Vault",
					"Solid rock on every side.",
					"There are no obvious exits.",
				),
				Want: []RoomEvent{{
					Hint:  "A Sealed Vault",
					Desc:  "Solid rock on every side.",
					Exits: "none",
				}},
			},
			ConformanceCase{
				Name: "prompt_bounds_the_block_so_earlier_output_is_excluded",
				Feed: strippedLines(
					"Someone tells you 'meet me at the square'.",
					"100H 100M 100V > ",
					"The Temple Square",
					"There is one obvious exit: north.",
				),
				Want: []RoomEvent{{
					Hint:  "The Temple Square",
					Desc:  "",
					Exits: "north",
				}},
			},
			ConformanceCase{
				// The guard-hit / regex-miss shape, as a correctness case as well as the
				// benchmark filler: these lines are extremely common on a Diku server and
				// must never be mistaken for an exits line.
				Name: "prose_that_passes_the_cheap_guard_still_emits_nothing",
				Feed: strippedLines(
					"The large orc hits you very hard.",
					"The door swings shut behind you.",
					"There is a sword here.",
				),
			},
			ConformanceCase{
				Name: "ordinary_prose_starting_with_the_emits_nothing",
				Feed: strippedLines(
					"The guard glares at you.",
					"The door is locked.",
					"The obvious thing to do is leave.",
				),
			},
		)
}

// strippedLines is fixture sugar: fixtures are written as plain text, and a
// declarative detector never reads Line.Raw.
func strippedLines(lines ...string) []Line {
	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		out = append(out, StrippedLine(l))
	}
	return out
}
