package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// maxUploadBytes caps the archive upload size. The reference corpus is ~3MB
// unpacked; 64MB is generous headroom while bounding memory.
const (
	maxUploadBytes        = 64 << 20
	maxMultipartMemory    = 8 << 20
	maxMultipartOverhead  = 1 << 20
	maxUploadRequestBytes = maxUploadBytes + maxMultipartOverhead
)

// toStorageRooms converts parsed .mm2 rooms into persisted rows: direction lists
// become JSON text, the Ch list becomes a bitmask, and BColor is
// serialized to a *string (nil => NULL) so int and "cl..." forms round-trip.
func toStorageRooms(parsed []mapimport.Room) []storage.Room {
	out := make([]storage.Room, 0, len(parsed))
	for _, r := range parsed {
		out = append(out, storage.Room{
			Zone:        r.Zone,
			X:           r.X,
			Y:           r.Y,
			L:           r.L,
			DX:          r.DX,
			DY:          r.DY,
			DL:          r.DL,
			Tag:         r.Tag,
			Hint:        r.Hint,
			Desc:        r.Desc,
			Exits:       r.Exits,
			EDirs:       jsonArray(r.EDirs),
			Doors:       jsonArray(r.Doors),
			Ch:          r.ChMask(),
			ImageIndex:  r.ImageIndex,
			Note:        r.Note,
			IsDT:        r.IsDT,
			Pipe:        r.Pipe,
			BColor:      bcolorToString(r.BColor),
			Fingerprint: r.Fingerprint,
		})
	}
	return out
}

// legacySeamDrafts retains importer-only source strings just long enough for
// CreateMapSet to materialize map_edges in its transaction. Rooms never store
// these strings and all later reads use normalized seam rows.
func legacySeamDrafts(parsed []mapimport.Room) []storage.LegacySeamDraft {
	drafts := make([]storage.LegacySeamDraft, 0)
	for _, r := range parsed {
		if len(r.Automaps) == 0 {
			continue
		}
		drafts = append(drafts, storage.LegacySeamDraft{
			From:    storage.EdgeCell{Zone: r.Zone, X: r.X, Y: r.Y, L: r.L},
			Entries: append([]string(nil), r.Automaps...),
		})
	}
	return drafts
}

func jsonArray(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// bcolorToString serializes a parsed BColor (nil, int, or "cl..." string) to a
// nullable text column value. Ints are stored as decimal strings; the "cl..."
// idents are stored verbatim.
func bcolorToString(v any) *string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		s := t
		return &s
	case int:
		s := strconv.Itoa(t)
		return &s
	case int64:
		s := strconv.FormatInt(t, 10)
		return &s
	}
	return nil
}

// importMapSet handles POST /api/map-sets/import — a multipart upload of a .zip
// in form field "archive". Each upload creates a NEW map set and invalidates
// all live map-set selectors/panes after it commits.
func (s *Server) importMapSet(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxUploadRequestBytes {
		writeMapSetJSONError(w, http.StatusRequestEntityTooLarge, "archive request too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeMapSetJSONError(w, http.StatusRequestEntityTooLarge, "archive request too large")
			return
		}
		writeMapSetJSONError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("archive")
	if err != nil {
		writeMapSetJSONError(w, http.StatusBadRequest, "missing archive file")
		return
	}
	defer file.Close()
	if !strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
		writeMapSetJSONError(w, http.StatusBadRequest, "map archive must be a .zip file")
		return
	}

	data := make([]byte, 0)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > maxUploadBytes {
				writeMapSetJSONError(w, http.StatusRequestEntityTooLarge, "archive too large")
				return
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			writeMapSetJSONError(w, http.StatusBadRequest, "read archive failed")
			return
		}
	}

	name := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	if name == "" {
		name = "map set"
	}

	parsed, err := mapimport.ParseZip(data, name)
	if err != nil {
		message := "failed to parse map archive"
		conflicts := []mapimport.ImportConflict{}
		truncated := false
		var validation *mapimport.ValidationError
		if errors.As(err, &validation) {
			message = validation.Error()
			if validation.Summary.Conflicts != nil {
				conflicts = validation.Summary.Conflicts
			}
			truncated = validation.Summary.ConflictsTruncated
		}
		writeMapSetJSONErrorWithConflicts(w, http.StatusBadRequest, message, conflicts, truncated)
		return
	}

	in := storage.MapSetInput{
		Name:          name,
		SourceArchive: header.Filename,
		ZoneCount:     parsed.Summary.ZoneCount,
		RoomCount:     parsed.Summary.RoomCount,
		SeamCount:     parsed.Summary.SeamCount,
		Rooms:         toStorageRooms(parsed.Rooms),
		LegacySeams:   legacySeamDrafts(parsed.Rooms),
	}
	id, err := s.store.CreateMapSet(in)
	if err != nil {
		writeMapSetJSONError(w, http.StatusInternalServerError, "failed to store map set")
		return
	}

	s.manager.BroadcastMapSetsChanged()

	resp := map[string]any{
		"id":         id,
		"name":       name,
		"zone_count": parsed.Summary.ZoneCount,
		"room_count": parsed.Summary.RoomCount,
		"seam_count": parsed.Summary.SeamCount,
		"unresolved": parsed.Summary.Unresolved,
		"conflicts":  parsed.Summary.Conflicts,
	}
	if resp["unresolved"] == nil {
		resp["unresolved"] = []string{}
	}
	if resp["conflicts"] == nil {
		resp["conflicts"] = []mapimport.ImportConflict{}
	}
	writeJSON(w, resp)
}

// listMapSets handles GET /api/map-sets.
func (s *Server) listMapSets(w http.ResponseWriter, r *http.Request) {
	sets, err := s.store.ListMapSets()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sets == nil {
		sets = []storage.MapSet{}
	}
	writeJSON(w, sets)
}

