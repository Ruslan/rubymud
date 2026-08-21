package session

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// ErrNoActiveMapSet is returned by the topology write-path when the session has
// no (or a cleared) active map set — the caller soft-fails with a reason.
var ErrNoActiveMapSet = errors.New("no active map set for this session")

// (exported for the web layer to distinguish the soft-fail from a real error.)

// This file wires the mapper position tracker into the session. Design
// constraints:
//   - The room-block parser runs in processLine (a latency-tracked HOT path) but
//     stays off the DB/critical path: a cheap prefix guard runs first, and the
//     bounded accumulator only does regex/reconstruction on the rare exits line.
//   - The tracker index is rebuilt from storage (never drifted) on connect /
//     active-set change / import affecting the active set (AGENTS #2).
//   - Broadcasts (room, room_position) go through broadcastMsg, off the hot path.

// LoadActiveMapSet (re)builds the tracker index from the session's active map
// set. Call on connect, active-set change, and after an import that affects the
// active set. It reads the DB, so it MUST run off the incoming-line hot path.
// A missing/NULL/dangling set degrades gracefully to an empty (nil-index)
// tracker (soft red), no panics.
func (s *Session) LoadActiveMapSet() {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	idx := s.buildActiveIndex()
	// A route's PathSteps belong to the previous graph. Invalidate before the
	// index swap, preventing a confirmation callback from dispatching its tail
	// against the new active set.
	s.invalidateMapWalk("active map changed")

	s.mapMu.Lock()
	recordingWasActive := s.mapRecordingMode != "" && s.mapRecordingMode != MapRecordingOff
	s.stopMapRecordingLocked("active map changed")
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndex(idx)
	}
	s.syncDoorOpenLocked()
	recordingState := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if recordingWasActive {
		s.broadcastMsg(mapRecordingMsg(recordingState))
	}
}

// SetActiveMapSet serializes a user-visible map switch against reconciliation's
// off-mapMu storage transaction, so either the switch wins before reservation or
// the committed reconciliation is finalized before the switch proceeds.
func (s *Session) SetActiveMapSet(id int64) error {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	if err := s.store.SetActiveMapSetID(s.sessionID, id); err != nil {
		return err
	}
	s.LoadActiveMapSetUnlocked()
	return nil
}

func (s *Session) LoadActiveMapSetUnlocked() {
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()
	idx := s.buildActiveIndex()
	s.invalidateMapWalk("active map changed")
	s.mapMu.Lock()
	recordingWasActive := s.mapRecordingMode != "" && s.mapRecordingMode != MapRecordingOff
	s.stopMapRecordingLocked("active map changed")
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndex(idx)
	}
	s.syncDoorOpenLocked()
	state := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if recordingWasActive {
		s.broadcastMsg(mapRecordingMsg(state))
	}
}

// syncDoorOpenLocked points the freshly-installed index at the session's room-format
// profile door-opening template (mapper.ProfileSpec.DoorOpen), so a route can open a
// door the map has no per-edge command for.
//
// It runs at BOTH ends of the pairing — every index swap and every profile install —
// because the two change independently: switching map sets rebuilds the index under
// the same profile, and editing the room format replaces the profile under the same
// index. Setting it in only one place leaves a route sending the previous mud's
// door verb, or none at all, until the other happened to fire.
//
// Caller holds mapMu; both s.mapDetector and the tracker's index are read under it.
func (s *Session) syncDoorOpenLocked() {
	if s.mapTracker == nil {
		return
	}
	s.mapTracker.Index().SetDoorOpenTemplate(mapper.DetectorDoorOpen(s.mapDetector))
}

// buildActiveIndex reads the session's active set from storage and builds its
// tracker index (with the DT-annotation overlay applied), or nil when there is
// no/dangling active set. It performs DB reads, so it MUST run off the hot path
// and OUTSIDE mapMu (callers take the lock only to swap the built index in).
func (s *Session) buildActiveIndex() *mapper.Index {
	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		log.Printf("mapper: GetActiveMapSetID failed: %v", err)
	}
	if !ok || setID <= 0 {
		return nil
	}
	set, err := s.store.GetMapSet(setID)
	if err != nil {
		log.Printf("mapper: GetMapSet(%d) failed: %v", setID, err)
		return nil
	}
	rooms, err := s.store.ListRooms(setID)
	if err != nil {
		log.Printf("mapper: ListRooms(%d) failed: %v", setID, err)
		return nil
	}
	// map_edges is the sole runtime topology authority. A failed read is not an
	// invitation to reinterpret legacy importer data/edirs in memory: leave the
	// map unavailable and require the explicit backfill/rematerialize repair.
	edges, err := s.store.ListMapEdges(setID)
	if err != nil {
		log.Printf("mapper: ListMapEdges(%d) failed: %v", setID, err)
		return nil
	}
	idx := mapper.BuildIndexWithEdges(setID, rooms, edges)
	idx.TopologyRevision = set.TopologyRevision
	// Overlay DT annotations onto the effective is_dt so mud_path's DT-refusal and
	// mud_look_map's DT display treat an annotated cell as a death trap.
	// Annotations are a separate overlay path — they never fork the set; the index
	// is rebuilt on annotations_changed.
	annos, err := s.store.ListRoomAnnotations(setID, "")
	if err != nil {
		log.Printf("mapper: ListRoomAnnotations(%d) failed: %v", setID, err)
		return idx
	}
	var dt []mapper.Coord
	for _, a := range annos {
		if a.DT {
			dt = append(dt, mapper.Coord{Zone: a.Zone, X: a.X, Y: a.Y, L: a.L})
		}
	}
	idx.ApplyAnnotationDT(dt)
	return idx
}

// --- room-format resolution -----------------------------------------------
//
// A session parses with a mapper.RoomDetector. Which one is the
// `sessions.mapper_format` TEXT column (migration 015):
//
//	NULL            -> the default compiled-in detector (mapper.DefaultDetectorKey
//	                   == "rmud"). This is the normal state and must behave exactly
//	                   as existing sessions expect.
//	builtin:<key>   -> a registered detector: a declarative builtin OR a
//	                   contributed Go one. The session cannot tell them apart, and
//	                   that is the point.
//	db:<id>         -> a user row in mapper_profiles, compiled to a SpecDetector.
//
// COST MODEL. Resolution — a DB read, a JSON unmarshal and a regex compile —
// happens ONCE per detector (re)build: on connect, on assignment change, and when
// the assigned profile row is edited. It NEVER happens per incoming line. The
// per-line cost in observeIncomingLine is a nil check on mapDetector, one
// interface dispatch into Feed, and the detector's own guard (for the declarative
// path: one prefix compare + one ring append on a non-exits line).

