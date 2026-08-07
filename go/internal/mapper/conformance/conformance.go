// Package conformance is the shared, table-driven test harness that EVERY
// registered room detector must pass.
//
// The point is that it enumerates the registry rather than carrying a table. A
// contributed detector is picked up by calling RegisterDetector — there is no
// suite to edit, no list to append to, and therefore no way to land a detector
// that quietly skips the checks. Adding `detectors/mymud.go` with an init() is
// enough for RunAll in that package's conformance_test.go to start running it.
//
// It lives in its own package, not in `mapper`, for one reason: it imports
// `testing`, and `mapper` is linked into the production binary. Only _test.go
// files import this package, so `testing` never reaches mudhost.
//
// WHAT IT PROVES, and what it deliberately does not:
//
//	proves — the detector describes itself honestly enough to render, each
//	         factory call yields an INDEPENDENT instance, the author's own
//	         fixtures produce exactly the events claimed, Reset really drops
//	         in-flight state, and a line that does not complete a block allocates
//	         nothing.
//	does not prove — that the detector is correct on the target MUD. Nothing can:
//	         "Contributed" provenance means exactly "correct on the fixtures its
//	         author supplied". That is why fixtures are mandatory and why a
//	         detector without them fails rather than being skipped.
package conformance

import (
	"fmt"
	"reflect"
	"testing"

	"rubymud/go/internal/mapper"
)

// allocPrime is how many filler lines are pushed before the allocation check, so
// the detector's internal buffers have reached steady state and `append` is not
// still growing a ring. It is comfortably above any sane Block.MaxLines.
const allocPrime = 64

// RunAll runs the suite over EVERY detector in the registry. This is the whole
// public surface a contribution needs:
//
//	func TestConformance(t *testing.T) { conformance.RunAll(t) }
//
// It fails when the registry is empty: an empty registry means the package under
// test did not link any detector, which would turn this into a test that passes
// by doing nothing.
func RunAll(t *testing.T) {
	t.Helper()
	infos := mapper.DetectorInfos()
	if len(infos) == 0 {
		t.Fatal("conformance: no detectors registered — the suite would pass vacuously")
	}
	for _, info := range infos {
		info := info
		t.Run(info.Key, func(t *testing.T) { Run(t, info.Key) })
	}
}