// deleteMapSet handles DELETE /api/map-sets/{id}. The DB is authoritative: the
// image directory is first atomically renamed aside, the DB transaction removes
// the set and clears active sessions, then the staged files are removed. A
// post-commit filesystem cleanup error is reported in the successful response
// because retrying DELETE cannot resurrect a committed map set.
func (s *Server) deleteMapSet(w http.ResponseWriter, r *http.Request) {
	// Lock ordering is global admin mutex -> per-set topology lock. Session COW
	// never acquires the global mutex, and only nests source -> newly-created fork
	// locks, so no cycle can form. Holding this lock means a COW cannot fork a set
	// after DELETE has checked for forks and staged its image directory.
	s.mapSetDeleteMu.Lock()
	defer s.mapSetDeleteMu.Unlock()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeMapSetJSONError(w, http.StatusBadRequest, "invalid map set id")
		return
	}
	topologyLock := s.store.TopologyLock(id)
	topologyLock.Lock()
	defer topologyLock.Unlock()
	if s.mapSetDeleteAfterLock != nil {
		s.mapSetDeleteAfterLock()
	}
	if err := s.store.PreflightMapSetDelete(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeMapSetJSONError(w, http.StatusNotFound, "map set not found")
			return
		}
		if errors.Is(err, storage.ErrMapSetHasForks) {
			writeMapSetJSONError(w, http.StatusConflict, "delete editable forks first")
			return
		}
		writeMapSetJSONError(w, http.StatusInternalServerError, "check map set deletion failed")
		return
	}
	staged, err := s.stageMapSetImageDir(id)
	if err != nil {
		writeMapSetJSONError(w, http.StatusInternalServerError, "stage room image cleanup failed")
		return
	}
	if s.mapSetDeleteBeforeStoreDelete != nil {
		s.mapSetDeleteBeforeStoreDelete()
	}
	affected, err := s.store.DeleteMapSetWithAffectedSessions(id)
	if err != nil {
		if rollbackErr := staged.rollback(); rollbackErr != nil {
			writeMapSetJSONError(w, http.StatusInternalServerError, "restore room image cleanup failed")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeMapSetJSONError(w, http.StatusNotFound, "map set not found")
			return
		}
		if errors.Is(err, storage.ErrMapSetHasForks) {
			writeMapSetJSONError(w, http.StatusConflict, "delete editable forks first")
			return
		}
		writeMapSetJSONError(w, http.StatusInternalServerError, "delete map set failed")
		return
	}

	s.manager.ReloadDeletedMapSetSessions(affected)
	s.manager.BroadcastMapSetsChanged()
	response := map[string]any{"deleted": true}
	if err := staged.finalize(); err != nil {
		response["image_cleanup_error"] = err.Error()
	}
	writeJSON(w, response)
}

func writeMapSetJSONError(w http.ResponseWriter, status int, message string) {
	writeMapSetJSONErrorWithConflicts(w, status, message, []mapimport.ImportConflict{}, false)
}

func writeMapSetJSONErrorWithConflicts(w http.ResponseWriter, status int, message string, conflicts []mapimport.ImportConflict, truncated bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error":               message,
		"conflicts":           conflicts,
		"conflicts_truncated": truncated,
	})
}

// listMapSetZones handles GET /api/map-sets/{id}/zones.
func (s *Server) listMapSetZones(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid map set id", http.StatusBadRequest)
		return
	}
	zones, err := s.store.ListZones(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, zones)
}

// searchMapRooms handles GET /api/map-sets/{id}/rooms/search?q=&limit=&prefer_zone=.
func (s *Server) searchMapRooms(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid map set id", http.StatusBadRequest)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
	}
	if limit > 50 {
		limit = 50
	}
	hits, err := s.store.SearchMapRooms(
		id,
		r.URL.Query().Get("q"),
		r.URL.Query().Get("prefer_zone"),
		limit,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, hits)
}

// listRooms handles GET /api/rooms?map_set=<id>&zone=<z>.
func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("map_set"), 10, 64)
	if err != nil {
		http.Error(w, "invalid or missing map_set", http.StatusBadRequest)
		return
	}
	zone := r.URL.Query().Get("zone")
	if zone == "" {
		http.Error(w, "missing zone", http.StatusBadRequest)
		return
	}
	rooms, err := s.store.ListSlimRooms(id, zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, rooms)
}

// getActiveMapSet handles GET /api/sessions/{sessionID}/active-map-set.
// Returns {"active_map_set_id": <id>|null} for the session so the UI map pane
// knows which set to fetch. The column lives outside SessionRecord (managed via
// SetActiveMapSetID) so it is surfaced through this dedicated endpoint.
func (s *Server) getActiveMapSet(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	setID, ok, err := s.store.GetActiveMapSetID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"active_map_set_id": nil})
		return
	}
	writeJSON(w, map[string]any{"active_map_set_id": setID})
}

// setActiveMapSet handles POST /api/sessions/{sessionID}/active-map-set with
// body {"map_set_id": <id>} (id<=0 or null clears it). It mirrors the MCP
// mud_set_active_map_set write: persist the column, then reload the live
// session's tracker index via ReloadActiveMapSet (AGENTS #2).
func (s *Server) setActiveMapSet(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		MapSetID *int64 `json:"map_set_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var setID int64
	if body.MapSetID != nil {
		setID = *body.MapSetID
	}
	var setErr error
	if sess, ok := s.manager.GetSession(id); ok {
		setErr = sess.SetActiveMapSet(setID)
	} else {
		setErr = s.store.SetActiveMapSetID(id, setID)
	}
	if err := setErr; err != nil {
		if errors.Is(err, storage.ErrSessionNotFound) {
			http.Error(w, fmt.Sprintf("session %d not found", id), http.StatusNotFound)
			return
		}
		if errors.Is(err, storage.ErrMapSetNotFound) {
			http.Error(w, fmt.Sprintf("map set %d not found", setID), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Rebuild the tracker index for the live session, if any, and broadcast.
	if setID > 0 {
		writeJSON(w, map[string]any{"active_map_set_id": setID})
	} else {
		writeJSON(w, map[string]any{"active_map_set_id": nil})
	}
}

// anchorMapPosition handles POST /api/sessions/{sessionID}/map-anchor with body
// {zone,x,y,l} — the REST equivalent of the "I'm here" picker / MCP
// mud_anchor_here. It reuses the same tracker Anchor path and broadcasts the new
// position to UI clients. No-op (200) semantics: if the session is not connected
// or has no tracker, it reports that in the JSON without failing hard.
func (s *Server) anchorMapPosition(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Zone string `json:"zone"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
		L    int    `json:"l"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Zone = zone
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	// Session.AnchorMapPosition is the shared anchor path (tracker anchor +
	// position broadcast); map-record-room re-anchors through the same call so the
	// two cannot drift.
	anchored, exact := sess.AnchorMapPosition(mapper.Coord{Zone: body.Zone, X: body.X, Y: body.Y, L: body.L})
	if !anchored {
		writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "exact": exact})
}

// mapPath handles POST /api/sessions/{sessionID}/map-path with body
// {to:{zone,x,y,l}} — the REST equivalent of the "click a room to route there"
// map interaction. It computes a lowest-weight route from the tracker's CURRENT
// position (mirroring MCP mud_path's from-current-position case) and returns the
// route as canonical LOWERCASE ENGLISH direction tokens (n/s/e/w/u/d), one per
// server step, so the UI can join them with ';' and drop them into the command
// input. A seam hop has no single letter, so it emits the seam's MUD command for
// that step and is flagged. Soft-fails (200 with {reachable:false,reason}) when
// there is no session/tracker/position or the target is a DT — never 500.
func (s *Server) mapPath(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		To struct {
			Zone string `json:"zone"`
			X    int    `json:"x"`
			Y    int    `json:"y"`
			L    int    `json:"l"`
		} `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.To.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.To.Zone = zone
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"reachable": false, "reason": "session not connected — no live tracker"})
		return
	}
	to := mapper.Coord{Zone: body.To.Zone, X: body.To.X, Y: body.To.Y, L: body.To.L}
	var resp map[string]any
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		idx := t.Index()
		if idx == nil {
			resp = map[string]any{"reachable": false, "reason": "no active map set for this session"}
			return
		}
		pos := t.Position()
		if pos.Confidence == mapper.Red || !pos.Valid {
			resp = map[string]any{"reachable": false, "reason": "position lost — use \"I'm here\" to re-anchor"}
			return
		}
		// Clicking the current room: nothing to walk.
		if pos.Coord == to {
			resp = map[string]any{
				"reachable": true, "directions": []string{}, "summary": "already here",
				"here": true, "total_weight": float64(0),
			}
			return
		}
		res := idx.FindPath(pos.Coord, func(rm *mapper.IndexRoom) bool {
			return rm.Zone == to.Zone && rm.X == to.X && rm.Y == to.Y && rm.L == to.L
		})
		resp = pathResultJSON(res)
	})
	if !found {
		writeJSON(w, map[string]any{"reachable": false, "reason": "no active map set for this session"})
		return
	}
	writeJSON(w, resp)
}

