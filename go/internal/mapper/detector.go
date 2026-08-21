package mapper

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"rubymud/go/internal/mapref"
)

// ---------------------------------------------------------------------------
// RoomDetector is the contribution surface for custom room formats.
//
// The declarative room-block path uses a ProfileSpec (regexes, stops, strip,
// header rule) compiled into a Profile and driven by an Accumulator. That covers
// every format a regex can describe, which is most of them, and it needs no Go
// and no rebuild.
//
// It does not cover all of them. rubymud is open-source, and a player on a MUD
// whose room format cannot be expressed that way should be able to CONTRIBUTE a
// detector in Go, which we review and compile into the monolith. Go plugins are
// not viable here (build-tag fragility, platform limits, and a plugin can do
// anything the process can), so the model is compile-time registration, exactly
// like database/sql drivers: a contributed package calls RegisterDetector from
// init() and a blank import in main links it in.
//
// THE THINGS THAT MAKE THAT SAFE ARE MECHANICAL, NOT ASPIRATIONAL:
//
//  1. PANIC BOUNDARY, over the whole lifecycle — not just Feed. The session wraps
//     Feed (session.feedDetector), and SafeNewDetector/SafeSuppressUntilBoundary
//     wrap the FACTORY, Describe() and the BoundaryAware calls. That completeness
//     is load-bearing: LoadMapperProfile runs on the main goroutine BEFORE the
//     HTTP server listens, so an unguarded panic during construction took down the
//     host for every session AND left the user unable to reach the settings UI to
//     un-pick the format. A panicking detector now loses ITS session's mapping and
//     reports through the same channel a non-compiling spec already uses.
//     The one unguarded moment is a panic in a detector package's init(), which
//     nothing in Go can recover; see detectors/doc.go.
//  2. IMPORT + STATEMENT RESTRICTIONS. detectors/imports_test.go fails on a
//     denylist (net, net/http, os/exec, database/sql, runtime, storage, ...) and a
//     second full-AST pass rejects `go` and `select` statements, which the
//     ImportsOnly parse structurally cannot see. A detector takes lines and
//     returns events. Nothing else.
//  3. CONFORMANCE, over the registry rather than a directory. The suite
//     (internal/mapper/conformance) runs EVERY registered detector through its own
//     fixtures, a zero-allocation check on the hot path and a benchmark; a
//     detector that ships no fixtures — or only fixtures that expect nothing —
//     FAILS.
//  4. REGISTERED IMPLIES SCANNED. cmd/mudhost's TestEveryRegisteredDetectorLives-
//     InASanctionedPackage enumerates the production registry and rejects any
//     detector implemented outside `mapper` or `mapper/detectors`. Without it, 2
//     and 3 were both directory scans that a detector could simply sit outside:
//     registered, selectable, and covered by nothing.
//
// GROWTH INTENT (documented, deliberately NOT built): protocol data (MSDP/GMCP)
// is outside the current detector interface. When it lands it arrives as a SECOND entry
// point — an optional interface a detector may implement (in the shape of the
// BoundaryAware/Conformer optionals below), not as a new parameter on Feed. That
// is why Feed takes a struct rather than positional strings: adding a field to
// Line, or adding an optional interface, breaks nobody.
// ---------------------------------------------------------------------------

// Line is one incoming line handed to a detector, in BOTH forms the session
// already has.
//
// COST NOTE. This is free at the call site: session.processLine computes
// `processed` (control-sanitized, ANSI intact) and `plainText` (ANSI stripped)
// one line above the mapper hook regardless — see io.go. Passing both instead of
// one adds a string header copy to an already-existing call and no work.
//
// Stripped drives exits, boundaries and body rules. Raw is consumed by the
// declarative `ansi_delimited` header rule and may also be used by contributed
// detectors for colour-based heuristics.
type Line struct {
	// Stripped is the line with ANSI/control sequences removed and no trailing
	// CR-normalization applied beyond what the session already does.
	Stripped string
	// Raw is the same line with its ANSI escape sequences still in it. It may be
	// equal to Stripped when the line carried no colour. A detector that does not
	// care about colour must ignore it.
	Raw string
}