// Run runs the suite over ONE registered detector, by key.
func Run(t *testing.T, key string) {
	t.Helper()

	newDet := func() mapper.RoomDetector {
		t.Helper()
		d, ok := mapper.NewDetector(key)
		if !ok {
			t.Fatalf("conformance: detector %q is not registered", key)
		}
		if d == nil {
			t.Fatalf("conformance: detector %q factory returned nil", key)
		}
		return d
	}

	det := newDet()
	info := det.Describe()

	// --- self-description ----------------------------------------------------
	// Everything checked here is rendered in the settings UI. A detector with no
	// description is a detector nobody can choose responsibly.
	t.Run("describe", func(t *testing.T) {
		if info.Key != key {
			t.Errorf("Describe().Key = %q, want %q — the key is stored in sessions.mapper_format and must be stable", info.Key, key)
		}
		if info.Title == "" {
			t.Error("Describe().Title is empty — it is the UI label")
		}
		if info.MUD == "" {
			t.Error("Describe().MUD is empty — say which server or family this targets")
		}
		if !info.Provenance.Valid() {
			t.Errorf("Describe().Provenance = %q is not one of Verified/Starter/Contributed/User", info.Provenance)
		}
		if len(info.Description) < 40 {
			t.Errorf("Describe().Description is %d chars — say what this recognizes; it is shown verbatim to the person choosing it", len(info.Description))
		}
		// Describe must be stable: the REST layer caches it at registration time.
		if again := newDet().Describe(); !reflect.DeepEqual(again, info) {
			t.Errorf("Describe() is not stable across instances:\n first %+v\n again %+v", info, again)
		}

		// EDITABLE MUST NOT LIE. The settings UI reads Editable to decide whether to
		// offer "Make a copy", and the copy is built from SpecProvider.Spec(). A Go
		// detector that declares Editable:true therefore gets a copy button that hands
		// the user a BLANK form presented as a copy of a working format — they save
		// it, assign it, and their mapper silently stops recognizing rooms. The two
		// must agree by construction.
		_, hasSpec := det.(mapper.SpecProvider)
		if hasSpec != info.Editable {
			t.Errorf(`Describe().Editable = %v but SpecProvider implemented = %v — these must agree.

Editable=true promises the UI there is a spec behind this detector that can be
copied into an editable format. Without SpecProvider the "Make a copy" button
produces an empty format that silently parses nothing.`, info.Editable, hasSpec)
		}

		// Tell the contributor which OPTIONAL behaviour they did and did not opt into.
		// BoundaryAware in particular is silently lost otherwise: without it a format
		// swap mid-room emits a headerless block, which the tracker reads as a pipe
		// corridor and which demotes a confirmed player to yellow.
		_, boundaryAware := det.(mapper.BoundaryAware)
		t.Logf("optional interfaces for %q: SpecProvider=%v BoundaryAware=%v", key, hasSpec, boundaryAware)
	})

	// --- fixtures are mandatory --------------------------------------------
	conf, ok := det.(mapper.Conformer)
	if !ok {
		t.Fatalf("conformance: detector %q does not implement mapper.Conformer — every registered detector must ship its own fixtures, because its fixtures ARE its correctness claim", key)
	}
	cases := conf.ConformanceCases()
	if len(cases) == 0 {
		t.Fatalf("conformance: detector %q supplies no conformance cases", key)
	}
	// NON-VACUITY. A detector whose Feed always returns false, shipping a single
	// case with a nil Feed and a nil Want, used to pass every check green: the
	// fixture matched (nothing in, nothing out) and the two behavioural checks
	// below t.Skip'd themselves because no case emitted. That is a detector that
	// does nothing, certified as conformant. At least one case must prove the
	// detector actually produces a room event.
	if firstEmittingCase(cases) == nil {
		t.Fatalf(`conformance: detector %q has no case that emits a RoomEvent.

Fixtures are a detector's correctness claim, so at least one must feed a real
block and assert the event it produces. Cases that expect NO event are valuable
too (they catch firing on unrelated output) but cannot be the only kind.`, key)
	}
	filler := conf.FillerLine()
	if filler.Stripped == "" {
		t.Fatalf("conformance: detector %q supplies an empty FillerLine — it must be a realistic body line that does NOT complete a block", key)
	}

	// --- instance independence ------------------------------------------------
	// One detector per session. If the factory handed out a shared instance (or
	// the type kept its state in a package-level var), one session's half-finished
	// block would leak into another's. Feed a partial run into A, then run a full
	// case through a fresh B and require B's result to be unaffected.
	t.Run("instances_are_independent", func(t *testing.T) {
		c := firstEmittingCase(cases)
		if c == nil {
			// Unreachable: the non-vacuity check above already failed. A t.Skip here is
			// what let a do-nothing detector pass.
			t.Fatal("no case emits an event; independence is only observable on one that does")
		}
		a := newDet()
		for _, l := range c.Feed[:len(c.Feed)-1] {
			a.Feed(l)
		}
		b := newDet()
		got := feedAll(b, c.Feed)
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("a fresh instance was affected by another instance's in-flight state:\n got %+v\nwant %+v", got, c.Want)
		}
	})

	// --- the author's fixtures -----------------------------------------------
	for _, c := range cases {
		c := c
		t.Run("case/"+c.Name, func(t *testing.T) {
			got := feedAll(newDet(), c.Feed)
			if !reflect.DeepEqual(got, c.Want) {
				t.Errorf("events differ:\n got %s\nwant %s", formatEvents(got), formatEvents(c.Want))
			}
		})
	}

	// --- Reset drops in-flight state -----------------------------------------
	// After Reset a detector must behave as a freshly constructed one. The session
	// relies on this on reconnect: without it, lines from before the link dropped
	// join lines from after it into a block that never existed.
	t.Run("reset_returns_to_fresh", func(t *testing.T) {
		c := firstEmittingCase(cases)
		if c == nil {
			t.Fatal("no case emits an event")
		}
		d := newDet()
		for _, l := range c.Feed[:len(c.Feed)-1] {
			d.Feed(l)
		}
		d.Reset()
		got := feedAll(d, c.Feed)
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("after Reset the detector did not behave as fresh:\n got %s\nwant %s", formatEvents(got), formatEvents(c.Want))
		}
	})

	// --- hot path: no allocation on a non-completing line --------------------
	// AGENTS.md #3. Feed runs for EVERY line the MUD sends; a single allocation
	// per line is a per-session garbage stream on a busy MUD. This is the check
	// that keeps a contribution from doing string concatenation or building a
	// slice on the common path.
	t.Run("filler_line_does_not_allocate", func(t *testing.T) {
		d := newDet()
		if _, emitted := d.Feed(filler); emitted {
			t.Fatalf("FillerLine %q completed a block — it must be a line that does NOT", filler.Stripped)
		}
		if raceDetectorEnabled {
			// The correctness half above still ran. The measurement half cannot: the
			// race detector's allocation accounting reports allocations that a normal
			// build does not make (regexp's pooled machine is the case that bites
			// here). Skipping loudly beats asserting something that is not true of the
			// shipped binary — the ordinary `go test ./...` pass enforces this.
			t.Skip("allocation accounting is unreliable under -race; enforced by the non-race test run")
		}
		for i := 0; i < allocPrime; i++ {
			d.Feed(filler)
		}
		got := testing.AllocsPerRun(200, func() { d.Feed(filler) })
		if got != 0 {
			t.Errorf("Feed allocated %v times per non-completing line, want 0 (AGENTS.md #3: this runs on every incoming line)", got)
		}
	})
}