// LoadMapperProfile resolves the session's room-format reference and swaps in a
// fresh detector built from it. Call on connect, after the assignment changes,
// and after the assigned profile row is edited or deleted (AGENTS #2 — a SQLite
// admin edit must reach the live session with no restart).
//
// The name is retained because it is called from six places including the web
// layer; renaming it would churn far more than it clarifies.
//
// WHAT IT DOES NOT TOUCH: the tracker. Position, confidence, the pending-move
// queue and the index all live on s.mapTracker, which this function never reads
// or writes. Only mapDetector is replaced. This is the same footgun class as the
// annotation-DT reset and the topology-write index rebuild (both solved by NOT
// rebuilding the tracker); TestSwapProfileOnLiveSessionPreservesTrackerState is
// the permanent guard.
//
// The in-flight state inside the OLD detector is dropped, so a block that is
// half-arrived when the swap happens is lost. That loss is made SILENT on
// purpose: the replacement is told to suppress it (mapper.BoundaryAware), because
// the block's tail would otherwise still reach the fresh detector, complete a
// headerless "block", and be read by the tracker as a pipe corridor — demoting a
// confirmed green player to yellow every time they touch the settings dropdown.
// Covered by TestSwapMidBlockDoesNotEmitATruncatedEvent.
//
// Performs DB reads, so it MUST run off the incoming-line hot path. Resolution
// runs OUTSIDE mapMu; the lifecycle reservation is acquired before resolving so
// an in-flight autorecord cannot commit after the profile change returns. Only
// the swap is under mapMu, and the client broadcast happens after the locks are
// released.
func (s *Session) LoadMapperProfile() {
	// Serialize the whole resolve+swap against other loads. Without this, a
	// profile edit and an assignment change can interleave resolve/resolve/
	// swap/swap and leave the session running the OLDER profile while the DB says
	// otherwise — a lost update that only a reconnect would clear. Serializing
	// makes the second caller read the DB after the first has finished, so it
	// always sees the newer state.
	//
	// This is deliberately NOT mapMu: resolution does DB reads, and mapMu is the
	// lock a concurrent MCP mud_where / hot-path broadcast contends on. The
	// global order is lifecycle -> profile -> mapMu; autorecord uses lifecycle ->
	// topology -> mapMu only for the final epoch check, and never the reverse.
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	s.mapProfileMu.Lock()
	defer s.mapProfileMu.Unlock()
	s.mapAutorecordCommitMu.Lock()
	defer s.mapAutorecordCommitMu.Unlock()

	res := s.resolveDetector()

	s.mapMu.Lock()
	recordingWasActive := s.mapRecordingMode != "" && s.mapRecordingMode != MapRecordingOff
	s.stopMapRecordingLocked("room format changed")
	// An explicit (re)load is the user's "try this again": clear the crash latch so
	// a fixed detector gets a fresh chance, and let installDetectorLocked decide
	// whether mapping ends up disabled.
	s.mapDetectorFellBack = false
	s.mapDetectorDisabled = false
	s.installDetectorLocked(res)
	msg := s.mapperProfileMsgLocked()
	warn := s.mapProfileErr
	recordingState := s.mapRecordingStateLocked()
	s.mapMu.Unlock()
	if recordingWasActive {
		s.broadcastMsg(mapRecordingMsg(recordingState))
	}

	if warn != "" {
		// A broken profile fails INVISIBLY otherwise: the session keeps running and
		// keeps mapping, just with the wrong (builtin) rules.
		log.Printf("mapper: %s", warn)
	}
	// ONE client-facing channel: the typed mapper_profile message, which the web
	// client renders as a red [mapper] line when it carries an error.
	//
	// This broadcast alone is NOT enough, and an echo here would not have helped:
	// on the owner's actual startup path (`make run` passes --mud) Manager.Connect
	// — and so this function — runs BEFORE the HTTP server listens, so there are
	// zero attached clients and both broadcastMsg and BroadcastEcho drop silently;
	// the echo is not persisted to the log store either, so it would not come back
	// on reload. The status is therefore ALSO replayed from sendRestoreState on
	// every connect and page reload, out of MapperProfileStatus. Between the two,
	// the message reaches the user whether or not anyone was listening when the
	// profile was resolved.
	s.broadcastMsg(msg)
}

// resolvedDetector is what resolution produces: the detector to install plus
// everything the status line needs to explain it.
type resolvedDetector struct {
	det mapper.RoomDetector
	// ref is the reference actually honoured (NOT necessarily the stored one — on
	// a fallback it is the default's).
	ref string
	// profileID is the mapper_profiles row id for a db: reference, else 0.
	profileID int64
	// name is the display name shown in the status line.
	name string
	// warn is non-empty only when the stored assignment could NOT be honoured.
	warn string
}

// defaultDetector builds the default (compiled-in) detector. It is the fallback
// for every failure path.
//
// It can still come back with a NIL det — if the default itself were unregistered
// or its factory panicked. That case does NOT degrade to "the hot path's nil
// check" (an earlier comment here claimed it did; feedDetector(nil, ...) would
// have panicked, and paired with the lazy re-install in observeIncomingLine that
// is an unbounded loop dumping a stack per incoming line). Instead a nil det
// makes installDetectorLocked DISABLE mapping for the session, with the warning
// on screen. See mapDetectorDisabled.
func (s *Session) defaultDetector() resolvedDetector {
	det, info, err := mapper.SafeNewDetector(mapper.DefaultDetectorKey)
	res := resolvedDetector{det: det, ref: mapper.BuiltinRef(mapper.DefaultDetectorKey), name: info.Title}
	if err != nil {
		// Nothing left to fall back TO. Say so rather than pretending.
		res.name = mapper.DefaultDetectorKey
		res.warn = fmt.Sprintf("the default room format (%s) could not be built: %v — this session cannot map rooms",
			mapper.BuiltinRef(mapper.DefaultDetectorKey), err)
	}
	return res
}

