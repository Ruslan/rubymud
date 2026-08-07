package session

import (
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// ---------------------------------------------------------------------------
// The door-opening template has to be pushed into the tracker's INDEX, and the
// index and the profile change independently:
//
//	LoadActiveMapSet          — new index, same profile (map set switched/imported)
//	reloadPreservingPosition  — new index after EVERY topology write
//	installDetectorLocked     — new profile, same index (format edited)
//	recoverFromDetectorPanic  — new (or no) profile, same index
//
// Miss any one and the promise and the behaviour come apart: the editor and the
// agent quote one command while a route sends another, or none. That divergence is
// exactly why the template stopped being a constant in shared code, so each pairing
// gets a test that fails when ITS sync call is removed.
// ---------------------------------------------------------------------------

// doorTemplate reads what a route on this session would actually fall back to.
func doorTemplate(s *Session) string {
	var out string
	s.WithMapTracker(func(t *mapper.Tracker) { out = t.Index().DoorOpenTemplate() })
	return out
}

// doorSyncHarness is profileHarness plus a real map set the session is active on,
// so LoadActiveMapSet builds a REAL index from storage rather than the harness's
// hand-made one.
func doorSyncHarness(t *testing.T) (*Session, *storage.Store) {
	t.Helper()
	s, store, _, _, _ := profileHarness(t)
	id, err := store.CreateMapSet(storage.MapSetInput{
		Name: "doors", RoomCount: 2,
		Rooms: []storage.Room{
			{Zone: "Z", X: 0, Y: 0, L: 0, Hint: "a", Exits: "(S)", EDirs: `["S"]`, Ch: 2, Doors: `["S"]`},
			{Zone: "Z", X: 1, Y: 0, L: 0, Hint: "b", Exits: "N", EDirs: `["N"]`, Ch: 1, Doors: "[]"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMapSet: %v", err)
	}
	if err := store.SetActiveMapSetID(s.sessionID, id); err != nil {
		t.Fatalf("SetActiveMapSetID: %v", err)
	}
	return s, store
}

// TestLoadActiveMapSetCarriesTheDoorTemplate — the connect / active-set-change
// pairing. LoadActiveMapSet runs BEFORE LoadMapperProfile on the connect path, so
// the sync has to happen at BOTH ends; this is the end that fires when the map set
// changes under an already-resolved profile.
func TestLoadActiveMapSetCarriesTheDoorTemplate(t *testing.T) {
	s, _ := doorSyncHarness(t)
	s.LoadMapperProfile() // rmud (the default) — it ships a door verb
	if got := doorTemplate(s); got != mapper.RMUDSpec().DoorOpen {
		t.Fatalf("precondition: template = %q, want rmud's", got)
	}
	// Now the map set is (re)loaded, which REPLACES the index the template lives on.
	s.LoadActiveMapSet()
	if got := doorTemplate(s); got != mapper.RMUDSpec().DoorOpen {
		t.Errorf("the freshly built index lost the profile's door template: %q", got)
	}
}

// TestTopologyWriteReloadKeepsTheDoorTemplate — the most load-bearing pairing and
// the one that was least protected: reloadPreservingPosition rebuilds the index
// after EVERY topology write, so without the sync the template would die on the
// player's first map edit and every route afterwards would stop opening doors.
func TestTopologyWriteReloadKeepsTheDoorTemplate(t *testing.T) {
	s, _ := doorSyncHarness(t)
	s.LoadMapperProfile()
	s.LoadActiveMapSet()
	if got := doorTemplate(s); got == "" {
		t.Fatalf("precondition: no template to lose")
	}
	// Any topology write goes through the write-path, which ends in
	// reloadPreservingPosition.
	if _, err := s.WriteTopology(storage.TopologyOp{
		Kind: storage.TopoSetEdgeAttrs, Zone: "Z", X: 0, Y: 0, L: 0, Dir: "S",
		Attrs: storage.EdgeAttrPatch{NoRoute: boolPtrS(true)},
	}); err != nil {
		t.Fatalf("WriteTopology: %v", err)
	}
	if got := doorTemplate(s); got != mapper.RMUDSpec().DoorOpen {
		t.Errorf("a map edit dropped the door template: %q", got)
	}
}

// TestDisablingMappingStopsClaimingADoorVerb — the panic path that leaves the
// session with NO detector. The route kept sending the crashed format's command
// while the status broadcast (which the map pane's editor reads) correctly said
// doors do not open.
func TestDisablingMappingStopsClaimingADoorVerb(t *testing.T) {
	s, _ := doorSyncHarness(t)
	s.LoadMapperProfile()
	s.LoadActiveMapSet()
	if got := doorTemplate(s); got == "" {
		t.Fatalf("precondition: no template to lose")
	}
	// Drive the "the fallback itself crashed" branch: mapping goes off entirely.
	s.mapMu.Lock()
	s.mapDetectorFellBack = true
	s.mapMu.Unlock()
	s.recoverFromDetectorPanic("boom")

	s.mapMu.Lock()
	disabled := s.mapDetectorDisabled
	s.mapMu.Unlock()
	if !disabled {
		t.Fatalf("precondition: mapping should be disabled after the fallback crashed")
	}
	if got := doorTemplate(s); got != "" {
		t.Errorf("mapping is off, yet routes still send %q", got)
	}
}

// TestFallingBackToTheDefaultResyncsTheDoorVerb — the other panic path: the session
// swaps to a DIFFERENT format, which may open doors differently or not at all.
// installDetectorLocked is deliberately skipped there (asking the just-crashed
// detector AtBlockBoundary() would re-enter the crash), so the sync is by hand.
func TestFallingBackToTheDefaultResyncsTheDoorVerb(t *testing.T) {
	s, store := doorSyncHarness(t)
	// Start on a format that does NOT describe doors, so the fallback to rmud is a
	// visible change rather than a no-op.
	prof, err := store.CreateMapperProfile("english", englishSpecJSON(t, "english"))
	if err != nil {
		t.Fatalf("CreateMapperProfile: %v", err)
	}
	if err := store.SetSessionMapperFormat(s.sessionID, mapper.FormatRefForDB(prof.ID)); err != nil {
		t.Fatalf("SetSessionMapperFormat: %v", err)
	}
	s.LoadMapperProfile()
	s.LoadActiveMapSet()
	if got := doorTemplate(s); got != "" {
		t.Fatalf("precondition: a format with no door verb should give %q, got %q", "", got)
	}

	s.recoverFromDetectorPanic("boom") // falls back to the builtin default (rmud)

	if got := doorTemplate(s); got != mapper.RMUDSpec().DoorOpen {
		t.Errorf("after falling back to rmud the route still refuses to open doors: %q", got)
	}
}

func boolPtrS(b bool) *bool { return &b }