// mapWalk starts the server-owned, confirmation-paced version of "send route".
// Unlike mapPath it never returns a semicolon script for the browser to blast at
// the MUD; Session owns the structured PathResult and sends one confirmed hop.
func (s *Server) mapWalk(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		To struct {
			Zone string `json:"zone"`
			X    int    `json:"x"`
			Y    int    `json:"y"`
			L    int    `json:"l"`
		} `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.To.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.To.Zone = zone
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"active": false, "reason": "session not connected — no live tracker"})
		return
	}
	writeJSON(w, sess.StartMapWalk(mapper.Coord{Zone: body.To.Zone, X: body.To.X, Y: body.To.Y, L: body.To.L}))
}

func (s *Server) cancelMapWalk(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"active": false, "reason": "session not connected"})
		return
	}
	writeJSON(w, sess.CancelMapWalk("cancelled by user"))
}

func (s *Server) resumeMapWalk(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"active": false, "reason": "session not connected"})
		return
	}
	writeJSON(w, sess.ResumeMapWalk())
}

// mapCellPatch handles POST /api/sessions/{sessionID}/map-cell-patch with body
// {zone,x,y,l, add_exits:[...], remove_exits:[...]} — the "update this cell from
// the live game state" write behind the map cell context menu's yellow-diff
// button. It patches THAT room of the session's ACTIVE map set IN PLACE (adds /
// removes the given exit directions on edirs + the ch connectivity bitmask), then
// reloads the tracker index so the new edge is known on the next reconcile and
// broadcasts rooms_changed so open map panes reload. Soft-fails (200 with
// {ok:false,reason}) when there is no active set or the room is not found — never
// 500. Directions are canonical upper-case letters ("N".."D"); unknown tokens are
// ignored by the store.
func (s *Server) mapCellPatch(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Zone        string   `json:"zone"`
		X           int      `json:"x"`
		Y           int      `json:"y"`
		L           int      `json:"l"`
		AddExits    []string `json:"add_exits"`
		RemoveExits []string `json:"remove_exits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Zone = zone
	// map-cell-patch now runs through the UNIFIED topology write-path:
	// fork-if-frozen (copy-on-write) + apply + undo journal + position-preserving
	// index refresh + rooms_changed broadcast. The write-path lives on the live
	// session (it owns the tracker + broadcasts), so an unconnected session soft-
	// fails here just as before.
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	op := storage.TopologyOp{
		Kind:        storage.TopoPatchExits,
		Zone:        body.Zone,
		X:           body.X,
		Y:           body.Y,
		L:           body.L,
		AddExits:    normalizeDirs(body.AddExits),
		RemoveExits: normalizeDirs(body.RemoveExits),
	}
	res, err := sess.WriteTopology(op)
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !res.Applied {
		writeJSON(w, map[string]any{"ok": false, "reason": "room not found in the active map set"})
		return
	}
	out := map[string]any{"ok": true}
	// A fork means the session's active set changed — tell the UI/agent so it can
	// switch to the new editable set.
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	writeJSON(w, out)
}

// mapLiveRoom handles GET /api/sessions/{sessionID}/map-live-room — the current
// live room block the parser last saw, as {seen,hint,desc,exits}.
//
// The `room` broadcast is fire-and-forget: a browser that connects (or reloads
// the page) AFTER the last room block has no live room until the player moves
// again, while the BACKEND still holds it and would happily record it. That
// disagreement let the UI offer a write it could not preview. This endpoint is
// the catch-up read so a freshly mounted map pane starts in the same state the
// server is in. Read-only; soft-fails with seen=false rather than erroring.
func (s *Server) mapLiveRoom(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"seen": false})
		return
	}
	ev, seen := sess.LastRoomEvent()
	if !seen {
		writeJSON(w, map[string]any{"seen": false})
		return
	}
	writeJSON(w, map[string]any{
		"seen": true, "hint": ev.Hint, "desc": ev.Desc, "exits": ev.Exits,
	})
}

