package web

import (
	"fmt"
	"math"
	"strings"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// annotationsChangedMsg builds the light change-notification broadcast for an
// annotation write. It carries the map set id and the touched cell so a
// future UI can re-render just that cell; no annotation body is included (the UI
// re-reads). Reuses ServerMsg's existing map coordinate fields.
func annotationsChangedMsg(mapSetID int64, c mcpCoordArg) session.ServerMsg {
	return session.ServerMsg{
		Type:  "annotations_changed",
		Zone:  c.Zone,
		RoomX: c.X,
		RoomY: c.Y,
		RoomL: c.L,
	}
}

// This file implements the mapper MCP tools: thin read-only wrappers over
// the same backend tracker state the UI consumes, plus the gated
// mud_set_active_map_set write. Each positional tool surfaces the confidence enum
// inline (the "Контракт свежести/потери").

type mcpCoordArg struct {
	Zone string `json:"zone"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	L    int    `json:"l"`
}

type mcpPathArgs struct {
	ToZone string
	ToHint string
	To     *mcpCoordArg
	From   *mcpCoordArg
}

// formatMCPMapWalkState keeps every map-walk MCP tool on the same compact,
// inspectable state contract. The underlying state remains session-owned.
func formatMCPMapWalkState(st session.MapWalkState) string {
	return fmt.Sprintf("active: %t\nstate: %s\ncurrent: %d/%d\ntotal: %d\nreason: %s\ncan_resume: %t\nepoch: %d\njob: %d\nrevision: %d\n",
		st.Active, st.State, st.Current, st.Total, st.Total, st.Reason, st.CanResume, st.Epoch, st.JobID, st.Revision)
}

func (s *Server) mcpMapWalkStart(sid int64, to mcpCoordArg) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return fmt.Sprintf("Session %d not connected — no live tracker.", sid), true
	}
	st := sess.StartMapWalk(mapper.Coord{Zone: to.Zone, X: to.X, Y: to.Y, L: to.L})
	// "already here" is a successful no-op, like a zero-hop mud_path result.
	// A paused result means dispatch was unsafe (for example, a partial write
	// made the command outcome ambiguous), even though Session retains the job
	// so it can be re-anchored and resumed later.
	return formatMCPMapWalkState(st), st.State == "paused" || (!st.Active && st.Reason != "already here")
}

func (s *Server) mcpMapWalkStatus(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return fmt.Sprintf("Session %d not connected — no live tracker.", sid), true
	}
	st, _ := sess.MapWalkSnapshot()
	return formatMCPMapWalkState(st), false
}

func (s *Server) mcpMapWalkStop(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return fmt.Sprintf("Session %d not connected — no live tracker.", sid), true
	}
	return formatMCPMapWalkState(sess.CancelMapWalk("cancelled by MCP")), false
}

func (s *Server) mcpMapWalkResume(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return fmt.Sprintf("Session %d not connected — no live tracker.", sid), true
	}
	before, active := sess.MapWalkSnapshot()
	if !active || before.State != "paused" {
		before.Reason = "no paused route to continue"
		return formatMCPMapWalkState(before), true
	}
	st := sess.ResumeMapWalk()
	// A replan may leave the job paused (pending/lost tracker) or complete it
	// immediately if a fresh anchor is already the target; only the latter is a
	// successful terminal no-op.
	return formatMCPMapWalkState(st), st.State == "paused" || (!st.Active && st.Reason != "completed")
}

// confidenceGlyph renders a confidence enum for text output.
func confidenceGlyph(c mapper.Confidence) string {
	switch c {
	case mapper.Green:
		return "green (anchored)"
	case mapper.Yellow:
		return "yellow (tracker-only)"
	case mapper.Red:
		return "red (lost)"
	default:
		return string(c)
	}
}

// mcpWhere implements mud_where.
func (s *Server) mcpWhere(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return "session not connected — no live tracker.", true
	}
	var sb strings.Builder
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		idx := t.Index()
		if idx == nil {
			sb.WriteString("Active map set: (none)\n")
		} else {
			sb.WriteString(fmt.Sprintf("Active map set: id=%d (%d rooms)\n", idx.MapSetID, idx.RoomCount()))
		}
		pos := t.Position()
		sb.WriteString(fmt.Sprintf("Confidence: %s\n", confidenceGlyph(pos.Confidence)))
		sb.WriteString(fmt.Sprintf("pending_moves: %d\n", t.PendingCount()))
		if !pos.Valid {
			sb.WriteString("Position: unknown\n")
			if pos.Reason != "" {
				sb.WriteString("Reason: " + pos.Reason + "\n")
			}
			return
		}
		sb.WriteString(fmt.Sprintf("Position: zone=%q x=%d y=%d l=%d\n", pos.Coord.Zone, pos.Coord.X, pos.Coord.Y, pos.Coord.L))
		if room := t.CurrentRoom(); room != nil {
			tag := "(none)"
			if room.Tag != nil {
				tag = fmt.Sprintf("%d", *room.Tag)
			}
			sb.WriteString(fmt.Sprintf("Room: %q  tag=%s\n", room.Hint, tag))
			flags := []string{}
			if room.IsDT {
				flags = append(flags, "DEATH TRAP")
			}
			if room.Pipe {
				flags = append(flags, "pipe")
			}
			if len(flags) > 0 {
				sb.WriteString("Flags: " + strings.Join(flags, ", ") + "\n")
			}
		}
		if pos.Confidence != mapper.Green && pos.Reason != "" {
			sb.WriteString("Reason: " + pos.Reason + "\n")
		}
		// Structured exit diff (populated when we assumed a cell on a mismatch):
		// +live = in the game but not the map; -map = in the map but not the game.
		// This feeds a UI hover-diff and a future map-patch tool.
		if len(pos.ExitsAddedLive) > 0 || len(pos.ExitsRemovedMap) > 0 {
			var d []string
			for _, x := range pos.ExitsAddedLive {
				d = append(d, "+"+x)
			}
			for _, x := range pos.ExitsRemovedMap {
				d = append(d, "-"+x)
			}
			sb.WriteString("Exit diff (live vs map): " + strings.Join(d, " ") + "\n")
		}
	})
	if !found {
		return "No tracker for this session yet (no active map set loaded).", false
	}
	return sb.String(), false
}

// mcpLookMap implements mud_look_map / mud_room.
func (s *Server) mcpLookMap(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return "session not connected — no live tracker.", true
	}
	var sb strings.Builder
	var annoCoord *mcpCoordArg
	var mapSetID int64
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		pos := t.Position()
		sb.WriteString(fmt.Sprintf("Confidence: %s\n", confidenceGlyph(pos.Confidence)))
		room := t.CurrentRoom()
		if room == nil {
			sb.WriteString("Current room: unknown (no anchored position).\n")
			return
		}
		if idx := t.Index(); idx != nil {
			mapSetID = idx.MapSetID
		}
		annoCoord = &mcpCoordArg{Zone: room.Zone, X: room.X, Y: room.Y, L: room.L}
		sb.WriteString(fmt.Sprintf("Hint: %s\n", room.Hint))
		if room.Desc != "" {
			sb.WriteString(fmt.Sprintf("Desc: %s\n", room.Desc))
		}
		if room.IsDT {
			sb.WriteString("** DEATH TRAP **\n")
		}
		if room.Pipe {
			sb.WriteString("(pipe corridor)\n")
		}
		sb.WriteString("Exits:\n")
		writeExitLine(&sb, t, room)
	})
	// Surface the current room's annotation (if any) below the structural view.
	// Read off the tracker lock (DB access must not run under mapMu).
	if found && annoCoord != nil && mapSetID > 0 {
		if anno, ok, err := s.store.GetRoomAnnotation(mapSetID, annoCoord.Zone, annoCoord.X, annoCoord.Y, annoCoord.L); err == nil && ok {
			// showDT=false: the room-level "** DEATH TRAP **" above already covers it.
			sb.WriteString(formatAnnotation(anno, false))
		}
		cell := storage.EdgeCell{Zone: annoCoord.Zone, X: annoCoord.X, Y: annoCoord.Y, L: annoCoord.L}
		rows, truncated, err := s.store.ListRoomInhabitantsBounded(mapSetID, cell, storage.InhabitantListDefaultLimit)
		if err == nil && len(rows) > 0 {
			sb.WriteString("Known inhabitants:\n")
			for _, row := range rows {
				flags := row.Kind
				if row.Resident {
					flags += ", resident"
				}
				sb.WriteString(fmt.Sprintf("  %s [%s]", row.Name, flags))
				if row.Strategy != "" {
					sb.WriteString(" strategy=" + row.Strategy)
				}
				sb.WriteByte('\n')
			}
			if truncated {
				sb.WriteString("  ... inhabitants truncated after 20\n")
			}
		}
	}
	if !found {
		return "No tracker for this session yet (no active map set loaded).", false
	}
	return sb.String(), false
}

// writeExitLine renders the materialized graph, not room parser fields or import drafts.
// The latter are source inputs and can disagree with a manual edge, a dangling
// row, or two seams whose commands map to the same direction.
func writeExitLine(sb *strings.Builder, t *mapper.Tracker, room *mapper.IndexRoom) {
	idx := t.Index()
	edges := room.OutgoingTopology()
	if len(edges) == 0 {
		sb.WriteString("  (none)\n")
		return
	}
	total := len(edges)
	if len(edges) > mcpLookMapEdgeLimit {
		edges = edges[:mcpLookMapEdgeLimit]
	}
	for _, edge := range edges {
		resolution := topologyResolution(idx, edge)
		target := topologyTarget(idx, edge)
		attrs := topologyAttrs(edge)
		if edge.Kind == storage.EdgeKindGrid {
			line := fmt.Sprintf("  grid dir=%s source=%s passable=%t resolution=%s target=%s%s",
				edge.Direction, edge.Source, edge.Passable, resolution, target, attrs)
			if edge.Source == storage.EdgeSourceManual && edge.Passable && !room.ExitsInDir(edge.Direction) {
				line += " visibility=hidden [HIDDEN EXIT]"
			}
			if !edge.Passable {
				line += " [NO PASSAGE]"
			}
			sb.WriteString(line + "\n")
			continue
		}
		tag := "null"
		if edge.HasTargetTag {
			tag = fmt.Sprintf("%d", edge.TargetTag)
		}
		direction := seamDirFor(edge.Command)
		if direction == "" {
			direction = "unparsed"
		}
		line := fmt.Sprintf("  seam order=%d selector={target_zone=%q target_tag=%s command=%q} source=%s direction=%s passable=%t resolution=%s target=%s%s",
			edge.SeamOrder, edge.TargetZone, tag, edge.Command, edge.Source, direction, edge.Passable, resolution, target, attrs)
		sb.WriteString(line + "\n")
	}
	if total > len(edges) {
		sb.WriteString(fmt.Sprintf("  ... topology truncated: %d more outgoing edge(s)\n", total-len(edges)))
	}
}

const mcpLookMapEdgeLimit = 128

func topologyResolution(idx *mapper.Index, edge mapper.TopologyEdge) string {
	if !edge.Passable {
		return "blocked"
	}
	if !edge.HasTarget {
		if edge.Kind == storage.EdgeKindSeam {
			return "dead_target"
		}
		return "dangling"
	}
	if idx == nil || idx.Room(edge.Target) == nil {
		return "dangling"
	}
	if edge.NoRoute {
		return "vetoed"
	}
	return "mapped"
}

func topologyTarget(idx *mapper.Index, edge mapper.TopologyEdge) string {
	if !edge.HasTarget {
		return "unknown"
	}
	target := fmt.Sprintf("{zone=%q x=%d y=%d l=%d", edge.Target.Zone, edge.Target.X, edge.Target.Y, edge.Target.L)
	if idx != nil {
		if room := idx.Room(edge.Target); room != nil && room.IsDT {
			target += " is_dt=true"
		}
	}
	return target + "}"
}

func topologyAttrs(edge mapper.TopologyEdge) string {
	var parts []string
	if weight, ok := topologyNonDefaultWeight(edge.Weight); ok {
		parts = append(parts, fmt.Sprintf("weight=%g", weight))
	}
	if edge.Phrase != "" {
		parts = append(parts, fmt.Sprintf("phrase=%q", edge.Phrase))
	}
	if edge.Door {
		parts = append(parts, "door=true")
	}
	if edge.Locked {
		parts = append(parts, "locked=true")
	}
	if edge.OpenCmd != "" {
		parts = append(parts, fmt.Sprintf("open_cmd=%q", edge.OpenCmd))
	}
	if edge.NoRoute {
		parts = append(parts, "no_route=true")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func topologyNonDefaultWeight(weight float64) (float64, bool) {
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return 1, false
	}
	return weight, weight != 1
}

// mcpPath implements mud_path.
func (s *Server) mcpPath(sid int64, args mcpPathArgs) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return "session not connected — no live tracker.", true
	}
	var out string
	var isErr bool
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		idx := t.Index()
		if idx == nil {
			out, isErr = "No active map set for this session.", true
			return
		}
		// Determine start.
		var start mapper.Coord
		if args.From != nil {
			start = mapper.Coord{Zone: args.From.Zone, X: args.From.X, Y: args.From.Y, L: args.From.L}
		} else {
			pos := t.Position()
			if pos.Confidence == mapper.Red || !pos.Valid {
				out = "cannot path: position lost, re-anchor with mud_anchor_here"
				isErr = true
				return
			}
			start = pos.Coord
		}
		// Build the goal predicate.
		goal, desc, gerr := buildGoal(args)
		if gerr != "" {
			out, isErr = gerr, true
			return
		}
		res := idx.FindPath(start, goal)
		out, isErr = formatPath(desc, res)
	})
	if !found {
		return "No tracker for this session yet (no active map set loaded).", true
	}
	return out, isErr
}

// buildGoal converts the tool args into a BFS goal predicate + a human label.
func buildGoal(args mcpPathArgs) (func(*mapper.IndexRoom) bool, string, string) {
	switch {
	case args.To != nil:
		to := *args.To
		return func(r *mapper.IndexRoom) bool {
			return r.Zone == to.Zone && r.X == to.X && r.Y == to.Y && r.L == to.L
		}, fmt.Sprintf("cell {%s %d,%d,%d}", to.Zone, to.X, to.Y, to.L), ""
	case strings.TrimSpace(args.ToHint) != "":
		needle := strings.ToLower(strings.TrimSpace(args.ToHint))
		return func(r *mapper.IndexRoom) bool {
			return strings.Contains(strings.ToLower(r.Hint), needle)
		}, fmt.Sprintf("hint~%q", args.ToHint), ""
	case strings.TrimSpace(args.ToZone) != "":
		zone, err := storage.NormalizeZoneName(args.ToZone)
		if err != nil {
			return nil, "", "invalid to_zone: " + err.Error()
		}
		return func(r *mapper.IndexRoom) bool {
			return r.Zone == zone
		}, fmt.Sprintf("zone %q", zone), ""
	default:
		return nil, "", "no target given: provide to_zone, to_hint, or to:{zone,x,y,l}"
	}
}

// formatPath renders a PathResult as text, returning (text, isError).
func formatPath(targetDesc string, res mapper.PathResult) (string, bool) {
	if res.DTTarget {
		return fmt.Sprintf("REFUSED: target %s is a DEATH TRAP — die on entry.", targetDesc), true
	}
	if !res.Reachable {
		reason := res.Reason
		if reason == "" {
			reason = "unreachable"
		}
		if res.VetoedOnly {
			return fmt.Sprintf("No route to %s: %s "+
				"(the map is NOT disconnected — every remaining way is a face marked no_route via mud_room_mark_edge).",
				targetDesc, reason), true
		}
		return fmt.Sprintf("No route to %s: %s.", targetDesc, reason), true
	}
	var sb strings.Builder
	seams := 0
	dt := 0
	confirmedDoors := 0
	presumedDoors := 0
	locked := 0
	opens := 0
	var cmds []string
	for _, st := range res.Steps {
		if st.Seam {
			seams++
		}
		if st.IsDT {
			dt++
		}
		switch st.DoorKind {
		case mapper.DoorConfirmed:
			confirmedDoors++
		case mapper.DoorPresumed:
			presumedDoors++
		}
		if st.Locked {
			locked++
		}
		if st.OpenAction != "" {
			opens++
		}
		// The opening action is a COMMAND OF THE ROUTE, not a footnote: it is what
		// stops a door from cutting the walk short. It goes in front of the hop it
		// unlocks, in send order, exactly as the REST feed emits it.
		if st.OpenAction != "" {
			cmds = append(cmds, st.OpenAction)
		}
		cmds = append(cmds, st.Command)
	}
	sb.WriteString(fmt.Sprintf("Route to %s: %d command(s), %d seam(s), total weight %.6g", targetDesc, len(res.Steps), seams, res.TotalWeight))
	if confirmedDoors > 0 {
		sb.WriteString(fmt.Sprintf(", %d door(s) to open", confirmedDoors))
	}
	if presumedDoors > 0 {
		sb.WriteString(fmt.Sprintf(", %d presumed door(s)", presumedDoors))
	}
	if locked > 0 {
		sb.WriteString(fmt.Sprintf(", %d marked LOCKED by default", locked))
	}
	if dt > 0 {
		sb.WriteString(fmt.Sprintf(", WARNING %d death trap(s) on path", dt))
	}
	sb.WriteString("\n")
	sb.WriteString("Commands: " + strings.Join(cmds, "; ") + "\n")
	if confirmedDoors > 0 || presumedDoors > 0 {
		// Be honest about WHICH of the two door stories this route tells. A profile
		// that describes no door verb (every one but rmud today) produces no opening
		// commands, and promising the agent that the list is complete would send it
		// walking into a shut door.
		if opens > 0 {
			sb.WriteString("Note: the opening command for every door hop below is ALREADY in the command list, right before the move — the route is complete, not truncated at the door. " +
				"A presumed door is recorded only on the far side; opening is harmless if there is none. " +
				"A hop marked LOCKED needs the key: the route still plans through it because you may hold one.\n")
		} else {
			sb.WriteString("Note: this route crosses doors and carries NO opening commands — neither the edges nor this session's room format says how these doors open. " +
				"Open them by hand, or record the command with mud_room_mark_edge open_cmd=... so future routes carry it.\n")
		}
	}
	for i, st := range res.Steps {
		tag := ""
		switch st.DoorKind {
		case mapper.DoorConfirmed:
			tag += "  [DOOR" + doorOpenNote(st.OpenAction) + "]"
		case mapper.DoorPresumed:
			tag += "  [door? presumed" + doorOpenNote(st.OpenAction) + "]"
		}
		if st.Locked {
			tag += "  [LOCKED by default from this side — key needed]"
		}
		if st.Phrase != "" {
			tag += "  [taken by the phrase \"" + st.Phrase + "\", not by a direction]"
		}
		if st.Seam {
			tag += "  [SEAM →" + st.ToZone
			if st.SeamCommand != "" {
				tag += ", map cmd \"" + st.SeamCommand + "\""
			}
			tag += "]"
			if st.SeamUnparsed {
				tag += "  [WARNING seam command not a direction — sends raw \"" + st.SeamCommand + "\"; verify]"
			}
		}
		if st.IsDT {
			tag += "  [DEATH TRAP]"
		}
		if st.Cells > 1 {
			tag += fmt.Sprintf("  [pipe run: %d cells, one step]", st.Cells)
		}
		hint := st.Hint
		if hint == "" {
			hint = "?"
		}
		sb.WriteString(fmt.Sprintf("  %2d) %-10s -> %s%s\n", i+1, st.Command, hint, tag))
	}
	return sb.String(), false
}

// mcpAnchorHere implements mud_anchor_here.
func (s *Server) mcpAnchorHere(sid int64, c mcpCoordArg) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return "session not connected — no live tracker.", true
	}
	var out string
	var isErr bool
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		if t.Index() == nil {
			out, isErr = "No active map set for this session — cannot anchor.", true
			return
		}
		pos, exact := t.Anchor(mapper.Coord{Zone: c.Zone, X: c.X, Y: c.Y, L: c.L})
		var sb strings.Builder
		if exact {
			sb.WriteString("Anchored.\n")
		} else {
			sb.WriteString("Anchored to an unmapped cell (yellow).\n")
		}
		sb.WriteString(fmt.Sprintf("Confidence: %s\n", confidenceGlyph(pos.Confidence)))
		sb.WriteString(fmt.Sprintf("Position: zone=%q x=%d y=%d l=%d\n", pos.Coord.Zone, pos.Coord.X, pos.Coord.Y, pos.Coord.L))
		if room := t.CurrentRoom(); room != nil {
			sb.WriteString(fmt.Sprintf("Room: %q\n", room.Hint))
		}
		out = sb.String()
	})
	if !found {
		return "No tracker for this session yet (no active map set loaded).", true
	}
	// Broadcast the new position to UI clients.
	sess.BroadcastMapPosition()
	return out, isErr
}

// annotationTimeFormat renders an annotation's updated_at unambiguously (UTC).
const annotationTimeFormat = "2006-01-02 15:04:05 -0700"

// formatAnnotation renders one annotation as a labelled block for text output.
// Only non-empty fields are shown. showDT controls the dt line: callers that
// already surface the cell's DEATH-TRAP status at the room level (mud_look_map)
// pass false to avoid printing DEATH TRAP twice; the annotations reader passes
// true so dt is visible there.
func formatAnnotation(a storage.RoomAnnotation, showDT bool) string {
	var sb strings.Builder
	sb.WriteString("Annotation:\n")
	if showDT && a.DT {
		sb.WriteString("  dt: true (DEATH TRAP — annotated)\n")
	}
	if a.Hazard != "" {
		sb.WriteString("  hazard: " + a.Hazard + "\n")
	}
	if a.Note != "" {
		sb.WriteString("  note: " + a.Note + "\n")
	}
	if a.BattleLog != "" {
		sb.WriteString("  battle_log: " + a.BattleLog + "\n")
	}
	meta := ""
	if a.Author != "" {
		meta = "author=" + a.Author
	}
	if a.UpdatedAt != nil && !a.UpdatedAt.Time.IsZero() {
		if meta != "" {
			meta += " "
		}
		meta += "updated_at=" + a.UpdatedAt.Time.UTC().Format(annotationTimeFormat)
	}
	if meta != "" {
		sb.WriteString("  (" + meta + ")\n")
	}
	return sb.String()
}

// annotationFieldCap bounds each free-text annotation field so an agent can't
// persist unbounded text. Over-length values are truncated (with a soft note)
// rather than rejected — a slightly clipped note is more useful than a hard fail.
const annotationFieldCap = 2048

// capField truncates a field pointer in place to annotationFieldCap runes,
// returning the field label if it was clipped (for a soft note), else "".
func capField(p *string, label string) string {
	if p == nil {
		return ""
	}
	r := []rune(*p)
	if len(r) <= annotationFieldCap {
		return ""
	}
	*p = string(r[:annotationFieldCap])
	return label
}

// mcpRoomAnnotate implements mud_room_annotate (LLM overlay write). It resolves +
// VALIDATES the session's ACTIVE map set (in Go — a dangling active_map_set_id
// pointing at a deleted set soft-fails, not relying on the FK), caps the
// free-text fields, upserts the annotation for the given cell (edit-in-place,
// partial: only provided fields change), and broadcasts annotations_changed. A
// DT-touching annotate updates the live tracker index's effective is_dt IN PLACE
// (no rebuild) so mud_path's DT-refusal / mud_look_map reflect it WITHOUT resetting
// the tracker position (annotating the current room dt:true must not lose
// position). An annotation on a cell that is not a room in the active set is still
// written (dangling annotations are intentional) but flagged with a soft note.
// Soft-fails (isError) when the session has no valid active map set.
func (s *Server) mcpRoomAnnotate(sid int64, c mcpCoordArg, f storage.AnnotationFields) (string, bool) {
	setID, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil {
		return "failed to resolve active map set: " + err.Error(), true
	}
	if !ok || setID <= 0 {
		return "No active map set for this session — set one with mud_set_active_map_set before annotating.", true
	}
	// Validate the set exists in Go (the FK is not guaranteed enforced on every
	// pooled-connection path): a dangling reference must soft-fail, not 500 or
	// orphan a write.
	if _, err := s.store.GetMapSet(setID); err != nil {
		return fmt.Sprintf("Active map set %d for this session no longer exists — re-set it with mud_set_active_map_set.", setID), true
	}

	// Cap free-text fields (truncate, note which were clipped).
	var clipped []string
	for _, lbl := range []string{
		capField(f.Hazard, "hazard"),
		capField(f.Note, "note"),
		capField(f.BattleLog, "battle_log"),
		capField(f.Author, "author"),
	} {
		if lbl != "" {
			clipped = append(clipped, lbl)
		}
	}

	anno, err := s.store.UpsertRoomAnnotation(setID, c.Zone, c.X, c.Y, c.L, f)
	if err != nil {
		return "failed to write annotation: " + err.Error(), true
	}

	// Is this coord an actual room in the active set? A dangling annotation (on a
	// not-yet-mapped cell) is intentional per spec — we still write it, but note it.
	roomExists, err := s.store.RoomExistsInSet(setID, c.Zone, c.X, c.Y, c.L)
	if err != nil {
		roomExists = true // don't block on a lookup failure; skip the soft note
	}

	// Keep the tracker index in sync (AGENTS #2). A DT-touching annotate updates
	// the ONE cell's effective is_dt in place — no rebuild — so the tracker's
	// position/confidence and pending queue are untouched. Non-DT annotates need
	// no index change. Always broadcast the change for any UI.
	if sess, sok := s.manager.GetSession(sid); sok {
		if f.DT != nil {
			sess.ApplyAnnotationDT(c.Zone, c.X, c.Y, c.L, *f.DT)
		}
		sess.BroadcastServerMsg(annotationsChangedMsg(setID, c))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Annotated cell {%s %d,%d,%d} on map set %d.\n", c.Zone, c.X, c.Y, c.L, setID))
	if !roomExists {
		sb.WriteString("note: no room at this cell in the active set — annotation is dangling (kept for a not-yet-mapped cell).\n")
	}
	if len(clipped) > 0 {
		sb.WriteString(fmt.Sprintf("note: truncated to %d chars: %s.\n", annotationFieldCap, strings.Join(clipped, ", ")))
	}
	sb.WriteString(formatAnnotation(anno, true))
	return sb.String(), false
}

// mcpRoomAnnotations implements mud_room_annotations — a read of the annotations
// for the session's active set, optionally filtered to one zone.
func (s *Server) mcpRoomAnnotations(sid int64, zone string) (string, bool) {
	setID, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil {
		return "failed to resolve active map set: " + err.Error(), true
	}
	if !ok || setID <= 0 {
		return "No active map set for this session.", true
	}
	rows, err := s.store.ListRoomAnnotations(setID, zone)
	if err != nil {
		return "failed to list annotations: " + err.Error(), true
	}
	var sb strings.Builder
	scope := "all zones"
	if zone != "" {
		scope = fmt.Sprintf("zone %q", zone)
	}
	sb.WriteString(fmt.Sprintf("Map set %d annotations (%s): %d\n", setID, scope, len(rows)))
	if len(rows) == 0 {
		sb.WriteString("(none)\n")
		return sb.String(), false
	}
	for _, a := range rows {
		sb.WriteString(fmt.Sprintf("\n{%s %d,%d,%d}\n", a.Zone, a.X, a.Y, a.L))
		sb.WriteString(formatAnnotation(a, true))
	}
	return sb.String(), false
}

// mcpSetActiveMapSet implements mud_set_active_map_set (gated write).
func (s *Server) mcpSetActiveMapSet(sid, mapSet int64) (string, bool) {
	if mapSet <= 0 {
		return "map_set must be a positive id.", true
	}
	if _, err := s.store.GetMapSet(mapSet); err != nil {
		return fmt.Sprintf("map set %d not found.", mapSet), true
	}
	var switchErr error
	if sess, ok := s.manager.GetSession(sid); ok {
		switchErr = sess.SetActiveMapSet(mapSet)
	} else {
		switchErr = s.store.SetActiveMapSetID(sid, mapSet)
	}
	if err := switchErr; err != nil {
		return "failed to set active map set: " + err.Error(), true
	}
	set, _ := s.store.GetMapSet(mapSet)
	// Rebuild the tracker index for the live session, if any.
	return fmt.Sprintf("Active map set for session %d set to id=%d (%s). Tracker index rebuilt.", sid, mapSet, set.Name), false
}

// mcpMapUndo implements mud_map_undo: pop the last topology write on the session's
// active set and RESTORE the recorded before-state through the SAME unified
// write-path (broadcast + position-preserving refresh). The journal is per-map-set
// so this only reverses writes made to the currently-active set. Returns
// what was undone or "nothing to undo". Soft-fails (isError) when the session is not
// connected or has no active set.
func (s *Server) mcpMapUndo(sid int64) (string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return "session not connected — no live tracker.", true
	}
	res, entry, err := sess.UndoTopology()
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			return "No active map set for this session — nothing to undo.", true
		}
		return "failed to undo: " + err.Error(), true
	}
	if res.NothingToUndo {
		return fmt.Sprintf("Nothing to undo on map set %d.", res.SetID), false
	}
	zone, x, y, l, ok := entry.PrimaryCell()
	if !res.Applied {
		// Every target cell vanished (e.g. deleted by a later write) — the before-state
		// could not be restored. Report it rather than silently succeeding.
		if ok {
			return fmt.Sprintf("Undo skipped: target cell {%s %d,%d,%d} no longer exists in set %d.",
				zone, x, y, l, res.SetID), true
		}
		return fmt.Sprintf("Undo skipped: target no longer exists in set %d.", res.SetID), true
	}
	return fmt.Sprintf("Undid last write on map set %d (%s) — restored cell {%s %d,%d,%d}.",
		res.SetID, entry.Label, zone, x, y, l), false
}

// --- topology CRUD ----------------------------------------------------------
//
// mud_room_upsert / mud_room_link / mud_room_unlink / mud_room_delete all funnel
// through Session.WriteTopology (the unified write-path): fork-if-frozen +
// per-set lock + generalized before-state undo journal + rooms_changed broadcast +
// position-preserving tracker refresh. Each soft-fails (isError) on no active set /
// invalid input, and reports forked_to when the write forked a frozen set. Undo of
// any of them is mud_map_undo.

// writeTopologyResultText renders the shared confirmation tail (fork note) for a
// successful topology write.
func writeTopologyResultText(sb *strings.Builder, res session.TopologyWriteResult) {
	if res.Forked {
		sb.WriteString(fmt.Sprintf("forked_to: %d (frozen set forked to an editable copy; session now active on it)\n", res.ForkedTo))
	}
}

// topoWrite runs op through the session write-path and maps the common outcomes
// (no session / no active set) to soft-fail text. Returns (res, ""/errText,
// isError). On success errText is "" and isError false.
func (s *Server) topoWrite(sid int64, op storage.TopologyOp) (session.TopologyWriteResult, string, bool) {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return session.TopologyWriteResult{}, "session not connected — no live tracker.", true
	}
	res, err := sess.WriteTopology(op)
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			return res, "No active map set for this session — set one with mud_set_active_map_set first.", true
		}
		return res, "failed to write topology: " + err.Error(), true
	}
	return res, "", false
}

func formatZoneMoveReport(p storage.ZoneMovePreview) string {
	return fmt.Sprintf("revision=%d rooms=%d internal_edges=%d boundary_grid_edges=%d portals=%d incoming=%d outgoing=%d cross_zone_contacts=%d components=%d disconnected=%t annotations_moved=%d collisions=%d source_empties=%t target_exists=%t",
		p.Revision, p.Rooms, p.InternalEdges, p.BoundaryGridEdges, p.Portals, p.Incoming, p.Outgoing, p.CrossZoneContacts, p.Components, p.Disconnected, p.AnnotationsMoved, p.CollisionCount, p.SourceEmpties, p.TargetExists)
}

// mcpZoneMove keeps dry-run read-only. Apply intentionally goes through topoWrite
// so COW, serialization, undo and live tracker relocation are identical to every
// other topology write.
func (s *Server) mcpZoneMove(sid int64, in storage.ZoneMoveInput, dryRun bool) (string, bool) {
	if dryRun {
		setID, ok, err := s.store.GetActiveMapSetID(sid)
		if err != nil {
			return "failed to read active map set: " + err.Error(), true
		}
		if !ok {
			return "No active map set for this session.", true
		}
		p, err := s.store.PreviewZoneMove(setID, in)
		if err != nil {
			return err.Error(), true
		}
		return "Preview: " + formatZoneMoveReport(p) + ".", false
	}
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{Kind: storage.TopoMoveRooms, ZoneMove: &in})
	if isErr {
		return errText, true
	}
	if !res.Applied || res.ZoneMove == nil {
		return "zone move did not apply.", true
	}
	if !res.Forked {
		s.manager.RefreshSharedZoneMove(res.SetID, sid, in)
	}
	var sb strings.Builder
	sb.WriteString("Applied: " + formatZoneMoveReport(*res.ZoneMove) + ".\n")
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// mcpRoomUpsert implements mud_room_upsert: create-or-partial-update a room by
// key (zone,x,y,l). Only provided fields change (create defaults the rest). The
// free-text fields (hint/desc/note) are capped to annotationFieldCap runes —
// truncated with a soft note rather than rejected — so an agent in a loop cannot
// bloat a room row (matching the annotation cap used by the annotation API).
func (s *Server) mcpRoomUpsert(sid int64, c mcpCoordArg, f storage.RoomFields) (string, bool) {
	var clipped []string
	for _, lbl := range []string{
		capField(f.Hint, "hint"),
		capField(f.Desc, "desc"),
		capField(f.Note, "note"),
	} {
		if lbl != "" {
			clipped = append(clipped, lbl)
		}
	}

	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: storage.TopoUpsertRoom, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L, Fields: f,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return "upsert did not apply (unexpected).", true
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Upserted room {%s %d,%d,%d} on map set %d.\n", c.Zone, c.X, c.Y, c.L, res.SetID))
	if len(clipped) > 0 {
		sb.WriteString(fmt.Sprintf("note: truncated to %d chars: %s.\n", annotationFieldCap, strings.Join(clipped, ", ")))
	}
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// mcpRoomLink / mcpRoomUnlink implement mud_room_link / mud_room_unlink: add or
// remove an exit-edge between the cell and its grid neighbor (bidirectional when
// the neighbor room exists; one-sided otherwise).
func (s *Server) mcpRoomLink(sid int64, c mcpCoordArg, dir string, add bool) (string, bool) {
	kind := storage.TopoLink
	verb := "Linked"
	if !add {
		kind = storage.TopoUnlink
		verb = "Unlinked"
	}
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: kind, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L, Dir: dir,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return fmt.Sprintf("No room at {%s %d,%d,%d} in the active set — cannot %s.",
			c.Zone, c.X, c.Y, c.L, strings.ToLower(verb)), true
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s exit %s at {%s %d,%d,%d} on map set %d (mirrored on the grid neighbor if a room is there).\n",
		verb, dir, c.Zone, c.X, c.Y, c.L, res.SetID))
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// --- the hand-drawn layer (hidden exits + negation) ------------------------
//
// WHY THIS IS NOT A FLAG ON mud_room_link. The obvious cheaper move would have
// been mud_room_link{manual:true}, and it was rejected: link and this write
// disagree about WHAT they change, not about how. mud_room_link edits the ROOM's
// exits — it writes rooms.edirs/ch/exits, the result appears in the room's exit
// list, and the very next «Записать комнату» / mud_room_upsert re-parses the
// live text and overwrites it. This write touches no room field; it writes the
// edge, which is exactly what makes it survive. A boolean that silently moves the
// write to a different table, changes what the room reports afterwards, and
// changes whether the next room update erases it, is not a modifier of an
// operation — it is a second operation wearing the first one's name. And
// mud_room_unlink would have become ambiguous the moment both layers can speak
// about one face: which of the two does it take back?
//
// Marks are mutually exclusive on a face (one row per (cell, dir)), so ONE tool
// with an explicit mark beats three verbs that can contradict each other.

// manualMarkKind maps the tool's `mark` argument to the topology op kind.
func manualMarkKind(mark string) (storage.TopologyOpKind, bool) {
	switch strings.ToLower(strings.TrimSpace(mark)) {
	case "open":
		return storage.TopoAddManualEdge, true
	case "blocked":
		return storage.TopoBlockManualEdge, true
	case "auto":
		return storage.TopoRemoveManualEdge, true
	}
	return "", false
}

// mcpTrimmedPtr trims a supplied free-text attribute, preserving "omitted" (nil):
// a stray " " must not read as "an opening command is recorded" and suppress the
// default one.
func mcpTrimmedPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// doorOpenNote annotates a door hop with the command the route sends for it, or
// says plainly that there is none — never an empty pair of quotes, which reads as
// "send nothing" and is indistinguishable from a bug.
func doorOpenNote(open string) string {
	if open == "" {
		return " — no opening command known; open it by hand"
	}
	return " — open with \"" + open + "\""
}

// sessionDoorOpenTemplate reports the door-opening command template the session's
// ROOM-FORMAT PROFILE supplies, or "" when it describes none. It is what a route
// falls back to for a door with no per-edge command, so anything that TELLS the user
// what will be sent has to ask here rather than name a constant.
func (s *Server) sessionDoorOpenTemplate(sid int64) string {
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		return ""
	}
	var out string
	sess.WithMapTracker(func(t *mapper.Tracker) { out = t.Index().DoorOpenTemplate() })
	return out
}

// mcpRoomEdgeAttrs implements the ATTRIBUTE half of mud_room_mark_edge: the
// traversal phrase, the door + lock + opening command, and the routing veto.
//
// It lives on the SAME tool rather than a new one because it is the same question
// asked of the same object — "what is true about this face" — and an agent that has
// just found a secret passage taken by "двигать ящик" would otherwise have to know
// to call a second tool to finish the one thought. The write is a separate
// TopologyOp under the hood (attributes are one-sided; a mark defaults to the pair),
// so the two halves are separately undoable, which is what one would want anyway.
//
// Always exactly one face — see storage.setEdgeAttrsWithSnapshot for why `one_way`
// does not apply here.
func (s *Server) mcpRoomEdgeAttrs(sid int64, c mcpCoordArg, dir string, patch storage.EdgeAttrPatch) (string, bool) {
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: storage.TopoSetEdgeAttrs, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L, Dir: dir, Attrs: patch,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return fmt.Sprintf("Cannot describe the %s face of {%s %d,%d,%d}: there is no passage on it in the active set. "+
			"Attributes say HOW to take a passage; create one first with mud_room_mark_edge mark=\"open\" "+
			"(or fix the room's exits with mud_room_upsert).", dir, c.Zone, c.X, c.Y, c.L), true
	}
	var parts []string
	if patch.Weight != nil {
		parts = append(parts, fmt.Sprintf("route weight %.6g", *patch.Weight))
	}
	if patch.Phrase != nil {
		if *patch.Phrase == "" {
			parts = append(parts, "traversal phrase cleared (the direction letter takes it again)")
		} else {
			parts = append(parts, fmt.Sprintf("traversal phrase %q — routes emit it INSTEAD of %q", *patch.Phrase, strings.ToLower(dir)))
		}
	}
	if patch.Door != nil {
		if *patch.Door {
			parts = append(parts, "door: yes")
		} else {
			parts = append(parts, "door: no")
		}
	}
	if patch.Locked != nil {
		if *patch.Locked {
			parts = append(parts, "locked by default from this side (reported on the route, not refused)")
		} else {
			parts = append(parts, "not locked from this side")
		}
	}
	if patch.OpenCmd != nil {
		if *patch.OpenCmd == "" {
			// What the route falls back to is THIS SESSION's room-format profile, not a
			// fixed string: a profile that describes no door verb sends nothing at all.
			// Reporting a constant here would promise an English mud a Russian command.
			if tmpl := s.sessionDoorOpenTemplate(sid); tmpl != "" {
				parts = append(parts, fmt.Sprintf("opening command cleared (routes fall back to this session's room format: %q)", tmpl))
			} else {
				parts = append(parts, "opening command cleared (this session's room format describes no door verb, "+
					"so routes will send nothing and the door has to be opened by hand)")
			}
		} else {
			parts = append(parts, fmt.Sprintf("opening command %q", *patch.OpenCmd))
		}
	}
	if patch.NoRoute != nil {
		if *patch.NoRoute {
			parts = append(parts, "DO NOT ROUTE through this face (still walkable; the tracker accepts the step as usual)")
		} else {
			parts = append(parts, "routing allowed through this face again")
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Face %s of {%s %d,%d,%d} on map set %d: %s.\n",
		dir, c.Zone, c.X, c.Y, c.L, res.SetID, strings.Join(parts, "; ")))
	sb.WriteString("The face is now HAND-DRAWN, so re-recording the room will not erase this. " +
		"mud_room_mark_edge mark=\"auto\" gives it back to the room's own exits (and drops these attributes).\n")
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// mcpRoomSeamAttrs is the semantic-selector counterpart to mcpRoomEdgeAttrs.
// A seam has no grid direction, so accepting a dir here would invite a caller to
// edit an unrelated face; source coordinate plus parsed target fields identify it.
func (s *Server) mcpRoomSeamAttrs(sid int64, c mcpCoordArg, seam storage.SeamSelector, patch storage.EdgeAttrPatch) (string, bool) {
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: storage.TopoSetEdgeAttrs, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L, Seam: &seam, Attrs: patch,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return fmt.Sprintf("Cannot describe seam %q to %s tag %d from {%s %d,%d,%d}: it is absent or ambiguous in the active set.",
			seam.Command, seam.TargetZone, *seam.TargetTag, c.Zone, c.X, c.Y, c.L), true
	}
	var parts []string
	if patch.Weight != nil {
		parts = append(parts, fmt.Sprintf("route weight %.6g", *patch.Weight))
	}
	if patch.Phrase != nil {
		parts = append(parts, fmt.Sprintf("traversal phrase %q", *patch.Phrase))
	}
	if patch.Door != nil {
		parts = append(parts, fmt.Sprintf("door: %t", *patch.Door))
	}
	if patch.Locked != nil {
		parts = append(parts, fmt.Sprintf("locked by default: %t", *patch.Locked))
	}
	if patch.OpenCmd != nil {
		parts = append(parts, fmt.Sprintf("opening command %q", *patch.OpenCmd))
	}
	if patch.NoRoute != nil {
		parts = append(parts, fmt.Sprintf("routing allowed: %t", !*patch.NoRoute))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Seam %q to %s tag %d from {%s %d,%d,%d} on map set %d: %s.\n",
		seam.Command, seam.TargetZone, *seam.TargetTag, c.Zone, c.X, c.Y, c.L, res.SetID, strings.Join(parts, "; ")))
	sb.WriteString("The seam is now manually owned, so room re-recording does not erase these attributes.\n")
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

func (s *Server) mcpRoomPortal(sid int64, c mcpCoordArg, op storage.TopologyOp) (string, bool) {
	res, errText, isErr := s.topoWrite(sid, op)
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return fmt.Sprintf("Portal operation could not apply from {%s %d,%d,%d}: the source, exact target, or selected occurrence is absent.", c.Zone, c.X, c.Y, c.L), true
	}
	p := res.Portal
	if p == nil {
		return "Portal write completed without an authoritative selector.", true
	}
	verb := "Updated"
	if op.Kind == storage.TopoCreatePortal {
		verb = "Created"
	}
	if op.Kind == storage.TopoDeletePortal {
		verb = "Deleted"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s directed portal selector={order=%d} source={zone=%q x=%d y=%d l=%d}", verb, p.Selector.Order, p.Source.Zone, p.Source.X, p.Source.Y, p.Source.L))
	if p.Target != nil {
		sb.WriteString(fmt.Sprintf(" target={zone=%q x=%d y=%d l=%d}", p.Target.Zone, p.Target.X, p.Target.Y, p.Target.L))
	}
	sb.WriteString(fmt.Sprintf(" command=%q deleted=%t. Orders are stable and sparse; this is a portal/jump, not a grid direction or symbolic zone/tag seam.\n", p.Command, p.Deleted))
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// mcpRoomMarkEdge implements mud_room_mark_edge.
func (s *Server) mcpRoomMarkEdge(sid int64, c mcpCoordArg, dir string, kind storage.TopologyOpKind, oneWay bool) (string, bool) {
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: kind, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L, Dir: dir, OneWay: oneWay,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		// The only soft failure left: one of the two cells has no room. Say WHICH
		// constraint failed, because "nothing happened" is otherwise unreadable.
		return fmt.Sprintf("Cannot mark the %s face of {%s %d,%d,%d}: a hand-drawn edge needs a room on BOTH sides, "+
			"and the grid neighbour in that direction (or the cell itself) is empty in the active set. "+
			"Create it with mud_room_upsert first.", dir, c.Zone, c.X, c.Y, c.L), true
	}
	var sb strings.Builder
	scope := "both directions"
	if oneWay {
		scope = "one way only (out of this cell)"
	}
	switch kind {
	case storage.TopoAddManualEdge:
		sb.WriteString(fmt.Sprintf("Marked a HIDDEN EXIT %s from {%s %d,%d,%d} on map set %d — %s. "+
			"It is not in the room's exits line and re-recording the room will not remove it.\n",
			dir, c.Zone, c.X, c.Y, c.L, res.SetID, scope))
	case storage.TopoBlockManualEdge:
		sb.WriteString(fmt.Sprintf("Marked %s from {%s %d,%d,%d} as NO PASSAGE on map set %d — %s. "+
			"Routes will not use it, and re-recording the room will not bring it back.\n",
			dir, c.Zone, c.X, c.Y, c.L, res.SetID, scope))
	default:
		sb.WriteString(fmt.Sprintf("Cleared the hand-drawn mark on %s from {%s %d,%d,%d} on map set %d — %s. "+
			"This face is decided by the room's own exits again.\n",
			dir, c.Zone, c.X, c.Y, c.L, res.SetID, scope))
	}
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// mcpRoomDelete implements mud_room_delete: delete the room at the cell. Its
// room_annotations / room_images are left dangling (annotation-overlay model).
func (s *Server) mcpRoomDelete(sid int64, c mcpCoordArg) (string, bool) {
	res, errText, isErr := s.topoWrite(sid, storage.TopologyOp{
		Kind: storage.TopoDeleteRoom, Zone: c.Zone, X: c.X, Y: c.Y, L: c.L,
	})
	if isErr {
		return errText, true
	}
	if !res.Applied {
		return fmt.Sprintf("No room at {%s %d,%d,%d} in the active set — nothing to delete.",
			c.Zone, c.X, c.Y, c.L), true
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Deleted room {%s %d,%d,%d} on map set %d (any annotations/images left dangling; mud_map_undo recreates it).\n",
		c.Zone, c.X, c.Y, c.L, res.SetID))
	writeTopologyResultText(&sb, res)
	return sb.String(), false
}

// isCanonicalDir reports whether dir is one of the six canonical direction
// letters (already upper-cased/trimmed by the caller).
func isCanonicalDir(dir string) bool {
	switch dir {
	case "N", "S", "E", "W", "U", "D":
		return true
	}
	return false
}

// --- small helpers ---------------------------------------------------------

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// seamDirFor maps a seam command to a canonical direction letter for annotation
// (best-effort; empty if it does not map to a single direction).
func seamDirFor(cmd string) string {
	if d, ok := mapper.MoveDir(cmd); ok {
		return d
	}
	fields := strings.Fields(strings.ToLower(cmd))
	if len(fields) == 0 {
		return ""
	}
	if d, ok := mapper.MoveDir(fields[len(fields)-1]); ok {
		return d
	}
	return ""
}