// resolveDetector turns the session's stored mapper_format reference into a live
// detector. It NEVER fails: every error path degrades to the default and returns
// a human-readable warning instead, because a session that cannot parse rooms is
// strictly worse than one parsing them with the default rules — and a zero
// Profile (an ignored Compile error) would map nothing at all while looking fine.
//
// Runs OFF the hot path: DB read, JSON unmarshal, regex compile.
func (s *Session) resolveDetector() resolvedDetector {
	def := s.defaultDetector()
	if s.store == nil {
		// Bare session (unit-test harness): no DB to consult.
		return def
	}

	ref, err := s.store.GetSessionMapperFormat(s.sessionID)
	if err != nil {
		def.warn = fmt.Sprintf("could not read this session's room format: %v — falling back to %q", err, def.name)
		return def
	}

	kind, key, id, ok := mapper.ParseFormatRef(ref)
	if !ok {
		// A hand-edited or newer-build value. Say what we did not understand: this is
		// the one place the user can see that their column is malformed.
		def.warn = fmt.Sprintf("room format reference %q is not understood — falling back to %q", ref, def.name)
		return def
	}

	switch kind {
	case mapper.RefDefault:
		return def

	case mapper.RefBuiltin:
		// SafeNewDetector, not NewDetector: the factory and Describe() are both
		// third-party code, and this runs on the main goroutine before the HTTP
		// server listens. An unguarded panic here is an unbootable install.
		det, info, err := mapper.SafeNewDetector(key)
		if err != nil {
			// Either a detector this build does not link (an older binary meeting a
			// newer build's reference, or a contribution that was reverted), or one
			// whose construction blew up. Both degrade the same way.
			def.warn = fmt.Sprintf("room format %s could not be loaded: %v — falling back to %s (%s)",
				ref, err, def.name, def.ref)
			return def
		}
		return resolvedDetector{det: det, ref: ref, name: info.Title}

	default: // mapper.RefDB
		row, found, err := s.store.GetMapperProfile(id)
		if err != nil {
			def.warn = fmt.Sprintf("could not load room format #%d: %v — falling back to %q", id, err, def.name)
			return def
		}
		if !found {
			// Dangling assignment (the row was deleted out from under us). The delete
			// path clears the column, so this only happens on a direct DB edit.
			def.warn = fmt.Sprintf("room format #%d not found — falling back to %q", id, def.name)
			return def
		}
		prof, err := mapper.CompileJSON(row.Spec)
		if err != nil {
			// Should be unreachable through the app (the store compile-checks every
			// write) — reachable by hand-editing the DB or by a spec written by a newer
			// build. Report the compiler's verbatim message: it names the failing rule.
			def.warn = fmt.Sprintf("room format %q (#%d) does not compile: %v — falling back to %q",
				row.Name, id, err, def.name)
			return def
		}
		det := mapper.NewSpecDetector(prof, mapper.DetectorInfo{
			Key:         ref,
			Title:       row.Name,
			MUD:         "",
			Provenance:  mapper.ProvenanceUser,
			Description: "A room format written in this installation's settings.",
		})
		return resolvedDetector{det: det, ref: ref, profileID: row.ID, name: row.Name}
	}
}

// installDetectorLocked swaps in a resolved detector. Caller holds mapMu.
//
// If the OUTGOING detector was part-way through a block, the replacement would
// otherwise emit the block's tail as a headerless event and demote the player to
// yellow. Both sides of that are OPTIONAL behaviour (mapper.BoundaryAware): a
// contributed detector that does not implement it simply does not get the
// suppression, which costs at most one torn block on the rare occasion a user
// changes format mid-room. See Accumulator.SuppressUntilBoundary.
func (s *Session) installDetectorLocked(res resolvedDetector) {
	// Both AtBlockBoundary and SuppressUntilBoundary are third-party code reached
	// from LoadMapperProfile, i.e. from the main goroutine at startup. Guarded.
	mapper.SafeSuppressUntilBoundary(s.mapDetector, res.det)

	s.mapDetector = res.det
	// A resolution that produced no detector at all leaves the session unable to
	// map. Disable it explicitly instead of leaving a nil in the hot path, and let
	// an explicit LoadMapperProfile re-enable it.
	s.mapDetectorDisabled = res.det == nil
	s.mapFormatRef = res.ref
	s.mapProfileID = res.profileID
	s.mapProfileName = res.name
	s.mapProfileErr = res.warn
	// The new profile may open doors differently (or not describe them at all).
	s.syncDoorOpenLocked()
}

// mapperProfileMsgLocked builds the room-format status broadcast. Caller holds
// mapMu; send it after releasing the lock.
func (s *Session) mapperProfileMsgLocked() ServerMsg {
	return ServerMsg{
		Type:               "mapper_profile",
		MapperFormat:       s.mapFormatRef,
		MapperProfileID:    s.mapProfileID,
		MapperProfileName:  s.mapProfileName,
		MapperProfileError: s.mapProfileErr,
		MapperDoorOpen:     mapper.DetectorDoorOpen(s.mapDetector),
	}
}

// MapperProfileStatus reports which room format the live session is parsing
// with, and the fallback warning if the assignment could not be honoured
// (empty when it was). Read-only; used by the REST layer so the settings UI can
// show what the SESSION actually ended up with rather than what the DB says it
// should be.
//
// `ref` is the reference the session ACTUALLY honoured, which on a fallback is
// the default's rather than the stored one — that difference, paired with a
// non-empty warning, is exactly what the settings UI renders.
func (s *Session) MapperProfileStatus() (ref string, name string, warning string) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return s.mapFormatRef, s.mapProfileName, s.mapProfileErr
}

// MapperDoorOpenTemplate reports the door-opening command template the session's
// room format supplies, or "" when it describes none. Read-only; the restore-state
// replay carries it to the map pane, which must show what a route WILL send rather
// than a constant of its own.
func (s *Session) MapperDoorOpenTemplate() string {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return mapper.DetectorDoorOpen(s.mapDetector)
}

// MapperProfileRowID reports the mapper_profiles row id the session resolved to,
// or 0 when it is parsing with a compiled-in detector. Separate from
// MapperProfileStatus because only the REST layer's back-compat field needs it.
func (s *Session) MapperProfileRowID() int64 {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return s.mapProfileID
}