// mapRecordRoom handles POST /api/sessions/{sessionID}/map-record-room with body
// {zone,x,y,l} (all optional) — the «Записать комнату» human autorecord behind the
// map cell menu. It writes the room the player is standing in
// RIGHT NOW into the active map set, using the live room text the parser already
// produced, covering BOTH cases with one mechanism:
//
//   - the cell exists but is outdated  -> partial UPDATE of hint/desc/exits;
//   - the cell does not exist at all   -> CREATE at the tracker's dead-reckoned
//     (🟡 "edge/cell not in map") coord.
//
// Exactly ONE TopologyOp (TopoUpsertRoom) is emitted, so exactly one undo entry
// reverses a record. No explicit link op is needed: IndexRoom.ConnectsTo treats an
// edge as real when EITHER endpoint advertises it, and the live event's exits
// already contain the direction back to where we came from — so routing into the
// new cell works in both directions from the upsert alone.
//
// COST of that simplification, which the caller MUST surface to the operator:
// writing the live exits makes the cell HasExplicitExits(), so ConnectsTo's first
// clause ("this room advertises dir") now fires for EVERY live exit — including
// ones whose real destination is not the room the map happens to draw at that grid
// coord. A recorded room can therefore create routing edges into unrelated
// neighbours, even neighbours that explicitly deny the back-link (the
// displaced-room guard only protects the direction ConnectsTo evaluates SECOND).
// We do not trim the exit set (that would defeat recording) and we do not touch
// ConnectsTo (a field-validated rule). Instead the response reports every
// neighbouring cell the written exits now touch, in `touches`, so the write is
// visible and reviewable, and one undo reverses it.
//
// Only Hint/Desc/Exits are set; Note/IsDT/Pipe/ImageIndex/DX/DY/DL are left nil so
// an existing room's manual metadata survives the update (upsertRoomWithSnapshot
// starts from the stored row and applies only non-nil fields). The fingerprint IS
// always recomputed from the resolved hint/desc/exits.
//
// The coord is optional: omitted means "the tracker's current position". An
// explicitly supplied coord MUST equal the current position — the live text
// describes where the player IS, so recording it onto some other cell would write
// the wrong room; that is refused (soft-fail), not silently applied.
//
// REFUSALS that protect existing map data (each a 200 + reason):
//   - 🔴 lost: Position.Valid stays TRUE on a text veto / true loss while holding
//     the LAST-KNOWN coord, and lastRoom already holds the teleport DESTINATION's
//     text — recording would overwrite a good, unrelated room and then cement a
//     false 🟢. (mapPath refuses Red for the same reason.)
//   - Position.EventDescribesCell false: the coord is held for bookkeeping but the
//     event does not describe it (ambiguous neighbor match, hint-less pipe event).
//   - empty live hint: the exits-only accumulator path yields Hint==""/Desc=="",
//     and writing it would wipe the room's name, description and fingerprint —
//     after which nothing can ever fingerprint-match that cell again.
//   - pending moves in flight: the post-write re-anchor FLUSHES the pending queue
//     (Tracker.Anchor's contract), which would silently drop an in-flight
//     speedwalk; it is also the state where the live text is least trustworthy.
//
// On success the tracker is re-anchored to the target cell (🟢) through the shared
// Session.AnchorMapPosition: recording is an explicit human assertion "this room is
// here", which is the conscious re-anchor the tracker requires instead of an
// automatic upgrade. That anchor also flushes the (already-verified-empty) pending
// queue. Never 4xx/5xx except a malformed body or a real storage error.
func (s *Server) mapRecordRoom(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Zone *string `json:"zone"`
		X    *int    `json:"x"`
		Y    *int    `json:"y"`
		L    *int    `json:"l"`
	}
	// An empty body is legal ("record where I am"), so a decode error on EOF is not
	// a client error.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, recordRefusal("session not connected — no live tracker"))
		return
	}
	// ONE coherent snapshot of (live room, position, occupancy, pending) under a
	// single mapMu acquisition. Reading the text and the position separately could
	// interleave with an incoming room block and pair the PREVIOUS room's text with
	// the NEW position — exactly the write that corrupts a cell.
	st := sess.LiveRecordState()
	if !st.HaveTracker || !st.HaveIndex {
		writeJSON(w, recordRefusal("no active map set for this session"))
		return
	}
	if !st.RoomSeen {
		writeJSON(w, recordRefusal("no live room seen yet — walk into the room (or look) first"))
		return
	}
	ev := st.Room
	pos := st.Position
	if !pos.Valid {
		writeJSON(w, recordRefusal("position unknown — use \"I'm here\" to re-anchor first"))
		return
	}
	if pos.Confidence == mapper.Red {
		writeJSON(w, recordRefusal("position lost (🔴) — the live room describes another cell; "+
			"use \"I'm here\" first"))
		return
	}
	if !pos.EventDescribesCell {
		writeJSON(w, recordRefusal("the live room does not match this cell "+
			"(position is held by dead reckoning) — use \"I'm here\" first"))
		return
	}
	if ev.Hint == "" {
		writeJSON(w, recordRefusal("the latest room block has no name "+
			"(corridor or exits only) — recording would erase the name and description"))
		return
	}
	if st.Pending > 0 {
		stepWord := "steps"
		if st.Pending == 1 {
			stepWord = "step"
		}
		writeJSON(w, recordRefusal(fmt.Sprintf(
			"movement in progress (%d queued %s) — recording would clear the queue; wait until it finishes",
			st.Pending, stepWord)))
		return
	}
	target := pos.Coord
	existed := st.RoomExists

	// An explicit coord is accepted only when it IS the current cell (see above).
	// Guard on ANY coord field being present, not just a non-empty zone: a body
	// like {"x":5,"y":9} must still be checked, not silently ignored.
	if body.Zone != nil || body.X != nil || body.Y != nil || body.L != nil {
		want := mapper.Coord{Zone: target.Zone, X: target.X, Y: target.Y, L: target.L}
		if body.Zone != nil {
			zone, zerr := storage.NormalizeZoneName(*body.Zone)
			if zerr != nil {
				http.Error(w, zerr.Error(), http.StatusBadRequest)
				return
			}
			want.Zone = zone
		}
		if body.X != nil {
			want.X = *body.X
		}
		if body.Y != nil {
			want.Y = *body.Y
		}
		if body.L != nil {
			want.L = *body.L
		}
		if want != target {
			writeJSON(w, recordRefusal("can only record the cell you are standing in "+
				fmt.Sprintf("(you are at {%s %d,%d,%d})", target.Zone, target.X, target.Y, target.L)))
			return
		}
	}

	// Exits go through RoomFields.Exits (the DISPLAY string) so the store's single
	// normalizer derives edirs/ch/doors consistently — the same path the MCP upsert
	// and the importer use.
	fields := storage.RoomFields{Hint: &ev.Hint, Desc: &ev.Desc, Exits: &ev.Exits}
	res, err := sess.WriteTopology(storage.TopologyOp{
		Kind: storage.TopoUpsertRoom,
		Zone: target.Zone, X: target.X, Y: target.Y, L: target.L,
		Fields: fields,
	})
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, recordRefusal("no active map set for this session"))
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !res.Applied {
		writeJSON(w, recordRefusal("the write did not apply to the active map set"))
		return
	}

	// Re-anchor onto the cell we just asserted. The index was already rebuilt by the
	// write-path, so a freshly CREATED cell is now real and the anchor is exact (🟢).
	_, exact := sess.AnchorMapPosition(target)

	out := map[string]any{
		"applied": true,
		"created": !existed,
		"updated": existed,
		"forked":  res.Forked,
		"set_id":  res.SetID,
		"exact":   exact,
		"zone":    target.Zone, "x": target.X, "y": target.Y, "l": target.L,
		"exits": ev.Exits,
		"hint":  ev.Hint,
		// Which existing cells the exits we just wrote now touch (see the
		// surplus-exit cost in the doc comment). The UI shows this so a fabricated
		// edge is visible immediately rather than discovered later via a bad route.
		"touches": recordTouches(sess, target),
	}
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	writeJSON(w, out)
}

