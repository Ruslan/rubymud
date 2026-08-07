package mapper_test

import (
	"fmt"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/mapper/conformance"
)

// This file is `package mapper_test` rather than `package mapper` because it
// imports the conformance harness, which imports mapper. An internal test file
// would be an import cycle.

// TestBuiltinDetectorConformance runs the shared suite over the detectors the
// mapper package itself registers (rmud, diku-en). The contributed ones are
// covered by the same suite from the detectors package.
func TestBuiltinDetectorConformance(t *testing.T) { conformance.RunAll(t) }

func BenchmarkBuiltinDetectors(b *testing.B) { conformance.BenchAll(b) }

// TestDefaultDetectorIsRMUD pins the default. A session with a NULL
// mapper_format resolves through DefaultDetectorKey, so changing this silently
// re-parses every existing installation with different rules.
func TestDefaultDetectorIsRMUD(t *testing.T) {
	if mapper.DefaultDetectorKey != "rmud" {
		t.Fatalf("DefaultDetectorKey = %q, want \"rmud\"", mapper.DefaultDetectorKey)
	}
	det, ok := mapper.NewDetector(mapper.DefaultDetectorKey)
	if !ok {
		t.Fatal("the default detector is not registered")
	}
	info := det.Describe()
	if info.Provenance != mapper.ProvenanceVerified {
		t.Errorf("rmud provenance = %q, want Verified", info.Provenance)
	}
	if !info.Editable {
		t.Error("a declarative builtin must be Editable so the UI can offer a copy")
	}
}

// TestDefaultDetectorParsesIdenticallyToBuiltinRMUD is the bridge between the
// new interface and the byte-identity goldens: the goldens pin
// Accumulator+BuiltinRMUD, and this pins that the registry's default is that
// exact thing rather than a lookalike.
func TestDefaultDetectorParsesIdenticallyToBuiltinRMUD(t *testing.T) {
	lines := []string{"", "Кают-компания", ".[ ]-[ ]....   Вы стоите в большой каюте.", "[ Exits: (N) E S ]"}

	acc := mapper.NewAccumulator(mapper.BuiltinRMUD())
	var viaAcc mapper.RoomEvent
	for _, l := range lines {
		if ev, ok := acc.Feed(l); ok {
			viaAcc = ev
		}
	}

	det, _ := mapper.NewDetector(mapper.DefaultDetectorKey)
	var viaDet mapper.RoomEvent
	for _, l := range lines {
		if ev, ok := det.Feed(mapper.StrippedLine(l)); ok {
			viaDet = ev
		}
	}

	if viaAcc != viaDet {
		t.Errorf("the registered default drifted from BuiltinRMUD:\n accumulator %+v\n detector    %+v", viaAcc, viaDet)
	}
	if viaDet.Hint == "" {
		t.Fatal("no event produced — the fixture is wrong, not the code")
	}
}

// TestSpecDetectorExposesItsSpec covers the «make a copy» path: a declarative
// builtin must hand out the spec behind it, and it must be the same spec the
// package constant defines.
func TestSpecDetectorExposesItsSpec(t *testing.T) {
	det, _ := mapper.NewDetector("rmud")
	sp, ok := det.(mapper.SpecProvider)
	if !ok {
		t.Fatal("the rmud detector does not implement SpecProvider — the UI could not offer a copy")
	}
	if got := sp.Spec(); got.Exits.Pattern != mapper.RMUDSpec().Exits.Pattern {
		t.Errorf("spec drift: got %q, want %q", got.Exits.Pattern, mapper.RMUDSpec().Exits.Pattern)
	}
}

// TestRegisterDuplicateKeyPanics — a duplicate key must be a loud programming
// error, not a silent shadow. Keys live in sessions.mapper_format; shadowing one
// would re-parse somebody's sessions with another author's rules.
func TestRegisterDuplicateKeyPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registering a duplicate key did not panic")
		}
		if !strings.Contains(toString(r), "duplicate room detector key") {
			t.Errorf("panic message does not name the problem: %v", r)
		}
	}()
	mapper.RegisterDetector(func() mapper.RoomDetector {
		return mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.RMUDDetectorInfo())
	})
}

// TestRegisterRejectsUnknownProvenance stops a contribution inventing a
// reassuring label ("Official", "Stable") that the UI would print verbatim.
func TestRegisterRejectsUnknownProvenance(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("registering an unknown provenance did not panic")
		} else if !strings.Contains(toString(r), "unknown provenance") {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	mapper.RegisterDetector(func() mapper.RoomDetector {
		info := mapper.RMUDDetectorInfo()
		info.Key = "bogus-provenance-probe"
		info.Provenance = "Official"
		return mapper.NewSpecDetector(mapper.BuiltinRMUD(), info)
	})
}

