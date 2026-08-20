package mapper

import (
	"sort"
	"strings"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/storage"
)

// Coord is a room's grid key within the active set.
type Coord struct {
	Zone string `json:"zone"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	L    int    `json:"l"`
}

// IndexRoom is the tracker's view of one room, distilled from storage.Room. It
// keeps only what the state machine and BFS need, plus decoded direction lists.
type IndexRoom struct {
	Coord
	Tag         *int
	Hint        string
	Desc        string
	Exits       string
	EDirs       []string // canonical exit dir letters (door markers stripped)
	Doors       []string // door dir letters
	Ch          int      // connectivity bitmask ChN..ChD
	IsDT        bool     // EFFECTIVE death-trap: nativeIsDT || annotation dt
	nativeIsDT  bool     // the room's own is_dt (from the map data), before overlay
	Pipe        bool
	ImageIndex  *int
	Fingerprint string

	// grid holds this room's MATERIALIZED outgoing grid edges, indexed by dirOrder
	// position (== chBitOrder bit), nil where there is no edge. One entry = one
	// stored directed row; the way back is the neighbour's own row.
	grid [6]*Edge
	// seams holds this room's materialized seam edges in imported occurrence order (the
	// walk's neighbour order, and therefore BFS tie-breaking, must not change).
	seams []*Edge
}

// Edge is one materialized topology edge as the walk sees it — oriented AWAY from
// the room that carries it. To is nil when the edge leads nowhere known (a
// dangling exit or a dead seam): such an edge is recorded but not traversable.
type Edge struct {
	Kind   string // storage.EdgeKindGrid | storage.EdgeKindSeam
	Source string // storage.EdgeSource* — parsed / grid fallback / manual
	// Passable=false is a hand-drawn NEGATION: the row exists so that it OWNS the
	// face and the derivation cannot resurrect the edge on the next room write, but
	// there is no way through. The edge is kept in the index rather than dropped at
	// build time so consumers can SAY so (mud_look_map annotates the denied
	// direction) instead of a passage silently not being there; worldNeighbors is
	// what refuses to walk it.
	Passable   bool
	To         *Coord
	Command    string // seam: the MUD command as printed by the map
	TargetZone string // seam selector: logical target zone, even for a dead seam
	TargetTag  *int   // seam selector: nil when no valid tag was materialized
	seamOrder  int    // persisted source occurrence; keeps route tie-breaking stable

	// The hand-drawn attribute half of the row (migration 018), mirrored field for
	// field from storage.EdgeAttrs: what you must DO to take this edge (Phrase,
	// Door/Locked/OpenCmd) and whether the router may (NoRoute).
	//
	// Note the division of labour with Passable, which is easy to confuse:
	//   Passable=false  there is no way — neither the walk nor the TRACKER accepts it;
	//   NoRoute=true    there is a way and walking it is normal — the tracker treats
	//                   the step like any other — but FindPath will not plan it.
	// Nothing in the tracker reads NoRoute, and that is deliberate rather than an
	// oversight: it is a routing preference, not a fact about the world.
	storage.EdgeAttrs
}

// TopologyEdge is an immutable value snapshot of one outgoing materialized edge.
// It is deliberately a value (including the target/tag presence bits) so web read
// paths cannot mutate the live, frozen index while holding a tracker's map lock.
type TopologyEdge struct {
	Kind      string
	Source    string
	Direction string
	Passable  bool
	HasTarget bool
	Target    Coord

	// Seam selector. HasTargetTag distinguishes a real zero tag from SQL NULL.
	Command      string
	TargetZone   string
	HasTargetTag bool
	TargetTag    int
	SeamOrder    int

	Weight  float64
	Phrase  string
	Door    bool
	Locked  bool
	OpenCmd string
	NoRoute bool
}

// Index is the in-memory index of the active map set. It is rebuilt (never
// mutated in place) on connect / active-set change / import affecting the active
// set, keeping it in sync with storage (AGENTS #2). It is read-only after build.
type Index struct {
	MapSetID int64
	// TopologyRevision is the storage revision at which this immutable index was
	// built.  Writers use it as a shared-set fence: a queued prediction made
	// against an older graph must not be committed after a peer edits the set.
	TopologyRevision int64
	byCoord          map[Coord]*IndexRoom
	byFP             map[string][]*IndexRoom         // fingerprint -> rooms (may collide)
	byTag            map[string]map[int][]*IndexRoom // zone -> tag -> rooms
	rooms            []*IndexRoom
	// doorOpen is the SESSION's room-format profile template for opening a door the
	// map has no per-edge command for (ProfileSpec.DoorOpen). It sits on the index
	// because that is what the route reads, and it is set by the session — the same
	// in-place, mapMu-guarded mutation SetAnnotationDT uses, for the same reason: a
	// profile change must reach routing without discarding the player's position by
	// rebuilding the whole index.
	//
	// Empty is the normal state: only the builtin rmud profile ships a template, and
	// an index nobody told behaves exactly as it did before doors were markup.
	doorOpen string
}

// SetDoorOpenTemplate points the index at the session's profile door template. The
// owning session must hold mapMu (the index is otherwise read-only after build).
func (idx *Index) SetDoorOpenTemplate(tmpl string) {
	if idx == nil {
		return
	}
	idx.doorOpen = tmpl
}

// DoorOpenTemplate returns the template a route will fall back to, for callers that
// need to TELL the user what will be sent (the MCP report, the map pane's editor).
func (idx *Index) DoorOpenTemplate() string {
	if idx == nil {
		return ""
	}
	return idx.doorOpen
}

// chBitOrder maps a direction letter to its Ch bitmask bit (ChN..ChD => 0..5).
var chBitOrder = map[string]int{"N": 0, "S": 1, "E": 2, "W": 3, "U": 4, "D": 5}

// HasCh reports whether the connectivity bitmask has a mapped edge in dir.
func (r *IndexRoom) HasCh(dir string) bool {
	bit, ok := chBitOrder[dir]
	if !ok {
		return false
	}
	return r.Ch&(1<<bit) != 0
}

// GridEdge returns the room's MATERIALIZED outgoing grid edge in dir, or nil when
// the graph holds none. It is how a reader tells the stored topology apart from
// the room's printed exits line: a hand-drawn hidden exit is an edge with no
// matching exit letter, and a hand-drawn negation is an exit letter with an
// impassable edge. Both are invisible to anything that reads EDirs alone.
func (r *IndexRoom) GridEdge(dir string) *Edge {
	bit, ok := chBitOrder[dir]
	if !ok {
		return nil
	}
	return r.grid[bit]
}

// HasExplicitExits reports whether the room carries authoritative connectivity
// data (ch bits or an edirs list). A room with neither is "permissive" — its
// exits are unknown and it only participates in the grid fallback.
func (r *IndexRoom) HasExplicitExits() bool {
	return storage.RoomHasExplicitExits(r.Ch, r.EDirs)
}

// ExitsInDir reports whether the room has a real exit in dir, using the
// AUTHORITATIVE connectivity data (ch over edirs; a room with neither is
// permissive). See storage.RoomExitsInDir for the full priority contract.
func (r *IndexRoom) ExitsInDir(dir string) bool {
	return storage.RoomExitsInDir(r.Ch, r.EDirs, dir)
}

// ConnectsTo reports whether there is a real edge from this room toward a
// grid-adjacent neighbor in direction dir. See storage.RoomsConnect for the rule.
//
// SINCE EDGE MATERIALIZATION THE WALK NO LONGER CALLS THIS. Edges
// are stored rows now, and worldNeighbors reads them off the index. What this rule
// remained is the DEFINITION of the edge set — storage.DeriveEdges is what import,
// the startup backfill and every topology write run to produce those rows — so
// collapsing it into a lookup would have deleted the definition and left only its
// output. It is kept exported here because two non-walk callers ask the same
// question directly: the UI record-room preview (web/mapper.go — "which neighbours
// would this write touch, and do they back-link?") and the corpus route invariant,
// which validates emitted routes against the rule rather than against the table
// they were computed from. Both want the rule, not the rows.
func (r *IndexRoom) ConnectsTo(dir string, nb *IndexRoom) bool {
	return storage.RoomsConnect(r.Ch, r.EDirs, nb.Ch, nb.EDirs, dir)
}

// BuildIndexWithEdges builds an Index from a set's rooms and its MATERIALIZED
// map_edges rows — the production path. The runtime never queries
// topology per step: the rows are read once, folded onto the rooms here, and the
// walk then only follows pointers, exactly as it followed grid deltas before.
func BuildIndexWithEdges(mapSetID int64, rooms []storage.Room, edges []storage.MapEdge) *Index {
	idx := buildRoomIndex(mapSetID, rooms)
	idx.attachEdges(edges)
	return idx
}

// buildRoomIndex builds the room half of the index (coord/fingerprint/tag maps).
// It computes/keeps the fingerprint (recomputing from raw fields via the shared
// mapimport function so resolve matches live events even if the stored value is
// empty).
func buildRoomIndex(mapSetID int64, rooms []storage.Room) *Index {
	idx := &Index{
		MapSetID: mapSetID,
		byCoord:  make(map[Coord]*IndexRoom, len(rooms)),
		byFP:     make(map[string][]*IndexRoom),
		byTag:    make(map[string]map[int][]*IndexRoom),
		rooms:    make([]*IndexRoom, 0, len(rooms)),
	}
	for i := range rooms {
		r := &rooms[i]
		fp := r.Fingerprint
		if fp == "" {
			fp = mapimport.Fingerprint(r.Hint, r.Desc, r.Exits)
		}
		ir := &IndexRoom{
			Coord:       Coord{Zone: r.Zone, X: r.X, Y: r.Y, L: r.L},
			Tag:         r.Tag,
			Hint:        r.Hint,
			Desc:        r.Desc,
			Exits:       r.Exits,
			EDirs:       decodeDirs(r.EDirs),
			Doors:       decodeDirs(r.Doors),
			Ch:          r.Ch,
			IsDT:        r.IsDT,
			nativeIsDT:  r.IsDT,
			Pipe:        r.Pipe,
			ImageIndex:  r.ImageIndex,
			Fingerprint: fp,
		}
		idx.byCoord[ir.Coord] = ir
		idx.byFP[fp] = append(idx.byFP[fp], ir)
		if ir.Tag != nil {
			zm := idx.byTag[ir.Zone]
			if zm == nil {
				zm = make(map[int][]*IndexRoom)
				idx.byTag[ir.Zone] = zm
			}
			zm[*ir.Tag] = append(zm[*ir.Tag], ir)
		}
		idx.rooms = append(idx.rooms, ir)
	}
	return idx
}

// attachEdges folds the materialized edge rows onto the rooms they leave. Rows are
// DIRECTED — one traversal each — so a row attaches to exactly one room, under the
// direction it is walked in; the reverse traversal is its own row, attached to the
// room on the other side. Seam rows attach in persisted occurrence order,
// because BFS tie-breaking depends on neighbour order. A row whose source room is
// not in this index (a stale row) is ignored.
//
// The Edge and Coord values are carved out of two backing arrays rather than
// allocated per row: a live set is ~19k rows, and this runs on every reconnect and
// after every topology write.
func (idx *Index) attachEdges(edges []storage.MapEdge) {
	// A manual seam owns its semantic transition just as a manual grid row owns a
	// face. Storage normally removes the parsed twin before this point, but keep
	// the runtime defensive for older or hand-edited databases: otherwise a stale
	// parsed row could silently bypass a manual no_route mark.
	manualSeams := make(map[seamIdentity]struct{})
	for i := range edges {
		e := &edges[i]
		if e.Kind == storage.EdgeKindSeam && e.Source == storage.EdgeSourceManual {
			if k, ok := seamIdentityOf(*e); ok {
				manualSeams[k] = struct{}{}
			}
		}
	}
	pool := make([]Edge, len(edges))
	coords := make([]Coord, len(edges))
	tags := make([]int, len(edges))
	for i := range edges {
		e := &edges[i]
		fr := idx.byCoord[Coord{e.FromZone, e.FromX, e.FromY, e.FromL}]
		if fr == nil {
			continue
		}
		var to *Coord
		if c, ok := e.To(); ok {
			coords[i] = Coord{c.Zone, c.X, c.Y, c.L}
			to = &coords[i]
		}
		ie := &pool[i]
		ie.Kind, ie.Source, ie.Passable, ie.To, ie.Command = e.Kind, e.Source, e.Passable, to, e.Command
		ie.EdgeAttrs = e.EdgeAttrs
		if e.Kind == storage.EdgeKindSeam {
			if e.Source != storage.EdgeSourceManual {
				if k, ok := seamIdentityOf(*e); ok {
					if _, shadowed := manualSeams[k]; shadowed {
						continue
					}
				}
			}
			ie.Command, ie.TargetZone = e.Command, e.TargetZone
			if e.TargetTag != nil {
				tags[i] = *e.TargetTag
				ie.TargetTag = &tags[i]
			}
			ie.seamOrder = e.SeamOrder
			// Equal values are legacy rows written before seam_order; stable insertion
			// retains ListMapEdges' id order for that compatibility case.
			at := len(fr.seams)
			for at > 0 && fr.seams[at-1].seamOrder > ie.seamOrder {
				at--
			}
			fr.seams = append(fr.seams, nil)
			copy(fr.seams[at+1:], fr.seams[at:])
			fr.seams[at] = ie
			continue
		}
		bit, ok := chBitOrder[e.Dir]
		if !ok {
			continue
		}
		fr.grid[bit] = ie
	}
}

type seamIdentity struct {
	from    Coord
	zone    string
	tag     int
	command string
	order   int
}

func seamIdentityOf(e storage.MapEdge) (seamIdentity, bool) {
	if e.TargetTag == nil || e.TargetZone == "" {
		return seamIdentity{}, false
	}
	return seamIdentity{
		from:    Coord{Zone: e.FromZone, X: e.FromX, Y: e.FromY, L: e.FromL},
		zone:    e.TargetZone,
		tag:     *e.TargetTag,
		command: e.Command,
		order:   e.SeamOrder,
	}, true
}

// ApplyAnnotationDT augments the index's effective is_dt with DT flags carried by
// room_annotations (a DT annotation augments rooms.is_dt). It ORs the
// annotation DT onto the matching IndexRoom's native is_dt so the EXISTING
// consumers — mud_path's DT-refusal (FindPath/worldNeighbors read IndexRoom.IsDT)
// and mud_look_map's DT display — pick it up automatically, with no separate DT
// lookup path. Annotation DT can only ADD a death trap (true), never clear a
// native one; a coord with no matching room is ignored (an annotation on a
// non-existent cell has no routing effect). Call once after BuildIndex on the
// full DT-coord set.
func (idx *Index) ApplyAnnotationDT(dtCoords []Coord) {
	if idx == nil {
		return
	}
	for _, c := range dtCoords {
		if r := idx.byCoord[c]; r != nil {
			r.IsDT = true // native || true; every coord here carries annotation dt
		}
	}
}

// SetAnnotationDT recomputes ONE cell's effective is_dt in place from its native
// is_dt OR'd with the given annotation DT flag — no rebuild. This is the seam for
// a single DT-touching annotate: it handles both dt:true (add the trap) and
// dt:false (drop back to the native value) without disturbing the tracker's
// position, confidence, or pending queue. A coord with no matching room is a
// no-op (dangling annotation — no routing effect). The owning session must hold
// mapMu while calling this (the index is otherwise read-only after build).
func (idx *Index) SetAnnotationDT(c Coord, annoDT bool) {
	if idx == nil {
		return
	}
	if r := idx.byCoord[c]; r != nil {
		r.IsDT = r.nativeIsDT || annoDT
	}
}

// decodeDirs decodes a JSON string array column into a slice; "" / bad => nil.
// It delegates to the storage decoder rather than keeping a local one: the same
// columns are decoded there when edges are materialized, and the two MUST agree.
// The local copy this replaces ignored escaping — harmless for direction letters,
// Import seam drafts hold arbitrary MUD text, so one quote in a zone name would have
// split a room's seams from the seam rows derived out of them.
func decodeDirs(jsonArr string) []string {
	return storage.DecodeJSONStrArray(strings.TrimSpace(jsonArr))
}

// Room returns the room at a coord, or nil.
func (idx *Index) Room(c Coord) *IndexRoom { return idx.byCoord[c] }

// RoomCount returns the number of rooms in the index.
func (idx *Index) RoomCount() int {
	if idx == nil {
		return 0
	}
	return len(idx.rooms)
}

// Rooms returns all rooms (read-only).
func (idx *Index) Rooms() []*IndexRoom { return idx.rooms }

// OutgoingTopology returns a stable, copy-only snapshot of every outgoing grid
// and seam edge. Grid edges are N,S,E,W,U,D; seams follow persisted source order.
// It never consults raw importer data.
func (r *IndexRoom) OutgoingTopology() []TopologyEdge {
	if r == nil {
		return nil
	}
	out := make([]TopologyEdge, 0, len(r.grid)+len(r.seams))
	for i, dir := range dirOrder {
		if e := r.grid[i]; e != nil {
			out = append(out, topologyEdgeSnapshot(e, dir))
		}
	}
	type orderedSeam struct {
		edge  TopologyEdge
		order int
	}
	seams := make([]orderedSeam, 0, len(r.seams))
	for _, e := range r.seams {
		seams = append(seams, orderedSeam{edge: topologyEdgeSnapshot(e, ""), order: e.seamOrder})
	}
	sort.SliceStable(seams, func(i, j int) bool { return seams[i].order < seams[j].order })
	for _, s := range seams {
		out = append(out, s.edge)
	}
	return out
}

func topologyEdgeSnapshot(e *Edge, direction string) TopologyEdge {
	v := TopologyEdge{
		Kind: e.Kind, Source: e.Source, Direction: direction, Passable: e.Passable,
		Command: e.Command, TargetZone: e.TargetZone, SeamOrder: e.seamOrder,
		Weight: e.EffectiveWeight(), Phrase: e.Phrase, Door: e.Door, Locked: e.Locked,
		OpenCmd: e.OpenCmd, NoRoute: e.NoRoute,
	}
	if e.To != nil {
		v.HasTarget, v.Target = true, *e.To
	}
	if e.TargetTag != nil {
		v.HasTargetTag, v.TargetTag = true, *e.TargetTag
	}
	return v
}

// resolveFingerprint returns the room a fingerprint uniquely resolves to during
// automatic resync: unique fingerprint => that room; a non-unique
// fingerprint never auto-resolves here (the caller's desc/exits uniqueness is
// already folded into the fingerprint, so a collision means genuine ambiguity).
func (idx *Index) resolveFingerprint(fp string) *IndexRoom {
	rooms := idx.byFP[fp]
	if len(rooms) == 1 {
		return rooms[0]
	}
	return nil
}
