package storage

import "sync"

// This file is the storage half of the unified topology write-path (plan §8).
// It defines the concrete topology write operations, how each applies to an
// EDITABLE map set, the BEFORE-STATE snapshot each write captures for undo, and
// the in-memory per-map-set undo journal. The session layer (session/mapper.go)
// wraps ApplyTopologyOp with fork-if-frozen + broadcast + position-preserving
// tracker refresh — serialized per map_set_id — so UI edits and MCP CRUD share
// ONE path ("никаких параллельных путей" scoped to topology).

// TopologyOpKind enumerates the topology write ops. This slice wires exactly one
// concrete op through the path — TopoPatchExits (the exit add/remove behind
// map-cell-patch). Slice 3's CRUD (upsert/link/unlink/delete room) plugs in as
// more kinds here: add the kind, its apply arm in ApplyTopologyOp, and (for
// create/delete) extend the before-state snapshot to carry the whole room or its
// absence — the journal, per-set serialization, fork, broadcast, and
// position-preservation machinery are reused unchanged.
type TopologyOpKind string

const (
	// TopoPatchExits adds AddExits and removes RemoveExits on ONE room's edirs /
	// ch bitmask / display exits (the PatchRoomExits primitive).
	TopoPatchExits TopologyOpKind = "patch_exits"
)

// TopologyOp is one topology write against a map set. Coord (zone,x,y,l) targets
// the room; the exit lists are canonical upper-case direction letters ("N".."D").
type TopologyOp struct {
	Kind        TopologyOpKind
	Zone        string
	X           int
	Y           int
	L           int
	AddExits    []string
	RemoveExits []string
}

// RoomExitState is the exact prior exit state of one room cell — the fields a
// TopoPatchExits write touches. It is the BEFORE-STATE snapshot captured before
// an op applies; undo restores the room to precisely this state. Because it holds
// the literal prior values (not a symmetric add/remove inverse), it is correct
// regardless of PatchRoomExits' idempotency: adding an exit the room already has,
// or removing an absent one, both snapshot the unchanged state, so undo restores
// it exactly rather than fabricating/deleting an exit. Exists=false marks a cell
// that had no room (reserved for slice-3 create/delete; TopoPatchExits only ever
// snapshots existing rooms).
type RoomExitState struct {
	Zone   string
	X      int
	Y      int
	L      int
	Exists bool
	EDirs  string // JSON array text, verbatim
	Doors  string // JSON array text, verbatim
	Ch     int
	Exits  string // display exits string, verbatim
}

// UndoEntry is one journal record: the human label of the write plus the
// before-state snapshot that reverses it. Undo applies the snapshot through
// RestoreRoomExitState.
type UndoEntry struct {
	Label  string
	Before RoomExitState
}

// ApplyTopologyOp applies one topology op to the given (already-editable) map set
// and returns the BEFORE-STATE snapshot needed to undo it. found=false (no error)
// means the target room does not exist, so callers soft-fail rather than 500; the
// snapshot is only meaningful when found=true. This is the ONLY function that
// mutates topology; every write-path (UI, MCP CRUD, undo) funnels through it.
func (s *Store) ApplyTopologyOp(mapSetID int64, op TopologyOp) (before RoomExitState, found bool, err error) {
	switch op.Kind {
	case TopoPatchExits:
		return s.PatchRoomExitsWithSnapshot(mapSetID, op.Zone, op.X, op.Y, op.L, op.AddExits, op.RemoveExits)
	default:
		return RoomExitState{}, false, nil
	}
}

// UndoLabel renders a short human description of what an op did (for the undo
// report). The label is stored in the journal entry at write time.
func (op TopologyOp) UndoLabel() string {
	switch op.Kind {
	case TopoPatchExits:
		var s string
		if len(op.AddExits) > 0 {
			s += "add " + joinFields(op.AddExits)
		}
		if len(op.RemoveExits) > 0 {
			if s != "" {
				s += ", "
			}
			s += "remove " + joinFields(op.RemoveExits)
		}
		if s == "" {
			s = "exit patch (no-op)"
		}
		return s
	default:
		return string(op.Kind)
	}
}

// --- undo journal ----------------------------------------------------------

// undoJournalCap bounds the per-map-set undo stack. Beyond this the oldest entry
// is dropped (a bounded, in-memory stack — lost on restart, the owner's chosen
// persistence: MCP writes can happen with no browser attached, so there is no UI
// state to durably mirror and no undo DB table).
const undoJournalCap = 100

// WriteJournal is the in-memory, per-map-set undo journal for topology writes.
// It is keyed by map_set_id (NOT session) because map sets are global and shared
// across sessions — an undo in session A must reverse a write B made to the same
// set, and a write to set X must never be undoable from set Y. One journal is
// owned by the shared Store so every session and the MCP tool reach the same
// stacks. Access is serialized by the journal's mutex.
type WriteJournal struct {
	mu    sync.Mutex
	bySet map[int64][]UndoEntry
}

func newWriteJournal() *WriteJournal {
	return &WriteJournal{bySet: make(map[int64][]UndoEntry)}
}

// Push records the before-state UndoEntry for a write just applied to mapSetID.
// The stack is bounded: the oldest entry is dropped past undoJournalCap.
func (j *WriteJournal) Push(mapSetID int64, entry UndoEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	stack := append(j.bySet[mapSetID], entry)
	if len(stack) > undoJournalCap {
		stack = stack[len(stack)-undoJournalCap:]
	}
	j.bySet[mapSetID] = stack
}

// Pop removes and returns the most recent UndoEntry for mapSetID. ok=false when
// the set's stack is empty ("nothing to undo").
func (j *WriteJournal) Pop(mapSetID int64) (UndoEntry, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	stack := j.bySet[mapSetID]
	if len(stack) == 0 {
		return UndoEntry{}, false
	}
	top := stack[len(stack)-1]
	j.bySet[mapSetID] = stack[:len(stack)-1]
	return top, true
}

// Depth returns the number of undoable entries on mapSetID's stack (for tests /
// diagnostics).
func (j *WriteJournal) Depth(mapSetID int64) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.bySet[mapSetID])
}

// WriteJournal returns the store's shared topology undo journal, lazily creating
// it. Every session write-path and the MCP undo tool reach the same instance so
// the per-map-set stacks are truly global.
func (s *Store) WriteJournal() *WriteJournal {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	if s.journal == nil {
		s.journal = newWriteJournal()
	}
	return s.journal
}

// --- per-map-set write serialization ---------------------------------------

// TopologyLock returns the per-map-set mutex serializing the topology write-path
// for a given active set id. The whole read-active-set → fork-if-frozen →
// setActive → apply → journal sequence is run under this lock, keyed on the set
// id at ENTRY, so two sessions racing their first write to the same FROZEN set
// cannot both read editable=false and double-fork it: the first holds the source
// set's lock through its fork+switch, the second blocks and then observes the
// active set is already the editable fork. It also guarantees journal order
// matches DB write order for a set. The lock lives on the shared Store so all
// sessions serialize against the same mutex per set.
func (s *Store) TopologyLock(mapSetID int64) *sync.Mutex {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	if s.topoLocks == nil {
		s.topoLocks = make(map[int64]*sync.Mutex)
	}
	m := s.topoLocks[mapSetID]
	if m == nil {
		m = &sync.Mutex{}
		s.topoLocks[mapSetID] = m
	}
	return m
}
