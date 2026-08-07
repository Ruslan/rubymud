package detectors

import (
	"strings"

	"rubymud/go/internal/mapper"
)

// ---------------------------------------------------------------------------
// `_example` — the reference contributed detector.
//
// It exists to prove the whole contribution path end to end (interface →
// registry → reference scheme → conformance suite → panic boundary) with real,
// compiled, tested code rather than a paragraph claiming it works. It targets no
// real MUD: we have no fixtures for one, and shipping a "Contributed" detector
// nobody has run would be exactly the dishonesty the Provenance vocabulary
// exists to prevent.
//
// This example predates the declarative ansi_delimited header rule and is now
// intentionally expressible as a ProfileSpec. It remains compiled because its
// purpose is to keep the Go contribution surface, registry, conformance checks
// and panic boundary exercised. A real contribution should use Go only when its
// state machine cannot be represented by the editable rules.
//
// The KEY starts with an underscore on purpose: it sorts first and reads as
// non-production. It is NOT hidden from the settings selector — it appears there
// like any other builtin, labelled "Contributed", with a description that says
// plainly it matches no real server. Hiding it would mean a second, untested code
// path in the UI; saying so is cheaper and more honest.
// ---------------------------------------------------------------------------

func init() { mapper.RegisterDetector(newExampleDetector) }

// ExampleDetectorKey is the registry key / `builtin:_example` reference.
const ExampleDetectorKey = "_example"

// nameSGR is the bright-cyan SGR sequence this imaginary server wraps the room
// name in. A real contribution would key off whatever its server actually emits.
const nameSGR = "\x1b[1;36m"

// exitsPrefix is the literal that closes a block. It is a LITERAL, checked with
// HasPrefix, precisely because Feed is the hot path — a regex here would run on
// every line the MUD sends.
const exitsPrefix = "Exits:"

// maxDescLines bounds the description buffer. Bounded is not an optimization: an
// unbounded buffer is a memory leak on any output the detector never closes a
// block on (a spammy MUD, a stuck fight, a detector bug).
const maxDescLines = 24

type exampleDetector struct {
	// haveName/name: the last bright-cyan line seen, which is this format's room
	// name. Cleared on emit and on Reset.
	haveName bool
	name     string
	// desc is preallocated to maxDescLines and only ever re-sliced, never grown,
	// so appending a body line does not allocate. This is the pattern a
	// contribution should copy: the conformance suite fails the detector otherwise.
	desc []string
}

func newExampleDetector() mapper.RoomDetector {
	return &exampleDetector{desc: make([]string, 0, maxDescLines)}
}

// Feed implements mapper.RoomDetector.
//
// HOT PATH. Per non-completing line the entire cost is two HasPrefix compares
// and (for a body line) one append into preallocated capacity. No regex, no
// allocation, no string building.
func (d *exampleDetector) Feed(l mapper.Line) (mapper.RoomEvent, bool) {
	// 1. The block-completing line. Cheapest discriminator first.
	if strings.HasPrefix(l.Stripped, exitsPrefix) {
		exits := strings.TrimSpace(strings.TrimPrefix(l.Stripped, exitsPrefix))
		if exits == "" {
			exits = "none"
		}
		if !d.haveName {
			// Exits with no preceding name: emit the exits-only event, matching what the
			// declarative accumulator does for a pipe corridor. The tracker reads an
			// empty hint as pipe-safe, so this must NOT be silently dropped.
			d.reset()
			return mapper.RoomEvent{Exits: exits}, true
		}
		// strings.Join allocates — which is fine HERE and only here: this runs once
		// per completed room block (a few times a second on a walking player), never
		// per line.
		ev := mapper.RoomEvent{Hint: d.name, Desc: strings.Join(d.desc, " "), Exits: exits}
		d.reset()
		return ev, true
	}

	// 2. The room name, recognizable ONLY in the raw form. This is the line that
	// cannot be expressed declaratively.
	if strings.HasPrefix(l.Raw, nameSGR) {
		d.haveName = true
		d.name = strings.TrimSpace(l.Stripped)
		d.desc = d.desc[:0]
		return mapper.RoomEvent{}, false
	}

	// 3. A body line, but only while a name is in flight — otherwise unrelated
	// chatter between rooms would become the next room's description.
	if d.haveName {
		if s := strings.TrimSpace(l.Stripped); s != "" && len(d.desc) < maxDescLines {
			d.desc = append(d.desc, s)
		}
	}
	return mapper.RoomEvent{}, false
}

