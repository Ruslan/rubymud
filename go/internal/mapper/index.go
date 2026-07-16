package mapper

import (
	"strconv"
	"strings"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/storage"
)

// Coord is a room's grid key within the active set.
type Coord struct {
	Zone string
	X    int
	Y    int
	L    int
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
	IsDT        bool
	Pipe        bool
	ImageIndex  *int
	Automaps    []string // "Zone|command|tag" seams
	Fingerprint string
}

// Seam is a parsed automaps entry.
type Seam struct {
	Zone    string
	Command string
	Tag     int
}

// Index is the in-memory index of the active map set. It is rebuilt (never
// mutated in place) on connect / active-set change / import affecting the active
// set, keeping it in sync with storage (AGENTS #2). It is read-only after build.
type Index struct {
	MapSetID int64
	byCoord  map[Coord]*IndexRoom
	byFP     map[string][]*IndexRoom         // fingerprint -> rooms (may collide)
	byTag    map[string]map[int][]*IndexRoom // zone -> tag -> rooms
	rooms    []*IndexRoom
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

// hasAnyCh reports whether the room has any mapped connectivity bit set.
func (r *IndexRoom) hasAnyCh() bool { return r.Ch != 0 }

// HasExplicitExits reports whether the room carries authoritative connectivity
// data (ch bits or an edirs list). A room with neither is "permissive" — its
// exits are unknown and it only participates in the grid fallback.
func (r *IndexRoom) HasExplicitExits() bool {
	return r.hasAnyCh() || len(r.EDirs) > 0
}

// ExitsInDir reports whether the room has a real exit in dir, using the
// AUTHORITATIVE connectivity data. FINDINGS.md: `ch` (ChN..ChD) is the real
// connectivity and is more authoritative than the `exits`/`edirs` string — so
// direction derivation must NOT be inferred from raw visual x,y,l deltas (which
// are wrong for displaced rooms and special entrances). Priority:
//  1. if the room has any ch bits, ch is authoritative — exit iff HasCh(dir);
//  2. else if the room lists edirs, exit iff dir ∈ edirs;
//  3. else (no connectivity data at all) permissive — treat as an exit (blank
//     cells stay reachable, matching the reference grid fallback).
func (r *IndexRoom) ExitsInDir(dir string) bool {
	if r.hasAnyCh() {
		return r.HasCh(dir)
	}
	if len(r.EDirs) > 0 {
		return containsDir(r.EDirs, dir)
	}
	return true
}

// ConnectsTo reports whether there is a real edge from this room toward a
// neighbor in direction dir, given the neighbor. Connectivity is bidirectional,
// so the edge is real when EITHER authoritative endpoint records it:
//   - this room explicitly advertises dir, OR
//   - the neighbor explicitly advertises the reverse (opposite dir) — this
//     recovers single-exit dead-end targets whose neighbor's map data failed to
//     record the forward link (the live "final turn into a dead-end dropped" bug).
//
// When NEITHER endpoint is explicit, fall back to permissive grid adjacency.
// Crucially, a *permissive* departing room does NOT fabricate an edge into an
// explicit neighbor that denies the back-link — that is the displaced-room guard
// from the previous fix (bank entered via the wrong visual direction).
func (r *IndexRoom) ConnectsTo(dir string, nb *IndexRoom) bool {
	opp := OppositeDir(dir)
	aExplicit := r.HasExplicitExits()
	bExplicit := nb.HasExplicitExits()
	if aExplicit && r.ExitsInDir(dir) {
		return true
	}
	if bExplicit && nb.ExitsInDir(opp) {
		return true
	}
	if !aExplicit && !bExplicit {
		return true // both permissive: grid fallback
	}
	return false
}

// BuildIndex builds an Index from a set's full rooms. It computes/keeps the
// fingerprint (recomputing from raw fields via the shared mapimport function so
// resolve matches live events even if the stored value is empty).
func BuildIndex(mapSetID int64, rooms []storage.Room) *Index {
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
			Pipe:        r.Pipe,
			ImageIndex:  r.ImageIndex,
			Automaps:    decodeDirs(r.Automaps),
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

// decodeDirs decodes a JSON string array column into a slice; "" / bad => nil.
func decodeDirs(jsonArr string) []string {
	jsonArr = strings.TrimSpace(jsonArr)
	if jsonArr == "" || jsonArr == "[]" {
		return nil
	}
	// storage stores these as JSON arrays; reuse storage's decode semantics via a
	// tiny local parser to avoid a cycle. Accept ["A","B"].
	var out []string
	inTok := false
	var cur strings.Builder
	for _, ch := range jsonArr {
		switch ch {
		case '"':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
			}
			inTok = !inTok
		default:
			if inTok {
				cur.WriteRune(ch)
			}
		}
	}
	return out
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

// ParseSeam parses one "Zone|command|tag" automaps entry (mirrors
// rmud_locate.parse_seam).
func ParseSeam(s string) (Seam, bool) {
	parts := strings.Split(s, "|")
	if len(parts) < 3 {
		return Seam{}, false
	}
	tag, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return Seam{}, false
	}
	return Seam{Zone: parts[0], Command: parts[1], Tag: tag}, true
}

// seamTarget resolves a seam to a room in the target zone (first room with the
// tag, mirroring rmud_locate's approximate resolution). Returns nil if the
// target zone/tag is absent (dead seam).
func (idx *Index) seamTarget(s Seam) *IndexRoom {
	zm := idx.byTag[s.Zone]
	if zm == nil {
		return nil
	}
	cand := zm[s.Tag]
	if len(cand) == 0 {
		return nil
	}
	return cand[0]
}

// resolveFingerprint returns the room a fingerprint uniquely resolves to (plan
// §5 legit auto-resync): unique fingerprint => that room; a non-unique
// fingerprint never auto-resolves here (the caller's desc/exits uniqueness is
// already folded into the fingerprint, so a collision means genuine ambiguity).
func (idx *Index) resolveFingerprint(fp string) *IndexRoom {
	rooms := idx.byFP[fp]
	if len(rooms) == 1 {
		return rooms[0]
	}
	return nil
}
