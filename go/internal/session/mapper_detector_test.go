package session

import (
	"strings"
	"sync/atomic"
	"testing"

	"rubymud/go/internal/mapper"
)

// ---------------------------------------------------------------------------
// The blast-radius boundary for contributed detectors.
//
// A contributed Go detector is third-party code running INSIDE the session's
// read loop — the goroutine that reads the MUD socket. Review is not a
// guarantee, so the guarantee has to be mechanical: the session wraps every
// Feed call in recover(). These tests are the proof that the wrapper is real
// and that the recovery does the four things it must (survive, fall back,
// report, keep parsing), rather than just catching the panic and going quiet.
//
// The detectors below are registered from init() in a _test.go file, so they
// exist ONLY in this test binary. They are deliberately not in the detectors
// package: a permanently-panicking detector registered for real would fail the
// conformance suite, which is exactly what should happen to one.
// ---------------------------------------------------------------------------

const (
	panicDetectorKey = "_test_panic"
	panicFactoryKey  = "_test_panic_factory"
	panicDescribeKey = "_test_panic_describe"
	panicBoundaryKey = "_test_panic_boundary"
)

// These arm the LATENT panics. RegisterDetector probes each factory (and its
// Describe) once at init to read the key, so a detector that panicked
// unconditionally would take the test binary down before any test ran — which is
// itself the point of item 1. Arming after registration models the realistic
// shape: a contribution that works on the author's machine and blows up on
// someone else's input.
var (
	armFactoryPanic  atomic.Bool
	armDescribePanic atomic.Bool
)

func init() {
	mapper.RegisterDetector(func() mapper.RoomDetector { return &panickingDetector{} })
	mapper.RegisterDetector(func() mapper.RoomDetector {
		if armFactoryPanic.Load() {
			panic("contributed detector's factory exploded")
		}
		return &inertTestDetector{key: panicFactoryKey}
	})
	mapper.RegisterDetector(func() mapper.RoomDetector { return &inertTestDetector{key: panicDescribeKey, describePanics: true} })
	mapper.RegisterDetector(func() mapper.RoomDetector { return &boundaryPanicDetector{} })
}

// panickingDetector panics on EVERY line it is given. It is the worst realistic
// contribution: a nil map write, an index out of range, a bad type assertion —
// all of which reach the session as a runtime panic inside Feed.
//
// fed counts calls, so a test can prove the session STOPPED calling it rather
// than merely stopped complaining about it.
type panickingDetector struct{ fed atomic.Int64 }

func (d *panickingDetector) Feed(l mapper.Line) (mapper.RoomEvent, bool) {
	d.fed.Add(1)
	panic("contributed detector exploded on: " + l.Stripped)
}

func (d *panickingDetector) Reset() {}

func (d *panickingDetector) Describe() mapper.DetectorInfo {
	return mapper.DetectorInfo{
		Key:         panicDetectorKey,
		Title:       "Deliberately panicking detector (test only)",
		MUD:         "none",
		Provenance:  mapper.ProvenanceContributed,
		Description: "Exists only in the session package's test binary, to prove the panic boundary catches a broken contribution.",
	}
}

// assignDetector points the harness session at a registered detector and loads
// it, returning once the session is live on it.
func assignDetector(t *testing.T, s *Session, sessID int64, store interface {
	SetSessionMapperFormat(int64, string) error
}, key string) {
	t.Helper()
	if err := store.SetSessionMapperFormat(sessID, mapper.BuiltinRef(key)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()
	if ref, _, _ := s.MapperProfileStatus(); ref != mapper.BuiltinRef(key) {
		t.Fatalf("precondition: the session is on %q, want %q", ref, mapper.BuiltinRef(key))
	}
}

// TestPanickingDetectorDoesNotKillTheReadLoop is the headline guarantee: the
// panic is contained, the call returns normally, and the session keeps going.
func TestPanickingDetectorDoesNotKillTheReadLoop(t *testing.T) {
	s, store, sessID, got, mu := profileHarness(t)
	assignDetector(t, s, sessID, store, panicDetectorKey)

	// If the boundary were missing, THIS line would take the test process down —
	// which is precisely what it would do to the session's read loop in production.
	s.observeIncomingLine("Вторая", "Вторая")

	// 1. Fell back to the default, for THIS session only.
	ref, name, warn := s.MapperProfileStatus()
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("after a panic the session is on %q, want the default builtin", ref)
	}
	if name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("fallback name = %q, want the default builtin's", name)
	}

	// 2. Reported through the SAME channel a non-compiling spec uses — the
	// MapperProfileStatus warning, which sendRestoreState replays on every connect
	// and page reload. A panic that only reached the server log would be invisible
	// to the person whose mapper stopped working.
	if warn == "" {
		t.Fatal("a panicking detector must be reported, not silently swallowed")
	}
	if !strings.Contains(warn, "crashed") {
		t.Errorf("warning = %q, want it to say the format crashed", warn)
	}

	// 3. And broadcast live, as the typed mapper_profile message.
	msgs := msgsOfType(got, mu, "mapper_profile")
	if len(msgs) == 0 {
		t.Fatal("no mapper_profile broadcast after the detector panicked")
	}
	last := msgs[len(msgs)-1]
	if last.MapperProfileError == "" {
		t.Error("the broadcast must carry the failure reason")
	}
	if last.MapperFormat != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("broadcast format = %q, want the default builtin it fell back to", last.MapperFormat)
	}
}