// StrippedLine builds a Line for a caller that only has the stripped text
// (tests, REPL work, the package-level convenience wrappers). Raw is set equal
// to Stripped, which is the truthful answer for a line with no escape sequences
// and a harmless one for a detector that ignores Raw.
func StrippedLine(s string) Line { return Line{Stripped: s, Raw: s} }

// Provenance says how much trust a detector has EARNED. These strings reach the
// settings UI verbatim, so they are written to be honest rather than
// encouraging: a player picking a format needs to know whether anyone has ever
// run it against a real server.
type Provenance string

const (
	// ProvenanceVerified — field-tested against a live server. Today that is RMUD
	// alone (8034 rooms imported and walked).
	ProvenanceVerified Provenance = "Verified"
	// ProvenanceStarter — a plausible declarative spec that NOBODY has run against
	// a live server. It is a starting point to copy and adjust, not a promise.
	ProvenanceStarter Provenance = "Starter"
	// ProvenanceContributed — a Go detector contributed for a specific MUD. Its
	// correctness is exactly its fixtures' correctness: the conformance suite proves
	// it does what its author says on the blocks its author supplied, and nothing
	// beyond that.
	ProvenanceContributed Provenance = "Contributed"
	// ProvenanceUser — a format the user wrote in this installation's settings.
	//
	// A user-created row also needs a provenance because the list endpoint returns
	// it alongside builtins; labelling it "Starter" or "Contributed" would be a lie
	// in the one place this vocabulary exists to prevent lies.
	ProvenanceUser Provenance = "User"
)

// Valid reports whether p is one of the known provenance values. The conformance
// suite enforces it so a contribution cannot invent a reassuring-sounding label.
func (p Provenance) Valid() bool {
	switch p {
	case ProvenanceVerified, ProvenanceStarter, ProvenanceContributed, ProvenanceUser:
		return true
	}
	return false
}

// DetectorInfo is a detector's self-description. Everything here is shown to the
// user, so it is the detector author's contract with the person choosing it.
type DetectorInfo struct {
	// Key is the stable id used in references ("rmud", "diku-en"). It appears in
	// sessions.mapper_format as `builtin:<Key>`, so RENAMING A KEY BREAKS EVERY
	// SESSION POINTING AT IT — treat it as data, not a label.
	Key string `json:"key"`
	// Title is the UI label ("RMUD", "Diku-family (English)").
	Title string `json:"title"`
	// MUD names the server or family this targets.
	MUD string `json:"mud"`
	// Provenance is how much this has been verified. See the constants.
	Provenance Provenance `json:"provenance"`
	// Description says what it recognizes, in the user's terms, and is rendered
	// verbatim in the settings UI.
	Description string `json:"description"`
	// Editable is true only for a declarative (spec-driven) detector, whose spec
	// can be copied into an editable user format. A Go detector is false: there is
	// no spec to copy, and the UI must say so rather than offering a broken copy.
	Editable bool `json:"editable"`
}

// RoomDetector turns a stream of incoming lines into RoomEvents.
//
// CONTRACT for an implementation:
//   - Feed is called once per incoming line, from the session's single read loop.
//     It is the hot path: on a line that does not complete a block it must not
//     allocate and must not run a regex without a cheap literal guard first. The
//     conformance suite enforces the zero-allocation half.
//   - Feed must be cheap and total. It must not do I/O, block, sleep, spawn
//     goroutines, or touch global mutable state.
//   - A detector instance is NOT safe for concurrent use, and never needs to be:
//     one instance belongs to one session.
//   - Reset drops in-flight state. After Reset the detector must behave as a
//     freshly constructed one. Nothing in the session calls it today — a profile
//     swap or a reconnect builds a FRESH detector — but the conformance suite
//     does, so implement it correctly rather than leaving it empty.
//   - Describe must return the same value every time; it is called off the hot
//     path only.
type RoomDetector interface {
	// Feed processes one incoming line and returns (event, true) when the line
	// completed a room block.
	Feed(Line) (RoomEvent, bool)
	// Reset drops in-flight state without changing configuration.
	Reset()
	// Describe reports the stable self-description shown to the user.
	Describe() DetectorInfo
}

