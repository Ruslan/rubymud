package mapper

// ---------------------------------------------------------------------------
// SpecDetector — the declarative (regex-spec) detector, as a RoomDetector.
//
// This is the path 99% of MUDs take and the path a non-programmer takes: a
// ProfileSpec edited in the settings UI, compiled to a Profile, driven by the
// Accumulator. The adapter puts that existing engine behind the shared interface,
// so a Go contribution and a regex spec are
// the same kind of thing to everything upstream (the session, the REST list, the
// selector, the conformance suite).
//
// WHY A WRAPPER RATHER THAN Accumulator IMPLEMENTING RoomDetector DIRECTLY:
// Accumulator.Feed takes a plain `string` and is called that way from the
// byte-identity goldens, whose whole job is to prove the parser did not
// move. Changing that signature would mean editing those tests, which is exactly
// what they exist to make impossible. The engine keeps its signature; the
// adapter provides the interface one. The extra hop is one non-escaping method
// call — it is non-escaping and does not allocate. The mapper package's
// BenchmarkFeedNonExitsLineViaDetector and allocation tests guard that property.
// ---------------------------------------------------------------------------

// SpecDetector adapts an Accumulator (and therefore a compiled ProfileSpec) to
// RoomDetector. It is stateful and belongs to exactly one session.
type SpecDetector struct {
	acc  *Accumulator
	info DetectorInfo
	// cases/filler are the detector's conformance fixtures. A registered detector
	// must have them (the suite fails otherwise); a detector built per-session from
	// a USER's stored row has none, and is never registered, so never reaches the
	// suite.
	cases  []ConformanceCase
	filler Line
}

// NewSpecDetector wraps a compiled profile as a detector. `info.Editable` is
// forced true: a declarative detector always has a spec that can be copied into
// an editable user format, and that is what distinguishes it in the UI from a Go
// contribution.
func NewSpecDetector(profile Profile, info DetectorInfo) *SpecDetector {
	info.Editable = true
	return &SpecDetector{acc: NewAccumulator(profile), info: info}
}

// WithConformance attaches the fixtures the shared suite runs. Required for a
// detector that is REGISTERED; pointless for a per-session user profile.
// Returns the receiver so a registration factory stays a single expression.
func (d *SpecDetector) WithConformance(filler Line, cases ...ConformanceCase) *SpecDetector {
	d.filler = filler
	d.cases = cases
	return d
}

// Feed implements RoomDetector. Both forms are buffered in the bounded
// accumulator; only ansi_delimited profiles inspect Raw, and only on an exits
// line after the cheap guard has completed a candidate block.
func (d *SpecDetector) Feed(l Line) (RoomEvent, bool) { return d.acc.FeedLine(l) }

// Reset implements RoomDetector.
func (d *SpecDetector) Reset() { d.acc.Reset() }

// Describe implements RoomDetector.
func (d *SpecDetector) Describe() DetectorInfo { return d.info }

// AtBlockBoundary / SuppressUntilBoundary implement BoundaryAware, so a
// mid-block format swap does not emit a torn, headerless block.
func (d *SpecDetector) AtBlockBoundary() bool  { return d.acc.AtBlockBoundary() }
func (d *SpecDetector) SuppressUntilBoundary() { d.acc.SuppressUntilBoundary() }

// Spec implements SpecProvider: the underlying ProfileSpec, so the settings UI
// can copy a builtin into an editable user format.
func (d *SpecDetector) Spec() ProfileSpec { return d.acc.Profile().Spec() }

// Profile exposes the compiled profile (tests and the session's status line).
func (d *SpecDetector) Profile() Profile { return d.acc.Profile() }

// DoorOpenTemplate implements DoorOpener from the spec's own field, so a
// declarative format describes how its doors open in the same place it describes
// everything else about that mud.
func (d *SpecDetector) DoorOpenTemplate() string { return d.acc.Profile().DoorOpen() }

// ConformanceCases / FillerLine implement Conformer.
func (d *SpecDetector) ConformanceCases() []ConformanceCase { return d.cases }
func (d *SpecDetector) FillerLine() Line                    { return d.filler }