// TestSessionKeepsMappingAfterADetectorPanic — falling back is only useful if the
// fallback then WORKS. After the crash the session must parse RMUD blocks with
// the builtin rules and move the tracker, with no reconnect.
func TestSessionKeepsMappingAfterADetectorPanic(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	assignDetector(t, s, sessID, store, panicDetectorKey)

	s.observeIncomingLine("anything", "anything")

	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok || ev.Hint != "Вторая" {
		t.Fatalf("the session stopped mapping after the panic: ok=%v ev=%+v", ok, ev)
	}
	if pos := s.mapTracker.Position(); pos.Coord.X != 1 {
		t.Errorf("the tracker did not advance after the fallback: %+v", pos)
	}
}

// TestDetectorPanicDoesNotRewriteTheStoredAssignment — the fallback is
// IN-MEMORY. Rewriting the column would mean a detector fixed in the next build
// never gets used again unless the user notices and re-picks their format, and
// it would silently destroy a choice they made.
func TestDetectorPanicDoesNotRewriteTheStoredAssignment(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	assignDetector(t, s, sessID, store, panicDetectorKey)

	s.observeIncomingLine("boom", "boom")

	stored, err := store.GetSessionMapperFormat(sessID)
	if err != nil {
		t.Fatalf("GetSessionMapperFormat: %v", err)
	}
	if stored != mapper.BuiltinRef(panicDetectorKey) {
		t.Errorf("stored assignment = %q, want it left alone at %q", stored, mapper.BuiltinRef(panicDetectorKey))
	}
	// And an explicit reload puts the (still broken) detector back — the user's
	// choice is honoured until they change it.
	s.LoadMapperProfile()
	if ref, _, _ := s.MapperProfileStatus(); ref != mapper.BuiltinRef(panicDetectorKey) {
		t.Errorf("after an explicit reload the session is on %q, want the stored assignment", ref)
	}
}

// TestRepeatedDetectorPanicsDoNotSpin: the FIRST fallback. A detector that panics
// on every line must not turn into a fallback-rebuild loop. After the first
// fallback the session is on the default, and the default does not panic — so the
// second line is parsed, not recovered.
//
// NOTE this test does NOT reach the "already fallen back" branch, which is why it
// passed while that branch was broken. TestFallbackDetectorPanicDisablesMapping
// below is the one that covers it.
func TestRepeatedDetectorPanicsDoNotSpin(t *testing.T) {
	s, store, sessID, got, mu := profileHarness(t)
	assignDetector(t, s, sessID, store, panicDetectorKey)

	for i := 0; i < 50; i++ {
		s.observeIncomingLine("line", "line")
	}
	if n := len(msgsOfType(got, mu, "mapper_profile")); n > 2 {
		t.Errorf("mapper_profile broadcasts = %d after 50 lines; the fallback is re-firing per line", n)
	}
}