// observeIncomingLine is the mapper hook called from processLine for every
// incoming line. It is intentionally cheap: it feeds the detector (which does a
// cheap guard before any regex) and, only when a full room block completes, runs
// tracker reconciliation and broadcasts. It also watches for movement refusals
// ("не можете идти") to head-cancel the pending queue. No DB access.
//
// `plainText` is the already-ANSI/control-stripped line and `raw` is the same
// line with its escape sequences intact; processLine computes BOTH one call
// above this one for its own purposes, so passing both is free (see
// mapper.Line). The detector consumes them directly — NO second ANSI strip on
// the hot path.
//
// Client broadcasts are built while mapMu is held but sent AFTER the lock is
// released, so a slow/stuck WS client never stalls under the map lock (which
// would block a concurrent MCP mud_where).
func (s *Session) observeIncomingLine(plainText, raw string) {
	// A route hop publishes its FIFO entry and expected landing after its socket
	// write succeeds. Hold parser reconciliation behind that short protocol so a
	// very fast response cannot land in the former write→publish gap. Close does
	// not acquire this lock and can therefore interrupt a blocked Write.
	s.mapWalkIOMu.RLock()
	gateHeld := true
	releaseWalkIOGate := func() {
		if gateHeld {
			s.mapWalkIOMu.RUnlock()
			gateHeld = false
		}
	}
	var scanAfterBoundary *mapScanJob
	defer func() {
		releaseWalkIOGate()
		if scanAfterBoundary != nil {
			go func(job mapScanJob) {
				defer s.recoverGoroutine("mapper scan writer")
				s.writeMapScan(job)
			}(*scanAfterBoundary)
		}
	}()

	var toBroadcast []ServerMsg
	var walkPosition *mapper.Position
	recordingConflict := false

	s.mapMu.Lock()
	scanAfterBoundary = s.takeMapScanAfterRoomBoundaryLocked(plainText)
	scanMsg, scanLine := s.consumeMapScanLineLocked(plainText)
	if scanMsg != nil {
		toBroadcast = append(toBroadcast, *scanMsg)
	}
	if scanLine {
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		return
	}

	// Movement refusal: head-cancel the queue, no position move. Cheap substring
	// check; only meaningful when a tracker exists.
	if s.mapTracker != nil && mapper.IsRefusal(plainText) {
		beforeRecordingRevision := s.mapRecordingRevision
		trackerCanceled := s.mapTracker.CancelHead()
		if len(s.mapRecordingPending) > 0 {
			s.cancelAcceptedMapMoveLocked()
		}
		if trackerCanceled {
			toBroadcast = append(toBroadcast, s.roomPositionMsgLocked())
		}
		if s.mapRecordingRevision != beforeRecordingRevision {
			toBroadcast = append(toBroadcast, mapRecordingMsg(s.mapRecordingStateLocked()))
		}
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		releaseWalkIOGate()
		s.ObserveMapWalkRefusal()
		return
	}

	// Mapping disabled for this session (the detector panicked and so did its
	// replacement, or no detector could be built at all). Do NOT lazily re-install:
	// that is exactly the loop this flag exists to stop.
	if s.mapDetectorDisabled {
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		return
	}
	if s.mapDetector == nil {
		def := s.defaultDetector()
		if def.det == nil {
			s.mapDetectorDisabled = true
			s.syncDoorOpenLocked()
			s.mapMu.Unlock()
			s.broadcastAll(toBroadcast)
			return
		}
		s.mapDetector = def.det
		// Transient — the next LoadMapperProfile corrects it either way — but a session
		// that maps for a while off this lazily installed default would meanwhile route
		// with whatever template the PREVIOUS detector had. Two assignments, one place.
		s.syncDoorOpenLocked()
	}
	ev, ok, panicked := feedDetector(s.mapDetector, mapper.Line{Stripped: plainText, Raw: raw})
	if panicked != nil {
		// THE BLAST-RADIUS BOUNDARY. A contributed detector is
		// third-party code running in the session's read loop; a panic in it must cost
		// this session its mapping and nothing else. Handled with mapMu released,
		// because the recovery takes mapProfileMu and the lock order is
		// mapProfileMu -> mapMu.
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		s.recoverFromDetectorPanic(panicked)
		return
	}
	if !ok {
		s.mapMu.Unlock()
		s.broadcastAll(toBroadcast)
		return
	}

	// Retain the live room so a human "record this room" write can reuse the
	// game's own text. Two field assignments under a lock we
	// already hold — no allocation, no extra work on the common (non-exits) line,
	// which returns above without reaching here.
	s.lastRoom = ev
	s.lastRoomOK = !ev.Obscured

	// Room event to clients (UI current-room signal).
	toBroadcast = append(toBroadcast, ServerMsg{
		Type:      "room",
		RoomHint:  ev.Hint,
		RoomDesc:  ev.Desc,
		RoomExits: ev.Exits,
	})

	var autorecordJob *mapAutorecordJob
	if s.mapTracker != nil {
		beforeRecordingRevision := s.mapRecordingRevision
		var conflicted bool
		conflicted, autorecordJob = s.observeMapRecordingRoomLockedForAutorecord(ev)
		recordingConflict = conflicted
		pos, changed := s.mapTracker.Reconcile(ev)
		walkPosition = &pos
		if changed {
			toBroadcast = append(toBroadcast, s.roomPositionMsgLocked())
		}
		if !conflicted && (s.mapRecordingMode == MapRecordingArmed || s.mapRecordingMode == MapRecordingRecording) && (!pos.Valid || pos.Confidence == mapper.Red) {
			s.stopMapRecordingLocked("position lost")
		}
		if s.mapRecordingRevision != beforeRecordingRevision {
			toBroadcast = append(toBroadcast, mapRecordingMsg(s.mapRecordingStateLocked()))
		}
	}
	s.mapMu.Unlock()
	// The room parser has finished its mapMu section. The single detached write
	// keeps SQLite off the incoming-line path; OBS admits no second step meanwhile.
	if autorecordJob != nil {
		s.startMapAutorecord(*autorecordJob)
	}

	s.broadcastAll(toBroadcast)
	s.scheduleMapScanAfterRoomBoundary()
	if walkPosition != nil {
		releaseWalkIOGate()
		if recordingConflict {
			s.stopMapWalkForZoneMove("map recording conflict")
			return
		}
		s.ObserveMapWalkPosition(*walkPosition)
	}
}

// --- the detector panic boundary -------------------------------------------
//
// A contributed Go detector is reviewed, but review is not a guarantee. Feed
// runs inside the session's read loop, so an index-out-of-range in somebody's
// contribution would otherwise take down the loop that reads the MUD socket —
// the player's whole connection — for a mapper bug.
//
// So the call is wrapped. The cost of that wrapper is the point of contention:
// `defer` in a function this hot used to be expensive, but an open-coded defer
// (no loops, no conditionals around it) is a few nanoseconds and ZERO
// allocations. TestObserveIncomingLineDoesNotAllocate and the hot-path benchmark
// in the mapper package are the permanent guards on that claim; the
// BenchmarkFeedNonExitsLineViaDetector benchmark and the allocation test cover
// the bounded overhead without relying on an external report.

// feedDetector calls det.Feed under recover(). Returns the event and ok as
// normal, or a non-nil `panicked` carrying the recovered value.
//
// It is a free function rather than a method so it captures nothing but its two
// arguments: the deferred closure must not keep the *Session alive or the
// wrapper starts allocating.
func feedDetector(det mapper.RoomDetector, line mapper.Line) (ev mapper.RoomEvent, ok bool, panicked any) {
	defer func() {
		if r := recover(); r != nil {
			ev, ok, panicked = mapper.RoomEvent{}, false, r
		}
	}()
	ev, ok = det.Feed(line)
	return ev, ok, nil
}

