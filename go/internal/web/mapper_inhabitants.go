package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"rubymud/go/internal/storage"
)

type roomInhabitantRequest struct {
	InhabitantID *int64 `json:"inhabitant_id,omitempty"`
	Zone         string `json:"zone"`
	X            *int   `json:"x"`
	Y            *int   `json:"y"`
	L            *int   `json:"l"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	LookTarget   string `json:"look_target"`
	LookText     string `json:"look_text"`
	Note         string `json:"note"`
	Strategy     string `json:"strategy"`
	PresenceText string `json:"presence_text"`
	Resident     bool   `json:"resident"`
}

const roomInhabitantBodyLimit int64 = 64 << 10

type inhabitantCatalogResponse struct {
	Items     []storage.MapInhabitantSummary `json:"items"`
	Truncated bool                           `json:"truncated"`
}

type roomInhabitantListResponse struct {
	Items      []storage.RoomInhabitantView `json:"items"`
	Room       storage.MapRoomKnowledge     `json:"room"`
	Truncated  bool                         `json:"truncated"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

const roomInhabitantCursorVersion = 2

var errLegacyRoomInhabitantCursor = errors.New("legacy_room_inhabitant_cursor")

type roomInhabitantCursor struct {
	Version int    `json:"v"`
	ID      int64  `json:"id"`
	Name    string `json:"name,omitempty"`
}

func encodeRoomInhabitantCursor(row storage.RoomInhabitantView) string {
	payload, _ := json.Marshal(roomInhabitantCursor{Version: roomInhabitantCursorVersion, ID: row.ID})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRoomInhabitantCursor(raw string) (string, int64, error) {
	if raw == "" {
		return "", 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(payload) > 4096 {
		return "", 0, errors.New("invalid inhabitant cursor")
	}
	var cursor roomInhabitantCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return "", 0, errors.New("invalid inhabitant cursor")
	}
	// Version 0 covers both the original name+id cursor and the short-lived
	// 02fb cursor that emitted only {"id":N}. A positive id is enough to
	// identify either as an expired legacy cursor; zero/negative ids remain
	// ordinary malformed input below.
	if cursor.Version == 0 && cursor.ID > 0 {
		return "", 0, errLegacyRoomInhabitantCursor
	}
	if cursor.Version != roomInhabitantCursorVersion || cursor.ID <= 0 || cursor.Name != "" || utf8.RuneCountInString(cursor.Name) > 256 {
		return "", 0, errors.New("invalid inhabitant cursor")
	}
	return cursor.Name, cursor.ID, nil
}

func (body roomInhabitantRequest) cell() (storage.EdgeCell, error) {
	if body.X == nil || body.Y == nil || body.L == nil {
		return storage.EdgeCell{}, errors.New("zone, x, y, and l are required")
	}
	zone, err := storage.NormalizeZoneName(body.Zone)
	if err != nil {
		return storage.EdgeCell{}, err
	}
	return storage.EdgeCell{Zone: zone, X: *body.X, Y: *body.Y, L: *body.L}, nil
}

func (body roomInhabitantRequest) fields() storage.RoomInhabitantFields {
	return storage.RoomInhabitantFields{
		InhabitantFields: storage.InhabitantFields{
			Name: body.Name, Kind: body.Kind, LookTarget: body.LookTarget,
			LookText: body.LookText, Note: body.Note, Strategy: body.Strategy,
		},
		PresenceText: body.PresenceText,
		Resident:     body.Resident,
	}
}

func mapperInhabitantIDs(r *http.Request) (mapSetID, inhabitantID int64, err error) {
	mapSetID, err = strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || mapSetID <= 0 {
		return 0, 0, errors.New("invalid map set id")
	}
	if raw := chi.URLParam(r, "inhabitantID"); raw != "" {
		inhabitantID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || inhabitantID <= 0 {
			return 0, 0, errors.New("invalid inhabitant id")
		}
	}
	return mapSetID, inhabitantID, nil
}

func roomCellFromQuery(r *http.Request) (storage.EdgeCell, error) {
	q := r.URL.Query()
	zone, err := storage.NormalizeZoneName(q.Get("zone"))
	if err != nil {
		return storage.EdgeCell{}, err
	}
	values := make([]int, 3)
	for i, name := range []string{"x", "y", "l"} {
		raw, ok := q[name]
		if !ok || len(raw) != 1 || raw[0] == "" {
			return storage.EdgeCell{}, errors.New("zone, x, y, and l are required")
		}
		values[i], err = strconv.Atoi(raw[0])
		if err != nil {
			return storage.EdgeCell{}, errors.New(name + " must be an integer")
		}
	}
	return storage.EdgeCell{Zone: zone, X: values[0], Y: values[1], L: values[2]}, nil
}

func decodeRoomInhabitantRequest(w http.ResponseWriter, r *http.Request) (roomInhabitantRequest, storage.EdgeCell, error) {
	var body roomInhabitantRequest
	r.Body = http.MaxBytesReader(w, r.Body, roomInhabitantBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return body, storage.EdgeCell{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return body, storage.EdgeCell{}, errors.New("request body must contain one JSON object")
		}
		return body, storage.EdgeCell{}, err
	}
	cell, err := body.cell()
	return body, cell, err
}

func writeRoomInhabitantDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
}

func writeRoomInhabitantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrInvalidInhabitant), errors.Is(err, storage.ErrInvalidZoneName):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, storage.ErrMapSetNotFound), errors.Is(err, storage.ErrInhabitantNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, storage.ErrDuplicateRoomInhabitant):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "inhabitant storage failed", http.StatusInternalServerError)
	}
}

