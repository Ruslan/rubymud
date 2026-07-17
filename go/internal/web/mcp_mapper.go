package web

import (
	"fmt"
	"strings"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// annotationsChangedMsg builds the light change-notification broadcast for an
// annotation write (plan §8). It carries the map set id and the touched cell so a
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

// This file implements the phase-3 mapper MCP tools: thin read-only wrappers over
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
	}
	if !found {
		return "No tracker for this session yet (no active map set loaded).", false
	}
	return sb.String(), false
}

// writeExitLine writes one line per direction the room reports, with door
// markers, ch-connectivity (mapped|unmapped), seam target zone, and target DT.
func writeExitLine(sb *strings.Builder, t *mapper.Tracker, room *mapper.IndexRoom) {
	idx := t.Index()
	doorSet := map[string]bool{}
	for _, d := range room.Doors {
		doorSet[d] = true
	}
	// Seam commands keyed by their canonical dir for annotation.
	seamByDir := map[string]mapper.Seam{}
	for _, a := range room.Automaps {
		if seam, ok := mapper.ParseSeam(a); ok {
			seamByDir[seamDirFor(seam.Command)] = seam
		}
	}
	if len(room.EDirs) == 0 && len(seamByDir) == 0 {
		sb.WriteString("  (none)\n")
		return
	}
	for _, dir := range []string{"N", "S", "E", "W", "U", "D"} {
		if !containsStr(room.EDirs, dir) {
			continue
		}
		marker := dir
		if doorSet[dir] {
			marker = "(" + dir + ")"
		}
		conn := "unmapped"
		if room.HasCh(dir) {
			conn = "mapped"
		}
		line := fmt.Sprintf("  %s  %s", marker, conn)
		if seam, ok := seamByDir[dir]; ok {
			line += fmt.Sprintf("  →zone %q (%s)", seam.Zone, seam.Command)
			if idx != nil {
				if tr := seamTargetRoom(idx, seam); tr != nil && tr.IsDT {
					line += "  [target DEATH TRAP]"
				}
			}
		}
		sb.WriteString(line + "\n")
	}
	// Seams that don't line up with an exit direction (rare) — list them too.
	for dir, seam := range seamByDir {
		if containsStr(room.EDirs, dir) {
			continue
		}
		sb.WriteString(fmt.Sprintf("  seam →zone %q (%s)\n", seam.Zone, seam.Command))
	}
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
		zone := strings.TrimSpace(args.ToZone)
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
		return fmt.Sprintf("No route to %s: %s.", targetDesc, reason), true
	}
	var sb strings.Builder
	seams := 0
	dt := 0
	confirmedDoors := 0
	presumedDoors := 0
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
		cmds = append(cmds, st.Command)
	}
	sb.WriteString(fmt.Sprintf("Route to %s: %d command(s), %d seam(s)", targetDesc, len(res.Steps), seams))
	if confirmedDoors > 0 {
		sb.WriteString(fmt.Sprintf(", %d door(s) to open", confirmedDoors))
	}
	if presumedDoors > 0 {
		sb.WriteString(fmt.Sprintf(", %d presumed door(s)", presumedDoors))
	}
	if dt > 0 {
		sb.WriteString(fmt.Sprintf(", WARNING %d death trap(s) on path", dt))
	}
	sb.WriteString("\n")
	sb.WriteString("Commands: " + strings.Join(cmds, "; ") + "\n")
	if confirmedDoors > 0 || presumedDoors > 0 {
		sb.WriteString("Note: [DOOR] hops need `open` first; [дверь? presumed] hops record a door only on the far side — `open` there too (harmless if none). Do not blindly batch a door hop.\n")
	}
	for i, st := range res.Steps {
		tag := ""
		switch st.DoorKind {
		case mapper.DoorConfirmed:
			tag += "  [DOOR — open first]"
		case mapper.DoorPresumed:
			tag += "  [дверь? presumed — open first]"
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
	if err := s.store.SetActiveMapSetID(sid, mapSet); err != nil {
		return "failed to set active map set: " + err.Error(), true
	}
	set, _ := s.store.GetMapSet(mapSet)
	// Rebuild the tracker index for the live session, if any.
	if sess, ok := s.manager.GetSession(sid); ok {
		sess.ReloadActiveMapSet()
	}
	return fmt.Sprintf("Active map set for session %d set to id=%d (%s). Tracker index rebuilt.", sid, mapSet, set.Name), false
}

// mcpMapUndo implements mud_map_undo: pop the last topology write on the session's
// active set and RESTORE the recorded before-state through the SAME unified
// write-path (broadcast + position-preserving refresh). The journal is per-map-set
// (plan §8), so this only reverses writes made to the currently-active set. Returns
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
	if !res.Applied {
		// The target cell vanished (e.g. deleted by a later write) — the before-state
		// could not be restored. Report it rather than silently succeeding.
		b := entry.Before
		return fmt.Sprintf("Undo skipped: target cell {%s %d,%d,%d} no longer exists in set %d.",
			b.Zone, b.X, b.Y, b.L, res.SetID), true
	}
	b := entry.Before
	return fmt.Sprintf("Undid last write on map set %d (%s) — restored cell {%s %d,%d,%d}.",
		res.SetID, entry.Label, b.Zone, b.X, b.Y, b.L), false
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

// seamTargetRoom resolves a seam's target room via the index (first tag match).
func seamTargetRoom(idx *mapper.Index, seam mapper.Seam) *mapper.IndexRoom {
	for _, r := range idx.Rooms() {
		if r.Zone == seam.Zone && r.Tag != nil && *r.Tag == seam.Tag {
			return r
		}
	}
	return nil
}
