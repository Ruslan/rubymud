package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// ---------------------------------------------------------------------------
// Room-format profiles wired into a live session.
//
// This package can link BOTH storage and mapper, so it is where the REAL compile
// gate is exercised end to end: a spec written through the store is a spec a
// session can load, and a spec that somehow is not (a hand-edited DB) degrades
// to the builtin loudly instead of silently mapping nothing.
// ---------------------------------------------------------------------------

func newProfileTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.New().String())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.MigrateForTest(db); err != nil {
		t.Fatalf("MigrateForTest: %v", err)
	}
	return storage.NewTestStore(db)
}

// englishSpecJSON is a synthetic non-RMUD room format: different exits grammar,
// a prompt as block boundary, fixed-width left-column cut, header = first line.
// Nothing about it overlaps RMUD, so "the session is using THIS profile" is
// unambiguous from the parse result alone.
func englishSpecJSON(t *testing.T, name string) string {
	t.Helper()
	spec := mapper.ProfileSpec{
		Name: name,
		Exits: mapper.ExitsSpec{
			Guard:     "Obvious exits:",
			Pattern:   `^Obvious exits:\s*(.*?)\s*\.?$`,
			NoneToken: "None",
		},
		Block: mapper.BlockSpec{
			Stops:    []mapper.BlockStop{mapper.StopPrompt, mapper.StopBlank, mapper.StopMaxLines},
			MaxLines: 16,
		},
		Prompt: `^<\d+hp \d+m>$`,
		Strip:  mapper.StripSpec{Mode: mapper.StripFixedWidth, Width: 12},
		Header: mapper.HeaderSpec{Rule: mapper.HeaderFirst},
	}
	blob, err := mapper.MarshalSpec(spec)
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}
	return string(blob)
}

// profileHarness is trackerHarness plus a real store and session id, so the
// session can resolve an assignment from the DB. The tracker holds the same
// two-room corridor, already anchored at (0,0,0).
func profileHarness(t *testing.T) (*Session, *storage.Store, int64, *[]ServerMsg, *sync.Mutex) {
	t.Helper()
	store := newProfileTestStore(t)
	rec, err := store.CreateSession("t", "127.0.0.1", 4000)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rooms := []storage.Room{
		mkTrackRoom("Z", 0, 0, 0, 1, "Первая", "one", "S"),
		mkTrackRoom("Z", 1, 0, 0, 2, "Вторая", "two", "N"),
	}
	s := &Session{
		sessionID:  rec.ID,
		store:      store,
		clients:    make(map[int]clientSink),
		mapTracker: mapper.NewTracker(buildTestMapIndex(1, rooms)),
	}

	var mu sync.Mutex
	var got []ServerMsg
	s.AttachClient("test", func(msg ServerMsg) error {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
		return nil
	})
	return s, store, rec.ID, &got, &mu
}

func msgsOfType(got *[]ServerMsg, mu *sync.Mutex, typ string) []ServerMsg {
	mu.Lock()
	defer mu.Unlock()
	var out []ServerMsg
	for _, m := range *got {
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

// --- 1. a session with NO assignment is exactly today's session -------------

// TestUnassignedSessionUsesBuiltin is the compatibility gate at the SESSION
// level (mapper's byte-identity goldens cover the parser itself): mapper_profile_id
// NULL must resolve to the builtin, parse an RMUD block exactly as before, and
// report no error.
func TestUnassignedSessionUsesBuiltin(t *testing.T) {
	s, _, _, got, mu := profileHarness(t)

	s.LoadMapperProfile()

	ref, name, warn := s.MapperProfileStatus()
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("format ref = %q, want the default builtin's", ref)
	}
	if id := s.MapperProfileRowID(); id != 0 {
		t.Errorf("profile row id = %d, want 0 (a builtin is not a row)", id)
	}
	if name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("profile name = %q, want %q", name, mapper.RMUDDetectorInfo().Title)
	}
	if warn != "" {
		t.Errorf("an unassigned session must report no problem, got %q", warn)
	}

	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok || ev.Hint != "Вторая" || ev.Exits != "N" || !strings.Contains(ev.Desc, "two") {
		t.Fatalf("builtin RMUD parse changed: ok=%v ev=%+v", ok, ev)
	}
	if pos := s.mapTracker.Position(); pos.Coord.X != 1 || pos.Confidence != mapper.Green {
		t.Errorf("expected the usual green step to x=1, got %+v", pos)
	}
	if msgs := msgsOfType(got, mu, "mapper_profile"); len(msgs) != 1 || msgs[0].MapperProfileError != "" {
		t.Errorf("mapper_profile broadcasts = %+v, want exactly one clean one", msgs)
	}
}