// TestNewDetectorUnknownKeyIsNotAnError — an older binary meeting a reference to
// a detector a newer build shipped must degrade, not explode.
func TestNewDetectorUnknownKeyIsNotAnError(t *testing.T) {
	if d, ok := mapper.NewDetector("no-such-detector"); ok || d != nil {
		t.Fatalf("NewDetector on an unknown key returned (%v, %v), want (nil, false)", d, ok)
	}
}

// TestNewDetectorReturnsIndependentInstances — one detector per session.
func TestNewDetectorReturnsIndependentInstances(t *testing.T) {
	a, _ := mapper.NewDetector("rmud")
	b, _ := mapper.NewDetector("rmud")
	if a == b {
		t.Fatal("NewDetector handed out the same instance twice — two sessions would share a ring buffer")
	}
	// Half a block into a, a whole block into b.
	a.Feed(mapper.StrippedLine("Первая"))
	b.Feed(mapper.StrippedLine("Вторая"))
	b.Feed(mapper.StrippedLine(".[ ].....      two"))
	ev, ok := b.Feed(mapper.StrippedLine("[ Exits: N ]"))
	if !ok || ev.Hint != "Вторая" {
		t.Fatalf("b's block was polluted by a: %+v (ok=%v)", ev, ok)
	}
}

func TestParseFormatRef(t *testing.T) {
	cases := []struct {
		in   string
		kind mapper.RefKind
		key  string
		id   int64
		ok   bool
	}{
		{"", mapper.RefDefault, "rmud", 0, true},
		{"   ", mapper.RefDefault, "rmud", 0, true},
		{"builtin:rmud", mapper.RefBuiltin, "rmud", 0, true},
		{"builtin:diku-en", mapper.RefBuiltin, "diku-en", 0, true},
		{"builtin:_example", mapper.RefBuiltin, "_example", 0, true},
		{"db:7", mapper.RefDB, "", 7, true},
		{" db:7 ", mapper.RefDB, "", 7, true},
		{"db:0", mapper.RefDefault, "", 0, false},
		{"db:-1", mapper.RefDefault, "", 0, false},
		{"db:abc", mapper.RefDefault, "", 0, false},
		{"builtin:", mapper.RefDefault, "", 0, false},
		{"7", mapper.RefDefault, "", 0, false},
		{"rmud", mapper.RefDefault, "", 0, false},
	}
	for _, c := range cases {
		kind, key, id, ok := mapper.ParseFormatRef(c.in)
		if kind != c.kind || key != c.key || id != c.id || ok != c.ok {
			t.Errorf("ParseFormatRef(%q) = (%v,%q,%d,%v), want (%v,%q,%d,%v)",
				c.in, kind, key, id, ok, c.kind, c.key, c.id, c.ok)
		}
	}
	if got := mapper.BuiltinRef("rmud"); got != "builtin:rmud" {
		t.Errorf("BuiltinRef = %q", got)
	}
	if got := mapper.FormatRefForDB(7); got != "db:7" {
		t.Errorf("FormatRefForDB = %q", got)
	}
}

// TestLineCarriesRawSeparately pins the detector input
// carries BOTH forms, and they can differ. An implementation that collapsed them
// would silently disable every colour-based contributed detector.
func TestLineCarriesRawSeparately(t *testing.T) {
	l := mapper.Line{Stripped: "The Glass Bridge", Raw: "\x1b[1;36mThe Glass Bridge\x1b[0m"}
	if l.Stripped == l.Raw {
		t.Fatal("Line collapsed Raw into Stripped")
	}
	if s := mapper.StrippedLine("plain"); s.Raw != "plain" || s.Stripped != "plain" {
		t.Errorf("StrippedLine = %+v", s)
	}
}

// toString renders a recovered panic value for message matching.
func toString(v any) string { return fmt.Sprint(v) }

// BenchmarkFeedNonExitsLineViaDetector is the A/B partner of the mapper
// package's BenchmarkFeedNonExitsLine: the SAME line, the same profile, the same
// steady state — the only difference is that the call goes through the
// RoomDetector interface (SpecDetector -> Accumulator) instead of straight into
// the accumulator. The per-line interface cost is the delta between these two
// numbers, so keep them measuring the same thing.
func BenchmarkFeedNonExitsLineViaDetector(b *testing.B) {
	det, _ := mapper.NewDetector(mapper.DefaultDetectorKey)
	line := mapper.StrippedLine(".[ ]-[ ]......... круглый стол на двенадцать персон приколочен к полу")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		det.Feed(line)
	}
}