// recordTouches lists the neighbouring cells the just-written room's exits
// ACTUALLY connected it to, so the caller can show WHICH rooms the record reached.
// It reads the rebuilt index (post-write) and reports one entry per outgoing GRID
// EDGE the write produced, with the room on the other side. Seams have no grid
// direction and are skipped. Read-only: it makes no routing decision, it only
// describes what the write produced.
//
// IT READS THE EDGES, NOT THE EXITS LINE. This report exists solely to make a
// write's blast radius visible, so a false entry in it is worse than no report at
// all: walking `room.EDirs` announced "the record connected you to X" for a face
// the player had marked as NO PASSAGE, where the write created nothing — the exits
// line still names the direction, and only the graph knows the passage was denied.
// Reading the materialized edges reports exactly the connections that exist, and
// picks up a hand-drawn hidden exit through the same cell for free.
func recordTouches(sess *session.Session, target mapper.Coord) []map[string]any {
	var out []map[string]any
	sess.WithMapTracker(func(t *mapper.Tracker) {
		idx := t.Index()
		if idx == nil {
			return
		}
		room := idx.Room(target)
		if room == nil {
			return
		}
		for _, dir := range []string{"N", "S", "E", "W", "U", "D"} {
			e := room.GridEdge(dir)
			if e == nil || !e.Passable || e.To == nil {
				continue
			}
			nc := *e.To
			nb := idx.Room(nc)
			if nb == nil {
				continue
			}
			out = append(out, map[string]any{
				"dir": dir, "zone": nc.Zone, "x": nc.X, "y": nc.Y, "l": nc.L,
				"hint": nb.Hint,
				// back_link=false means the neighbour does NOT advertise the reverse
				// exit: the edge exists only because the recorded room asserts it.
				"back_link": nb.HasExplicitExits() && nb.ExitsInDir(mapper.OppositeDir(dir)),
				// manual=true means the player drew this passage; the record did not
				// create it and undoing the record will not remove it.
				"manual": e.Source == storage.EdgeSourceManual,
			})
		}
	})
	return out
}

// recordRefusal is the shared soft-fail body for map-record-room: HTTP 200 with
// applied:false plus a human reason the UI shows verbatim.
func recordRefusal(reason string) map[string]any {
	return map[string]any{"applied": false, "created": false, "updated": false, "reason": reason}
}

// mapMarkEdge handles POST /api/sessions/{sessionID}/map-mark-edge with body
// {zone,x,y,l,dir,mark} — the write behind the map pane's EDGE EDITOR, the popup
// that opens on the boundary between two cells and shows both directed halves of
// that boundary side by side.
//
// `mark` is one of:
//
//	"open"    hand-drawn hidden exit  — there IS a way, whatever the exits line says
//	"blocked" hand-drawn negation     — there is NO way, whatever the exits line says
//	"auto"    drop the hand-drawn mark; this face is the derivation's again
//
// It is ALWAYS one directed face per call (one_way is implied): the popup edits
// the two halves independently, which is what makes a one-way passage — or a
// one-way wall — expressible at all. The MCP tool is the one that defaults to
// marking the pair.
//
// Same soft-fail contract as the other map writes: 200 with {ok:false,reason},
// never 4xx/5xx for a domain refusal.
func (s *Server) mapMarkEdge(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Zone string `json:"zone"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
		L    int    `json:"l"`
		Dir  string `json:"dir"`
		Mark string `json:"mark"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Zone = zone
	dir := strings.ToUpper(strings.TrimSpace(body.Dir))
	if !isCanonicalDir(dir) {
		writeJSON(w, map[string]any{"ok": false, "reason": "bad direction"})
		return
	}
	kind, ok := manualMarkKind(body.Mark)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "bad mark"})
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	res, err := sess.WriteTopology(storage.TopologyOp{
		Kind: kind, Zone: body.Zone, X: body.X, Y: body.Y, L: body.L, Dir: dir, OneWay: true,
	})
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !res.Applied {
		writeJSON(w, map[string]any{"ok": false,
			"reason": "a hand-drawn edge needs a room on BOTH sides of the boundary"})
		return
	}
	out := map[string]any{"ok": true}
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	writeJSON(w, out)
}

