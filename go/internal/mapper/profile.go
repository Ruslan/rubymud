package mapper

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Room-format profile: declarative input and its compiled runtime form.
//
// The room-block detector used to bake ONE mud's format into the code: the
// "[ Exits: ]" regex, the "[ Exits:" hot-path guard, the ASCII left-column
// glyph set, the "header = last non-column line before the first column line"
// rule and the "scan back to a blank line, cap 32" block rule. Those four
// concerns now live in a Profile value that the Accumulator takes as a
// parameter.
//
// The split is deliberate:
//
//   - ProfileSpec is PURE DATA (strings, ints, enums) with JSON tags. It is what
//     the persistence layer stores in SQLite and edits in the settings UI. It holds
//     no regexes and no derived state, so it round-trips through JSON unchanged.
//   - Profile is the COMPILED form: regexes and glyph lookup tables are built
//     once, at construction, and never rebuilt per line. The accumulator only
//     ever reads a Profile.
//
// Vocabulary: this package says "left column" throughout for the strippable
// left-hand region of a body line. On RMUD that column happens to be an ASCII
// minimap, but the rules are format-agnostic, so only RMUD-specific identifiers
// (RMUDGlyphs, RMUDSpec) mention the minimap.
//
// Nothing here touches the DB, the network, or the session. This package adds no
// persistence, no endpoints and no UI — only the parameterization.
// ---------------------------------------------------------------------------

// StripMode selects how the left column is removed from a body line before the
// prose is collected into the description.
type StripMode string

const (
	// StripGreedy removes the leading maximal run of glyph chars with NO minimum
	// length. This is the historical RMUD behaviour and it is lossy: a body line
	// that happens to start with a single glyph char loses that char (see
	// TestStripModes and the `v` hazard documented there).
	StripGreedy StripMode = "greedy"
	// StripMinRun is StripGreedy gated on a minimum run length: the leading glyph
	// run is removed only when it is at least MinRun runes long. A run shorter
	// than MinRun is left alone (the line is still space-trimmed).
	StripMinRun StripMode = "min_run"
	// StripFixedWidth cuts the line at a fixed rune column (Width) and trims.
	// A line of Width runes or fewer collapses to "".
	StripFixedWidth StripMode = "fixed_width"
)

// HeaderRule selects how the room name is picked out of a collected block.
type HeaderRule string

const (
	// HeaderLastBeforeStripped is the RMUD rule: the header is the last line that
	// does NOT carry a left column, immediately preceding the first line that
	// does. This exists so a movement preamble above the name ("Вы последовали за
	// … на север.") is excluded from the block. With no column line at all, the
	// first line of the block is the header.
	HeaderLastBeforeStripped HeaderRule = "last_before_stripped"
	// HeaderFirst takes the first non-blank line of the block as the header.
	// Typical for servers that print no left column.
	HeaderFirst HeaderRule = "first"
	// HeaderANSIDelimited takes the text between one configured SGR sequence and
	// the next ANSI escape. It covers servers where colour is the only reliable
	// room-name marker.
	HeaderANSIDelimited HeaderRule = "ansi_delimited"
)

// BlockStop is one stop condition for the backward scan that reconstructs a
// block when the exits line arrives.
type BlockStop string

const (
	// StopBlank stops the scan at a blank (whitespace-only) line. The blank line
	// itself is not part of the block. RMUD depends on this: without it the scan
	// reaches back through blank separators into unrelated output.
	StopBlank BlockStop = "blank"
	// StopPrompt stops the scan at a line matching the profile's prompt regex.
	// The prompt line itself is not part of the block. This is the ONLY use of
	// the prompt in this profile model — it is not a room trigger and there is no
	// telnet-GA handling and no 3-tier prompt cascade here.
	StopPrompt BlockStop = "prompt"
	// StopMaxLines is INFORMATIONAL ONLY. Block.MaxLines is always applied: it
	// sizes and caps the accumulator's ring, so a block can never exceed it
	// whether or not this stop is listed. Listing it documents intent; omitting
	// it changes nothing. It exists so the spec reads as a complete ordered set
	// of boundaries rather than hiding one of them in the ring implementation.
	StopMaxLines BlockStop = "max_lines"
)