// --- 2. an assignment actually changes what the session parses --------------

func TestAssignedProfileChangesParsing(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)

	prof, err := store.CreateMapperProfile("english", englishSpecJSON(t, "english"))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	if ref, name, warn := s.MapperProfileStatus(); ref != storage.MapperFormatDBRef(prof.ID) || name != "english" || warn != "" {
		t.Fatalf("status = (%q,%q,%q), want the assigned profile and no error", ref, name, warn)
	}

	// An RMUD block must now produce NOTHING — this profile does not know it.
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if _, ok := s.LastRoomEvent(); ok {
		t.Error("the english profile must not recognize an RMUD exits line")
	}

	// Its own format must parse.
	for _, l := range []string{
		"<100hp 50m>",
		"The Old Cobbled Road",
		"  [ ]-[ ]   Ancient stones pave this road.",
		"Obvious exits: north, east.",
	} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok {
		t.Fatal("the english profile did not parse its own block")
	}
	if ev.Hint != "The Old Cobbled Road" || ev.Exits != "north, east" {
		t.Errorf("event = %+v", ev)
	}
	if ev.Desc != "Ancient stones pave this road." {
		t.Errorf("desc = %q — fixed_width(12) strip did not apply", ev.Desc)
	}
}

// --- 3. THE footgun: a live swap must not disturb the tracker ---------------

// TestSwapProfileOnLiveSessionPreservesTrackerState is the load-bearing test of
// this contract. Changing the room format rebuilds ONLY the accumulator. The
// tracker's position, confidence, pending-move queue and index are a separate
// object and must come through untouched — the same failure class as the
// annotation-DT reset and the topology-write index rebuild, both of which were
// fixed by NOT rebuilding the tracker. A regression here shows up as the player
// going 🔴 every time they touch a settings dropdown.
func TestSwapProfileOnLiveSessionPreservesTrackerState(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	s.LoadMapperProfile() // start on the builtin

	// Walk: anchor, step S, and confirm it with a real RMUD block.
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	// Then queue a move that is still in flight when the profile changes.
	s.mapTracker.PushMove("N")

	beforePos := s.mapTracker.Position()
	beforePending := s.mapTracker.PendingCount()
	beforeIdx := s.mapTracker.Index()
	if !beforePos.Valid || beforePos.Coord.X != 1 || beforePos.Confidence != mapper.Green {
		t.Fatalf("precondition: expected a confirmed green position at x=1, got %+v", beforePos)
	}
	if beforePending != 1 {
		t.Fatalf("precondition: expected 1 in-flight move, got %d", beforePending)
	}
	if beforeIdx == nil {
		t.Fatal("precondition: expected a tracker index")
	}

	// Swap the room format on the LIVE session.
	prof, err := store.CreateMapperProfile("english", englishSpecJSON(t, "english"))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	afterPos := s.mapTracker.Position()
	if afterPos.Coord != beforePos.Coord {
		t.Errorf("coord moved across a profile swap: %v -> %v", beforePos.Coord, afterPos.Coord)
	}
	if afterPos.Confidence != beforePos.Confidence {
		t.Errorf("confidence changed across a profile swap: %q -> %q", beforePos.Confidence, afterPos.Confidence)
	}
	if !afterPos.Valid {
		t.Error("position became invalid across a profile swap")
	}
	if n := s.mapTracker.PendingCount(); n != beforePending {
		t.Errorf("pending queue changed across a profile swap: %d -> %d", beforePending, n)
	}
	if s.mapTracker.Index() != beforeIdx {
		t.Error("the tracker index was rebuilt by a profile swap — it must be left alone entirely")
	}
	if _, ok := s.LastRoomEvent(); !ok {
		t.Error("the retained live room must survive a profile swap")
	}

	// And the NEW rules are in force afterwards.
	sd, ok := s.mapDetector.(*mapper.SpecDetector)
	if !ok {
		t.Fatalf("detector = %T, want the declarative one built from the assigned row", s.mapDetector)
	}
	if !strings.Contains(sd.Profile().Name(), "english") {
		t.Errorf("detector profile = %q, want the newly assigned one", sd.Profile().Name())
	}
}