// mapEdgeAttrs handles POST /api/sessions/{sessionID}/map-edge-attrs with body
// {zone,x,y,l,dir?|seam?, weight?,phrase?,door?,locked?,open_cmd?,no_route?} — the
// write behind the directed grid and seam attribute controls. Exactly one of dir
// or seam is required; a seam is selected by target_zone, target_tag, command,
// and may include its persisted occurrence order to disambiguate duplicates.
//
// ONE DIRECTED FACE per call, and no `one_way` option, unlike map-mark-edge: every
// one of these attributes is a statement about the side you approach from (see
// storage.setEdgeAttrsWithSnapshot), so marking the pair would assert something
// about a face the operator never opened.
//
// Every attribute is OPTIONAL and omitted means "leave it alone" — the popup sends
// only the control that moved, so two people editing the two halves of a door cannot
// clobber each other's field.
//
// Same soft-fail contract as the other map writes: 200 with {ok:false,reason}.
func (s *Server) mapEdgeAttrs(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Zone string `json:"zone"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
		L    int    `json:"l"`
		Dir  string `json:"dir"`
		Seam *struct {
			TargetZone string  `json:"target_zone"`
			TargetTag  *int    `json:"target_tag"`
			Command    *string `json:"command"`
			Order      *int    `json:"order"`
		} `json:"seam"`
		Weight  *float64 `json:"weight"`
		Phrase  *string  `json:"phrase"`
		Door    *bool    `json:"door"`
		Locked  *bool    `json:"locked"`
		OpenCmd *string  `json:"open_cmd"`
		NoRoute *bool    `json:"no_route"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := storage.NormalizeZoneName(body.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Zone = zone
	dir := strings.ToUpper(strings.TrimSpace(body.Dir))
	var seam *storage.SeamSelector
	if body.Seam != nil && body.Seam.Command != nil {
		targetZone, zerr := storage.NormalizeZoneName(body.Seam.TargetZone)
		if zerr != nil {
			http.Error(w, zerr.Error(), http.StatusBadRequest)
			return
		}
		seam = &storage.SeamSelector{TargetZone: targetZone, TargetTag: body.Seam.TargetTag, Command: *body.Seam.Command, Order: body.Seam.Order}
	}
	if strings.TrimSpace(body.Zone) == "" || (dir == "" && seam == nil) || (dir != "" && body.Seam != nil) || (dir != "" && !isCanonicalDir(dir)) || (seam != nil && !seam.Valid()) || (body.Seam != nil && seam == nil) {
		writeJSON(w, map[string]any{"ok": false, "reason": "provide exactly one valid direction or seam selector"})
		return
	}
	patch := storage.EdgeAttrPatch{
		Weight: body.Weight, Phrase: trimmedPtr(body.Phrase), Door: body.Door, Locked: body.Locked,
		OpenCmd: trimmedPtr(body.OpenCmd), NoRoute: body.NoRoute,
	}
	if patch.Empty() {
		writeJSON(w, map[string]any{"ok": false, "reason": "nothing to set"})
		return
	}
	if err := patch.Validate(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
		return
	}
	if reason := edgeCommandsRefusal(patch); reason != "" {
		writeJSON(w, map[string]any{"ok": false, "reason": reason})
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	res, err := sess.WriteTopology(storage.TopologyOp{
		Kind: storage.TopoSetEdgeAttrs, Zone: body.Zone, X: body.X, Y: body.Y, L: body.L,
		Dir: dir, Seam: seam, Attrs: patch,
	})
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !res.Applied {
		writeJSON(w, map[string]any{"ok": false,
			"reason": "the directed passage is missing or the selected seam is ambiguous"})
		return
	}
	out := map[string]any{"ok": true}
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	writeJSON(w, out)
}

// mapPortal is the exact-target directed portal CRUD surface. It deliberately
// shares the session topology path with room and edge edits, so frozen-set COW,
// undo, tracker reload, and the single rooms_changed broadcast stay uniform.
func (s *Server) mapPortal(w http.ResponseWriter, r *http.Request) {
	if !validateMCPAdminOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	type coord struct {
		Zone *string `json:"zone"`
		X    *int    `json:"x"`
		Y    *int    `json:"y"`
		L    *int    `json:"l"`
	}
	type body struct {
		Zone    *string  `json:"zone"`
		X       *int     `json:"x"`
		Y       *int     `json:"y"`
		L       *int     `json:"l"`
		Order   *int     `json:"order"`
		Target  *coord   `json:"target"`
		Command *string  `json:"command"`
		Weight  *float64 `json:"weight"`
		Phrase  *string  `json:"phrase"`
		Door    *bool    `json:"door"`
		Locked  *bool    `json:"locked"`
		OpenCmd *string  `json:"open_cmd"`
		NoRoute *bool    `json:"no_route"`
	}
	var b body
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		writeJSON(w, map[string]any{"ok": false, "reason": "invalid body: " + err.Error()})
		return
	}
	if b.Zone == nil || b.X == nil || b.Y == nil || b.L == nil {
		writeJSON(w, map[string]any{"ok": false, "reason": "zone,x,y,l are required"})
		return
	}
	fromZone, zerr := storage.NormalizeZoneName(*b.Zone)
	if zerr != nil {
		http.Error(w, zerr.Error(), http.StatusBadRequest)
		return
	}
	from := storage.EdgeCell{Zone: fromZone, X: *b.X, Y: *b.Y, L: *b.L}
	attrs := storage.EdgeAttrPatch{Weight: b.Weight, Phrase: trimmedPtr(b.Phrase), Door: b.Door, Locked: b.Locked, OpenCmd: trimmedPtr(b.OpenCmd), NoRoute: b.NoRoute}
	if err := attrs.Validate(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
		return
	}
	if reason := edgeCommandsRefusal(attrs); reason != "" {
		writeJSON(w, map[string]any{"ok": false, "reason": reason})
		return
	}
	var op storage.TopologyOp
	switch r.Method {
	case http.MethodPost:
		if b.Order != nil {
			writeJSON(w, map[string]any{"ok": false, "reason": "order is not allowed when creating a portal"})
			return
		}
		if b.Target == nil || b.Target.Zone == nil || b.Target.X == nil || b.Target.Y == nil || b.Target.L == nil || b.Command == nil {
			writeJSON(w, map[string]any{"ok": false, "reason": "target zone,x,y,l and command are required"})
			return
		}
		targetZone, zerr := storage.NormalizeZoneName(*b.Target.Zone)
		if zerr != nil {
			http.Error(w, zerr.Error(), http.StatusBadRequest)
			return
		}
		op = storage.TopologyOp{Kind: storage.TopoCreatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L,
			Portal: &storage.PortalTarget{Zone: targetZone, X: *b.Target.X, Y: *b.Target.Y, L: *b.Target.L}, PortalCommand: *b.Command, PortalAttrs: attrs}
	case http.MethodPatch:
		if b.Order == nil || *b.Order < 0 {
			writeJSON(w, map[string]any{"ok": false, "reason": "non-negative portal order is required"})
			return
		}
		patch := storage.PortalPatch{Command: b.Command, Attrs: attrs}
		if b.Target != nil {
			if b.Target.Zone == nil || b.Target.X == nil || b.Target.Y == nil || b.Target.L == nil {
				writeJSON(w, map[string]any{"ok": false, "reason": "target requires zone,x,y,l"})
				return
			}
			targetZone, zerr := storage.NormalizeZoneName(*b.Target.Zone)
			if zerr != nil {
				http.Error(w, zerr.Error(), http.StatusBadRequest)
				return
			}
			patch.Target = &storage.PortalTarget{Zone: targetZone, X: *b.Target.X, Y: *b.Target.Y, L: *b.Target.L}
		}
		op = storage.TopologyOp{Kind: storage.TopoUpdatePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, PortalSelector: &storage.PortalSelector{Order: *b.Order}, PortalPatch: patch}
	case http.MethodDelete:
		if b.Order == nil || *b.Order < 0 {
			writeJSON(w, map[string]any{"ok": false, "reason": "non-negative portal order is required"})
			return
		}
		if b.Target != nil || b.Command != nil || !attrs.Empty() {
			writeJSON(w, map[string]any{"ok": false, "reason": "delete accepts only source coordinates and order"})
			return
		}
		op = storage.TopologyOp{Kind: storage.TopoDeletePortal, Zone: from.Zone, X: from.X, Y: from.Y, L: from.L, PortalSelector: &storage.PortalSelector{Order: *b.Order}}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	res, err := sess.WriteTopology(op)
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
		return
	}
	if !res.Applied {
		writeJSON(w, map[string]any{"ok": false, "reason": "source, target, or selected portal occurrence was not found"})
		return
	}
	out := map[string]any{"ok": true, "active_map_set": res.SetID}
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	if res.Portal != nil {
		p := res.Portal
		out["order"] = p.Selector.Order
		out["selector"] = map[string]any{"order": p.Selector.Order}
		out["portal"] = p
	}
	writeJSON(w, out)
}

// mapZoneMove exposes a two-phase namespace move. Preview is strictly read-only;
// apply compares the preview's revision in the same transaction that changes the
// rooms, edges and annotations. Rename is simply all source rooms moved at once.
func (s *Server) mapZoneMove(w http.ResponseWriter, r *http.Request) {
	if !validateMCPAdminOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	_, sid, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	type body struct {
		SourceZone       *string              `json:"source_zone"`
		TargetZone       *string              `json:"target_zone"`
		Rooms            *[]storage.ZoneCoord `json:"rooms"`
		ExpectedRevision *int64               `json:"expected_revision"`
	}
	var b body
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		writeJSON(w, map[string]any{"ok": false, "reason": "invalid body: " + err.Error()})
		return
	}
	if b.SourceZone == nil || b.TargetZone == nil {
		writeJSON(w, map[string]any{"ok": false, "reason": "source_zone and target_zone are required"})
		return
	}
	in := storage.ZoneMoveInput{SourceZone: *b.SourceZone, TargetZone: *b.TargetZone}
	action := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/api/sessions/%d/", sid))
	if strings.HasPrefix(action, "map-zones/rename") {
		if b.Rooms != nil {
			writeJSON(w, map[string]any{"ok": false, "reason": "rename selects the complete source zone server-side"})
			return
		}
		in.AllSource = true
	} else {
		if b.Rooms == nil {
			writeJSON(w, map[string]any{"ok": false, "reason": "rooms is required for a partial move"})
			return
		}
		in.Rooms = *b.Rooms
	}
	in, err = storage.NormalizeZoneMove(in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setID, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
		return
	}
	if action == "map-zones/preview" || strings.HasSuffix(action, "/preview") {
		report, err := s.store.PreviewZoneMove(setID, in)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "active_map_set": setID, "preview": report})
		return
	}
	if b.ExpectedRevision == nil {
		writeJSON(w, map[string]any{"ok": false, "reason": "expected_revision is required for apply"})
		return
	}
	in.ExpectedRevision = *b.ExpectedRevision
	sess, ok := s.manager.GetSession(sid)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	res, err := sess.WriteTopology(storage.TopologyOp{Kind: storage.TopoMoveRooms, ZoneMove: &in})
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
		return
	}
	if !res.Applied {
		writeJSON(w, map[string]any{"ok": false, "reason": "zone move did not apply"})
		return
	}
	if !res.Forked {
		s.manager.RefreshSharedZoneMove(res.SetID, sid, in)
	}
	out := map[string]any{"ok": true, "active_map_set": res.SetID, "preview": res.ZoneMove}
	if res.Forked {
		out["forked_to"] = res.ForkedTo
	}
	writeJSON(w, out)
}

