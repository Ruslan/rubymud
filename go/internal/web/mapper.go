package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"rubymud/go/internal/mapimport"
	"rubymud/go/internal/mapper"
	"rubymud/go/internal/session"
	"rubymud/go/internal/storage"
)

// maxUploadBytes caps the archive upload size. The reference corpus is ~3MB
// unpacked; 64MB is generous headroom while bounding memory.
const maxUploadBytes = 64 << 20

// toStorageRooms converts parsed .mm2 rooms into persisted rows: direction lists
// and automaps become JSON text, the Ch list becomes a bitmask, and BColor is
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
			Automaps:    jsonArray(r.Automaps),
			Fingerprint: r.Fingerprint,
		})
	}
	return out
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
// in form field "archive". Each upload creates a NEW map set. On success it
// best-effort broadcasts map_sets_changed to the requesting session's clients.
func (s *Server) importMapSet(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("archive")
	if err != nil {
		http.Error(w, "missing 'archive' file field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	data := make([]byte, 0, header.Size)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > maxUploadBytes {
				http.Error(w, "archive too large", http.StatusRequestEntityTooLarge)
				return
			}
		}
		if rerr != nil {
			break
		}
	}

	name := strings.TrimSuffix(header.Filename, ".zip")
	if name == "" {
		name = "map set"
	}

	parsed, err := mapimport.ParseZip(data, name)
	if err != nil {
		http.Error(w, "failed to parse archive: "+err.Error(), http.StatusBadRequest)
		return
	}

	in := storage.MapSetInput{
		Name:          name,
		SourceArchive: header.Filename,
		ZoneCount:     parsed.Summary.ZoneCount,
		RoomCount:     parsed.Summary.RoomCount,
		SeamCount:     parsed.Summary.SeamCount,
		Rooms:         toStorageRooms(parsed.Rooms),
	}
	id, err := s.store.CreateMapSet(in)
	if err != nil {
		http.Error(w, "failed to store map set: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Best-effort broadcast to the requesting session's clients, if identifiable.
	if sid := s.optionalSessionID(r); sid != 0 {
		if sess, ok := s.manager.GetSession(sid); ok {
			sess.BroadcastServerMsg(session.ServerMsg{Type: "map_sets_changed"})
		}
	}

	resp := map[string]any{
		"id":         id,
		"name":       name,
		"zone_count": parsed.Summary.ZoneCount,
		"room_count": parsed.Summary.RoomCount,
		"seam_count": parsed.Summary.SeamCount,
		"unresolved": parsed.Summary.Unresolved,
	}
	if resp["unresolved"] == nil {
		resp["unresolved"] = []string{}
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
	if setID > 0 {
		if _, err := s.store.GetMapSet(setID); err != nil {
			http.Error(w, fmt.Sprintf("map set %d not found", setID), http.StatusBadRequest)
			return
		}
	}
	if err := s.store.SetActiveMapSetID(id, setID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Rebuild the tracker index for the live session, if any, and broadcast.
	if sess, ok := s.manager.GetSession(id); ok {
		sess.ReloadActiveMapSet()
	}
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
	sess, ok := s.manager.GetSession(id)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "reason": "session not connected — no live tracker"})
		return
	}
	var anchored, exact bool
	found := sess.WithMapTracker(func(t *mapper.Tracker) {
		if t.Index() == nil {
			return
		}
		_, exact = t.Anchor(mapper.Coord{Zone: body.Zone, X: body.X, Y: body.Y, L: body.L})
		anchored = true
	})
	if !found || !anchored {
		writeJSON(w, map[string]any{"ok": false, "reason": "no active map set for this session"})
		return
	}
	// Broadcast the new tracker position to UI clients (map panes follow it).
	sess.BroadcastMapPosition()
	writeJSON(w, map[string]any{"ok": true, "exact": exact})
}

// optionalSessionID reads a session id from ?session_id= for best-effort
// broadcast targeting. Returns 0 when absent/invalid.
func (s *Server) optionalSessionID(r *http.Request) int64 {
	v := r.URL.Query().Get("session_id")
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, fmt.Sprintf("encode: %v", err), http.StatusInternalServerError)
	}
}