// TestSwapMidBlockDoesNotEmitATruncatedEvent covers the swap's OTHER victim.
// The replacement accumulator starts with an empty ring, so if the swap lands
// part-way through a room block the exits line still arrives and still completes
// a "block" — a headerless one. That event is not harmlessly ignored: an empty
// hint reads as a pipe corridor, so the tracker answers with yellow / "no hint
// to reconcile" and demotes a confirmed green player, blocking «Записать
// комнату» until the next clean block. Before this test path existed nothing could trigger
// it on demand; a settings dropdown can.
func TestSwapMidBlockDoesNotEmitATruncatedEvent(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	s.LoadMapperProfile()

	// Walk to a confirmed green position.
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if pos := s.mapTracker.Position(); pos.Confidence != mapper.Green {
		t.Fatalf("precondition: expected green, got %+v", pos)
	}

	// A new block starts arriving...
	s.mapTracker.PushMove("N")
	s.observeIncomingLine("Первая", "Первая")
	s.observeIncomingLine(".[ ].....      one", ".[ ].....      one")

	// ...and the user changes the room format RIGHT NOW, mid-block.
	prof, err := store.CreateMapperProfile("rmud-copy", func() string {
		blob, mErr := mapper.MarshalSpec(mapper.RMUDSpec())
		if mErr != nil {
			t.Fatalf("MarshalSpec: %v", mErr)
		}
		return string(blob)
	}())
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	// The block's tail lands on the fresh accumulator.
	s.observeIncomingLine("[ Exits: S ]", "[ Exits: S ]")

	if pos := s.mapTracker.Position(); pos.Confidence != mapper.Green {
		t.Errorf("a mid-block profile swap demoted the player to %q (%s) — the torn block must be swallowed, not emitted headerless",
			pos.Confidence, pos.Reason)
	}

	// Exactly ONE block is lost — the torn one. The N move it would have confirmed
	// is therefore still in flight, which is the honest cost of the swap.
	if n := s.mapTracker.PendingCount(); n != 1 {
		t.Errorf("pending = %d, want the torn block's move still in flight", n)
	}

	// The very next COMPLETE block (here: the player types `look`) parses normally
	// and confirms that move — the accumulator re-arms itself, no reconnect.
	s.observeIncomingLine("", "")
	for _, l := range []string{"Первая", ".[ ].....      one", "[ Exits: S ]"} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok || ev.Hint != "Первая" {
		t.Fatalf("the block after the swap must parse normally, got ok=%v ev=%+v", ok, ev)
	}
	if pos := s.mapTracker.Position(); pos.Confidence != mapper.Green || pos.Coord.X != 0 {
		t.Errorf("expected the pending move to confirm green at x=0 after the swap, got %+v", pos)
	}
}

// TestSwapAtABlockBoundaryLosesNothing is the counterpart: when the swap lands
// BETWEEN blocks there is no torn block, so nothing may be suppressed — the very
// next room must register.
func TestSwapAtABlockBoundaryLosesNothing(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	s.LoadMapperProfile()
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})

	// A completed block leaves the accumulator at a boundary.
	for _, l := range []string{"Первая", ".[ ].....      one", "[ Exits: S ]"} {
		s.observeIncomingLine(l, l)
	}

	blob, _ := mapper.MarshalSpec(mapper.RMUDSpec())
	prof, err := store.CreateMapperProfile("rmud-copy", string(blob))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok || ev.Hint != "Вторая" {
		t.Fatalf("a swap at a block boundary must not swallow the next block, got ok=%v ev=%+v", ok, ev)
	}
	if pos := s.mapTracker.Position(); pos.Coord.X != 1 || pos.Confidence != mapper.Green {
		t.Errorf("expected the normal green step, got %+v", pos)
	}
}

// --- 4. a broken stored spec: fall back to the builtin, loudly --------------