// validateEdgeCommand refuses a traversal phrase or an opening command that is
// itself a MOVEMENT COMMAND, returning a human reason (or "" when the value is
// fine). Called on both write surfaces — REST map-edge-attrs and MCP
// mud_room_mark_edge — because it is the only thing standing between the two.
//
// WHY THIS IS A REFUSAL AND NOT A NOTE. Everything a route emits goes out through
// the normal command path, where Session.observeOutgoingMove asks mapper.MoveDir
// whether each command is a step and pushes it onto the tracker's pending queue if
// it is. A one-word value like "вверх" or "u" therefore does not merely fail to
// open a door — it injects a PHANTOM STEP and moves the tracker's idea of where the
// player is standing. That is position corruption, silent, and it survives until a
// re-anchor. A phrase that is a direction is also pointless: the direction letter
// already expresses it.
//
// It lives in web, not in storage, because storage cannot import mapper (mapper
// imports storage) and re-stating mapper's movement-word table here would be the
// exact drift that table is single for.
func validateEdgeCommand(field, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if dir, ok := mapper.MoveDir(v); ok {
		return fmt.Sprintf("%s %q is a movement command (direction %s): the route would send it, "+
			"the tracker would count it as a step, and your position would drift. "+
			"Use a command that is not a bare direction.", field, v, dir)
	}
	return ""
}

// trimmedPtr trims a supplied text attribute, preserving "omitted" (nil). Trimming
// matters because an accidental " " would count as "an opening command is recorded"
// and suppress the default one.
func trimmedPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// edgeCommandsRefusal validates both free-text members of a patch at once, so the
// two write surfaces cannot check different halves of it.
func edgeCommandsRefusal(patch storage.EdgeAttrPatch) string {
	if patch.Phrase != nil {
		if r := validateEdgeCommand("traversal phrase", *patch.Phrase); r != "" {
			return r
		}
	}
	if patch.OpenCmd != nil {
		if r := validateEdgeCommand("opening command", *patch.OpenCmd); r != "" {
			return r
		}
	}
	return ""
}

