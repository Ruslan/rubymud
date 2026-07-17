package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"rubymud/go/internal/storage"
	"strconv"
	"strings"
	"time"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
	ID      any    `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	var res any
	var err error

	switch req.Method {
	case "initialize":
		res = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mudhost-mcp", "version": "0.0.6.4"},
		}
	case "notifications/initialized":
		return
	case "tools/list":
		res = s.mcpListTools()
	case "tools/call":
		res, err = s.mcpCallTool(req.Params)
	default:
		res = jsonRPCError{Code: -32601, Message: "Method not found"}
	}

	if err != nil {
		res = jsonRPCError{Code: -32603, Message: err.Error()}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		Result:  res,
		ID:      req.ID,
	})
}

func (s *Server) mcpResolveSessionID(id int64) (int64, error) {
	if id != 0 {
		return id, nil
	}
	sessions, err := s.manager.ListSessions()
	if err != nil {
		return 0, fmt.Errorf("failed to list sessions: %w", err)
	}
	if len(sessions) == 0 {
		return 0, fmt.Errorf("no sessions available")
	}
	return sessions[0].ID, nil
}

func (s *Server) mcpListTools() any {
	sessionIDProp := map[string]any{
		"type":        "integer",
		"description": "Session ID. Optional — defaults to the first available session.",
	}
	tzProp := map[string]any{
		"type":        "string",
		"description": "IANA timezone (e.g. 'Europe/Kyiv') used to render timestamps. Optional — defaults to the session's timezone, then UTC.",
	}
	return map[string]any{
		"tools": []any{
			map[string]any{
				"name":        "mud_list_sessions",
				"description": "List all MUD sessions with their id, name, host, port and connection status.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
			map[string]any{
				"name":        "mud_get_output",
				"description": "Get the last N lines of output from a MUD session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"limit":      map[string]any{"type": "integer", "description": "Number of lines to return (default 200)."},
						"tz":         tzProp,
					},
				},
			},
			map[string]any{
				"name":        "mud_get_output_range",
				"description": "Get output lines before a specific log entry ID (paginate upward).",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"before_id":  map[string]any{"type": "integer", "description": "Return lines with ID less than this value."},
						"limit":      map[string]any{"type": "integer", "description": "Number of lines to return (default 50)."},
						"tz":         tzProp,
					},
					"required": []string{"before_id"},
				},
			},
			map[string]any{
				"name":        "mud_search",
				"description": "Search log history for a query string — matches both MUD output lines and sent commands (shown as '> command' hints). Returns up to max_groups match groups with surrounding context. Use before_id to paginate deeper into history.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"query":      map[string]any{"type": "string", "description": "Case-insensitive search string."},
						"context":    map[string]any{"type": "integer", "description": "Lines of context around each match (default 5)."},
						"max_groups": map[string]any{"type": "integer", "description": "Maximum number of match groups to return (default 10)."},
						"before_id":  map[string]any{"type": "integer", "description": "Search only in log entries with ID less than this value. Use to paginate deeper into history."},
						"tz":         tzProp,
					},
					"required": []string{"query"},
				},
			},
			map[string]any{
				"name":        "mud_send_command",
				"description": "Send a command to a MUD session and return the MUD's response. Aliases are expanded ($var substitution and %1 %2 positional args apply). Commands go through the same pipeline as user input. By default waits 0.5 seconds and returns all output lines received during that window — set sync=0 for fire-and-forget.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"command":    map[string]any{"type": "string", "description": "The command to send. Aliases are expanded: use alias name to trigger expansion (e.g. 'gre corpse'). Variables are substituted via $varname. Positional args via %1, %2, etc."},
						"sync":       map[string]any{"type": "number", "description": "Seconds to wait for MUD response after sending (default 0.5, max 10). Set to 0 for fire-and-forget without waiting for output."},
					},
					"required": []string{"command"},
				},
			},
			map[string]any{
				"name":        "mud_get_variables",
				"description": "List all session variables with their current values, defaults, and source (session override, profile default, or unset).",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_set_variable",
				"description": "Set a session variable override. The variable will be substituted as $key in alias templates and commands.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"key":        map[string]any{"type": "string", "description": "Variable name (without $)."},
						"value":      map[string]any{"type": "string", "description": "Value to set."},
					},
					"required": []string{"key", "value"},
				},
			},
			map[string]any{
				"name":        "mud_get_aliases",
				"description": "List all active aliases for the session across all active profiles. Aliases expand shorthand commands: the name is what you type, the template is what gets sent. In templates: $varname is replaced with the variable value; %1, %2, ... are positional arguments passed after the alias name (e.g. 'alias arg1 arg2').",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_get_triggers",
				"description": "List all active triggers for the session across all active profiles. Triggers fire automatically when MUD output matches their pattern, sending the associated command. Use this to understand side effects: knowing triggers helps predict what will happen in response to game events.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_map_sets",
				"description": "List imported map sets (world maps) and which one is active for the session. Each set shows id, name, zone_count and room_count. A NULL/dangling active set is reported as '(no active set)'. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_map_zone",
				"description": "List the rooms of one zone of a map set in slim form (z t h x y l e d ch a p i s + dx dy dl + img). Defaults to the session's active map set. A zone can be hundreds of rooms; the result is bounded and notes when truncated. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string", "description": "Zone name. Required — there is no current-position signal in phase 1."},
						"map_set":    map[string]any{"type": "integer", "description": "Map set id. Optional — defaults to the session's active map set."},
						"limit":      map[string]any{"type": "integer", "description": "Max rooms to return (default 300, max 1000). The response notes if truncated."},
					},
					"required": []string{"zone"},
				},
			},
			map[string]any{
				"name":        "mud_where",
				"description": "Where the player is, from the backend position tracker. Reports the active map set (or none), a confidence enum (green=anchored, yellow=tracker-only dead-reckoning, red=lost), pending_moves:N (unconfirmed steps still in the FIFO queue — orthogonal to confidence; you can be green with pending>0), and zone/x/y/l/tag/hint plus is_dt/pipe flags. On yellow/red it includes a reason. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_look_map",
				"description": "Describe the current room from the map: hint/desc, exits with door markers, and per-exit connectivity from the authoritative ch mask (mapped|unmapped), '→zone' if the exit is a seam, and the target's is_dt when known. Includes the confidence enum inline. This is the structural map view (distinct from mud_get_output's raw scrollback). Alias: mud_room. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_path",
				"description": "Route from the current position (or a given 'from') to a target: to_zone (first room of a zone), to:{zone,x,y,l}, or to_hint (first room whose hint contains the text). Output is an ordered list of RU direction/seam commands for mud_send_command, hop count, seam-crossing flags, and any known death traps on the path (refused/warned). Refuses with isError when position is lost (red) — re-anchor with mud_anchor_here first. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"to_zone":    map[string]any{"type": "string", "description": "Target zone name (routes to its first room)."},
						"to_hint":    map[string]any{"type": "string", "description": "Target by room hint substring (routes to the first match)."},
						"to": map[string]any{
							"type":        "object",
							"description": "Exact target cell {zone,x,y,l}.",
							"properties": map[string]any{
								"zone": map[string]any{"type": "string"},
								"x":    map[string]any{"type": "integer"},
								"y":    map[string]any{"type": "integer"},
								"l":    map[string]any{"type": "integer"},
							},
						},
						"from": map[string]any{
							"type":        "object",
							"description": "Optional start cell {zone,x,y,l}; defaults to the tracker's current position.",
							"properties": map[string]any{
								"zone": map[string]any{"type": "string"},
								"x":    map[string]any{"type": "integer"},
								"y":    map[string]any{"type": "integer"},
								"l":    map[string]any{"type": "integer"},
							},
						},
					},
				},
			},
			map[string]any{
				"name":        "mud_anchor_here",
				"description": "Manually set the tracker's position (the MCP equivalent of the UI 'I'm here' button) to recover from lost/teleport/following. Input {session_id?, zone,x,y,l}. Returns the new confidence and position. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
					},
					"required": []string{"zone", "x", "y", "l"},
				},
			},
			map[string]any{
				"name":        "mud_room_annotate",
				"description": "Annotate a room cell of the session's ACTIVE map set — a crowdsourced/LLM overlay stored separately from the map topology (works on frozen/imported sets; never forks). Keyed by the logical cell {zone,x,y,l}. Edit-in-place, partial update: only the fields you pass change; omitted fields are preserved. Fields: dt (mark the cell a DEATH TRAP — augments the map's is_dt so mud_path refuses routing INTO it and mud_look_map shows it DT), hazard, note, battle_log, author. To CLEAR a text field pass an empty string; to clear dt pass false. Soft-fails (isError) when the session has no active map set. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
						"dt":         map[string]any{"type": "boolean", "description": "Mark/unmark the cell as a death trap. Augments the map's is_dt for mud_path/mud_look_map."},
						"hazard":     map[string]any{"type": "string", "description": "Short hazard note (e.g. 'aggressive mob', 'no-recall'). Pass '' to clear."},
						"note":       map[string]any{"type": "string", "description": "Free-form note. Pass '' to clear."},
						"battle_log": map[string]any{"type": "string", "description": "Battle/observation log for the cell. Pass '' to clear."},
						"author":     map[string]any{"type": "string", "description": "Who wrote this annotation. Pass '' to clear."},
					},
					"required": []string{"zone", "x", "y", "l"},
				},
			},
			map[string]any{
				"name":        "mud_room_annotations",
				"description": "List the crowdsourced/LLM annotations of the session's active map set (dt/hazard/note/battle_log + author/updated_at per cell), optionally filtered to one zone. Read side of mud_room_annotate. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string", "description": "Optional zone filter; omit for all zones in the active set."},
					},
				},
			},
			map[string]any{
				"name":        "mud_set_active_map_set",
				"description": "Set the session's active map set and rebuild the tracker's in-memory index. Input {session_id?, map_set}. Returns confirmation. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"map_set":    map[string]any{"type": "integer", "description": "Map set id to activate."},
					},
					"required": []string{"map_set"},
				},
			},
		},
	}
}

// mcpMapZoneDefaultLimit / Max bound the slim-room result — a zone can be
// hundreds of rooms, so an unbounded dump would blow the response.
const (
	mcpMapZoneDefaultLimit = 300
	mcpMapZoneMaxLimit     = 1000
)

func (s *Server) mcpCallTool(params json.RawMessage) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, err
	}

	var content string
	var isError bool

	switch call.Name {
	case "mud_list_sessions":
		sessions, err := s.manager.ListSessions()
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, sess := range sessions {
			name := sess.Name
			if name == "" {
				name = fmt.Sprintf("%s:%d", sess.MudHost, sess.MudPort)
			}
			sb.WriteString(fmt.Sprintf("[id=%d] %s (%s:%d) status=%s\n", sess.ID, name, sess.MudHost, sess.MudPort, sess.Status))
		}
		if sb.Len() == 0 {
			content = "No sessions found."
		} else {
			content = sb.String()
		}

	case "mud_get_output":
		var args struct {
			SessionID int64  `json:"session_id"`
			Limit     int    `json:"limit"`
			Tz        string `json:"tz"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Limit <= 0 {
			args.Limit = 200
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		entries, err := s.store.RecentLogs(sid, args.Limit)
		if err != nil {
			return nil, err
		}
		content = s.formatMcpEntries(entries, "", s.resolveRequestLocation(sid, args.Tz))

	case "mud_get_output_range":
		var args struct {
			SessionID int64  `json:"session_id"`
			BeforeID  int64  `json:"before_id"`
			Limit     int    `json:"limit"`
			Tz        string `json:"tz"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Limit <= 0 {
			args.Limit = 50
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		entries, err := s.store.LogRangeDetailed(sid, args.BeforeID, args.Limit)
		if err != nil {
			return nil, err
		}
		content = s.formatMcpEntries(entries, "", s.resolveRequestLocation(sid, args.Tz))

	case "mud_search":
		var args struct {
			SessionID int64  `json:"session_id"`
			Query     string `json:"query"`
			Context   int    `json:"context"`
			MaxGroups int    `json:"max_groups"`
			BeforeID  int64  `json:"before_id"`
			Tz        string `json:"tz"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Context <= 0 {
			args.Context = 15
		}
		if args.MaxGroups <= 0 {
			args.MaxGroups = 10
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		groups, _, err := s.store.SearchLogsDetailed(sid, args.Query, args.Context, args.BeforeID)
		if err != nil {
			return nil, err
		}

		if len(groups) == 0 {
			content = "No matches found."
		} else {
			totalGroups := len(groups)
			// groups are chronological (ascending). Take the most recent max_groups.
			if len(groups) > args.MaxGroups {
				groups = groups[len(groups)-args.MaxGroups:]
			}
			content = formatMcpSearch(groups, args.Query, totalGroups, args.MaxGroups, s.resolveRequestLocation(sid, args.Tz))
		}

	case "mud_send_command":
		var args struct {
			SessionID int64    `json:"session_id"`
			Command   string   `json:"command"`
			Sync      *float64 `json:"sync"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		syncSecs := 0.5
		if args.Sync != nil {
			syncSecs = *args.Sync
			if syncSecs > 10 {
				syncSecs = 10
			}
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		sess, ok := s.manager.GetSession(sid)
		if !ok {
			content = fmt.Sprintf("Session %d not found or not connected", sid)
			isError = true
			break
		}
		// Record the latest log ID before sending so we can return new output.
		lastID, err := s.store.LatestLogID(sid)
		if err != nil {
			return nil, err
		}
		if err := sess.SendCommand(args.Command, "mcp"); err != nil {
			content = fmt.Sprintf("Error sending command: %v", err)
			isError = true
			break
		}
		if syncSecs <= 0 {
			content = fmt.Sprintf("Command '%s' sent to session %d", args.Command, sid)
			break
		}
		time.Sleep(time.Duration(syncSecs * float64(time.Second)))
		entries, err := s.store.LogsSinceID(sid, lastID, 200)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			content = fmt.Sprintf("Command '%s' sent. No output received in %.1fs.", args.Command, syncSecs)
		} else {
			content = fmt.Sprintf("Command '%s' sent. Response (%.1fs window):\n\n", args.Command, syncSecs) +
				formatMcpGroup(entries, "")
		}

	case "mud_get_variables":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		vars, err := s.store.ListResolvedVariablesForSession(sid)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, v := range vars {
			switch {
			case v.HasValue:
				sb.WriteString(fmt.Sprintf("$%s = %s\n", v.Name, v.Value))
			case v.UsesDefault:
				sb.WriteString(fmt.Sprintf("$%s = %s  (default)\n", v.Name, v.DefaultValue))
			default:
				sb.WriteString(fmt.Sprintf("$%s = (unset)\n", v.Name))
			}
		}
		content = sb.String()
		if content == "" {
			content = "No variables defined."
		}

	case "mud_set_variable":
		var args struct {
			SessionID int64  `json:"session_id"`
			Key       string `json:"key"`
			Value     string `json:"value"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		if err := s.store.SetVariable(sid, args.Key, args.Value); err != nil {
			return nil, err
		}
		if sess, ok := s.manager.GetSession(sid); ok {
			sess.NotifySettingsChanged("variables")
		}
		content = fmt.Sprintf("$%s set to %q", args.Key, args.Value)

	case "mud_get_aliases":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		profileIDs, err := s.store.GetOrderedProfileIDs(sid)
		if err != nil {
			return nil, err
		}
		aliases, err := s.store.LoadAliasesForProfiles(profileIDs)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, a := range aliases {
			if !a.Enabled {
				continue
			}
			group := ""
			if a.GroupName != "" {
				group = fmt.Sprintf(" [%s]", a.GroupName)
			}
			sb.WriteString(fmt.Sprintf("%s%s → %s\n", a.Name, group, a.Template))
		}
		content = sb.String()
		if content == "" {
			content = "No aliases defined."
		}

	case "mud_get_triggers":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		profileIDs, err := s.store.GetOrderedProfileIDs(sid)
		if err != nil {
			return nil, err
		}
		triggers, err := s.store.LoadTriggersForProfiles(profileIDs)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, tr := range triggers {
			if !tr.Enabled {
				continue
			}
			flags := ""
			if tr.StopAfterMatch {
				flags = " [stop]"
			}
			if tr.IsButton {
				flags += " [button]"
			}
			group := ""
			if tr.GroupName != "" {
				group = fmt.Sprintf(" [%s]", tr.GroupName)
			}
			sb.WriteString(fmt.Sprintf("/%s/%s%s → %s\n", tr.Pattern, group, flags, tr.Command))
		}
		content = sb.String()
		if content == "" {
			content = "No triggers defined."
		}

	case "mud_map_sets":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		sets, err := s.store.ListMapSets()
		if err != nil {
			return nil, err
		}
		activeID, hasActive, err := s.store.GetActiveMapSetID(sid)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		if len(sets) == 0 {
			sb.WriteString("No map sets imported.\n")
		}
		activeValid := false
		for _, set := range sets {
			marker := ""
			if hasActive && set.ID == activeID {
				marker = "  * ACTIVE"
				activeValid = true
			}
			sb.WriteString(fmt.Sprintf("[id=%d] %s — %d zones, %d rooms%s\n",
				set.ID, set.Name, set.ZoneCount, set.RoomCount, marker))
		}
		if !activeValid {
			sb.WriteString("(no active set)\n")
		}
		content = sb.String()

	case "mud_map_zone":
		var args struct {
			SessionID int64  `json:"session_id"`
			Zone      string `json:"zone"`
			MapSet    int64  `json:"map_set"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		mapSetID := args.MapSet
		if mapSetID == 0 {
			active, ok, err := s.store.GetActiveMapSetID(sid)
			if err != nil {
				return nil, err
			}
			if !ok {
				content = "No active map set for this session, and no map_set given."
				isError = true
				break
			}
			mapSetID = active
		}
		if strings.TrimSpace(args.Zone) == "" {
			content = "zone is required."
			isError = true
			break
		}
		limit := args.Limit
		if limit <= 0 {
			limit = mcpMapZoneDefaultLimit
		}
		if limit > mcpMapZoneMaxLimit {
			limit = mcpMapZoneMaxLimit
		}
		rooms, err := s.store.ListSlimRooms(mapSetID, args.Zone)
		if err != nil {
			return nil, err
		}
		content = formatMcpMapZone(mapSetID, args.Zone, rooms, limit)
		if len(rooms) == 0 {
			isError = false // empty zone is not an error, just report it
		}

	case "mud_where":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpWhere(sid)

	case "mud_look_map", "mud_room":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpLookMap(sid)

	case "mud_path":
		var args struct {
			SessionID int64  `json:"session_id"`
			ToZone    string `json:"to_zone"`
			ToHint    string `json:"to_hint"`
			To        *struct {
				Zone string `json:"zone"`
				X    int    `json:"x"`
				Y    int    `json:"y"`
				L    int    `json:"l"`
			} `json:"to"`
			From *struct {
				Zone string `json:"zone"`
				X    int    `json:"x"`
				Y    int    `json:"y"`
				L    int    `json:"l"`
			} `json:"from"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpPath(sid, mcpPathArgs{
			ToZone: args.ToZone,
			ToHint: args.ToHint,
			To:     (*mcpCoordArg)(args.To),
			From:   (*mcpCoordArg)(args.From),
		})

	case "mud_anchor_here":
		var args struct {
			SessionID int64  `json:"session_id"`
			Zone      string `json:"zone"`
			X         int    `json:"x"`
			Y         int    `json:"y"`
			L         int    `json:"l"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpAnchorHere(sid, mcpCoordArg{Zone: args.Zone, X: args.X, Y: args.Y, L: args.L})

	case "mud_room_annotate":
		var args struct {
			SessionID int64   `json:"session_id"`
			Zone      string  `json:"zone"`
			X         int     `json:"x"`
			Y         int     `json:"y"`
			L         int     `json:"l"`
			DT        *bool   `json:"dt"`
			Hazard    *string `json:"hazard"`
			Note      *string `json:"note"`
			BattleLog *string `json:"battle_log"`
			Author    *string `json:"author"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		if strings.TrimSpace(args.Zone) == "" {
			content = "zone is required."
			isError = true
			break
		}
		content, isError = s.mcpRoomAnnotate(sid,
			mcpCoordArg{Zone: args.Zone, X: args.X, Y: args.Y, L: args.L},
			storage.AnnotationFields{
				DT:        args.DT,
				Hazard:    args.Hazard,
				Note:      args.Note,
				BattleLog: args.BattleLog,
				Author:    args.Author,
			})

	case "mud_room_annotations":
		var args struct {
			SessionID int64  `json:"session_id"`
			Zone      string `json:"zone"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpRoomAnnotations(sid, strings.TrimSpace(args.Zone))

	case "mud_set_active_map_set":
		var args struct {
			SessionID int64 `json:"session_id"`
			MapSet    int64 `json:"map_set"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		content, isError = s.mcpSetActiveMapSet(sid, args.MapSet)

	default:
		return nil, fmt.Errorf("Tool not found: %s", call.Name)
	}

	return map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": content},
		},
		"isError": isError,
	}, nil
}

// formatMcpMapZone renders slim rooms as a compact text table, bounded by limit.
// Directions are shown as letters; a bounded result appends a truncation note.
func formatMcpMapZone(mapSetID int64, zone string, rooms []storage.SlimRoom, limit int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Map set %d, zone %q: %d room(s)", mapSetID, zone, len(rooms)))
	truncated := false
	shown := rooms
	if len(rooms) > limit {
		shown = rooms[:limit]
		truncated = true
		sb.WriteString(fmt.Sprintf(" (showing first %d)", limit))
	}
	sb.WriteString("\n\n")
	if len(rooms) == 0 {
		sb.WriteString("(no rooms — zone not in this set, or empty)\n")
		return sb.String()
	}
	for _, r := range shown {
		tag := "-"
		if r.T != nil {
			tag = strconv.Itoa(*r.T)
		}
		flags := ""
		if r.S {
			flags += " DT"
		}
		if r.P {
			flags += " pipe"
		}
		if r.Img == 1 {
			flags += " img"
		}
		seam := ""
		if len(r.A) > 0 {
			seam = "  seams=" + strings.Join(r.A, ",")
		}
		exits := strings.Join(r.E, "")
		doors := ""
		if len(r.D) > 0 {
			doors = " doors=" + strings.Join(r.D, "")
		}
		sb.WriteString(fmt.Sprintf("(%d,%d,L%d) tag=%s ch=%d exits=[%s]%s %q%s%s\n",
			r.X, r.Y, r.L, tag, r.Ch, exits, doors, r.H, flags, seam))
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n... truncated: %d more room(s) not shown. Increase limit (max %d) to see more.\n",
			len(rooms)-limit, mcpMapZoneMaxLimit))
	}
	return sb.String()
}

// mcpTimeFormat carries an explicit UTC offset so a record's calendar day is
// unambiguous regardless of the caller's zone.
const mcpTimeFormat = "2006-01-02 15:04:05 -0700"

func stripAnsi(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

// mcpHeader returns a header line with the current time and,
// if entries is non-empty, the timestamp of the first log entry, both in loc.
func mcpHeader(entries []storage.LogEntry, loc *time.Location) string {
	now := time.Now().In(loc).Format(mcpTimeFormat)
	if len(entries) == 0 {
		return fmt.Sprintf("Current time: %s\n\n", now)
	}
	first := entries[0].CreatedAt.Time
	if first.IsZero() {
		return fmt.Sprintf("Current time: %s\n\n", now)
	}
	return fmt.Sprintf("Current time: %s | Log from: %s\n\n", now, first.In(loc).Format(mcpTimeFormat))
}

// formatMcpGroup formats a single group of log entries with optional query highlighting.
// Does not include a header — callers add headers as needed.
func formatMcpGroup(entries []storage.LogEntry, query string) string {
	var sb strings.Builder
	q := strings.ToLower(query)
	for _, e := range entries {
		text := stripAnsi(e.DisplayPlainText())
		matched := q != "" && strings.Contains(strings.ToLower(stripAnsi(e.PlainText)), q)
		if matched {
			sb.WriteString("*** ")
		}
		sb.WriteString(fmt.Sprintf("[#%d] %s\n", e.ID, text))
		for _, cmd := range e.Commands {
			sb.WriteString(fmt.Sprintf("        > %s\n", cmd))
		}
	}
	return sb.String()
}

// formatMcpEntries formats entries for get_output / get_output_range (single block with header).
func (s *Server) formatMcpEntries(entries []storage.LogEntry, query string, loc *time.Location) string {
	return mcpHeader(entries, loc) + formatMcpGroup(entries, query)
}

// formatMcpSearch formats search results: header + groups separated by dividers with timestamps.
// totalGroups is the total found before slicing; maxGroups is the limit applied.
func formatMcpSearch(groups [][]storage.LogEntry, query string, totalGroups, maxGroups int, loc *time.Location) string {
	now := time.Now().In(loc).Format(mcpTimeFormat)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current time: %s\n", now))
	if totalGroups > maxGroups {
		// Find the minimum ID across all entries in the displayed groups for pagination hint.
		var minID int64
		for _, g := range groups {
			for _, e := range g {
				if minID == 0 || e.ID < minID {
					minID = e.ID
				}
			}
		}
		sb.WriteString(fmt.Sprintf("Showing %d of %d groups (most recent). To search earlier history, call again with before_id=%d\n", maxGroups, totalGroups, minID))
	} else {
		sb.WriteString(fmt.Sprintf("Found %d group(s).\n", totalGroups))
	}
	sb.WriteString("\n")
	for i, group := range groups {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		if len(group) > 0 && !group[0].CreatedAt.Time.IsZero() {
			sb.WriteString(fmt.Sprintf("Group %d — %s:\n", i+1, group[0].CreatedAt.Time.In(loc).Format(mcpTimeFormat)))
		} else {
			sb.WriteString(fmt.Sprintf("Group %d:\n", i+1))
		}
		sb.WriteString(formatMcpGroup(group, query))
	}
	return sb.String()
}