// TortillaPromptPattern is the "universal prompt" fallback pattern from the
// tortilla client: two or more "<number><stat-letter> " groups followed by an
// optional compass/door tail and a ">". Shipped as a named preset so a profile
// can opt in, and DELIBERATELY NOT enabled in BuiltinRMUD — RMUD's block
// boundary is the blank line, and turning the prompt on there would add a
// second boundary that could truncate blocks (and so drift fingerprints).
const TortillaPromptPattern = `(?:[0-9]+[HMVXCжэбом] +){2,}.*[СЮЗВПОv^()]*>`

// RMUDGlyphs is the RMUD left-column (ASCII minimap) glyph set: room cells
// [ ] [*] [^] [v], corridor connectors - | _, empty-cell dots, and the space
// that separates the column from the prose.
const RMUDGlyphs = `[]*^v.|_- `

// ExitsRule is one independently guarded exits-line grammar. Keeping the guard
// beside its regex lets a profile support several server wordings without
// putting a broad common prefix (or every incoming line) on the regex hot path.
type ExitsRule struct {
	Guard   string `json:"guard"`
	Pattern string `json:"pattern"`
}

// maxExitsAlternatives bounds work in LooksLikeExits: every incoming line
// checks the primary guard plus every alternative guard. Eight alternates leave
// room for substantially more wording variants than the built-in Diku profile's
// three while keeping a user-supplied profile from stretching that hot path.
const maxExitsAlternatives = 8

// ExitsSpec describes how the block-completing exits line is recognized.
type ExitsSpec struct {
	// Guard is the cheap literal prefix tested on EVERY incoming line before any
	// regex work. It must be a literal prefix of every line Pattern can match
	// (after space-trimming) or exits lines are missed, and it must not be empty
	// or every line pays the regex (AGENTS.md #3). Compile enforces both as far
	// as it can — see checkGuardMatchesPattern.
	Guard string `json:"guard"`
	// Pattern is the full-line exits regex. It must have exactly one capture
	// group: the token list.
	Pattern string `json:"pattern"`
	// Alt holds additional guard+pattern pairs in matching order. The primary
	// pair above remains required for backward-compatible stored JSON; omitempty
	// keeps every existing one-rule profile byte-for-byte in the old shape.
	Alt []ExitsRule `json:"alt,omitempty"`
	// NoneToken is the canonical dead-end token, returned verbatim both when the
	// capture is empty and when it case-insensitively equals this token.
	NoneToken string `json:"none_token"`
}

// StripSpec describes the per-line left-column removal.
type StripSpec struct {
	Mode StripMode `json:"mode"`
	// Glyphs is the left-column character set, as data. It is NOT strip-only:
	// StripGreedy and StripMinRun use it to size the run they cut, and
	// HeaderLastBeforeStripped uses it (via HasLeftColumn) to locate the room
	// name, for EVERY strip mode. So it is required whenever either of those
	// applies — including under StripFixedWidth, which ignores it when cutting
	// but still needs it for the header rule. Compile enforces both halves.
	Glyphs string `json:"glyphs"`
	// MinRun is the minimum glyph-run length (in runes) for StripMinRun.
	MinRun int `json:"min_run,omitempty"`
	// Width is the cut column (in runes) for StripFixedWidth.
	Width int `json:"width,omitempty"`
}

// BlockSpec describes how far back the reconstruction scan goes.
type BlockSpec struct {
	// Stops is the ordered set of stop conditions; the first one that fires ends
	// the scan. Order is preserved so a future profile consumer can attach differing
	// include/exclude semantics per stop without changing the shape.
	Stops []BlockStop `json:"stops"`
	// MaxLines bounds the accumulator's ring and therefore every block. It is
	// ALWAYS applied — see StopMaxLines.
	MaxLines int `json:"max_lines"`
}

// HeaderSpec describes how the room name is located inside a block.
type HeaderSpec struct {
	Rule HeaderRule `json:"rule"`
	// ColumnRun is how many leading glyph runes make a line "column-prefixed"
	// for HeaderLastBeforeStripped. RMUD uses 2.
	ColumnRun int `json:"column_run,omitempty"`
	// ANSIStart is the SGR parameter list without CSI and the final m, for
	// example "1;36" or "38;5;45". Used only by HeaderANSIDelimited.
	ANSIStart string `json:"ansi_start,omitempty"`
}