// Reset implements mapper.RoomDetector.
func (d *exampleDetector) Reset() { d.reset() }

func (d *exampleDetector) reset() {
	d.haveName = false
	d.name = ""
	d.desc = d.desc[:0]
}

// Describe implements mapper.RoomDetector. Editable is false and must be: there
// is no ProfileSpec behind a Go detector, so the UI cannot offer to copy it into
// an editable format — it has to say so instead of producing an empty one.
func (d *exampleDetector) Describe() mapper.DetectorInfo {
	return mapper.DetectorInfo{
		Key:        ExampleDetectorKey,
		Title:      "Example — do not select",
		MUD:        "none — a worked example, not a real server",
		Provenance: mapper.ProvenanceContributed,
		Editable:   false,
		Description: "A worked example of a contributed Go detector, kept compiled and " +
			"tested so the contribution path cannot rot. It recognizes an imaginary " +
			"server whose room name is marked by bright-cyan colour, followed by " +
			"description lines and an `Exits: ...` line. " +
			"That shape can now also be expressed by an editable ansi_delimited format; " +
			"this detector remains only as the tested contribution example. " +
			"DO NOT SELECT IT FOR A REAL SESSION. It is not merely useless: its block " +
			"terminator is the bare literal `Exits:`, which many English Diku servers " +
			"really do print, so on such a server it emits rooms with an EMPTY name. " +
			"The tracker reads an empty hint as a featureless corridor, so your position " +
			"sticks at yellow and never recovers — and because the format loaded fine, " +
			"nothing anywhere reports an error. Copy example.go and write your own.",
	}
}

// ConformanceCases implements mapper.Conformer. These are the shapes the shared
// suite runs; a contribution's cases are its correctness claim.
func (d *exampleDetector) ConformanceCases() []mapper.ConformanceCase {
	name := func(s string) mapper.Line { return mapper.Line{Stripped: s, Raw: nameSGR + s + "\x1b[0m"} }
	plain := func(s string) mapper.Line { return mapper.Line{Stripped: s, Raw: s} }

	return []mapper.ConformanceCase{
		{
			Name: "colour_marked_name_then_body_then_exits",
			Feed: []mapper.Line{
				plain("You walk north."),
				name("The Glass Bridge"),
				plain("A narrow span of green glass crosses the chasm."),
				plain("Wind howls beneath it."),
				plain("Exits: north south"),
			},
			Want: []mapper.RoomEvent{{
				Hint:  "The Glass Bridge",
				Desc:  "A narrow span of green glass crosses the chasm. Wind howls beneath it.",
				Exits: "north south",
			}},
		},
		{
			// The identical text WITHOUT the colour must not be read as a room name.
			// This is the case that proves the detector really uses Raw, and would fail
			// for any implementation that quietly fell back to Stripped.
			Name: "same_text_without_colour_is_not_a_name",
			Feed: []mapper.Line{
				plain("The Glass Bridge"),
				plain("A narrow span of green glass crosses the chasm."),
				plain("Exits: north south"),
			},
			Want: []mapper.RoomEvent{{Hint: "", Desc: "", Exits: "north south"}},
		},
		{
			Name: "empty_exits_reports_none",
			Feed: []mapper.Line{
				name("A Sealed Cell"),
				plain("Smooth walls, no door."),
				plain("Exits:"),
			},
			Want: []mapper.RoomEvent{{Hint: "A Sealed Cell", Desc: "Smooth walls, no door.", Exits: "none"}},
		},
		{
			Name: "two_blocks_in_a_row_do_not_bleed_into_each_other",
			Feed: []mapper.Line{
				name("First Room"),
				plain("One."),
				plain("Exits: north"),
				name("Second Room"),
				plain("Two."),
				plain("Exits: south"),
			},
			Want: []mapper.RoomEvent{
				{Hint: "First Room", Desc: "One.", Exits: "north"},
				{Hint: "Second Room", Desc: "Two.", Exits: "south"},
			},
		},
		{
			Name: "ordinary_output_emits_nothing",
			Feed: []mapper.Line{
				plain("A guard says 'move along'."),
				plain("You are hungry."),
				plain("The exits are that way."), // not the "Exits:" prefix
			},
		},
	}
}

// FillerLine implements mapper.Conformer: a realistic body line that must never
// complete a block, used for the zero-allocation check and the benchmark.
func (d *exampleDetector) FillerLine() mapper.Line {
	const s = "A narrow span of green glass crosses the chasm."
	return mapper.Line{Stripped: s, Raw: s}
}
