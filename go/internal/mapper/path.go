package mapper

import (
	"container/heap"
	"strings"

	"rubymud/go/internal/storage"
)

// Pathfinding computes a lowest-weight route over the set's rooms. Death-trap
// cells are hard-excluded and seams are directed edges. Output is an ordered
// list of direction/command tokens for mud_send_command.

// DoorKind grades whether a route hop crosses a door, and on which side the map
// recorded it. A door is ONE physical object A↔B; hand-drawn maps often record it
// on only one face, so the target's reverse face must also be consulted.
type DoorKind int

const (
	DoorNone      DoorKind = iota // no door on this hop
	DoorConfirmed                 // the SOURCE room records a door on the walked face
	DoorPresumed                  // only the TARGET records a door on the reverse face
)

// PathStep is one emitted hop of a route — one command per ACTUAL server step.
// A single PathStep may span several map cells when it crosses a pipe corridor
// (the server traverses a whole pipe run with one command; see collapsePipeRuns).
//
// Command is the CANONICAL ENGLISH move to send (n/s/e/w/u/d) for BOTH grid hops
// AND seams. English is the de-facto standard (RMUD accepts n/s/e/w alongside
// с/ю/в/з) and avoids clashing with client verb-aliases — crucially, a seam's raw
// .mm2 command "на восток" is parsed by the client as the verb "надеть" (wear)
// and derails the route, so we emit "e" instead and keep the raw command in
// SeamCommand for annotation. Dir carries the same english letter (or "" if a
// seam command could not be parsed to a direction — then Command falls back to
// the raw SeamCommand and SeamUnparsed is set so it is flagged, not silently
// emitted as an unusable token).
type PathStep struct {
	// To is the exact coordinate the server expects after this physical hop. It is
	// deliberately retained after pipe-run collapse: one command may span several
	// cells, but confirmation must match the final landing cell, not the first.
	To           Coord
	Command      string   // canonical english move to send ("n/s/e/w/u/d"); grid AND seam
	Dir          string   // canonical EN direction letter ("n/s/e/w/u/d"); "" if a seam couldn't be parsed
	Seam         bool     // true when this hop crosses a zone seam
	SeamCommand  string   // raw .mm2 seam command ("на восток") — annotation only, not emitted
	SeamUnparsed bool     // true when a seam command could not be mapped to a direction
	Door         bool     // true when this hop crosses a door (confirmed OR presumed)
	DoorKind     DoorKind // grade of the door on this hop (none|confirmed|presumed)
	ToZone       string   // target zone of the hop (the cell this step lands on)
	Hint         string   // target room hint (may be empty)
	IsDT         bool     // target is a death trap (should never be true; DTs excluded)
	Cells        int      // number of map cells this one command traverses (>=1)

	// Phrase is the hand-drawn command that TAKES this passage instead of the
	// direction ("двигать ящик"). When set it IS the move — Command carries it too,
	// so a consumer that only reads Command still walks correctly, and Dir keeps the
	// geometric direction for anything that needs to know which way the step goes.
	Phrase string
	// OpenAction is the command to send BEFORE this move to get through the door on
	// it. Filled from the edge's own open command when the operator recorded one, and
	// otherwise from the SESSION'S ROOM-FORMAT PROFILE template (ProfileSpec.DoorOpen,
	// carried on the Index) — which is what the imported doors use, none of which
	// carries a command of its own.
	//
	// It is "" when the hop crosses no door, AND when it crosses one on a mud whose
	// room format describes no door verb: every profile but the builtin rmud, where
	// sending anything would be a guess at another server's vocabulary.
	OpenAction string
	// Locked says the door on this hop is marked locked by default from the side we
	// approach it. It does NOT stop the route (the player may hold the key); it is
	// reported so "the walk stopped here" has a name.
	Locked bool
}

// PathResult is a computed route.
type PathResult struct {
	Steps     []PathStep
	Reachable bool
	DTTarget  bool // target itself is a death trap => refused
	Reason    string
	// VetoedOnly is set when the target is unreachable ONLY because of hand-drawn
	// "do not route me through here" marks: dropping them makes a route appear.
	// Without saying so, a veto is indistinguishable from a hole in the map and the
	// player goes looking for the hole. See FindPath.
	VetoedOnly bool
	// TotalWeight is the uncollapsed sum of directed edge weights selected by the
	// router. Pipe collapse changes commands, never the cost of the route.
	TotalWeight float64 `json:"total_weight"`
}