// ScanDirectionSpec identifies one server-authored line in the response to a
// mapper probe. Dir is a canonical mapper direction; Prefix is compared after
// trimming the incoming line's surrounding whitespace.
type ScanDirectionSpec struct {
	Dir    string `json:"dir"`
	Prefix string `json:"prefix"`
}

// ScanSpec is optional. When present, map recording may send Command once for a
// confirmed frontier room and collect only the bounded direction lines below.
// The captured text is ephemeral UI context, never room identity or map data.
type ScanSpec struct {
	Command    string              `json:"command"`
	Directions []ScanDirectionSpec `json:"directions"`
}

// ProfileSpec is the pure-data description of one server's room format. It is
// the persistable/editable form; call Compile to get a usable Profile.
type ProfileSpec struct {
	Name   string     `json:"name"`
	Exits  ExitsSpec  `json:"exits"`
	Block  BlockSpec  `json:"block"`
	Prompt string     `json:"prompt,omitempty"` // consulted by StopPrompt; always validated when set
	Strip  StripSpec  `json:"strip"`
	Header HeaderSpec `json:"header"`
	Scan   *ScanSpec  `json:"scan,omitempty"`
	// DoorOpen is the command template a ROUTE sends to open a door it has no
	// per-edge opening command for. Two optional placeholders stand for the hop's
	// direction — "{dir}" the Russian word ("откр две {dir}" on RMUD), "{dir_en}"
	// the canonical english letter ("open door {dir_en}") — so the argument is not
	// forced into one language; a verb that takes no argument uses neither.
	//
	// IT IS AN OUTPUT-SHAPED RULE IN THIS SPEC, and that is deliberate. It
	// was a package-level constant for exactly one commit, which meant an English
	// Diku got "откр две north" in its walk. The mistake is the same class the
	// detector was rescued from: a property of ONE mud baked into shared code (the
	// minimap glyph set, before it became StripSpec.Glyphs). Scan is the other
	// server exchange: unlike this route fallback it has a bounded response parser.
	// Neither is an i18n string — i18n is the language we speak TO THE USER, while this is a
	// command we send TO THE SERVER, decided by which mud is on the other end and
	// not by the user's interface language. A player with an English UI on RMUD must
	// still send "откр две".
	//
	// The room-format profile is the only place in the schema that says "this is how
	// THIS mud talks", it is already per-session and already editable in settings, so
	// this lives here despite the rest of the spec mostly describing INPUT.
	//
	// EMPTY MEANS "DO NOT SEND ANYTHING". Only the builtin rmud profile ships a
	// value; every other profile — builtin, contributed or user — leaves it blank,
	// so an unannotated door on an unknown mud behaves exactly as it did before this
	// existed (the route reports the door and sends no opening command). A per-edge
	// open_cmd always wins over this and works on any profile.
	DoorOpen string `json:"door_open,omitempty"`
}

// clone returns a deep copy of the spec. Its slices must not be shared or a
// caller mutating the value it got
// from Spec() would rewrite the profile it came from — including the
// package-level builtin, process-wide and permanently.
func (s ProfileSpec) clone() ProfileSpec {
	out := s
	if s.Exits.Alt != nil {
		out.Exits.Alt = append([]ExitsRule(nil), s.Exits.Alt...)
	}
	if s.Block.Stops != nil {
		out.Block.Stops = append([]BlockStop(nil), s.Block.Stops...)
	}
	if s.Scan != nil {
		scan := *s.Scan
		scan.Directions = append([]ScanDirectionSpec(nil), s.Scan.Directions...)
		out.Scan = &scan
	}
	return out
}

// Profile is a compiled ProfileSpec: same rules, plus the precompiled regexes
// and glyph lookup tables.
//
// Copying a Profile is safe: the compiled members (regexes, glyph tables) are
// immutable and shared by design, and reference-typed spec members are never
// mutated internally and are deep-copied both into Compile and out of Spec().
//
// Profile is a non-trivial value (the spec strings plus the 128-byte glyph
// table and optional scan rules), so the rule appliers below take a POINTER
// receiver — a value receiver copies the whole struct on every call, which is measurable on the per-line
// path. Only the trivial accessors (Spec, Name, MaxLines) use value receivers,
// so BuiltinRMUD().Name() still compiles.
//
// The zero Profile is NOT usable — build one with Compile, MustCompile or
// BuiltinRMUD. It is however inert rather than explosive: a zero Profile never
// recognizes an exits line and never emits an event, so an ignored Compile
// error degrades to "this session has no mapper" instead of panicking in the
// session's read loop.
type Profile struct {
	spec ProfileSpec

	exitsRules       []compiledExitsRule
	promptRE         *regexp.Regexp // compiled whenever spec.Prompt is set, used only when stopPrompt
	ansiHeaderPrefix string

	glyphASCII [128]bool
	glyphWide  []rune // non-ASCII glyphs, expected empty in practice

	stopBlank  bool
	stopPrompt bool
	scan       *ScanConfig
}

