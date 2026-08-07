package detectors_test

import (
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/mapper/conformance"

	// Blank import: this package's detectors register from init(). Without it the
	// registry here would hold only the builtins that `mapper` registers itself,
	// and the suite would silently stop covering contributions.
	_ "rubymud/go/internal/mapper/detectors"
)

// TestConformance is the ENTIRE test a contribution has to write. The shared
// suite enumerates the registry, so adding `mymud.go` with an init() that calls
// mapper.RegisterDetector makes this run its fixtures, its Reset behaviour and
// its zero-allocation check with no edit here.
func TestConformance(t *testing.T) { conformance.RunAll(t) }

// BenchmarkConformance likewise: a per-line number for every registered
// detector, without anyone writing a benchmark.
func BenchmarkConformance(b *testing.B) { conformance.BenchAll(b) }

// TestExampleDetectorIsRegistered pins the contribution path itself: if the
// blank import above or the init() in example.go stops working, the suite above
// would still pass (on the builtins alone) while covering nothing contributed.
func TestExampleDetectorIsRegistered(t *testing.T) {
	info, ok := mapper.LookupDetectorInfo("_example")
	if !ok {
		t.Fatal("the example contributed detector is not registered — compile-time registration is broken")
	}
	if info.Provenance != mapper.ProvenanceContributed {
		t.Errorf("provenance = %q, want %q", info.Provenance, mapper.ProvenanceContributed)
	}
	if info.Editable {
		t.Error("a Go detector must report Editable=false: there is no spec to copy into an editable format")
	}
}