type pathQueueItem struct {
	coord Coord
	cost  float64
	seq   uint64
}

type pathQueue []pathQueueItem

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return q[i].seq < q[j].seq
}
func (q pathQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(x any)   { *q = append(*q, x.(pathQueueItem)) }
func (q *pathQueue) Pop() any {
	old := *q
	n := len(old)
	v := old[n-1]
	*q = old[:n-1]
	return v
}

// dirWordRU renders a canonical direction letter as the Russian word RMUD takes as
// the argument of an open command. Mirrors the command substitution in
// ui/src/map/edgeEditor.ts:renderDefaultOpen.
// It is reachable ONLY through a profile template that asks for "{dir}", so no
// Russian reaches a mud that did not request it.
var dirWordRU = map[string]string{
	"N": "север", "S": "юг", "E": "восток", "W": "запад", "U": "вверх", "D": "вниз",
}

// The placeholders a profile's door-open template may use for the hop's direction.
// TWO of them, so the template does not force a language on the argument the way a
// single Russian-only one would: `{dir}` is the Russian word RMUD wants, `{dir_en}`
// is the canonical english letter the router speaks everywhere else and what an
// English mud's "open door e" needs. A template that names neither (a lever, a
// password) is equally fine and is left untouched.
const (
	doorDirRU = "{dir}"
	doorDirEN = "{dir_en}"
)

// renderDoorTemplate substitutes the direction placeholders in a profile template.
// An unknown direction (a seam whose command is not a direction) drops the argument
// rather than emitting the literal placeholder.
func renderDoorTemplate(tmpl, dir string) string {
	up := strings.ToUpper(dir)
	out := strings.ReplaceAll(tmpl, doorDirEN, strings.ToLower(up))
	out = strings.ReplaceAll(out, doorDirRU, dirWordRU[up])
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}

// doorOpenAction renders the command that opens the door met on a hop walked in
// `dir`.
//
//   - `cmd` is the EDGE's own opening command. It wins outright, and it works on
//     every mud: a lever, a password or an English verb is exactly the case the
//     per-edge field exists for.
//   - `tmpl` is the session's ROOM-FORMAT PROFILE template (ProfileSpec.DoorOpen)
//     for a door nobody annotated. Empty — which is every profile except the
//     builtin rmud — means the route sends nothing, i.e. the pre-door-markup
//     behaviour. Better silent than "откр две north" on an English Diku.
func doorOpenAction(cmd, dir, tmpl string) string {
	if strings.TrimSpace(cmd) != "" {
		return cmd
	}
	if strings.TrimSpace(tmpl) == "" {
		return ""
	}
	return renderDoorTemplate(tmpl, dir)
}

// worldNeighbor is one adjacency edge from a node.
type worldNeighbor struct {
	to           Coord
	command      string // canonical english move to send (n/s/e/w/u/d)
	dir          string // canonical UPPER direction letter ("" if a seam couldn't be parsed)
	seam         bool
	seamCommand  string // raw .mm2 seam command (annotation only)
	seamUnparsed bool
	doorKind     DoorKind // door grade on this edge (none|confirmed|presumed)
	toPipe       bool     // the cell this edge lands on is a pipe corridor
	toZone       string
	phrase       string // hand-drawn command that takes this passage, "" if none
	openAction   string // command to send before the move to get through a door
	locked       bool   // that door is marked locked by default from this side
	weight       float64
	exactCommand bool // corridor Command is transport truth; Phrase is annotation
}

// worldNeighbors yields the room's traversable neighbors: its materialized grid
// edges plus its seam edges, with DT targets excluded.
//
// Since edge materialization this reads STORED edges instead of
// re-deriving connectivity per step: the index carries each room's grid edges
// indexed by direction and its seams in imported occurrence order, so the iteration order —
// and therefore BFS tie-breaking and every existing route golden — is unchanged.
// A dangling exit or a dead seam (To == nil) is a recorded, non-traversable edge
// and is skipped here exactly as the derivation used to drop it silently.
// `ignoreVetoes` drops the hand-drawn "do not route me through here" filter. It is
// how FindPath tells "there is no way" apart from "the only ways are ones you told
// me not to use" — the second pass runs with it on, and nothing else ever does.
// It also returns the destinations it DECLINED because of a routing veto, which is
// what lets FindPath ask the precise question "did a veto actually cut anything
// off" instead of the blunt "does a veto exist". Normally nil: nothing is allocated
// unless a veto is met.
func (idx *Index) worldNeighbors(c Coord, ignoreVetoes bool) (out []worldNeighbor, vetoedTo []Coord) {
	r := idx.Room(c)
	if r == nil {
		return nil, nil
	}
	for i, dir := range dirOrder {
		e := r.grid[i]
		if e == nil || e.To == nil {
			continue
		}
		// A hand-drawn NEGATION ("no way here, whatever the exits line says") is a
		// stored row precisely so it survives re-derivation; it is not a neighbour.
		// Skipping it HERE and not at index-build time keeps the row visible to
		// everything that reports on a room's exits.
		if !e.Passable {
			continue
		}
		// The routing VETO — a different statement from the negation above. The
		// passage exists and the tracker walks it normally (nothing in tracker.go reads
		// this); the router is simply told not to choose it.
		if e.NoRoute && !ignoreVetoes {
			vetoedTo = append(vetoedTo, *e.To)
			continue
		}
		nc := *e.To
		nb := idx.Room(nc)
		if nb == nil || nb.IsDT {
			continue
		}
		door, openAct, locked := doorOnHop(r, nb, dir, idx.doorOpen)
		cmd := strings.ToLower(dir)
		exact := e.Kind == storage.EdgeKindCorridor
		if exact {
			cmd = e.Command
		}
		out = append(out, worldNeighbor{
			to:           nc,
			command:      cmd,
			dir:          dir,
			seam:         false,
			doorKind:     door,
			toPipe:       nb.Pipe,
			toZone:       nc.Zone,
			phrase:       e.Phrase,
			openAction:   openAct,
			locked:       locked,
			weight:       e.EffectiveWeight(),
			exactCommand: exact,
		})
	}
	for _, e := range r.seams {
		if e.To == nil {
			continue // dead seam: its zone/tag does not exist in this set
		}
		if !e.Passable {
			continue // hand-drawn negation, same as the grid case above
		}
		if e.NoRoute && !ignoreVetoes {
			vetoedTo = append(vetoedTo, *e.To)
			continue
		}
		tr := idx.Room(*e.To)
		if tr == nil || tr.IsDT {
			continue
		}
		// A seam's raw .mm2 command ("на восток") must NOT be emitted — the client
		// parses "на <X>" as "надеть" (wear) and the route derails. Emit the
		// canonical english letter derived from the seam's RU direction word;
		// keep the raw command as an annotation. If it can't be parsed to a
		// direction, fall back to the raw command but flag it (don't silently emit
		// an unusable token).
		enDir := seamDir(e.Command) // canonical UPPER letter, or "" if unparsable
		cmd := strings.ToLower(enDir)
		unparsed := false
		if enDir == "" {
			cmd = e.Command
			unparsed = true
		}
		if e.Phrase != "" {
			cmd = e.Phrase
			unparsed = false // explicit command is safer than an unknown raw automap verb
		}
		door, open := DoorNone, ""
		if e.Door {
			door = DoorConfirmed
			if e.OpenCmd != "" {
				open = e.OpenCmd
			} else if enDir != "" {
				open = doorOpenAction("", enDir, idx.doorOpen)
			}
		}
		// The manual semantic row owns these attributes across re-derivation and
		// shadows the parsed twin produced by the import materialization.
		out = append(out, worldNeighbor{
			to:           tr.Coord,
			command:      cmd,
			dir:          enDir,
			seam:         true,
			seamCommand:  e.Command,
			seamUnparsed: unparsed,
			doorKind:     door,
			toPipe:       tr.Pipe,
			toZone:       tr.Zone,
			phrase:       e.Phrase,
			openAction:   open,
			locked:       e.Locked,
			weight:       e.EffectiveWeight(),
		})
	}
	return out, vetoedTo
}

// doorGrade classifies the door on a hop from room a to neighbor b in direction
// dir. A door is one physical object A↔B; maps often record it on one face only:
//   - DoorConfirmed: the edge LEAVING a in dir carries a door.
//   - DoorPresumed:  it does not, but the edge leaving b in the REVERSE direction
//     does — the same one-sided-recorded door seen from the other end (the live
//     field bug: hop N into a cell whose SOUTH face carries the door).
//   - DoorNone:      neither directed row records a door on this passage.
//
// SINCE MIGRATION 018 IT ASKS THE EDGES, NOT rooms.doors. The two questions are the
// same one for an imported map — the derivation puts rooms.doors on the parsed rows
// face for face, so the grade this returns is unchanged element for element — but
// the edge is where a HAND-DRAWN door can live, and rooms.doors cannot hold one: it
// is rewritten from the exits line by the next «обновить комнату». The one-sided
// reading survives verbatim because the "other side" is now simply the passage's
// other directed row.
//
// We flag presumed generously: a false presumed is cheap (an open command is a
// harmless no-op when there is no door), whereas a MISSED door breaks the route.
func doorGrade(a, b *IndexRoom, dir string) DoorKind {
	if a != nil {
		if e := a.GridEdge(dir); e != nil && e.Door {
			return DoorConfirmed
		}
	}
	if b != nil {
		if e := b.GridEdge(OppositeDir(dir)); e != nil && e.Door {
			return DoorPresumed
		}
	}
	return DoorNone
}

// doorOnHop grades the door on a grid hop and resolves what to DO about it: the
// command that opens it and whether it is marked locked. Both come from the SIDE
// that records the door — the walked face when it is confirmed, the target's reverse
// face when it is only presumed — because the opening command and the lock are
// per-direction (a door locked from one side opens freely from the other).
func doorOnHop(a, b *IndexRoom, dir, tmpl string) (kind DoorKind, openAction string, locked bool) {
	kind = doorGrade(a, b, dir)
	switch kind {
	case DoorConfirmed:
		e := a.GridEdge(dir)
		return kind, doorOpenAction(e.OpenCmd, dir, tmpl), e.Locked
	case DoorPresumed:
		// The door was recorded only on the far side. Its opening command is the one
		// stored there, but the LOCK is not: "locked from the other side" says nothing
		// about this one, and claiming it would report a lock the operator never marked.
		e := b.GridEdge(OppositeDir(dir))
		return kind, doorOpenAction(e.OpenCmd, dir, tmpl), false
	default:
		return DoorNone, "", false
	}
}

// FindPath runs deterministic Dijkstra from start to the lowest-cost goal. DT targets are
// refused. Returns Reachable=false with a reason when unreachable.
//
// WHEN IT FAILS IT ASKS ONCE MORE WITHOUT THE VETOES. A hand-drawn "do not route me
// through here" mark (map_edges.no_route) is invisible in the answer "no route to
// target": the map looks disconnected, and the player goes hunting for a passage
// that is right there and that they themselves closed to the router. So an
// unreachable result is re-run with the vetoes ignored, and if THAT finds a route
// the failure is reported as what it is. The extra search only ever runs on a
// failure, and only when the set carries at least one veto.
func (idx *Index) FindPath(start Coord, goal func(*IndexRoom) bool) PathResult {
	res, vetoCutSomethingOff := idx.findPath(start, goal, false)
	if res.Reachable || res.DTTarget || idx == nil {
		return res
	}
	// THE SECOND SEARCH RUNS ONLY IF A VETO ACTUALLY CUT SOMETHING OFF — i.e. the
	// first search declined an edge into a cell it then never reached by any other
	// way. That is the only situation in which lifting the vetoes could produce a
	// route, and the search itself answers it for free while it walks.
	//
	// Two blunter versions were measured on the live corpus and both were wrong to
	// keep. "Does the set contain a veto anywhere" (a whole-set scan) cost O(set) on
	// EVERY failed search. "Did the search meet a veto" was free but still fired on
	// every fully-failed search, because a failed search explores the entire
	// reachable component and so meets every veto in it. "No route" is an ordinary
	// answer, produced under the session's mapMu — which the incoming-line path
	// contends for — so the question has to be the precise one.
	if !vetoCutSomethingOff {
		return res
	}
	if alt, _ := idx.findPath(start, goal, true); alt.Reachable {
		res.VetoedOnly = true
		res.Reason = "a route exists only through edges marked \"avoid in routes\" — " +
			"clear the mark in the edge editor if the passage is usable"
	}
	return res
}

// findPath is one Dijkstra pass. `sawVeto` reports whether it DECLINED an edge because
// of a routing veto — the cheap, exact signal FindPath uses to decide whether the
// veto-ignoring second pass could possibly help.
func (idx *Index) findPath(start Coord, goal func(*IndexRoom) bool, ignoreVetoes bool) (PathResult, bool) {
	if idx == nil {
		return PathResult{Reason: "no active map set"}, false
	}
	// Refuse a DT target up front (check the full index incl. DTs).
	for _, r := range idx.rooms {
		if r.IsDT && goal(r) {
			return PathResult{DTTarget: true, Reason: "target is a death trap"}, false
		}
	}
	startRoom := idx.Room(start)
	if startRoom == nil {
		return PathResult{Reason: "start room not in the active set"}, false
	}
	if startRoom.IsDT {
		return PathResult{Reason: "start room is a death trap"}, false
	}

	type prevEdge struct {
		from Coord
		step rawStep
	}
	prev := map[Coord]*prevEdge{start: nil}
	dist := map[Coord]float64{start: 0}
	queue := &pathQueue{{coord: start, cost: 0}}
	heap.Init(queue)
	var seq uint64
	var goalCoord *Coord
	// Cells a routing veto kept the search out of. Collected rather than counted so
	// the question below can be the exact one; nil on a map with no vetoes.
	var declined []Coord

	for queue.Len() > 0 {
		item := heap.Pop(queue).(pathQueueItem)
		n := item.coord
		if known := dist[n]; item.cost != known {
			continue
		}
		if r := idx.Room(n); r != nil && goal(r) {
			c := n
			goalCoord = &c
			break
		}
		nbs, vetoed := idx.worldNeighbors(n, ignoreVetoes)
		declined = append(declined, vetoed...)
		for _, nb := range nbs {
			nextCost := item.cost + nb.weight
			if old, seen := dist[nb.to]; seen && old <= nextCost {
				continue
			}
			target := idx.Room(nb.to)
			rs := rawStep{
				to:           nb.to,
				command:      nb.command,
				dir:          nb.dir,
				seam:         nb.seam,
				seamCommand:  nb.seamCommand,
				seamUnparsed: nb.seamUnparsed,
				doorKind:     nb.doorKind,
				toPipe:       nb.toPipe,
				toZone:       nb.toZone,
				phrase:       nb.phrase,
				openAction:   nb.openAction,
				locked:       nb.locked,
				exactCommand: nb.exactCommand,
			}
			if target != nil {
				rs.hint = target.Hint
				rs.isDT = target.IsDT
			}
			prev[nb.to] = &prevEdge{from: n, step: rs}
			dist[nb.to] = nextCost
			seq++
			heap.Push(queue, pathQueueItem{coord: nb.to, cost: nextCost, seq: seq})
		}
	}

	if goalCoord == nil {
		// DID A VETO ACTUALLY CUT SOMETHING OFF? Only a declined destination that the
		// search never reached by any other way could open a new route if the veto were
		// lifted — a veto beside a cell the search walked into anyway changes nothing.
		// Asking the blunt question instead ("was any veto met") made every fully-failed
		// search pay for the second pass, because a failed search explores the whole
		// reachable component and therefore meets every veto in it.
		cut := false
		for _, c := range declined {
			if _, seen := prev[c]; !seen {
				cut = true
				break
			}
		}
		return PathResult{Reachable: false, Reason: "no route to target in the active set"}, cut
	}

	// Reconstruct the per-cell raw steps (one per map cell traversed).
	var raw []rawStep
	for c := *goalCoord; prev[c] != nil; {
		e := prev[c]
		raw = append(raw, e.step)
		c = e.from
	}
	// reverse
	for l, r := 0, len(raw)-1; l < r; l, r = l+1, r-1 {
		raw[l], raw[r] = raw[r], raw[l]
	}

	return PathResult{Steps: collapsePipeRuns(raw), Reachable: true, TotalWeight: dist[*goalCoord]}, false
}

// rawStep is one per-cell routing edge before pipe-run collapse.
type rawStep struct {
	to           Coord
	command      string
	dir          string // canonical dir letter ("" if a seam couldn't be parsed)
	seam         bool
	seamCommand  string
	seamUnparsed bool
	doorKind     DoorKind
	toPipe       bool // the cell this edge lands on is a pipe corridor
	toZone       string
	hint         string
	isDT         bool
	phrase       string
	openAction   string
	locked       bool
	exactCommand bool
}

// strongerDoor returns the stronger door signal of two kinds: confirmed beats
// presumed beats none (used when collapsing a pipe run that contains doors).
func strongerDoor(a, b DoorKind) DoorKind {
	if a == DoorConfirmed || b == DoorConfirmed {
		return DoorConfirmed
	}
	if a == DoorPresumed || b == DoorPresumed {
		return DoorPresumed
	}
	return DoorNone
}

// collapsePipeRuns folds a run of consecutive same-direction grid cells that pass
// THROUGH pipe corridors into a single emitted command — because the MUD server
// traverses a whole pipe run with one step/command, while the map stores it as N
// adjacent cells. Rule: a raw step continues the previous emitted command (does
// not emit a new one) when it is a grid move in the SAME direction as the running
// command AND the cell it departs from is a pipe corridor. The emitted step then
// lands on the run's final cell (its hint/zone/dt), keeping seams and doors from
// wherever they occur in the run. Non-pipe and direction-changing steps flush a
// new command as usual. Seams never collapse (a seam is always its own command).
func collapsePipeRuns(raw []rawStep) []PathStep {
	var out []PathStep
	for i := 0; i < len(raw); i++ {
		rs := raw[i]
		// Can this step continue the previous emitted command? Only when: there is
		// a previous emitted step, neither is a seam, it is the same grid
		// direction, and the cell we are LEAVING (raw[i-1]'s landing cell) is a
		// pipe corridor traversed in one server step.
		// A hop that needs its OWN command — a hand-drawn traversal phrase, or a door
		// to open first — can never be folded into a neighbour's single command, and
		// cannot swallow a following cell either: the server does not walk a pipe run
		// with "двигать ящик", and an opening action must land between two moves, not
		// inside one. So such a step both refuses to continue the previous command and
		// stops the next one from continuing it.
		ownCommand := rs.phrase != "" || rs.openAction != "" || rs.exactCommand
		prevOwn := len(out) > 0 && (out[len(out)-1].Phrase != "" || out[len(out)-1].OpenAction != "" || raw[i-1].exactCommand)
		if len(out) > 0 && !ownCommand && !prevOwn && !rs.seam && !out[len(out)-1].Seam &&
			rs.dir != "" && rs.dir == raw[i-1].dir && raw[i-1].toPipe {
			last := &out[len(out)-1]
			last.Cells++
			last.To = rs.to
			last.ToZone = rs.toZone
			last.Hint = rs.hint
			last.IsDT = last.IsDT || rs.isDT
			last.DoorKind = strongerDoor(last.DoorKind, rs.doorKind)
			last.Door = last.DoorKind != DoorNone
			continue
		}
		// A traversal phrase REPLACES the direction as the thing to send. It goes into
		// Command as well as Phrase so every existing consumer — which reads Command —
		// walks the passage correctly without knowing the attribute exists; Dir keeps
		// the geometry for whoever needs to know which way the step goes.
		cmd := rs.command
		if rs.phrase != "" && !rs.exactCommand {
			cmd = rs.phrase
		}
		out = append(out, PathStep{
			To:           rs.to,
			Command:      cmd,
			Dir:          strings.ToLower(rs.dir),
			Seam:         rs.seam,
			SeamCommand:  rs.seamCommand,
			SeamUnparsed: rs.seamUnparsed,
			Door:         rs.doorKind != DoorNone,
			DoorKind:     rs.doorKind,
			ToZone:       rs.toZone,
			Hint:         rs.hint,
			IsDT:         rs.isDT,
			Cells:        1,
			Phrase:       rs.phrase,
			OpenAction:   rs.openAction,
			Locked:       rs.locked,
		})
	}
	return out
}