// BenchAll benchmarks every registered detector's hot path (one non-completing
// line). A contribution gets a number without writing a benchmark:
//
//	func BenchmarkConformance(b *testing.B) { conformance.BenchAll(b) }
func BenchAll(b *testing.B) {
	for _, info := range mapper.DetectorInfos() {
		info := info
		b.Run(info.Key, func(b *testing.B) {
			d, ok := mapper.NewDetector(info.Key)
			if !ok {
				b.Fatalf("detector %q is not registered", info.Key)
			}
			conf, ok := d.(mapper.Conformer)
			if !ok {
				b.Skipf("detector %q supplies no fixtures", info.Key)
			}
			line := conf.FillerLine()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.Feed(line)
			}
		})
	}
}

// feedAll runs a line sequence through a detector and returns the events it
// emitted, in order. A nil result (no events) compares equal to a nil Want,
// which is how a "must emit nothing" case is written.
func feedAll(d mapper.RoomDetector, lines []mapper.Line) []mapper.RoomEvent {
	var out []mapper.RoomEvent
	for _, l := range lines {
		if ev, ok := d.Feed(l); ok {
			out = append(out, ev)
		}
	}
	return out
}

func firstEmittingCase(cases []mapper.ConformanceCase) *mapper.ConformanceCase {
	for i := range cases {
		if len(cases[i].Want) > 0 && len(cases[i].Feed) > 0 {
			return &cases[i]
		}
	}
	return nil
}

func formatEvents(evs []mapper.RoomEvent) string {
	if len(evs) == 0 {
		return "(no events)"
	}
	s := ""
	for i, e := range evs {
		s += fmt.Sprintf("\n  [%d] hint=%q desc=%q exits=%q", i, e.Hint, e.Desc, e.Exits)
	}
	return s
}
