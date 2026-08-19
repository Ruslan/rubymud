package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

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
	rows, err := s.store.ListRoomInhabitants(mapSetID, cell)
	if err != nil {
		writeRoomInhabitantError(w, err)
		return
	}
	writeJSON(w, rows)
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
	placements, err := s.store.ListInhabitantPlacements(mapSetID, inhabitantID)
	if err != nil {
		http.Error(w, "inhabitant storage failed", http.StatusInternalServerError)
		return
	}
	cells := make([]storage.EdgeCell, len(placements))
	for i, placement := range placements {
		cells[i] = storage.EdgeCell{Zone: placement.Zone, X: placement.X, Y: placement.Y, L: placement.L}
	}
	s.manager.BroadcastRoomsKnowledgeChanged(mapSetID, cells)
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