// BoundaryAware is an OPTIONAL interface a detector may implement so a
// mid-block profile swap does not emit a torn block.
//
// Why optional rather than part of RoomDetector: it is a refinement, not a
// requirement. A detector that does not implement it simply does not get the
// suppression, which costs at most one truncated event on the rare occasion a
// user changes the format while a room block is half-arrived. Making every
// contributor implement two extra methods to avoid that would be a bad trade.
//
// See Accumulator.SuppressUntilBoundary for what the suppression protects
// against (a headerless "block" read by the tracker as a pipe corridor, which
// demotes a confirmed-green player to yellow).
type BoundaryAware interface {
	// AtBlockBoundary reports whether the detector is BETWEEN blocks rather than
	// part-way through one.
	AtBlockBoundary() bool
	// SuppressUntilBoundary makes the detector swallow the next block it would
	// emit unless it sees a boundary first.
	SuppressUntilBoundary()
}

// SpecProvider is an OPTIONAL interface implemented by declarative detectors: it
// hands out the ProfileSpec behind the detector so the settings UI can offer
// «make a copy» into an editable user format. A Go detector does not implement
// it, which is exactly what DetectorInfo.Editable=false tells the UI.
type SpecProvider interface {
	Spec() ProfileSpec
}

// DoorOpener is an OPTIONAL interface a detector may implement to say how THIS
// mud's doors are opened, so a computed route can send the command instead of
// stopping at the door.
//
// It is on the detector rather than in shared code because it is a property of the
// server on the other end, not of the client: "откр две {dir}" is right on RMUD and
// wrong everywhere else, exactly as the minimap glyph set was before it became
// StripSpec.Glyphs. It is optional for the same reason BoundaryAware is: a detector
// that does not implement it simply produces no opening command, which is the
// behaviour that existed before door markup and is never wrong — only incomplete.
//
// Declarative detectors get it from ProfileSpec.DoorOpen; a contributed Go detector
// may implement it directly.
type DoorOpener interface {
	// DoorOpenTemplate returns the command template, with "{dir}" standing for the
	// direction word of the hop, or "" for "this format does not say".
	DoorOpenTemplate() string
}

// DetectorDoorOpen asks a detector for its door-opening template, returning "" for
// a detector (or a nil one) that does not answer. It is the ONE place upstream code
// consults the optional interface, so a nil detector and an unimplementing one
// cannot diverge.
func DetectorDoorOpen(det RoomDetector) string {
	if d, ok := det.(DoorOpener); ok {
		return d.DoorOpenTemplate()
	}
	return ""
}

// ScanProvider is an OPTIONAL capability of a room detector. Declarative room
// formats use it to describe a bounded, server-specific surroundings probe.
// Contributed detectors and legacy profiles simply leave scanning disabled.
type ScanProvider interface {
	ScanConfig() (ScanConfig, bool)
}

// DetectorScan asks a detector for its scan contract without making that
// capability mandatory for third-party RoomDetector implementations.
func DetectorScan(det RoomDetector) (ScanConfig, bool) {
	if d, ok := det.(ScanProvider); ok {
		return d.ScanConfig()
	}
	return ScanConfig{}, false
}

// ---------------------------------------------------------------------------
// Conformance fixtures.
//
// A detector's fixtures ARE its correctness claim: "Contributed" provenance
// means precisely "it does this on these blocks". So they are part of the
// detector, supplied by its author, and the shared suite runs them — rather than
// the suite carrying a growing table that every contribution has to edit.
// ---------------------------------------------------------------------------

// ConformanceCase is one fixture: a run of lines and the events they must
// produce, in order.
type ConformanceCase struct {
	// Name identifies the case in test output.
	Name string
	// Feed is the line run, in arrival order.
	Feed []Line
	// Want is every event the run must produce, in order. An empty Want means the
	// run must produce NO event — which is the shape that catches a detector
	// firing on unrelated output.
	Want []RoomEvent
}