// ScanConfig is the validated, bounded scan contract exposed to the session.
// The prompt regexp stays private; callers use Boundary rather than compiling
// or evaluating server-owned patterns themselves.
type ScanConfig struct {
	Command    string
	Directions []ScanDirectionSpec
	MaxLines   int
	StopBlank  bool
	promptRE   *regexp.Regexp
}

// Match recognizes one configured scan line. It never treats unprefixed MUD
// output as scan data, so interleaved NPC chatter is harmless.
func (s ScanConfig) Match(line string) (dir, hint string, ok bool) {
	line = strings.TrimSpace(line)
	for _, rule := range s.Directions {
		if strings.HasPrefix(line, rule.Prefix) {
			return rule.Dir, strings.TrimSpace(strings.TrimPrefix(line, rule.Prefix)), true
		}
	}
	return "", "", false
}

// Boundary reports a configured prompt boundary. Blank-line termination is
// kept explicit in StopBlank because the capture only accepts a blank after at
// least one direction match.
func (s ScanConfig) Boundary(line string) bool {
	return s.promptRE != nil && s.promptRE.MatchString(strings.TrimSpace(line))
}

type compiledExitsRule struct {
	guard string
	re    *regexp.Regexp
}

// Spec returns the pure-data spec this profile was compiled from. The returned
// value is a deep copy: mutating it (as the future settings UI will) cannot
// affect this profile or any other holder.
func (p Profile) Spec() ProfileSpec { return p.spec.clone() }

// Name is the profile's human-readable name.
func (p Profile) Name() string { return p.spec.Name }

// DoorOpen is the profile's door-opening command template, or "" when this mud's
// format does not describe one (in which case a route sends no opening command).
func (p Profile) DoorOpen() string { return p.spec.DoorOpen }

// Scan returns a detached scan config. False means the profile deliberately
// disables mapper-driven probing.
func (p Profile) Scan() (ScanConfig, bool) {
	if p.scan == nil {
		return ScanConfig{}, false
	}
	out := *p.scan
	out.Directions = append([]ScanDirectionSpec(nil), p.scan.Directions...)
	return out, true
}

// MaxLines is the configured block/ring bound, as specified.
func (p Profile) MaxLines() int { return p.spec.Block.MaxLines }

// ringBound is MaxLines clamped to a usable value. It is the single source of
// truth for the accumulator's ring size and cap, so the zero Profile (MaxLines
// 0) neither panics in push nor grows the ring without bound.
func (p *Profile) ringBound() int {
	if n := p.spec.Block.MaxLines; n > 0 {
		return n
	}
	return 1
}