// TestBrokenStoredSpecFallsBackToBuiltinAndReports writes an uncompilable spec
// STRAIGHT INTO THE DB, bypassing the store's gate the way a hand-edited
// mudhost.db would. The session must keep parsing (with the builtin), never
// panic, and say so on every channel the user might be looking at.
func TestBrokenStoredSpecFallsBackToBuiltinAndReports(t *testing.T) {
	s, store, sessID, got, mu := profileHarness(t)

	prof, err := store.CreateMapperProfile("victim", englishSpecJSON(t, "victim"))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	// Corrupt it behind the store's back: an exits pattern with no capture group
	// is a spec Compile rejects but JSON accepts.
	if err := store.DB().Exec(
		`UPDATE mapper_profiles SET spec = ? WHERE id = ?`,
		`{"name":"victim","exits":{"guard":"X","pattern":"^X.*$","none_token":"none"},`+
			`"block":{"stops":["blank"],"max_lines":8},`+
			`"strip":{"mode":"greedy","glyphs":"[]"},"header":{"rule":"first"}}`,
		prof.ID,
	).Error; err != nil {
		t.Fatalf("corrupting the row: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}

	s.LoadMapperProfile()

	ref, name, warn := s.MapperProfileStatus()
	if warn == "" {
		t.Fatal("a spec that does not compile must be reported, not swallowed")
	}
	if !strings.Contains(warn, "capture group") {
		t.Errorf("warning = %q, want the compiler's verbatim reason", warn)
	}
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("fell back to (%q,%q), want the default builtin", ref, name)
	}

	// The session still maps — with the builtin rules.
	s.mapTracker.Anchor(mapper.Coord{Zone: "Z", X: 0, Y: 0, L: 0})
	s.mapTracker.PushMove("S")
	for _, l := range []string{"Вторая", ".[ ].....      two", "[ Exits: N ]"} {
		s.observeIncomingLine(l, l)
	}
	if pos := s.mapTracker.Position(); pos.Coord.X != 1 {
		t.Errorf("the fallback profile must still parse RMUD, position %+v", pos)
	}

	// Reported on the wire, in the ONE client-facing channel: the typed
	// mapper_profile message, which the web client renders as a red [mapper] line.
	// A BroadcastEcho here would be a second, duplicate line whenever a client
	// happens to be attached — and would still be invisible when none is (the
	// autoconnect case), because echoes are neither delivered to zero clients nor
	// persisted. The gap for a late/reloading client is closed on the other side,
	// by sendRestoreState replaying MapperProfileStatus; see
	// TestRestoreStateCarriesMapperProfileStatus in the web package.
	msgs := msgsOfType(got, mu, "mapper_profile")
	if len(msgs) != 1 {
		t.Fatalf("mapper_profile broadcasts = %d, want exactly 1", len(msgs))
	}
	if !strings.Contains(msgs[0].MapperProfileError, "does not compile") {
		t.Errorf("broadcast error = %q, want the fallback reason", msgs[0].MapperProfileError)
	}
	if msgs[0].MapperProfileName != mapper.RMUDDetectorInfo().Title {
		t.Errorf("broadcast name = %q, want the default builtin it fell back to", msgs[0].MapperProfileName)
	}
	if msgs[0].MapperFormat != mapper.BuiltinRef(mapper.DefaultDetectorKey) {
		t.Errorf("broadcast format ref = %q, want the default builtin's", msgs[0].MapperFormat)
	}
	// The status survives in memory for the restore replay, which is what actually
	// reaches the user on the owner's autoconnect startup path.
	if _, _, live := s.MapperProfileStatus(); live == "" {
		t.Error("MapperProfileStatus must retain the warning for sendRestoreState to replay")
	}
}

// TestDanglingAssignmentFallsBackToBuiltin: the row is gone but the column still
// points at it (only reachable by a direct DB edit — DeleteMapperProfile clears
// the column). Degrade, do not fail.
func TestDanglingAssignmentFallsBackToBuiltin(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)
	if err := store.SetSessionMapperFormat(sessID, "db:9999"); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}

	s.LoadMapperProfile()

	ref, name, warn := s.MapperProfileStatus()
	if ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("status = (%q,%q), want the default builtin", ref, name)
	}
	if !strings.Contains(warn, "not found") {
		t.Errorf("warning = %q, want a not-found report", warn)
	}
}