// TestFallbackDetectorPanicDisablesMapping covers the branch the previous test
// never entered: the thing that panics is the detector we ALREADY fell back to.
//
// The old code logged "this session has stopped mapping" and returned WITHOUT
// touching s.mapDetector, so the panicking detector stayed installed and every
// subsequent line panicked, recovered and dumped a stack again — 100 lines
// produced 100 stack dumps and 200 mutex acquisitions. It neither stopped nor
// avoided spinning; the comment was the only thing that was true.
//
// The check here is behavioural, not cosmetic: it counts Feed CALLS, so it proves
// the session stopped calling the broken detector rather than merely stopping
// complaining about it.
func TestFallbackDetectorPanicDisablesMapping(t *testing.T) {
	s, store, sessID, got, mu := profileHarness(t)
	assignDetector(t, s, sessID, store, panicDetectorKey)

	// First panic: falls back to the default.
	s.observeIncomingLine("first", "first")
	if ref, _, _ := s.MapperProfileStatus(); ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Fatalf("precondition: expected the fallback to the default, got %q", ref)
	}

	// Now make the FALLBACK itself broken — exactly the state the branch is for
	// (the default panicked). White-box, because there is no other way to get a
	// panicking default without breaking the default for every other test.
	broken := &panickingDetector{}
	s.mapMu.Lock()
	s.mapDetector = broken
	s.mapMu.Unlock()

	for i := 0; i < 100; i++ {
		s.observeIncomingLine("line", "line")
	}

	// It must have been called at most ONCE more: the first of those 100 lines
	// trips the branch, and every line after that returns early.
	if n := broken.fed.Load(); n != 1 {
		t.Errorf("the broken fallback was fed %d times over 100 lines, want exactly 1 — the session is spinning on it", n)
	}

	s.mapMu.Lock()
	det, disabled := s.mapDetector, s.mapDetectorDisabled
	s.mapMu.Unlock()
	if det != nil {
		t.Errorf("mapDetector = %T after the fallback panicked, want nil — leaving it installed is what caused the spin", det)
	}
	if !disabled {
		t.Error("mapping must be DISABLED once the fallback itself panics")
	}

	// Reported once, not a hundred times.
	msgs := msgsOfType(got, mu, "mapper_profile")
	if len(msgs) > 3 {
		t.Errorf("mapper_profile broadcasts = %d; the disable is re-broadcasting per line", len(msgs))
	}
	if last := msgs[len(msgs)-1]; !strings.Contains(last.MapperProfileError, "mapping is off") {
		t.Errorf("final status = %q, want it to say mapping is off for this session", last.MapperProfileError)
	}

	// The session is otherwise untouched: it still accepts lines without panicking.
	s.observeIncomingLine("[ Exits: N ]", "[ Exits: N ]")

	// And an explicit reload is the way back — the user's "try again".
	if err := store.SetSessionMapperFormat(sessID, ""); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()
	s.mapMu.Lock()
	reDisabled := s.mapDetectorDisabled
	s.mapMu.Unlock()
	if reDisabled {
		t.Error("an explicit LoadMapperProfile must re-enable mapping")
	}
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if ev, ok := s.LastRoomEvent(); !ok || ev.Hint != "Вторая" {
		t.Errorf("mapping did not resume after an explicit reload: ok=%v ev=%+v", ok, ev)
	}
}

// TestBoundaryOnlyCostsTheOneSession — the blast radius claim. A second session
// running the default detector is untouched by the first one's crash.
func TestDetectorPanicDoesNotAffectOtherSessions(t *testing.T) {
	bad, store, badID, _, _ := profileHarness(t)
	assignDetector(t, bad, badID, store, panicDetectorKey)

	good, _, _, _, _ := profileHarness(t)
	good.LoadMapperProfile()

	bad.observeIncomingLine("boom", "boom")

	if ref, _, warn := good.MapperProfileStatus(); warn != "" || ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("an unrelated session was disturbed: ref=%q warn=%q", ref, warn)
	}
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		good.observeIncomingLine(l, l)
	}
	if ev, ok := good.LastRoomEvent(); !ok || ev.Hint != "Вторая" {
		t.Errorf("an unrelated session stopped mapping: ok=%v ev=%+v", ok, ev)
	}
}

// --- the hot path ----------------------------------------------------------

// TestObserveIncomingLineDoesNotAllocate is the AGENTS.md #3 guard at the
// SESSION level, one layer above mapper's TestFeedNonExitsLineDoesNotAllocate.
// The path includes an interface dispatch and a recover() wrapper, and both must
// be free of allocation. If this fails, the likely
// cause is the deferred closure in feedDetector capturing something that escapes.
func TestObserveIncomingLineDoesNotAllocate(t *testing.T) {
	s, _, _, _, _ := profileHarness(t)
	s.LoadMapperProfile()
	// Drop the clients so the measurement is the parse path, not the broadcast
	// path (a completed block allocates by design; a plain line must not).
	s.clients = make(map[int]clientSink)

	const line = ".[ ]-[ ]......... круглый стол на двенадцать персон"
	for i := 0; i < 64; i++ {
		s.observeIncomingLine(line, line)
	}
	if got := testing.AllocsPerRun(200, func() { s.observeIncomingLine(line, line) }); got != 0 {
		t.Errorf("observeIncomingLine allocated %v times per non-block line, want 0", got)
	}
}

func BenchmarkObserveIncomingLine(b *testing.B) {
	s := &Session{clients: make(map[int]clientSink)}
	s.mapDetector = mapper.NewSpecDetector(mapper.BuiltinRMUD(), mapper.RMUDDetectorInfo())
	const line = ".[ ]-[ ]......... круглый стол на двенадцать персон приколочен к полу"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.observeIncomingLine(line, line)
	}
}

// --- item 1: the boundary must cover the whole lifecycle, not just Feed ------

// inertTestDetector parses nothing. Its Describe optionally panics, to cover the
// second unguarded call in the construction path.
type inertTestDetector struct {
	key            string
	describePanics bool
}

