package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/mapper/conformance"
)

// ---------------------------------------------------------------------------
// Closing the "registered but never checked" hole.
//
// The initial registry check for contributed detectors used two paths, and BOTH scanned a
// DIRECTORY rather than the registry:
//
//   - detectors/imports_test.go walked os.Getwd(), so it only ever saw
//     internal/mapper/detectors.
//   - conformance.RunAll enumerated the registry, but only within a test binary
//     that happened to link the detector.
//
// Nothing required a registered detector to live in the scanned tree. A detector
// dropped in internal/mapper/evilmud/ — importing net/http, os/exec and storage,
// spawning a goroutine that shells out — was registered, linked, reachable by
// `builtin:<key>`, and invisible to both checks, with `go build` clean.
//
// THIS test is in package main on purpose: main is what links the production
// binary, so the blank-import list in main.go is the thing under test. It
// enumerates the REGISTRY (not a directory) and asserts every detector that can
// actually be selected in production comes from a package the other checks
// cover. That makes the chain
//
//	registered  =>  lives in a sanctioned package
//	            =>  scanned by detectors/imports_test.go
//	            =>  exercised by the conformance suite
//
// enforced rather than conventional. Adding a detector anywhere else fails here.
// ---------------------------------------------------------------------------

// sanctionedDetectorPackages are the only packages a REGISTERED detector may be
// implemented in.
//
//   - internal/mapper holds SpecDetector, the declarative engine every regex
//     format (builtin or user-written) runs on.
//   - internal/mapper/detectors is the contribution home, and is the tree
//     imports_test.go walks. A contributed detector MUST live here; that is what
//     makes the import denylist and the goroutine ban apply to it.
var sanctionedDetectorPackages = map[string]string{
	"rubymud/go/internal/mapper":           "the declarative SpecDetector engine",
	"rubymud/go/internal/mapper/detectors": "the contributed-detector package (scanned by its imports_test.go)",
}

// TestEveryRegisteredDetectorLivesInASanctionedPackage is the enforcement point.
func TestEveryRegisteredDetectorLivesInASanctionedPackage(t *testing.T) {
	infos := mapper.DetectorInfos()
	if len(infos) == 0 {
		t.Fatal("no detectors registered in the production binary — this test would pass vacuously")
	}

	var checked []string
	for _, info := range infos {
		det, _, err := mapper.SafeNewDetector(info.Key)
		if err != nil {
			t.Errorf("detector %q could not be built: %v", info.Key, err)
			continue
		}
		pkg := detectorPkgPath(det)
		if _, ok := sanctionedDetectorPackages[pkg]; !ok {
			t.Errorf(`detector %q (%T) is implemented in package %q, which is NOT sanctioned.

A registered detector must live in one of:
%s
because those are the packages the import denylist and the goroutine ban actually
scan. A detector outside them is reachable in production as "builtin:%s" while
being covered by nothing.`,
				info.Key, det, pkg, sanctionedList(), info.Key)
			continue
		}
		checked = append(checked, info.Key+" -> "+pkg)
	}
	t.Logf("registered detectors, all in sanctioned packages:\n  %s", strings.Join(checked, "\n  "))
}

// TestProductionRegistryPassesConformance runs the shared suite over exactly the
// set of detectors the shipped binary carries.
//
// The detectors package runs the same suite, but only over what ITS test binary
// links. This run is the one that matches production: if main.go's blank-import
// list gains a package, its detectors are conformance-tested here whether or not
// anyone remembered to wire up a test alongside them.
func TestProductionRegistryPassesConformance(t *testing.T) {
	conformance.RunAll(t)
}

// TestDefaultDetectorIsRegisteredInProduction: a NULL sessions.mapper_format
// resolves through DefaultDetectorKey, so if the production binary did not carry
// it, every existing session would fall back with a warning on connect.
func TestDefaultDetectorIsRegisteredInProduction(t *testing.T) {
	if _, _, err := mapper.SafeNewDetector(mapper.DefaultDetectorKey); err != nil {
		t.Fatalf("the default detector %q is missing from the production binary: %v",
			mapper.DefaultDetectorKey, err)
	}
}

// detectorPkgPath reports the package a detector's concrete type is declared in,
// dereferencing the pointer receivers detectors normally use.
func detectorPkgPath(det mapper.RoomDetector) string {
	rt := reflect.TypeOf(det)
	for rt != nil && rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if rt == nil {
		return "<nil>"
	}
	return rt.PkgPath()
}

func sanctionedList() string {
	var out []string
	for pkg, why := range sanctionedDetectorPackages {
		out = append(out, "  - "+pkg+"  ("+why+")")
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}