// recoverFromDetectorPanic is the response to a panicking detector: log it with
// a stack, fall THIS session back to the default detector, and report through
// the SAME channel a non-compiling spec already uses — the mapper_profile
// message plus MapperProfileStatus, which sendRestoreState replays on every
// connect and page reload so the notice survives having no client attached.
//
// It must NOT be called with mapMu held: it takes mapProfileMu, and the lock
// order is mapProfileMu -> mapMu.
//
// The session's stored assignment is deliberately NOT rewritten. The fallback is
// in-memory and lasts until the next LoadMapperProfile, so a detector fixed by
// the next build starts working again on reconnect without the user having to
// re-pick their format — and the DB never silently loses what they chose.
func (s *Session) recoverFromDetectorPanic(r any) {
	s.mapProfileMu.Lock()
	defer s.mapProfileMu.Unlock()

	s.mapMu.Lock()
	failed := s.mapProfileName
	failedRef := s.mapFormatRef
	// alreadyFallenBack means THIS session has already been moved onto the default
	// once by a previous panic — so the thing that just panicked IS the default (or
	// whatever replaced it), and installing another instance of it would panic
	// again on the very next line.
	alreadyFallenBack := s.mapDetectorFellBack
	s.mapMu.Unlock()

	if alreadyFallenBack {
		// STOP, for real. The previous version of this branch logged "stopped
		// mapping" and returned WITHOUT touching s.mapDetector — so the panicking
		// detector stayed installed and every subsequent line panicked, recovered and
		// logged again: 100 lines produced 100 stack dumps and 200 mutex
		// acquisitions. It neither stopped nor avoided spinning.
		//
		// Disabling is what actually stops it: the detector is dropped and the hot
		// path returns early on mapDetectorDisabled until an explicit
		// LoadMapperProfile re-enables mapping.
		s.mapMu.Lock()
		wasDisabled := s.mapDetectorDisabled
		beforeRecordingRevision := s.mapRecordingRevision
		s.stopMapRecordingLocked("room format detector crashed")
		s.mapDetector = nil
		s.mapDetectorDisabled = true
		s.mapProfileErr = fmt.Sprintf("the fallback room format %s also crashed (%v) — "+
			"room mapping is off for this session until you pick a format again. "+
			"Everything else about the session is unaffected.", failedRef, r)
		// There is no format any more, so there is no door verb either. Without this the
		// route kept sending the CRASHED format's command while the editor (which reads
		// the broadcast below) correctly said doors do not open — promise and behaviour
		// pulled apart, which is the whole failure mode that took this template out of a
		// global constant.
		s.syncDoorOpenLocked()
		msg := s.mapperProfileMsgLocked()
		recordingState := s.mapRecordingStateLocked()
		s.mapMu.Unlock()

		if !wasDisabled {
			// Log and broadcast ONCE, on the transition. Repeating either per line is
			// the bug this branch is supposed to prevent.
			log.Printf("mapper: the fallback room detector itself panicked (%v) — mapping disabled for this session", r)
			s.broadcastMsg(msg)
		}
		if recordingState.Revision != beforeRecordingRevision {
			s.broadcastMsg(mapRecordingMsg(recordingState))
		}
		return
	}

	log.Printf("mapper: room detector %q (%s) panicked: %v\n%s", failed, failedRef, r, debug.Stack())

	def := s.defaultDetector()
	def.warn = fmt.Sprintf("room format %q (%s) crashed while parsing a line (%v) — this session fell back to %s (%s). "+
		"This is a bug in that detector; the session is otherwise unaffected.", failed, failedRef, r, def.name, def.ref)

	s.mapMu.Lock()
	beforeRecordingRevision := s.mapRecordingRevision
	s.stopMapRecordingLocked("room format detector crashed")
	// Deliberately NOT installDetectorLocked: the outgoing detector just panicked,
	// so asking it AtBlockBoundary() would re-enter the code that crashed.
	s.mapDetector = def.det
	s.mapDetectorFellBack = true
	s.mapDetectorDisabled = def.det == nil
	s.mapFormatRef = def.ref
	s.mapProfileID = 0
	s.mapProfileName = def.name
	s.mapProfileErr = def.warn
	// The session is on a DIFFERENT format now, which may open doors differently or
	// not at all. installDetectorLocked (which does this) is deliberately skipped
	// here — asking the just-crashed detector AtBlockBoundary() would re-enter the
	// code that crashed — so the sync has to be repeated by hand.
	s.syncDoorOpenLocked()
	msg := s.mapperProfileMsgLocked()
	recordingState := s.mapRecordingStateLocked()
	s.mapMu.Unlock()

	s.broadcastMsg(msg)
	if recordingState.Revision != beforeRecordingRevision {
		s.broadcastMsg(mapRecordingMsg(recordingState))
	}
}

// broadcastAll sends each message to clients. Called OUTSIDE mapMu.
func (s *Session) broadcastAll(msgs []ServerMsg) {
	for _, m := range msgs {
		s.broadcastMsg(m)
	}
}

// observeOutgoingMove is called from the command write path (after alias/VM
// expansion) for each command actually written to the MUD. If the command is a
// movement direction it is pushed onto the tracker's FIFO pending-moves queue.
func (s *Session) observeOutgoingMove(cmd, source string) {
	dir, ok := mapper.MoveDir(cmd)
	if !ok {
		return
	}
	s.mapMu.Lock()
	beforeRevision := s.mapRecordingRevision
	var recordingState MapRecordingState
	if s.mapTracker != nil {
		s.mapTracker.PushMove(dir)
		s.recordAcceptedMapMoveLocked(cmd, dir, source, nil)
		recordingState = s.mapRecordingStateLocked()
	}
	s.mapMu.Unlock()
	if recordingState.Revision != beforeRevision {
		s.broadcastMsg(mapRecordingMsg(recordingState))
	}
}

// roomPositionMsgLocked builds the current tracker-position ServerMsg. Caller
// must hold mapMu; the caller is responsible for sending it AFTER releasing the
// lock (do not couple the map lock to client I/O). Returns an empty ServerMsg
// (Type=="") when there is no tracker; broadcastAll/broadcastMsg on it is
// harmless but callers generally only append when there is a tracker.
func (s *Session) roomPositionMsgLocked() ServerMsg {
	if s.mapTracker == nil {
		return ServerMsg{}
	}
	pos := s.mapTracker.Position()
	msg := ServerMsg{
		Type:            "room_position",
		Confidence:      string(pos.Confidence),
		PendingMoves:    s.mapTracker.PendingCount(),
		PositionValid:   pos.Valid,
		PositionReason:  pos.Reason,
		ExitsAddedLive:  pos.ExitsAddedLive,
		ExitsRemovedMap: pos.ExitsRemovedMap,

		EventDescribesCell: pos.EventDescribesCell,
	}
	if pos.Valid {
		msg.Zone = pos.Coord.Zone
		msg.RoomX = pos.Coord.X
		msg.RoomY = pos.Coord.Y
		msg.RoomL = pos.Coord.L
		if room := s.mapTracker.CurrentRoom(); room != nil {
			msg.RoomHint = room.Hint
			msg.IsDT = room.IsDT
			msg.Pipe = room.Pipe
		}
	}
	return msg
}

// RoomPositionSnapshot returns the current tracker position for a reconnecting
// client. It uses the same message builder as live broadcasts, but releases
// mapMu before the caller performs any client I/O. The bool is false only when
// the session has not built a tracker yet; an invalid tracker position is still
// a useful snapshot and is returned as a room_position message.
func (s *Session) RoomPositionSnapshot() (ServerMsg, bool) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return ServerMsg{}, false
	}
	return s.roomPositionMsgLocked(), true
}

// --- MCP / accessor surface (read-only over tracker state) -----------------

// MapTracker exposes the tracker under lock for MCP tools. The callback runs
// while mapMu is held; it must not block or call back into the session. Returns
// false if there is no tracker yet.
func (s *Session) WithMapTracker(fn func(*mapper.Tracker)) bool {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return false
	}
	fn(s.mapTracker)
	return true
}