// TestRealCompileGateRejectsBrokenSpecThroughStore closes the loop the storage
// package's stub cannot: the store, wired to the REAL mapper.Compile via the
// init-time registration, refuses a genuinely uncompilable spec.
func TestRealCompileGateRejectsBrokenSpecThroughStore(t *testing.T) {
	store := newProfileTestStore(t)

	// Guard with no possible match — Compile's silent-death check.
	spec := mapper.RMUDSpec()
	spec.Exits.Guard = "NEVER"
	blob, _ := mapper.MarshalSpec(spec)
	if _, err := store.CreateMapperProfile("bad", string(blob)); err == nil {
		t.Fatal("the store accepted a spec mapper.Compile rejects")
	} else if !strings.Contains(err.Error(), "can never match") {
		t.Errorf("error = %v, want Compile's verbatim guard message", err)
	}

	// Not even valid JSON.
	if _, err := store.CreateMapperProfile("bad2", `{`); err == nil {
		t.Fatal("the store accepted a spec that is not JSON")
	}

	// And the good one goes in.
	good, _ := mapper.MarshalSpec(mapper.RMUDSpec())
	if _, err := store.CreateMapperProfile("copy-of-builtin", string(good)); err != nil {
		t.Fatalf("a valid spec was rejected: %v", err)
	}
}

// --- 5. resolution happens ONCE, never per line -----------------------------

// TestProfileIsResolvedOncePerBuildNotPerLine proves the hot-path claim
// structurally rather than by comment: after LoadMapperProfile the assigned row
// is DELETED from the DB, and the session keeps parsing with the profile it
// already compiled. If any incoming line re-read or re-compiled the assignment,
// the parse would change (or fall back) mid-stream.
func TestProfileIsResolvedOncePerBuildNotPerLine(t *testing.T) {
	s, store, sessID, _, _ := profileHarness(t)

	prof, err := store.CreateMapperProfile("english", englishSpecJSON(t, "english"))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(sessID, storage.MapperFormatDBRef(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()

	if _, _, err := store.DeleteMapperProfile(prof.ID); err != nil {
		t.Fatalf("DeleteMapperProfile: %v", err)
	}
	if got, _ := store.GetSessionMapperFormat(sessID); got != "" {
		t.Fatalf("precondition: the delete should have cleared the assignment, got %q", got)
	}

	// No re-resolution happens on the line path, so the compiled english profile
	// is still in force even though the DB now says "builtin".
	for _, l := range []string{
		"<100hp 50m>",
		"The Old Cobbled Road",
		"Obvious exits: north.",
	} {
		s.observeIncomingLine(l, l)
	}
	ev, ok := s.LastRoomEvent()
	if !ok || ev.Hint != "The Old Cobbled Road" {
		t.Fatalf("the session re-resolved its profile on the line path: ok=%v ev=%+v", ok, ev)
	}
	if ref, _, _ := s.MapperProfileStatus(); ref != storage.MapperFormatDBRef(prof.ID) {
		t.Errorf("reported format ref = %q, want the one resolved at build time (%q)", ref, storage.MapperFormatDBRef(prof.ID))
	}

	// The change lands only when something explicitly rebuilds the detector.
	s.LoadMapperProfile()
	if ref, name, _ := s.MapperProfileStatus(); ref != mapper.BuiltinRef(mapper.DefaultDetectorKey) || name != mapper.RMUDDetectorInfo().Title {
		t.Errorf("after an explicit rebuild: (%q,%q), want the default builtin", ref, name)
	}
}

// TestLoadMapperProfileSpecRoundTripsThroughDB pins that what the store hands
// back compiles to the same rules that went in — the JSON round-trip contract
// now exercised over a real column.
func TestLoadMapperProfileSpecRoundTripsThroughDB(t *testing.T) {
	store := newProfileTestStore(t)
	in := englishSpecJSON(t, "english")
	row, err := store.CreateMapperProfile("english", in)
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	back, found, err := store.GetMapperProfile(row.ID)
	if err != nil || !found {
		t.Fatalf("GetMapperProfile: %v found=%v", err, found)
	}
	prof, err := mapper.CompileJSON(back.Spec)
	if err != nil {
		t.Fatalf("CompileJSON on a stored spec: %v", err)
	}
	var want, got mapper.ProfileSpec
	if err := json.Unmarshal([]byte(in), &want); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got = prof.Spec()
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("spec drifted through the DB:\n got %+v\nwant %+v", got, want)
	}
}
