package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"rubymud/go/internal/mapimport"
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