// LastRoomEvent returns a COPY of the most recent completed room block the
// parser saw, and whether one has been seen at all. RoomEvent is a value type
// (three strings), so the returned copy cannot alias session state.
func (s *Session) LastRoomEvent() (mapper.RoomEvent, bool) {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	return s.lastRoom, s.lastRoomOK
}

// RecordState is the COHERENT snapshot the «Записать комнату» autorecord needs:
// the live room text, the tracker position it belongs to, and what the map holds
// at that coord. Every field is read under ONE mapMu acquisition (see
// LiveRecordState) — reading the room text and the position in two separate
// locked calls could interleave with an incoming room block and pair the PREVIOUS
// room's text with the NEW position, which is precisely the write that corrupts
// a cell.
type RecordState struct {
	// Room / RoomSeen: the last completed room block, and whether one exists.
	Room     mapper.RoomEvent
	RoomSeen bool
	// HaveTracker/HaveIndex: whether there is a tracker at all and whether it has
	// an active-set index (no index => no active map set).
	HaveTracker bool
	HaveIndex   bool
	// Position is the tracker position that the Room event produced.
	Position mapper.Position
	// RoomExists reports whether the map ALREADY has a room at Position.Coord
	// (create vs update), evaluated before any write.
	RoomExists bool
	// Pending is the number of unconfirmed in-flight moves.
	Pending int
}

// LiveRecordState captures the live room + tracker state atomically for the
// record write-path. One lock acquisition, no client I/O, no DB access.
func (s *Session) LiveRecordState() RecordState {
	s.mapMu.Lock()
	defer s.mapMu.Unlock()

	st := RecordState{Room: s.lastRoom, RoomSeen: s.lastRoomOK}
	if s.mapTracker == nil {
		return st
	}
	st.HaveTracker = true
	idx := s.mapTracker.Index()
	if idx == nil {
		return st
	}
	st.HaveIndex = true
	st.Position = s.mapTracker.Position()
	st.Pending = s.mapTracker.PendingCount()
	if st.Position.Valid {
		st.RoomExists = idx.Room(st.Position.Coord) != nil
	}
	return st
}

// AnchorMapPosition re-anchors the tracker to coord c and broadcasts the new
// position — the ONE anchor path behind both the «Я здесь» REST endpoint and the
// post-record re-anchor, so the two cannot drift apart.
// Returns ok=false when there is no tracker/index (the caller soft-fails), and
// exact=true when the coord is a real cell of the active set (🟢); anchoring to
// an unmapped coord keeps the position but stays 🟡 (Tracker.Anchor's contract).
// The broadcast happens after the tracker lock is released (WithMapTracker).
func (s *Session) AnchorMapPosition(c mapper.Coord) (ok bool, exact bool) {
	var anchored bool
	found := s.WithMapTracker(func(t *mapper.Tracker) {
		if t.Index() == nil {
			return
		}
		_, exact = t.Anchor(c)
		anchored = true
	})
	if !found || !anchored {
		return false, false
	}
	s.BroadcastMapPosition()
	return true, exact
}

// BroadcastMapPosition broadcasts the current tracker position to UI clients
// (used after an MCP re-anchor). Builds the message under mapMu, then sends it
// after releasing the lock (client I/O never runs under the map lock).
func (s *Session) BroadcastMapPosition() {
	msg, ok := s.RoomPositionSnapshot()
	if ok {
		s.broadcastMsg(msg)
	}
	s.broadcastPausedMapWalkEligibility()
}

// ApplyAnnotationDT updates ONE cell's effective is_dt in the live tracker index
// in place (native is_dt || annoDT). It invalidates any route/FIFO first: a
// physical hop planned before a death-trap edit must never reconcile through the
// edited topology. Called by the MCP annotate write for dt-touching annotates
// only. Returns false when there is no tracker/index yet. Off the incoming-line
// hot path.
func (s *Session) ApplyAnnotationDT(zone string, x, y, l int, annoDT bool) bool {
	s.invalidateMapWalk("map topology changed")
	s.mapMu.Lock()
	defer s.mapMu.Unlock()
	if s.mapTracker == nil {
		return false
	}
	idx := s.mapTracker.Index()
	if idx == nil {
		return false
	}
	idx.SetAnnotationDT(mapper.Coord{Zone: zone, X: x, Y: y, L: l}, annoDT)
	return true
}

// ReloadActiveMapSet is the exported hook the web layer calls after changing the
// active set or importing rooms into it, to keep the index in sync (AGENTS #2).
func (s *Session) ReloadActiveMapSet() {
	s.LoadActiveMapSet()
	s.BroadcastMapPosition()
}

// RefreshTopologyAfterZoneMove is the cross-session counterpart of the writer's
// refresh. It translates a tracker standing in a moved cell before rebuilding.
func (s *Session) RefreshTopologyAfterZoneMove(mapSetID int64, move storage.ZoneMoveInput) {
	s.reloadPreservingPositionAfterZoneMove(move)
	s.broadcastRoomsChanged(mapSetID)
}
func (s *Session) RefreshSharedTopology(mapSetID int64) {
	s.reloadPreservingPosition()
	s.broadcastRoomsChanged(mapSetID)
}

// reloadPreservingPosition rebuilds the tracker index for the active set but
// PRESERVES the tracker's current position/confidence/pending when the current
// cell still exists in the rebuilt index: a topology write must
// not reset the tracker to 🔴 or flush pending just because the graph changed).
// It only falls to 🔴 when the current cell no longer exists (e.g. it was
// deleted). Broadcasts the (possibly unchanged) position afterward. DB reads run
// off mapMu; only the index swap is under the lock.
func (s *Session) reloadPreservingPosition() {
	s.reloadPreservingPositionWithBroadcast(true)
}

// reloadPreservingPositionWithoutBroadcast is the commit half of a compound
// autorecord publication. The caller anchors/publishes the final target after
// this index swap, so clients never observe a committed room with a stale
// yellow position in between.
func (s *Session) reloadPreservingPositionWithoutBroadcast() {
	s.reloadPreservingPositionWithBroadcast(false)
}

func (s *Session) reloadPreservingPositionWithBroadcast(broadcast bool) {
	idx := s.buildActiveIndex()
	// Topology edits may remove or redirect any remaining edge in a route.
	s.invalidateMapWalk("map topology changed")
	s.mapMu.Lock()
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		s.mapTracker.SetIndexPreservingPosition(idx)
	}
	s.syncDoorOpenLocked()
	s.mapMu.Unlock()
	if broadcast {
		s.BroadcastMapPosition()
	}
}