// Compile validates a spec and precompiles its regexes and lookup tables. The
// validation is deliberately strict: these specs will come from JSON and from
// LLM/MCP callers, where a silently-degrading profile (a strip
// that never strips, a guard no line can match) is far worse than a loud error,
// because it fails INVISIBLY — hint and exits still match, only the description
// and therefore mapimport.Fingerprint drift.
func Compile(spec ProfileSpec) (Profile, error) {
	p := Profile{spec: spec.clone()}
	fail := func(format string, args ...any) (Profile, error) {
		return Profile{}, fmt.Errorf("mapper: profile %q: "+format, append([]any{spec.Name}, args...)...)
	}

	// --- exits ---
	if len(spec.Exits.Alt) > maxExitsAlternatives {
		return fail("at most %d alternative exits rules are allowed; every incoming line checks each alternative guard", maxExitsAlternatives)
	}
	compileExitsRule := func(rule ExitsRule, label string) error {
		if rule.Pattern == "" {
			return fmt.Errorf("%sempty exits pattern", label)
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return fmt.Errorf("%sexits pattern: %w", label, err)
		}
		if n := re.NumSubexp(); n != 1 {
			return fmt.Errorf("%sexits pattern must have exactly 1 capture group, has %d", label, n)
		}
		if rule.Guard == "" {
			return fmt.Errorf("%sempty exits guard: every incoming line would pay the exits regex", label)
		}
		if err := checkGuardMatchesPattern(rule.Guard, rule.Pattern); err != nil {
			return fmt.Errorf("%s%w", label, err)
		}
		p.exitsRules = append(p.exitsRules, compiledExitsRule{guard: rule.Guard, re: re})
		return nil
	}
	if err := compileExitsRule(ExitsRule{Guard: spec.Exits.Guard, Pattern: spec.Exits.Pattern}, ""); err != nil {
		return fail("%w", err)
	}
	for i, rule := range spec.Exits.Alt {
		if err := compileExitsRule(rule, fmt.Sprintf("alternative %d: ", i+1)); err != nil {
			return fail("%w", err)
		}
	}

	// --- block ---
	if len(spec.Block.Stops) == 0 {
		return fail("block has no stop conditions: the scan would reach back through unrelated output")
	}
	for _, s := range spec.Block.Stops {
		switch s {
		case StopBlank:
			p.stopBlank = true
		case StopPrompt:
			p.stopPrompt = true
		case StopMaxLines:
			// Informational; MaxLines is always applied. See StopMaxLines.
		default:
			return fail("unknown block stop %q", s)
		}
	}
	if p.stopPrompt && spec.Prompt == "" {
		return fail("block stop %q needs a prompt pattern", StopPrompt)
	}
	if spec.Prompt != "" {
		// Always compiled, even when StopPrompt is not listed: otherwise a broken
		// prompt regex is accepted here and only explodes later, when someone
		// enables the stop.
		pre, err := regexp.Compile(spec.Prompt)
		if err != nil {
			return fail("prompt pattern: %w", err)
		}
		p.promptRE = pre
	}
	if spec.Block.MaxLines <= 0 {
		return fail("block max_lines must be > 0, got %d", spec.Block.MaxLines)
	}

	// --- optional scan probe ---
	if spec.Scan != nil {
		command := strings.TrimSpace(spec.Scan.Command)
		if command == "" {
			return fail("scan command must not be empty")
		}
		if strings.ContainsAny(command, "\r\n\x00") {
			return fail("scan command must be exactly one line")
		}
		if _, movement := MoveDir(command); movement {
			return fail("scan command %q is a movement command", command)
		}
		if len(spec.Scan.Directions) == 0 || len(spec.Scan.Directions) > 6 {
			return fail("scan needs between 1 and 6 direction prefixes")
		}
		seenDir := make(map[string]bool, len(spec.Scan.Directions))
		seenPrefix := make(map[string]bool, len(spec.Scan.Directions))
		rules := make([]ScanDirectionSpec, 0, len(spec.Scan.Directions))
		for _, rule := range spec.Scan.Directions {
			dir := strings.ToUpper(strings.TrimSpace(rule.Dir))
			prefix := strings.TrimSpace(rule.Prefix)
			switch dir {
			case "N", "E", "S", "W", "U", "D":
			default:
				return fail("scan direction %q is not one of N/E/S/W/U/D", rule.Dir)
			}
			if seenDir[dir] {
				return fail("duplicate scan direction %q", dir)
			}
			if prefix == "" {
				return fail("scan prefix for %s must not be empty", dir)
			}
			if seenPrefix[prefix] {
				return fail("duplicate scan prefix %q", prefix)
			}
			for other := range seenPrefix {
				if strings.HasPrefix(prefix, other) || strings.HasPrefix(other, prefix) {
					return fail("scan prefixes %q and %q overlap", other, prefix)
				}
			}
			seenDir[dir], seenPrefix[prefix] = true, true
			rules = append(rules, ScanDirectionSpec{Dir: dir, Prefix: prefix})
		}
		var scanPromptRE *regexp.Regexp
		if p.stopPrompt {
			scanPromptRE = p.promptRE
		}
		p.scan = &ScanConfig{Command: command, Directions: rules, MaxLines: spec.Block.MaxLines, StopBlank: p.stopBlank, promptRE: scanPromptRE}
	}

	// --- strip ---
	switch spec.Strip.Mode {
	case StripGreedy, StripMinRun:
		if spec.Strip.Glyphs == "" {
			// An empty glyph set would make the strip a silent no-op AND silently
			// degrade HeaderLastBeforeStripped to HeaderFirst (no line ever looks
			// column-prefixed).
			return fail("strip mode %q needs a non-empty glyph set", spec.Strip.Mode)
		}
		if spec.Strip.Mode == StripMinRun && spec.Strip.MinRun < 1 {
			return fail("strip min_run needs MinRun >= 1, got %d", spec.Strip.MinRun)
		}
	case StripFixedWidth:
		if spec.Strip.Width < 1 {
			// An omitted Width is the likeliest JSON/LLM mistake and would mean
			// "cut at column 0", i.e. no strip at all.
			return fail("strip fixed_width needs Width >= 1, got %d", spec.Strip.Width)
		}
	default:
		return fail("unknown strip mode %q", spec.Strip.Mode)
	}
	for _, r := range spec.Strip.Glyphs {
		if r < 128 {
			p.glyphASCII[r] = true
		} else {
			p.glyphWide = append(p.glyphWide, r)
		}
	}

	// --- header ---
	switch spec.Header.Rule {
	case HeaderLastBeforeStripped:
		if spec.Header.ColumnRun < 1 {
			return fail("header %q needs ColumnRun >= 1, got %d", spec.Header.Rule, spec.Header.ColumnRun)
		}
		// Strip.Glyphs has TWO consumers, and only one of them is the strip:
		// HasLeftColumn -> HeaderIndex uses the glyph set to find the first
		// column-prefixed line, for EVERY strip mode. StripFixedWidth ignores
		// Glyphs when cutting, so the strip switch above does not require it —
		// which used to leave this hole open: fixed_width + last_before_stripped +
		// no glyphs compiles, then no line ever looks column-prefixed, firstMap
		// stays -1, and the header silently becomes the FIRST line of the block —
		// i.e. the movement preamble ("Вы последовали за ... на север.") instead
		// of the room name. Every hint is then wrong, so every reconcile fails and
		// the player sits yellow/red forever, with no error anywhere. Exactly the
		// silent-degradation class the rest of this validation exists to prevent.
		if spec.Strip.Glyphs == "" {
			return fail("header %q needs a non-empty glyph set: it locates the room name by the first line carrying a left column, so with no glyphs no line ever does and the header silently becomes the first line of the block (typically a movement preamble, not the room name)", spec.Header.Rule)
		}
	case HeaderFirst:
	case HeaderANSIDelimited:
		if !validSGRParams(spec.Header.ANSIStart) {
			return fail("header %q needs ansi_start containing only SGR parameters such as %q", spec.Header.Rule, "1;36")
		}
		p.ansiHeaderPrefix = "\x1b[" + spec.Header.ANSIStart + "m"
	default:
		return fail("unknown header rule %q", spec.Header.Rule)
	}

	return p, nil
}