// Conformer is the interface a registered detector MUST implement. It is not
// part of RoomDetector because the session has no use for it, but the
// conformance suite fails a detector that lacks it: a detector nobody can test
// is a detector nobody can review.
type Conformer interface {
	// ConformanceCases returns this detector's fixtures. Must be non-empty.
	ConformanceCases() []ConformanceCase
	// FillerLine returns a line that must NOT complete a block, used for the
	// zero-allocation check and the benchmark. It should look like a real body
	// line of the target MUD, not like "x": the allocation check is only
	// meaningful on the shape the detector actually walks.
	FillerLine() Line
}

// ---------------------------------------------------------------------------
// The registry.
//
// Registration is by FACTORY, not by instance: every session needs its own
// stateful detector, so handing the same instance to two sessions would cross
// their ring buffers. NewDetector calls the factory each time.
// ---------------------------------------------------------------------------

type registration struct {
	info    DetectorInfo
	factory func() RoomDetector
}

var (
	detectorMu   sync.RWMutex
	detectorsMap = map[string]registration{}
)

// RegisterDetector adds a detector to the registry. Call it from an init()
// function in the package that defines the detector, database/sql-driver style.
//
// It PANICS on a duplicate key, an empty key, an unknown provenance, or a
// factory that returns nil. All four are programming errors in code that is
// compiled into the binary, so they are caught on the first run of any build
// that links the detector — including every test binary — rather than silently
// shadowing an existing format and re-parsing somebody's sessions.
func RegisterDetector(factory func() RoomDetector) {
	if factory == nil {
		panic("mapper: RegisterDetector called with a nil factory")
	}
	probe := factory()
	if probe == nil {
		panic("mapper: RegisterDetector: factory returned nil")
	}
	info := probe.Describe()
	if strings.TrimSpace(info.Key) == "" {
		panic("mapper: RegisterDetector: detector has an empty Key")
	}
	if !info.Provenance.Valid() {
		panic(fmt.Sprintf("mapper: RegisterDetector: detector %q has unknown provenance %q", info.Key, info.Provenance))
	}
	detectorMu.Lock()
	defer detectorMu.Unlock()
	if prev, dup := detectorsMap[info.Key]; dup {
		panic(fmt.Sprintf("mapper: duplicate room detector key %q (already registered as %q) — keys are stored in sessions.mapper_format and must be unique",
			info.Key, prev.info.Title))
	}
	detectorsMap[info.Key] = registration{info: info, factory: factory}
}

// NewDetector builds a fresh instance of the registered detector with this key,
// or ok=false when no such detector is linked into this binary. A missing key is
// NOT an error: it is what an older binary sees when a session references a
// detector a newer build shipped, and every caller must degrade to the default
// rather than fail.
func NewDetector(key string) (RoomDetector, bool) {
	detectorMu.RLock()
	reg, ok := detectorsMap[key]
	detectorMu.RUnlock()
	if !ok {
		return nil, false
	}
	return reg.factory(), true
}

// SafeNewDetector is NewDetector plus a panic boundary around the two pieces of
// third-party code it runs: the registered FACTORY and the new instance's
// Describe().
//
// WHY THIS EXISTS. The first panic guard wrapped only Feed. That left the whole
// construction path unguarded, and construction is not a quiet corner: the
// production chain is main() -> Manager.Connect -> LoadMapperProfile, ON THE MAIN
// GOROUTINE, BEFORE the HTTP server listens. A contributed detector whose factory
// panicked therefore took down the entire host for every session — and because
// the server never reached Listen, the user could not open the settings UI to
// un-pick it. A single bad `builtin:<key>` was an unbootable install.
//
// So: any panic here is reported as an error, the caller degrades to the default
// exactly as it does for a spec that will not compile, and the process lives.
//
// NOT guarded, and deliberately: a panic inside a detector package's init().
// init() runs before main and cannot be recovered by anything — the runtime tears
// the process down. That is the one unguarded moment in the contribution path and
// it is documented as such in detectors/doc.go. It is also the least dangerous
// place for it, because it fails identically on every machine the moment the
// binary starts, including the contributor's own `go test`.
func SafeNewDetector(key string) (det RoomDetector, info DetectorInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			det, info, err = nil, DetectorInfo{}, fmt.Errorf("room detector %q panicked while being constructed: %v", key, r)
		}
	}()
	d, ok := NewDetector(key)
	if !ok {
		return nil, DetectorInfo{}, fmt.Errorf("room detector %q is not compiled into this build", key)
	}
	if d == nil {
		return nil, DetectorInfo{}, fmt.Errorf("room detector %q built a nil detector", key)
	}
	return d, d.Describe(), nil
}

