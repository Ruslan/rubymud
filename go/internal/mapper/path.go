package mapper

// Pathfinding ports rmud_locate.world_neighbors + global BFS: fewest-steps route
// over the set's rooms, DT cells hard-excluded, seams as directed edges. Output
// is an ordered list of RU direction/command tokens for mud_send_command.

// PathStep is one emitted hop of a route — one command per ACTUAL server step.
// A single PathStep may span several map cells when it crosses a pipe corridor
// (the server traverses a whole pipe run with one command; see collapsePipeRuns).
type PathStep struct {
	Command string // RU direction ("с ю в з вв вн") or seam command ("на восток")
	Seam    bool   // true when this hop crosses a zone seam
	Door    bool   // true when the departing edge is a door (agent must open first)
	ToZone  string // target zone of the hop (the cell this step lands on)
	Hint    string // target room hint (may be empty)
	IsDT    bool   // target is a death trap (should never be true; DTs excluded)
	Cells   int    // number of map cells this one command traverses (>=1)
}

// PathResult is a computed route.
type PathResult struct {
	Steps     []PathStep
	Reachable bool
	DTTarget  bool // target itself is a death trap => refused
	Reason    string
}

// worldNeighbor is one adjacency edge from a node.
type worldNeighbor struct {
	to      Coord
	command string
	dir     string // canonical direction letter for a grid move ("" for seams)
	seam    bool
	door    bool // the edge is a door in this direction (from the source room)
	toPipe  bool // the cell this edge lands on is a pipe corridor
	toZone  string
}

// worldNeighbors yields exit-constrained grid neighbors + seam edges of a room,
// DT targets excluded (mirrors rmud_locate.world_neighbors).
func (idx *Index) worldNeighbors(c Coord) []worldNeighbor {
	r := idx.Room(c)
	if r == nil {
		return nil
	}
	var out []worldNeighbor
	for _, dir := range dirOrder {
		d := dirDelta[dir]
		nc := Coord{Zone: c.Zone, X: c.X + d.DX, Y: c.Y + d.DY, L: c.L + d.DL}
		nb := idx.Room(nc)
		if nb == nil || nb.IsDT {
			continue
		}
		// The edge must be a REAL connection derived from AUTHORITATIVE
		// connectivity (ch/edirs), never inferred from a raw visual-coord delta.
		// ConnectsTo accepts the edge when EITHER endpoint records it (connectivity
		// is bidirectional): this keeps the displaced-room fix (a permissive cell
		// cannot fabricate an edge into an explicit neighbor that denies the
		// back-link) AND fixes dropped final turns into single-exit dead-end
		// targets whose neighbor's map data omitted the forward link.
		if !r.ConnectsTo(dir, nb) {
			continue
		}
		out = append(out, worldNeighbor{
			to:      nc,
			command: DirRU(dir),
			dir:     dir,
			seam:    false,
			door:    containsDir(r.Doors, dir),
			toPipe:  nb.Pipe,
			toZone:  nc.Zone,
		})
	}
	for _, a := range r.Automaps {
		s, ok := ParseSeam(a)
		if !ok {
			continue
		}
		tr := idx.seamTarget(s)
		if tr == nil || tr.IsDT {
			continue
		}
		out = append(out, worldNeighbor{
			to:      tr.Coord,
			command: s.Command,
			seam:    true,
			toPipe:  tr.Pipe,
			toZone:  tr.Zone,
		})
	}
	return out
}

// FindPath runs BFS from start to the first room satisfying goal. DT targets are
// refused. Returns Reachable=false with a reason when unreachable.
func (idx *Index) FindPath(start Coord, goal func(*IndexRoom) bool) PathResult {
	if idx == nil {
		return PathResult{Reason: "no active map set"}
	}
	// Refuse a DT target up front (check the full index incl. DTs).
	for _, r := range idx.rooms {
		if r.IsDT && goal(r) {
			return PathResult{DTTarget: true, Reason: "target is a death trap"}
		}
	}
	startRoom := idx.Room(start)
	if startRoom == nil {
		return PathResult{Reason: "start room not in the active set"}
	}
	if startRoom.IsDT {
		return PathResult{Reason: "start room is a death trap"}
	}

	type prevEdge struct {
		from Coord
		step rawStep
	}
	prev := map[Coord]*prevEdge{start: nil}
	queue := []Coord{start}
	var goalCoord *Coord

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if r := idx.Room(n); r != nil && goal(r) {
			c := n
			goalCoord = &c
			break
		}
		for _, nb := range idx.worldNeighbors(n) {
			if _, seen := prev[nb.to]; seen {
				continue
			}
			target := idx.Room(nb.to)
			rs := rawStep{
				command: nb.command,
				dir:     nb.dir,
				seam:    nb.seam,
				door:    nb.door,
				toPipe:  nb.toPipe,
				toZone:  nb.toZone,
			}
			if target != nil {
				rs.hint = target.Hint
				rs.isDT = target.IsDT
			}
			prev[nb.to] = &prevEdge{from: n, step: rs}
			queue = append(queue, nb.to)
		}
	}

	if goalCoord == nil {
		return PathResult{Reachable: false, Reason: "no route to target in the active set"}
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

	return PathResult{Steps: collapsePipeRuns(raw), Reachable: true}
}

// rawStep is one per-cell BFS edge before pipe-run collapse.
type rawStep struct {
	command string
	dir     string // canonical dir letter ("" for seams)
	seam    bool
	door    bool
	toPipe  bool // the cell this edge lands on is a pipe corridor
	toZone  string
	hint    string
	isDT    bool
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
		if len(out) > 0 && !rs.seam && !out[len(out)-1].Seam &&
			rs.dir != "" && rs.dir == raw[i-1].dir && raw[i-1].toPipe {
			last := &out[len(out)-1]
			last.Cells++
			last.ToZone = rs.toZone
			last.Hint = rs.hint
			last.IsDT = last.IsDT || rs.isDT
			last.Door = last.Door || rs.door
			continue
		}
		out = append(out, PathStep{
			Command: rs.command,
			Seam:    rs.seam,
			Door:    rs.door,
			ToZone:  rs.toZone,
			Hint:    rs.hint,
			IsDT:    rs.isDT,
			Cells:   1,
		})
	}
	return out
}