func validSGRParams(value string) bool {
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, ";") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// checkGuardMatchesPattern catches the silent-death case: a Guard that the
// Pattern can never match means LooksLikeExits rejects every line the regex
// would have accepted, so the detector never fires again and the map simply
// stops updating — with no error anywhere.
//
// Go's LiteralPrefix returns "" for any ^-anchored pattern (verified), and
// every realistic exits pattern is anchored, so the anchor is stripped before
// probing. When the pattern starts with an alternation or a character class the
// literal prefix is legitimately empty; that is unprovable rather than wrong, so
// it is allowed through.
func checkGuardMatchesPattern(guard, pattern string) error {
	probe, err := regexp.Compile(strings.TrimPrefix(pattern, "^"))
	if err != nil {
		return nil // the real pattern already compiled; nothing to add
	}
	lit, _ := probe.LiteralPrefix()
	if lit == "" {
		return nil // cannot determine a literal prefix — not provably wrong
	}
	if strings.HasPrefix(guard, lit) || strings.HasPrefix(lit, guard) {
		return nil
	}
	return fmt.Errorf("exits guard %q can never match the pattern, whose literal prefix is %q — the detector would never fire", guard, lit)
}

// MustCompile is Compile for profiles known good at build time (the builtins).
func MustCompile(spec ProfileSpec) Profile {
	p, err := Compile(spec)
	if err != nil {
		panic(err)
	}
	return p
}