// TopologyWriteResult reports the outcome of a WriteTopology / UndoTopology call.
type TopologyWriteResult struct {
	// Applied is false when the target room was not found in the (editable) set —
	// callers soft-fail (200 with a reason) rather than erroring.
	Applied bool
	// Forked is true when the write forked a frozen imported set into an editable
	// copy; ForkedTo is the new editable set id the session is now active on.
	Forked   bool
	ForkedTo int64
	// SetID is the editable set the write actually landed on (the fork when Forked,
	// else the original active set).
	SetID int64
	// NothingToUndo is set by UndoTopology when the set's journal is empty.
	NothingToUndo bool
	// Portal is the exact occurrence selected inside the serialized storage
	// transaction for portal CRUD. It is nil for every other topology operation.
	Portal *storage.PortalWriteResult
	// ZoneMove is the bounded preview/report produced atomically with a move.
	ZoneMove *storage.ZoneMovePreview
}

// WriteTopology is the UNIFIED topology write-path: the ONE backend
// function UI edits and MCP CRUD funnel through. Per write it:
//  1. resolves the session's active set; if it is frozen (editable=false), forks
//     it copy-on-write, switches the session's active set to the fork, and
//     broadcasts map_sets_changed (a fork is a new set);
//  2. applies the op to the (now editable) set via the single ApplyTopologyOp;
//  3. pushes the write's BEFORE-STATE snapshot onto the in-memory per-map-set undo
//     journal (a literal prior-state record, not a symmetric inverse op);
//  4. broadcasts rooms_changed and refreshes the tracker index PRESERVING position.
//
// The WHOLE read-active-set → fork → setActive → apply → journal sequence runs
// under the shared per-map-set TopologyLock keyed on the active set id at ENTRY,
// so two sessions racing their first write to the same FROZEN set cannot both read
// editable=false and double-fork it: the second blocks on the source set's lock,
// then observes the active set is already the editable fork (no re-fork).
//
// The current orchestration wires TopoPatchExits behind map-cell-patch; other
// TopologyOpKind arms plug into ApplyTopologyOp with no change to this sequence.
// Returns Applied=false (no error) when the target
// room is absent so the caller soft-fails.
func (s *Session) WriteTopology(op storage.TopologyOp) (TopologyWriteResult, error) {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	return s.writeTopologyLocked(op)
}

// writeTopologyLocked is the unified topology write-path with the session
// lifecycle reservation already held. Keeping the reservation across the
// transaction, tracker refresh, and autorecord publication prevents an undo,
// active-set switch, or reconciliation apply from observing the short window
// between a committed room and the worker's final green anchor.
func (s *Session) writeTopologyLocked(op storage.TopologyOp) (TopologyWriteResult, error) {
	var err error
	op, err = storage.NormalizeTopologyOp(op)
	if err != nil {
		return TopologyWriteResult{}, err
	}
	for {
		entryID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
		if err != nil {
			return TopologyWriteResult{}, err
		}
		if !ok || entryID <= 0 {
			return TopologyWriteResult{}, ErrNoActiveMapSet
		}

		// A request can read entryID and then wait while another request forks and
		// switches the session. In that case the entry lock no longer protects the
		// active set: release it and restart on the id now stored in the session.
		// This also avoids ever nesting unrelated set locks in an arbitrary order.
		entryLock := s.store.TopologyLock(entryID)
		entryLock.Lock()
		res, retry, err := func() (TopologyWriteResult, bool, error) {
			defer entryLock.Unlock()
			var attempt TopologyWriteResult

			setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
			if err != nil {
				return attempt, false, err
			}
			if !ok || setID <= 0 {
				return attempt, false, ErrNoActiveMapSet
			}
			if setID != entryID {
				return attempt, true, nil
			}

			// Resolve editability; fork-if-frozen (copy-on-write).
			set, err := s.store.GetMapSet(setID)
			if err != nil {
				return attempt, false, err
			}
			var forkSourceID int64
			if !set.Editable {
				forkSourceID = setID
				newID, ferr := s.store.ForkMapSet(setID)
				if ferr != nil {
					return attempt, false, ferr
				}
				if serr := s.store.SetActiveMapSetID(s.sessionID, newID); serr != nil {
					return attempt, false, serr
				}
				setID = newID
				// This is the sole nested lock order: the frozen source, then its
				// newly-created fork. No request can hold the new lock and request its
				// source, so the order cannot form a cycle.
				forkLock := s.store.TopologyLock(newID)
				forkLock.Lock()
				defer forkLock.Unlock()
				attempt.Forked = true
				attempt.ForkedTo = newID
			}
			attempt.SetID = setID

			written, werr := s.applyTopologyToSet(setID, op, attempt, true)
			if forkSourceID != 0 && (werr != nil || !written.Applied) {
				// A refused first write must not switch the requesting session or leave
				// an orphan COW set. ApplyTopologyOp is transactional, so the fork is
				// still an exact copy and can be removed after switching back.
				if rerr := s.store.SetActiveMapSetID(s.sessionID, forkSourceID); rerr != nil {
					return written, false, errors.Join(werr, fmt.Errorf("restoring active map set after refused fork: %w", rerr))
				}
				if derr := s.store.DeleteMapSet(setID); derr != nil {
					return written, false, errors.Join(werr, fmt.Errorf("removing refused map-set fork: %w", derr))
				}
				written.Forked = false
				written.ForkedTo = 0
				written.SetID = forkSourceID
				return written, false, werr
			}
			if forkSourceID != 0 {
				// A fork becomes observable only after its first mutation committed.
				s.broadcastMapSetChanged(setID)
			}
			return written, false, werr
		}()
		if retry {
			continue
		}
		return res, err
	}
}

