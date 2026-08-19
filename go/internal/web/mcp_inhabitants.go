package web

import (
	"errors"
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

type mcpInhabitantWriteArgs struct {
	SessionID    int64             `json:"session_id"`
	Operation    string            `json:"operation"`
	InhabitantID int64             `json:"inhabitant_id"`
	Room         *mcpRequiredCoord `json:"room"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	LookTarget   string            `json:"look_target"`
	LookText     string            `json:"look_text"`
	Note         string            `json:"note"`
	Strategy     string            `json:"strategy"`
	PresenceText string            `json:"presence_text"`
	Resident     bool              `json:"resident"`
}

func (s *Server) mcpActiveMapSet(sid int64) (int64, string) {
	mapSetID, ok, err := s.store.GetActiveMapSetID(sid)
	if err != nil {
		return 0, "could not read the session's active map set"
	}
	if !ok {
		return 0, "No active map set for this session."
	}
	return mapSetID, ""
}

func (s *Server) mcpInhabitants(sid int64, room *mcpRequiredCoord, zone, query string, limit int) (string, bool) {
	if limit == 0 {
		limit = storage.InhabitantListDefaultLimit
	}
	if limit < 1 || limit > storage.InhabitantListMaxLimit {
		return "limit must be between 1 and 100.", true
	}
	if room != nil && strings.TrimSpace(zone) != "" {
		return "room and zone are mutually exclusive filters.", true
	}
	mapSetID, problem := s.mcpActiveMapSet(sid)
	if problem != "" {
		return problem, true
	}
	options := storage.InhabitantListOptions{Query: query, Zone: zone, Limit: limit}
	var exact *mcpCoordArg
	if room != nil {
		coord, ok := room.value()
		if !ok {
			return "room requires nonblank zone and integer x, y, l.", true
		}
		exact = &coord
		cell := storage.EdgeCell{Zone: coord.Zone, X: coord.X, Y: coord.Y, L: coord.L}
		options.Cell = &cell
	}
	rows, truncated, err := s.store.ListMapInhabitants(mapSetID, options)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidZoneName) || errors.Is(err, storage.ErrInvalidInhabitant) {
			return err.Error(), true
		}
		return "inhabitant list failed", true
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Map set: %d\n", mapSetID))
	if exact != nil {
		sb.WriteString(fmt.Sprintf("Room: zone=%q x=%d y=%d l=%d\n", exact.Zone, exact.X, exact.Y, exact.L))
	} else if strings.TrimSpace(zone) != "" {
		sb.WriteString(fmt.Sprintf("Zone filter: %q\n", strings.TrimSpace(zone)))
	}
	if len(rows) == 0 {
		sb.WriteString("No known inhabitants matched.\n")
	}
	for _, row := range rows {
		line := fmt.Sprintf("[id=%d] %s kind=%s placements=%d", row.ID, row.Name, row.Kind, row.PlacementCount)
		if exact != nil {
			view, found, err := s.store.GetRoomInhabitant(mapSetID,
				storage.EdgeCell{Zone: exact.Zone, X: exact.X, Y: exact.Y, L: exact.L}, row.ID)
			if err != nil || !found {
				continue
			}
			line += fmt.Sprintf(" resident=%t", view.Resident)
			if view.PresenceText != "" {
				line += fmt.Sprintf(" presence=%q", view.PresenceText)
			}
		}
		if row.LookTarget != "" {
			line += fmt.Sprintf(" look_target=%q", row.LookTarget)
		}
		if row.Strategy != "" {
			line += fmt.Sprintf(" strategy=%q", row.Strategy)
		}
		sb.WriteString(line + "\n")
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("... truncated after %d inhabitants; refine the filter.\n", limit))
	}
	return sb.String(), false
}

func (s *Server) mcpInhabitant(sid, inhabitantID int64) (string, bool) {
	if inhabitantID <= 0 {
		return "inhabitant_id must be a positive integer.", true
	}
	mapSetID, problem := s.mcpActiveMapSet(sid)
	if problem != "" {
		return problem, true
	}
	row, err := s.store.GetMapInhabitant(mapSetID, inhabitantID)
	if errors.Is(err, storage.ErrInhabitantNotFound) {
		return "inhabitant not found in the active map set.", true
	}
	if err != nil {
		return "inhabitant read failed", true
	}
	placements, truncated, err := s.store.ListInhabitantPlacementsBounded(mapSetID, inhabitantID, storage.InhabitantListMaxLimit)
	if err != nil {
		return "inhabitant placement read failed", true
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Inhabitant: id=%d map_set=%d name=%q kind=%s\n", row.ID, mapSetID, row.Name, row.Kind))
	sb.WriteString(fmt.Sprintf("look_target: %s\nlook_text: %s\nnote: %s\nstrategy: %s\n", row.LookTarget, row.LookText, row.Note, row.Strategy))
	sb.WriteString(fmt.Sprintf("placements: %d\n", len(placements)))
	for _, placement := range placements {
		sb.WriteString(fmt.Sprintf("  zone=%q x=%d y=%d l=%d resident=%t", placement.Zone, placement.X, placement.Y, placement.L, placement.Resident))
		if placement.PresenceText != "" {
			sb.WriteString(fmt.Sprintf(" presence=%q", placement.PresenceText))
		}
		sb.WriteByte('\n')
	}
	if truncated {
		sb.WriteString("  ... placements truncated after 100\n")
	}
	return sb.String(), false
}

func (s *Server) mcpInhabitantWrite(sid int64, args mcpInhabitantWriteArgs) (string, bool) {
	mapSetID, problem := s.mcpActiveMapSet(sid)
	if problem != "" {
		return problem, true
	}
	if args.Room == nil {
		return "room is required for inhabitant writes.", true
	}
	coord, ok := args.Room.value()
	if !ok {
		return "room requires nonblank zone and integer x, y, l.", true
	}
	cell := storage.EdgeCell{Zone: coord.Zone, X: coord.X, Y: coord.Y, L: coord.L}
	fields := storage.RoomInhabitantFields{
		InhabitantFields: storage.InhabitantFields{
			Name: args.Name, Kind: args.Kind, LookTarget: args.LookTarget,
			LookText: args.LookText, Note: args.Note, Strategy: args.Strategy,
		},
		PresenceText: args.PresenceText, Resident: args.Resident,
	}
	var text string
	var err error
	switch strings.ToLower(strings.TrimSpace(args.Operation)) {
	case "create":
		var row storage.RoomInhabitantView
		row, err = s.store.CreateRoomInhabitant(mapSetID, cell, fields)
		if err == nil {
			text = fmt.Sprintf("created inhabitant id=%d and placed it at zone=%q x=%d y=%d l=%d", row.ID, coord.Zone, coord.X, coord.Y, coord.L)
			s.manager.BroadcastRoomKnowledgeChanged(mapSetID, coord.Zone, coord.X, coord.Y, coord.L)
		}
	case "place":
		if args.InhabitantID <= 0 {
			return "inhabitant_id is required for place.", true
		}
		_, err = s.store.PlaceRoomInhabitant(mapSetID, cell, args.InhabitantID, args.PresenceText, args.Resident)
		if err == nil {
			text = fmt.Sprintf("placed inhabitant id=%d at zone=%q x=%d y=%d l=%d", args.InhabitantID, coord.Zone, coord.X, coord.Y, coord.L)
			s.manager.BroadcastRoomKnowledgeChanged(mapSetID, coord.Zone, coord.X, coord.Y, coord.L)
		}
	case "update":
		if args.InhabitantID <= 0 {
			return "inhabitant_id is required for update.", true
		}
		var found bool
		_, found, err = s.store.UpdateRoomInhabitant(mapSetID, cell, args.InhabitantID, fields)
		if err == nil && !found {
			return "room inhabitant not found.", true
		}
		if err == nil {
			text = fmt.Sprintf("updated inhabitant id=%d and its selected room placement", args.InhabitantID)
			placements, listErr := s.store.ListInhabitantPlacements(mapSetID, args.InhabitantID)
			if listErr != nil {
				return "updated inhabitant but could not broadcast placement changes", true
			}
			cells := make([]storage.EdgeCell, len(placements))
			for i, placement := range placements {
				cells[i] = storage.EdgeCell{Zone: placement.Zone, X: placement.X, Y: placement.Y, L: placement.L}
			}
			s.manager.BroadcastRoomsKnowledgeChanged(mapSetID, cells)
		}
	case "remove":
		if args.InhabitantID <= 0 {
			return "inhabitant_id is required for remove.", true
		}
		var deleted bool
		deleted, err = s.store.DeleteRoomInhabitant(mapSetID, cell, args.InhabitantID)
		if err == nil && !deleted {
			return "room inhabitant not found.", true
		}
		if err == nil {
			text = fmt.Sprintf("removed inhabitant id=%d from zone=%q x=%d y=%d l=%d", args.InhabitantID, coord.Zone, coord.X, coord.Y, coord.L)
			s.manager.BroadcastRoomKnowledgeChanged(mapSetID, coord.Zone, coord.X, coord.Y, coord.L)
		}
	default:
		return "operation must be create, place, update, or remove.", true
	}
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrInvalidInhabitant), errors.Is(err, storage.ErrInvalidZoneName),
			errors.Is(err, storage.ErrInhabitantNotFound), errors.Is(err, storage.ErrDuplicateRoomInhabitant):
			return err.Error(), true
		default:
			return "inhabitant write failed", true
		}
	}
	return text, false
}