// RMUDSpec is the pure-data form of the builtin RMUD profile. These are exactly
// the constants the detector used to hardcode; changing any of them changes
// every parsed description and therefore every mapimport.Fingerprint, which is
// the tracker's only re-localization path. Do not "tidy" them.
func RMUDSpec() ProfileSpec {
	return ProfileSpec{
		Name: "rmud",
		Exits: ExitsSpec{
			Guard:     "[ Exits:",
			Pattern:   `^\[ Exits:\s*(.*?)\s*\]$`,
			NoneToken: "none",
		},
		Block: BlockSpec{
			// StopBlank is load-bearing, not decoration: RMUD separates room blocks
			// from surrounding output with a blank line, and without this stop the
			// scan reaches back into whatever preceded the room.
			// The 3 fixture blocks are ~12 lines (header + up to ~11 column/desc
			// lines + exits); 32 is a safe cap that keeps memory bounded and the
			// reconstruction cheap.
			Stops:    []BlockStop{StopBlank, StopMaxLines},
			MaxLines: 32,
		},
		// Prompt deliberately empty: RMUD's boundary is the blank line. See
		// TortillaPromptPattern for the preset a profile can opt into.
		Prompt: "",
		Strip: StripSpec{
			Mode:   StripGreedy,
			Glyphs: RMUDGlyphs,
		},
		Header: HeaderSpec{
			Rule:      HeaderLastBeforeStripped,
			ColumnRun: 2,
		},
		Scan: &ScanSpec{
			Command: "scan",
			Directions: []ScanDirectionSpec{
				{Dir: "N", Prefix: "На севере:"},
				{Dir: "E", Prefix: "На востоке:"},
				{Dir: "S", Prefix: "На юге:"},
				{Dir: "W", Prefix: "На западе:"},
				{Dir: "U", Prefix: "Наверху:"},
				{Dir: "D", Prefix: "Внизу:"},
			},
		},
		// RMUD's own door verb. It lives HERE and nowhere else — see the field's
		// comment for why a global constant was the wrong home. The direction word is
		// an optional argument to `откр две` on RMUD, and mandatory when a room holds
		// two doors, so the template always passes it.
		DoorOpen: "откр две {dir}",
	}
}

// builtinRMUD is compiled once at init; BuiltinRMUD hands out copies.
var builtinRMUD = MustCompile(RMUDSpec())

// BuiltinRMUD returns the builtin RMUD profile — the one that reproduces the
// pre-parameterization detector byte for byte.
func BuiltinRMUD() Profile { return builtinRMUD }

// ---------------------------------------------------------------------------
// Rule application.
//
// Exactly ONE of these runs per incoming line: LooksLikeExits. Everything else
// runs once per completed room block, which on a walking player is a few times
// a second rather than a few thousand.
// ---------------------------------------------------------------------------

// LooksLikeExits is the cheap prefix guard the accumulator runs on EVERY
// incoming line before any regex work: only a line beginning with the profile's
// guard is a candidate for the block-completing regex. One literal prefix
// compare, no regex, no allocation.
//
// The argument must already be space-trimmed — Accumulator.Feed trims once and
// passes the result to both this and ParseExits.
func (p *Profile) LooksLikeExits(trimmed string) bool {
	for _, rule := range p.exitsRules {
		if strings.HasPrefix(trimmed, rule.guard) {
			return true
		}
	}
	return false
}

// ParseExits normalizes a raw exits line (already ANSI/CR-stripped) into the
// inner token string, or returns ok=false if the line is not an exits line.
// Returns the profile's NoneToken for a dead-end, else the captured tokens.
func (p *Profile) ParseExits(stripped string) (inner string, ok bool) {
	if len(p.exitsRules) == 0 {
		// Zero Profile (an ignored Compile error). Recognize nothing rather than
		// panicking inside the session's line pipeline.
		return "", false
	}
	line := strings.TrimSpace(stripped)
	var m []string
	for _, rule := range p.exitsRules {
		if strings.HasPrefix(line, rule.guard) {
			m = rule.re.FindStringSubmatch(line)
			if m != nil {
				break
			}
		}
	}
	if m == nil {
		return "", false
	}
	inner = strings.TrimSpace(m[1])
	if inner == "" || strings.EqualFold(inner, p.spec.Exits.NoneToken) {
		return p.spec.Exits.NoneToken, true
	}
	return inner, true
}