// SafeSuppressUntilBoundary applies the optional BoundaryAware mid-block
// suppression across a detector swap, under a panic boundary.
//
// Both calls are third-party code reached from LoadMapperProfile — which, again,
// runs on the main goroutine before the server listens. Reports whether the
// suppression was actually applied; a panic is swallowed to a false, because
// losing the suppression costs at most one torn block, and that is not worth
// failing a profile swap over.
func SafeSuppressUntilBoundary(outgoing, incoming RoomDetector) (applied bool) {
	defer func() {
		if r := recover(); r != nil {
			applied = false
		}
	}()
	out, ok := outgoing.(BoundaryAware)
	if !ok || out.AtBlockBoundary() {
		return false
	}
	in, ok := incoming.(BoundaryAware)
	if !ok {
		return false
	}
	in.SuppressUntilBoundary()
	return true
}

// DetectorInfos returns every registered detector's info, ordered by key so the
// settings UI and the conformance suite both see a stable list.
func DetectorInfos() []DetectorInfo {
	detectorMu.RLock()
	out := make([]DetectorInfo, 0, len(detectorsMap))
	for _, reg := range detectorsMap {
		out = append(out, reg.info)
	}
	detectorMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// LookupDetectorInfo returns one registered detector's info.
func LookupDetectorInfo(key string) (DetectorInfo, bool) {
	detectorMu.RLock()
	defer detectorMu.RUnlock()
	reg, ok := detectorsMap[key]
	return reg.info, ok
}

// ---------------------------------------------------------------------------
// Format references — the single TEXT column sessions.mapper_format.
//
// The scheme itself lives in the dependency-free `mapref` package, because
// `storage` needs it too and cannot import `mapper` (`mapper` imports
// `storage`). Everything below is a re-export so existing callers keep saying
// mapper.ParseFormatRef / mapper.BuiltinRef and there is still exactly ONE
// definition of each prefix. See mapref's package doc.
// ---------------------------------------------------------------------------

// DefaultDetectorKey is the detector a session with no stored reference uses.
const DefaultDetectorKey = mapref.DefaultDetectorKey

// Reference schemes.
const (
	RefBuiltinPrefix = mapref.RefBuiltinPrefix
	RefDBPrefix      = mapref.RefDBPrefix
)

// RefKind is what a parsed reference points at.
type RefKind = mapref.RefKind

const (
	RefDefault = mapref.RefDefault
	RefBuiltin = mapref.RefBuiltin
	RefDB      = mapref.RefDB
)

// ParseFormatRef splits a stored reference into its kind and payload. It is
// deliberately lenient; every WRITE path must re-render through Canonical (or
// BuiltinRef/FormatRefForDB) rather than storing the raw input, because storage
// matches this column by exact string. See mapref.ParseFormatRef.
func ParseFormatRef(ref string) (kind RefKind, key string, id int64, ok bool) {
	return mapref.ParseFormatRef(ref)
}

// BuiltinRef renders the canonical reference for a registered detector key.
func BuiltinRef(key string) string { return mapref.BuiltinRef(key) }

// FormatRefForDB renders the canonical reference for a mapper_profiles row id.
func FormatRefForDB(id int64) string { return mapref.FormatRefForDB(id) }

// CanonicalRef re-renders a parsed reference in canonical form ("" = default).
func CanonicalRef(kind RefKind, key string, id int64) string {
	return mapref.Canonical(kind, key, id)
}