func (d *inertTestDetector) Feed(mapper.Line) (mapper.RoomEvent, bool) {
	return mapper.RoomEvent{}, false
}
func (d *inertTestDetector) Reset() {}
func (d *inertTestDetector) Describe() mapper.DetectorInfo {
	if d.describePanics && armDescribePanic.Load() {
		panic("contributed detector's Describe exploded")
	}
	return mapper.DetectorInfo{
		Key:         d.key,
		Title:       "Inert test detector",
		MUD:         "none",
		Provenance:  mapper.ProvenanceContributed,
		Description: "Exists only in the session package's test binary, to prove the construction-path panic boundary.",
	}
}

// boundaryPanicDetector explodes in the OPTIONAL BoundaryAware calls, which run
// on every profile swap — i.e. from LoadMapperProfile, on the main goroutine.
type boundaryPanicDetector struct{}

func (d *boundaryPanicDetector) Feed(mapper.Line) (mapper.RoomEvent, bool) {
	return mapper.RoomEvent{}, false
}
func (d *boundaryPanicDetector) Reset() {}
func (d *boundaryPanicDetector) Describe() mapper.DetectorInfo {
	return mapper.DetectorInfo{
		Key:         panicBoundaryKey,
		Title:       "Boundary-panicking test detector",
		MUD:         "none",
		Provenance:  mapper.ProvenanceContributed,
		Description: "Exists only in the session package's test binary, to prove the BoundaryAware panic boundary.",
	}
}
func (d *boundaryPanicDetector) AtBlockBoundary() bool  { panic("AtBlockBoundary exploded") }
func (d *boundaryPanicDetector) SuppressUntilBoundary() { panic("SuppressUntilBoundary exploded") }

// TestPanickingFactoryDoesNotKillTheProcess is the CRITICAL case. The production
// chain is main() -> Manager.Connect -> LoadMapperProfile, on the MAIN goroutine,
// before the HTTP server listens. An unguarded factory panic there took the whole
// host down for every session — and since the server never reached Listen, the
// user could not open the settings UI to un-pick the format. One bad
// `builtin:<key>` was an unbootable install.
func TestPanickingFactoryDoesNotKillTheProcess(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	if err := store.SetSessionMapperFormat(sessID, mapper.BuiltinRef(panicFactoryKey)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	armFactoryPanic.Store(true)
	t.Cleanup(func() { armFactoryPanic.Store(false) })

	// Without the guard this call panics on the goroutine that, in production, is
	// main().
	s.LoadMapperProfile()

	ref, name, warn := s.MapperProfileStatus()
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("after a factory panic the session is on %q, want the default builtin", ref)
	}
	if warn == "" {
		t.Fatal("a panicking factory must be reported through the same channel a non-compiling spec uses")
	}
	if !strings.Contains(warn, "panicked") {
		t.Errorf("warning = %q, want it to name the panic", warn)
	}
	// And the session actually maps, with the default rules.
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if ev, ok := s.LastRoomEvent(); !ok || ev.Hint != "Вторая" {
		t.Errorf("session did not fall back to a working detector: ok=%v ev=%+v name=%q", ok, ev, name)
	}
}

// TestPanickingDescribeDoesNotKillTheProcess — Describe() is the second piece of
// third-party code on the construction path, called to render the status line.
func TestPanickingDescribeDoesNotKillTheProcess(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	if err := store.SetSessionMapperFormat(sessID, mapper.BuiltinRef(panicDescribeKey)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	armDescribePanic.Store(true)
	t.Cleanup(func() { armDescribePanic.Store(false) })

	s.LoadMapperProfile()

	ref, _, warn := s.MapperProfileStatus()
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || warn == "" {
		t.Errorf("after a Describe panic: ref=%q warn=%q, want the default with a warning", ref, warn)
	}
}

// TestPanickingBoundaryAwareDoesNotKillTheProcess — the mid-block swap
// suppression is OPTIONAL behaviour, so losing it must cost at most one torn
// block, never the process.
func TestPanickingBoundaryAwareDoesNotKillTheProcess(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	if err := store.SetSessionMapperFormat(sessID, mapper.BuiltinRef(panicBoundaryKey)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()
	if ref, _, _ := s.MapperProfileStatus(); ref != mapper.BuiltinRef(panicBoundaryKey) {
		t.Fatalf("precondition: session should be on the boundary-panicking detector, got %q", ref)
	}

	// Swapping AWAY calls the outgoing detector's AtBlockBoundary — which panics.
	if err := store.SetSessionMapperFormat(sessID, ""); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	if ref, _, warn := s.MapperProfileStatus(); ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || warn != "" {
		t.Errorf("swap away from a boundary-panicking detector: ref=%q warn=%q, want a clean default", ref, warn)
	}
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if ev, ok := s.LastRoomEvent(); !ok || ev.Hint != "Вторая" {
		t.Errorf("session did not map after the swap: ok=%v ev=%+v", ok, ev)
	}
}