// isGlyph reports whether r is in the profile's left-column glyph set.
func (p *Profile) isGlyph(r rune) bool {
	if r < 128 {
		return p.glyphASCII[r]
	}
	for _, g := range p.glyphWide {
		if g == r {
			return true
		}
	}
	return false
}

// glyphRun returns the byte end and rune length of the leading glyph run.
func (p *Profile) glyphRun(line string) (end, runes int) {
	end = len(line)
	for i, r := range line {
		if !p.isGlyph(r) {
			return i, runes
		}
		runes++
	}
	return end, runes
}

// HasLeftColumn reports whether a line begins with a left-column run of at
// least Header.ColumnRun glyphs. Used only by HeaderLastBeforeStripped to
// locate the block header.
func (p *Profile) HasLeftColumn(line string) bool {
	n := 0
	for _, r := range line {
		if !p.isGlyph(r) {
			return false
		}
		n++
		if n >= p.spec.Header.ColumnRun {
			return true
		}
	}
	return false
}

// StripColumn removes the left column from a body line per the profile's strip
// mode, returning the right-hand prose (best-effort). A line that is entirely
// column collapses to "".
func (p *Profile) StripColumn(line string) string {
	switch p.spec.Strip.Mode {
	case StripMinRun:
		end, runes := p.glyphRun(line)
		if runes < p.spec.Strip.MinRun {
			// Run too short to be a real column — keep the whole line.
			return strings.TrimSpace(line)
		}
		return strings.TrimSpace(line[end:])
	case StripFixedWidth:
		// Walk rune starts until column Width; a line of Width runes or fewer has
		// no prose.
		n := 0
		for i := range line {
			if n == p.spec.Strip.Width {
				return strings.TrimSpace(line[i:])
			}
			n++
		}
		return ""
	default: // StripGreedy
		end, _ := p.glyphRun(line)
		return strings.TrimSpace(line[end:])
	}
}

// isBlockStop reports whether the backward block scan must stop at this line.
// Called once per candidate line during reconstruction only — never per
// incoming line — so the prompt regex stays off the hot path.
//
// StopMaxLines is absent by design: MaxLines is enforced by the ring, so a
// block can never exceed it. See StopMaxLines.
func (p *Profile) isBlockStop(line string) bool {
	if p.stopBlank && strings.TrimSpace(line) == "" {
		return true
	}
	if p.stopPrompt && p.promptRE != nil && p.promptRE.MatchString(line) {
		return true
	}
	return false
}

// selectHeader returns the room name and the first body-line index. ANSI rules
// can return a substring rather than the whole line; found=false means the
// profile must emit an exits-only event instead of guessing a room name.
func (p *Profile) selectHeader(block []bufferedLine) (name string, bodyStart int, found bool) {
	switch p.spec.Header.Rule {
	case HeaderFirst:
		for i, l := range block {
			if name := strings.TrimSpace(l.stripped); name != "" {
				return name, i + 1, true
			}
		}
		return "", 0, false
	case HeaderANSIDelimited:
		for i, l := range block {
			start := strings.Index(l.raw, p.ansiHeaderPrefix)
			if start < 0 {
				continue
			}
			remaining := l.raw[start+len(p.ansiHeaderPrefix):]
			end := strings.IndexByte(remaining, '\x1b')
			if end < 0 {
				continue
			}
			if name := strings.TrimSpace(remaining[:end]); name != "" {
				return name, i + 1, true
			}
		}
		return "", 0, false
	default: // HeaderLastBeforeStripped
		// The block is [preamble?] header [column+desc lines]. The header is the
		// last non-column line immediately preceding the first column-prefixed
		// line, so a following/teleport preamble above it (e.g. "Вы последовали за
		// ...") is excluded. If there is no column line at all, the first line is
		// the header (brief/dark room with no map body).
		//
		// firstMap == 0 (the block STARTS with a column line) must fall through to
		// 0, not 0-1: the `> 0` here is load-bearing against an index panic in the
		// session read loop. TestHeaderBlockStartsWithColumnLine covers it.
		firstMap := -1
		for i, l := range block {
			if p.HasLeftColumn(l.stripped) {
				firstMap = i
				break
			}
		}
		if firstMap > 0 {
			idx := firstMap - 1
			return strings.TrimSpace(block[idx].stripped), idx + 1, true
		}
		return strings.TrimSpace(block[0].stripped), 1, true
	}
}