// mapUndo handles POST /api/sessions/{sessionID}/map-undo — a thin REST wrapper
// over Session.UndoTopology so a map edit made from the browser is reversible from
// the browser (undo was MCP-only until now, and the autorecord endpoint is explicitly
// experimental). It reverses the LAST topology write on the session's active set,
// whoever made it (the journal is per-map-set, not per-session). Same soft-fail
// contract as the other map writes: 200 with a reason, never 4xx/5xx.
//
// Undoing a record-CREATE DELETES the cell the record had just anchored to, so
// SetIndexPreservingPosition cannot preserve the position and the player would
// lose it as a side effect of reverting their own experiment. When that happens we
// re-anchor to the same coord: it lands 🟡 "anchored to unmapped cell", which is
// exactly the state the player was in before the record.
func (s *Server) mapUndo(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"applied": false, "reason": "session not connected — no live tracker"})
		return
	}
	// Capture the position BEFORE the undo: the undo may delete the cell we are
	// standing on, after which the tracker can no longer tell us where that was.
	var posBefore mapper.Position
	sess.WithMapTracker(func(t *mapper.Tracker) { posBefore = t.Position() })

	res, entry, err := sess.UndoTopology()
	if err != nil {
		if err == session.ErrNoActiveMapSet {
			writeJSON(w, map[string]any{"applied": false, "reason": "no active map set for this session"})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res.NothingToUndo {
		writeJSON(w, map[string]any{"applied": false, "nothing_to_undo": true, "set_id": res.SetID,
			"reason": "nothing to undo on this map set"})
		return
	}
	out := map[string]any{"applied": res.Applied, "nothing_to_undo": false,
		"label": entry.Label, "set_id": res.SetID}
	if zone, x, y, l, ok := entry.PrimaryCell(); ok {
		out["zone"], out["x"], out["y"], out["l"] = zone, x, y, l
		if !res.Applied {
			out["reason"] = fmt.Sprintf("target cell {%s %d,%d,%d} no longer exists", zone, x, y, l)
		}
	} else if !res.Applied {
		out["reason"] = "the recorded before-state could not be restored"
	}
	if res.Applied && undoDeletedCellAt(entry, posBefore) {
		// The cell under our feet was removed by the undo (an undone CREATE). Put
		// the position back where it was; without a room there it is 🟡 "anchored to
		// unmapped cell" — the pre-record state, not a loss.
		sess.AnchorMapPosition(posBefore.Coord)
		out["reanchored"] = true
	}
	writeJSON(w, out)
}

// undoDeletedCellAt reports whether the undo entry's before-state says "no room
// existed here" for the cell the tracker is standing on — i.e. applying it
// DELETED that cell out from under the player.
func undoDeletedCellAt(entry storage.UndoEntry, pos mapper.Position) bool {
	if !pos.Valid {
		return false
	}
	for _, snap := range entry.Before.Rooms {
		if snap.Existed {
			continue
		}
		r := snap.Room
		if r.Zone == pos.Coord.Zone && r.X == pos.Coord.X && r.Y == pos.Coord.Y && r.L == pos.Coord.L {
			return true
		}
	}
	return false
}

// normalizeDirs upper-cases + trims direction tokens and drops empties so the
// endpoint accepts "u"/"U"/" n " uniformly. Non-direction tokens are passed
// through (the store ignores unknown letters).
func normalizeDirs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToUpper(strings.TrimSpace(d))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// pathResultJSON renders a mapper.PathResult as the map-path endpoint's JSON.
//
// `directions` is THE WHOLE WALK, in send order — not one token per map hop. Three
// kinds of entry go into it, and the caller joins them with ';' without needing to
// know which is which:
//
//   - a grid hop: its canonical lowercase English letter;
//   - a seam hop: the seam's canonical english command (the raw "на <dir>" would be
//     mis-parsed as "надеть" by the client and derail the walk);
//   - a hop with a hand-drawn TRAVERSAL PHRASE: the phrase itself, in place of the
//     letter — "двигать ящик" IS the way down, and no direction letter takes it;
//   - and, BEFORE any hop that crosses a door, the command that opens that door.
//
// The last one is the change that stops the route from being cut short. Until now a
// door ended the walk and the player was told to open it by hand and ask again
// (`door_at` was the truncation point); now the opening action is a step of the
// route like any other. `door_at` is still reported, because the UI shows WHERE the
// doors are, but it is no longer a place to stop.
func pathResultJSON(res mapper.PathResult) map[string]any {
	if res.DTTarget {
		return map[string]any{"reachable": false, "dt": true, "reason": "target is a death trap"}
	}
	if !res.Reachable {
		reason := res.Reason
		if reason == "" {
			reason = "no route to target in the active set"
		}
		out := map[string]any{"reachable": false, "reason": reason}
		if res.VetoedOnly {
			// The map is NOT disconnected: the only ways through carry the player's own
			// "do not route me here" marks. Saying so is the difference between the
			// player removing a mark and the player hunting for a passage that exists.
			out["vetoed_only"] = true
		}
		return out
	}
	dirs := make([]string, 0, len(res.Steps))
	seams := 0
	dt := 0
	doors := 0
	locked := 0
	phrases := 0
	for _, st := range res.Steps {
		if st.OpenAction != "" {
			dirs = append(dirs, st.OpenAction)
		}
		switch {
		case st.Phrase != "":
			// A traversal phrase replaces the direction entirely, seam or not.
			dirs = append(dirs, st.Phrase)
			phrases++
		case st.Seam:
			dirs = append(dirs, st.Command)
		default:
			dirs = append(dirs, st.Dir)
		}
		if st.Seam {
			seams++
		}
		if st.IsDT {
			dt++
		}
		if st.Door {
			doors++
		}
		if st.Locked {
			locked++
		}
	}
	// The pane prints this summary verbatim, so keep it in the mapper UI language.
	stepWord := "steps"
	if len(res.Steps) == 1 {
		stepWord = "step"
	}
	summary := fmt.Sprintf("%d %s", len(res.Steps), stepWord)
	if seams > 0 {
		summary += fmt.Sprintf(", seams: %d", seams)
	}
	if doors > 0 {
		summary += fmt.Sprintf(", doors: %d (opening included in route)", doors)
	}
	if locked > 0 {
		summary += fmt.Sprintf(", ⚠ locked by default: %d", locked)
	}
	if phrases > 0 {
		summary += fmt.Sprintf(", phrase passages: %d", phrases)
	}
	if dt > 0 {
		summary += fmt.Sprintf(", ⚠ death traps on route: %d", dt)
	}
	out := map[string]any{
		"reachable":    true,
		"directions":   dirs,
		"summary":      summary,
		"total_weight": res.TotalWeight,
	}
	if seams > 0 {
		out["seams"] = seams
	}
	if dt > 0 {
		out["dt"] = true
	}
	if doors > 0 {
		// `door_at` is gone. It used to be an index into `directions` and the point at
		// which the walk was CUT; since the route carries each door's opening command,
		// `directions` holds more entries than there are hops and the index no longer
		// addresses what its name promised. Nothing truncates any more, so the count is
		// the whole story.
		out["doors"] = doors
	}
	if locked > 0 {
		out["locked"] = locked
	}
	if phrases > 0 {
		out["phrases"] = phrases
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, fmt.Sprintf("encode: %v", err), http.StatusInternalServerError)
	}
}