// listRoomInhabitants is intentionally room-scoped and lazy. The full catalogue
// never rides on GET /api/rooms, which remains the hot slim-zone payload.
func (s *Server) listRoomInhabitants(w http.ResponseWriter, r *http.Request) {
	mapSetID, _, err := mapperInhabitantIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cell, err := roomCellFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.store.GetMapSet(mapSetID); err != nil {
		http.Error(w, "map set not found", http.StatusNotFound)
		return
	}
	room, _, err := s.store.GetMapRoomKnowledge(mapSetID, cell)
	if err != nil {
		http.Error(w, "room storage failed", http.StatusInternalServerError)
		return
	}
	limit := storage.InhabitantListDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > storage.InhabitantListMaxLimit {
			http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}
	afterName, afterID, err := decodeRoomInhabitantCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		if errors.Is(err, errLegacyRoomInhabitantCursor) {
			http.Error(w, errLegacyRoomInhabitantCursor.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, truncated, err := s.store.ListRoomInhabitantsBoundedAfter(mapSetID, cell, limit, afterName, afterID)
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	nextCursor := ""
	if truncated && len(rows) > 0 {
		last := rows[0]
		for _, row := range rows[1:] {
			if row.ID > last.ID {
				last = row
			}
		}
		nextCursor = encodeRoomInhabitantCursor(last)
	}
	writeJSON(w, roomInhabitantListResponse{Items: rows, Room: room, Truncated: truncated, NextCursor: nextCursor})
}

func (s *Server) listInhabitantCatalog(w http.ResponseWriter, r *http.Request) {
	mapSetID, _, err := mapperInhabitantIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit := storage.InhabitantListDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > storage.InhabitantListMaxLimit {
			http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}
	queryText := r.URL.Query().Get("query")
	if len([]rune(queryText)) > storage.InhabitantQueryLimit {
		http.Error(w, "query exceeds 256 characters", http.StatusBadRequest)
		return
	}
	options := storage.InhabitantListOptions{Query: queryText, Limit: limit}
	q := r.URL.Query()
	if q.Has("exclude_zone") || q.Has("exclude_x") || q.Has("exclude_y") || q.Has("exclude_l") {
		zone, err := storage.NormalizeZoneName(q.Get("exclude_zone"))
		if err != nil {
			http.Error(w, "invalid exclude room", http.StatusBadRequest)
			return
		}
		values := make([]int, 3)
		for i, name := range []string{"exclude_x", "exclude_y", "exclude_l"} {
			values[i], err = strconv.Atoi(q.Get(name))
			if err != nil {
				http.Error(w, "invalid exclude room", http.StatusBadRequest)
				return
			}
		}
		options.ExcludeCell = &storage.EdgeCell{Zone: zone, X: values[0], Y: values[1], L: values[2]}
	}
	rows, truncated, err := s.store.ListMapInhabitants(mapSetID, options)
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	writeJSON(w, inhabitantCatalogResponse{Items: rows, Truncated: truncated})
}

func (s *Server) createRoomInhabitant(w http.ResponseWriter, r *http.Request) {
	mapSetID, _, err := mapperInhabitantIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, cell, err := decodeRoomInhabitantRequest(w, r)
	if err != nil {
		writeRoomInhabitantDecodeError(w, err)
		return
	}
	if body.InhabitantID != nil && *body.InhabitantID <= 0 {
		http.Error(w, "invalid inhabitant id", http.StatusBadRequest)
		return
	}
	var row storage.RoomInhabitantView
	if body.InhabitantID == nil {
		row, err = s.store.CreateRoomInhabitant(mapSetID, cell, body.fields())
	} else {
		_, err = s.store.PlaceRoomInhabitant(mapSetID, cell, *body.InhabitantID, body.PresenceText, body.Resident)
		if err == nil {
			var found bool
			row, found, err = s.store.GetRoomInhabitant(mapSetID, cell, *body.InhabitantID)
			if err == nil && !found {
				err = storage.ErrInhabitantNotFound
			}
		}
	}
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	s.manager.BroadcastRoomKnowledgeChanged(mapSetID, cell.Zone, cell.X, cell.Y, cell.L)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, row)
}

func (s *Server) updateRoomInhabitant(w http.ResponseWriter, r *http.Request) {
	mapSetID, inhabitantID, err := mapperInhabitantIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, cell, err := decodeRoomInhabitantRequest(w, r)
	if err != nil {
		writeRoomInhabitantDecodeError(w, err)
		return
	}
	row, found, err := s.store.UpdateRoomInhabitant(mapSetID, cell, inhabitantID, body.fields())
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	if !found {
		http.Error(w, "room inhabitant not found", http.StatusNotFound)
		return
	}
	s.manager.BroadcastMapKnowledgeChanged(mapSetID)
	writeJSON(w, row)
}

func (s *Server) deleteRoomInhabitant(w http.ResponseWriter, r *http.Request) {
	mapSetID, inhabitantID, err := mapperInhabitantIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cell, err := roomCellFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	deleted, err := s.store.DeleteRoomInhabitant(mapSetID, cell, inhabitantID)
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	if !deleted {
		http.Error(w, "room inhabitant not found", http.StatusNotFound)
		return
	}
	s.manager.BroadcastRoomKnowledgeChanged(mapSetID, cell.Zone, cell.X, cell.Y, cell.L)
	w.WriteHeader(http.StatusNoContent)
}