// applyTopologyToSet applies op to an ALREADY-EDITABLE set and refreshes the index
// preserving position. When journal is true (a fresh write) it captures the op's
// BEFORE-STATE snapshot and pushes it onto the per-set undo stack. Undo passes
// journal=false: the snapshot restore it performs is a consumed, one-way revert —
// re-journaling would turn the bounded stack into a two-state redo toggle and break
// the "nothing to undo when empty" contract. Shared so undo re-applies through the
// same single topology mutator (no parallel path). Runs under the caller's
// TopologyLock so the DB write and the journal push stay ordered.
func (s *Session) applyTopologyToSet(setID int64, op storage.TopologyOp, res TopologyWriteResult, journal bool) (TopologyWriteResult, error) {
	var portal storage.PortalWriteResult
	var zoneMove storage.ZoneMovePreview
	if op.Kind == storage.TopoCreatePortal || op.Kind == storage.TopoUpdatePortal || op.Kind == storage.TopoDeletePortal {
		op.PortalResult = &portal
	}
	if op.Kind == storage.TopoMoveRooms {
		op.ZoneMoveResult = &zoneMove
	}
	before, found, err := s.store.ApplyTopologyOp(setID, op)
	if err != nil {
		return res, err
	}
	if !found {
		return res, nil
	}
	if before.NoOp {
		res.Applied = true
		// A storage no-op changed neither topology nor revision. It must not
		// consume an undo slot: the next Undo must reverse the last real write.
		return res, nil
	}
	res.Applied = true
	var recordingStopped MapRecordingState
	recordingWasStopped := false
	if journal && op.Kind != storage.TopoAutorecordRoom {
		// A manual/admin topology edit invalidates predicted cells waiting in
		// the autorecord queue. The lifecycle lock held by WriteTopology makes
		// this epoch change atomic with the write, so no stale job can publish
		// against the edited graph afterward.
		s.mapMu.Lock()
		beforeRecordingRevision := s.mapRecordingRevision
		s.stopMapRecordingLocked("map topology changed")
		if s.mapRecordingRevision != beforeRecordingRevision {
			recordingStopped = s.mapRecordingStateLocked()
			recordingWasStopped = true
		}
		s.mapMu.Unlock()
	}
	if op.PortalResult != nil {
		res.Portal = &portal
	}
	if op.ZoneMoveResult != nil {
		res.ZoneMove = &zoneMove
	}

	if journal {
		// Journal the BEFORE-STATE snapshot, keyed to the (editable) set — NOT the
		// session. Restoring this literal prior state is a true inverse regardless of
		// the patch's idempotency.
		s.store.WriteJournal().Push(setID, storage.UndoEntry{
			Label:            op.UndoLabel(),
			Before:           before,
			ExpectedRevision: before.Revision + 1,
		})
	}

	// Refresh the tracker index (the graph changed) preserving position, and tell
	// open map panes to reload the zone (carry the set id so a pane can filter).
	if op.Kind == storage.TopoAutorecordRoom {
		// The worker publishes one coherent position/state/rooms_changed batch
		// after it anchors the newly-created target. Do not expose the transient
		// pre-anchor yellow position between the DB commit and that publication.
		s.reloadPreservingPositionWithoutBroadcast()
	} else if op.Kind == storage.TopoMoveRooms && op.ZoneMove != nil {
		s.reloadPreservingPositionAfterZoneMove(*op.ZoneMove)
	} else {
		s.reloadPreservingPosition()
	}
	if op.Kind != storage.TopoAutorecordRoom {
		s.broadcastRoomsChanged(setID)
	}
	if recordingWasStopped {
		s.broadcastMsg(mapRecordingMsg(recordingStopped))
	}
	return res, nil
}

// reloadPreservingPositionAfterZoneMove retains a live position when its exact
// room moved. Pending moves are retained because coordinates and all endpoints
// were translated atomically; the active map-walk is still cancelled because its
// previously planned route may no longer be optimal.
func (s *Session) reloadPreservingPositionAfterZoneMove(move storage.ZoneMoveInput) {
	idx := s.buildActiveIndex()
	s.stopMapWalkForZoneMove("map topology changed")
	s.mapMu.Lock()
	if s.mapTracker == nil {
		s.mapTracker = mapper.NewTracker(idx)
	} else {
		pos := s.mapTracker.Position()
		if pos.Valid && pos.Coord.Zone == move.SourceZone {
			moved := move.AllSource
			if !moved {
				for _, c := range move.Rooms {
					if c.X == pos.Coord.X && c.Y == pos.Coord.Y && c.L == pos.Coord.L {
						moved = true
						break
					}
				}
			}
			if moved {
				s.mapTracker.RelocatePosition(move.SourceZone, move.TargetZone, pos.Coord.X, pos.Coord.Y, pos.Coord.L)
			}
		}
		s.mapTracker.SetIndexPreservingPosition(idx)
	}
	s.syncDoorOpenLocked()
	s.mapMu.Unlock()
	s.BroadcastMapPosition()
}

// UndoTopology pops the last topology write on the session's active set and
// restores the recorded BEFORE-STATE through the store (the true inverse), then
// refreshes the index preserving position and broadcasts. It does NOT fork (undo
// only ever targets an editable set — a frozen set never had a write, so its
// journal is empty), and runs under the per-set TopologyLock so undo order matches
// write order. Returns NothingToUndo=true when the set's journal is empty, and the
// UndoEntry that was applied for reporting.
func (s *Session) UndoTopology() (TopologyWriteResult, storage.UndoEntry, error) {
	s.mapReconcileMu.Lock()
	defer s.mapReconcileMu.Unlock()
	return s.undoTopologyLocked()
}

func (s *Session) undoTopologyLocked() (TopologyWriteResult, storage.UndoEntry, error) {
	var res TopologyWriteResult

	setID, ok, err := s.store.GetActiveMapSetID(s.sessionID)
	if err != nil {
		return res, storage.UndoEntry{}, err
	}
	if !ok || setID <= 0 {
		return res, storage.UndoEntry{}, ErrNoActiveMapSet
	}

	lock := s.store.TopologyLock(setID)
	lock.Lock()
	defer lock.Unlock()

	res.SetID = setID

	entry, ok := s.store.WriteJournal().Pop(setID)
	if !ok {
		res.NothingToUndo = true
		return res, storage.UndoEntry{}, nil
	}
	if entry.Before.NoOp {
		set, serr := s.store.GetMapSet(setID)
		if serr != nil {
			s.store.WriteJournal().Unpop(setID, entry)
			return res, entry, serr
		}
		if set.TopologyRevision != entry.ExpectedRevision {
			s.store.WriteJournal().Unpop(setID, entry)
			return res, entry, fmt.Errorf("stale topology revision: expected %d, current %d", entry.ExpectedRevision, set.TopologyRevision)
		}
		res.Applied = true
		return res, entry, nil
	}
	applied, err := s.store.RestoreTopologyBeforeAtRevision(setID, entry.Before, entry.ExpectedRevision)
	if err != nil {
		// The restore rolled back, so the write it describes is still in the DB and
		// the journal must still describe it. Dropping the entry here would make the
		// NEXT undo reverse the write before this one — silently undoing something the
		// user never asked about, on top of an operation they think they undid.
		s.store.WriteJournal().Unpop(setID, entry)
		return res, entry, err
	}
	if !applied {
		// Every target cell vanished (deleted by a later write) — nothing to restore.
		return res, entry, nil
	}
	res.Applied = true
	s.store.WriteJournal().SetTopExpectedRevision(setID, entry.ExpectedRevision+1)
	if entry.Before.ZoneMove != nil {
		move := entry.Before.ZoneMove.Input
		move.SourceZone, move.TargetZone = move.TargetZone, move.SourceZone
		s.reloadPreservingPositionAfterZoneMove(move)
	} else {
		s.reloadPreservingPosition()
	}
	s.broadcastRoomsChanged(setID)
	return res, entry, nil
}

// broadcastRoomsChanged / broadcastMapSetChanged emit the write-path change
// notifications carrying the affected map_set_id so an open map pane bound to a
// now-frozen original can tell a change/fork moved the session to a different set
// and refetch.
func (s *Session) broadcastRoomsChanged(mapSetID int64) {
	s.BroadcastServerMsg(ServerMsg{Type: "rooms_changed", MapSetID: mapSetID})
}

func (s *Session) broadcastMapSetChanged(mapSetID int64) {
	s.BroadcastServerMsg(ServerMsg{Type: "map_sets_changed", MapSetID: mapSetID})
}
