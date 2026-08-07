package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"rubymud/go/internal/storage"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
	ID      any           `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	gate := s.mcpRuntime.snapshotValue()
	if !gate.enabled {
		http.NotFound(w, r)
		return
	}
	if !s.mcpAuthorized(gate, r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mudhost-mcp"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// MCP is not a browser API. Reject Origin-bearing requests to prevent a
	// hostile hostname resolving to a tokenless loopback listener (DNS rebinding).
	if len(r.Header.Values("Origin")) != 0 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	mcpSessionID, err := mcpSessionIDFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateMCPProtocolVersionHeader(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}
	hasID := len(req.ID) != 0
	isNotification := strings.HasPrefix(req.Method, "notifications/")
	if (isNotification && hasID) || (!isNotification && !hasID) {
		http.Error(w, "invalid JSON-RPC request id", http.StatusBadRequest)
		return
	}

	// Body reads are deliberately outside the runtime lock. Recheck the gate at
	// the dispatch boundary, then hold its read lock only while continuity state
	// is validated or created. A completed admin update therefore cannot be
	// followed by a request using its old bearer to create or touch session state.
	gate, unlockGate := s.mcpRuntime.lockSnapshot()
	if !gate.enabled {
		unlockGate()
		http.NotFound(w, r)
		return
	}
	if !s.mcpAuthorized(gate, r) {
		unlockGate()
		w.Header().Set("WWW-Authenticate", `Bearer realm="mudhost-mcp"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if isNotification {
		if mcpSessionID != "" && !s.mcpSessions.existsAt(mcpSessionID, gate.generation) {
			unlockGate()
			http.Error(w, "MCP session not found", http.StatusNotFound)
			return
		}
		unlockGate()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.Method == "initialize" {
		if mcpSessionID != "" {
			unlockGate()
			http.Error(w, "initialize must not include Mcp-Session-Id", http.StatusBadRequest)
			return
		}
		mcpSessionID, err = s.mcpSessions.createAt(gate.generation)
		if err != nil {
			unlockGate()
			http.Error(w, "could not create MCP session", http.StatusInternalServerError)
			return
		}
		w.Header().Set(mcpSessionHeaderName, mcpSessionID)
	} else if mcpSessionID != "" && !s.mcpSessions.existsAt(mcpSessionID, gate.generation) {
		unlockGate()
		// A stale opaque id must never silently start a different client's state.
		http.Error(w, "MCP session not found", http.StatusNotFound)
		return
	}
	unlockGate()

	var res any
	var rpcErr *jsonRPCError

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		res = map[string]any{
			"protocolVersion": negotiateMCPProtocolVersion(params.ProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mudhost-mcp", "version": "0.0.6.4"},
		}
	case "tools/list":
		res = s.mcpListTools()
	case "tools/call":
		res, err = s.mcpCallTool(r.Context(), req.Params, mcpSessionID)
	default:
		rpcErr = &jsonRPCError{Code: -32601, Message: "Method not found"}
	}

	if err != nil {
		res = nil
		rpcErr = &jsonRPCError{Code: -32603, Message: err.Error()}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		Result:  res,
		Error:   rpcErr,
		ID:      req.ID,
	})
}

func mcpSessionIDFromRequest(r *http.Request) (string, error) {
	values := r.Header.Values(mcpSessionHeaderName)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > mcpSessionIDMaxLength {
		return "", fmt.Errorf("invalid Mcp-Session-Id header")
	}
	for _, ch := range []byte(values[0]) {
		if ch < 0x21 || ch > 0x7e {
			return "", fmt.Errorf("invalid Mcp-Session-Id header")
		}
	}
	return values[0], nil
}

const mcpProtocolVersion = "2025-06-18"

func negotiateMCPProtocolVersion(requested string) string {
	if requested == "2025-03-26" || requested == mcpProtocolVersion {
		return requested
	}
	return mcpProtocolVersion
}

func validateMCPProtocolVersionHeader(r *http.Request) error {
	values := r.Header.Values("MCP-Protocol-Version")
	if len(values) == 0 {
		return nil
	}
	if len(values) != 1 || (values[0] != "2025-03-26" && values[0] != mcpProtocolVersion) {
		return fmt.Errorf("invalid or unsupported MCP-Protocol-Version header")
	}
	return nil
}

func (s *Server) mcpAuthorized(gate mcpRuntimeSnapshot, r *http.Request) bool {
	if !gate.hasToken {
		return true
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	const prefix = "Bearer "
	if len(values[0]) <= len(prefix) || !strings.HasPrefix(values[0], prefix) {
		return false
	}
	presented := values[0][len(prefix):]
	if presented == "" {
		return false
	}
	presentedHash := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(presentedHash[:], gate.tokenDigest[:]) == 1
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

const mcpBufferMaxLength = 128

// mcpRequiredCoord keeps omitted coordinate members distinct from valid zeroes.
// Tool schemas advertise all four members as required, but tools/call still has
// to enforce that contract when a caller bypasses client-side schema validation.
type mcpRequiredCoord struct {
	Zone *string `json:"zone"`
	X    *int    `json:"x"`
	Y    *int    `json:"y"`
	L    *int    `json:"l"`
}

func (c mcpRequiredCoord) value() (mcpCoordArg, bool) {
	if c.Zone == nil || strings.TrimSpace(*c.Zone) == "" || c.X == nil || c.Y == nil || c.L == nil {
		return mcpCoordArg{}, false
	}
	zone, err := storage.NormalizeZoneName(*c.Zone)
	if err != nil {
		return mcpCoordArg{}, false
	}
	return mcpCoordArg{Zone: zone, X: *c.X, Y: *c.Y, L: *c.L}, true
}

// mcpResolveBuffer preserves storage's exact window-name matching. Omitting
// the field selects main; an explicitly supplied empty or unsafe name is an
// input error rather than a silent fallback to main.
func mcpResolveBuffer(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "main", nil
	}
	var decoded *string
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return "", fmt.Errorf("buffer must be a string")
	}
	if *decoded == "" {
		return "", fmt.Errorf("buffer must be nonempty")
	}
	if utf8.RuneCountInString(*decoded) > mcpBufferMaxLength {
		return "", fmt.Errorf("buffer must be at most %d characters", mcpBufferMaxLength)
	}
	for _, r := range *decoded {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("buffer must not contain control characters")
		}
	}
	return *decoded, nil
}

func (s *Server) mcpEffectiveCursor(mcpSessionID string, mudSessionID int64, buffer string, explicit *int64) (int64, error) {
	if explicit != nil {
		if err := s.store.ValidateVisibleLogCursorForBuffer(mudSessionID, buffer, *explicit); err != nil {
			return 0, err
		}
		return *explicit, nil
	}
	if mcpSessionID == "" {
		if err := s.store.ValidateVisibleLogCursorForBuffer(mudSessionID, buffer, 0); err != nil {
			return 0, err
		}
		return 0, nil
	}
	cursor, ok := s.mcpSessions.cursorForBuffer(mcpSessionID, mudSessionID, buffer)
	if !ok {
		return 0, fmt.Errorf("MCP session not found")
	}
	if err := s.store.ValidateVisibleLogCursorForBuffer(mudSessionID, buffer, cursor); err != nil {
		return 0, err
	}
	return cursor, nil
}

func (s *Server) mcpRememberCursor(mcpSessionID string, mudSessionID int64, buffer string, cursor int64) {
	if mcpSessionID != "" && cursor > 0 {
		s.mcpSessions.mergeCursorForBuffer(mcpSessionID, mudSessionID, buffer, cursor)
	}
}

func mcpMaxEntryID(entries []storage.LogEntry) int64 {
	var max int64
	for _, entry := range entries {
		if entry.ID > max {
			max = entry.ID
		}
	}
	return max
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
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
	lastSeenProp := map[string]any{
		"type":        "integer",
		"minimum":     0,
		"description": "Optional visible log entry ID already seen by this client. 0 starts at retained visible history; overrides the MCP session's remembered cursor.",
	}
	bufferProp := map[string]any{
		"type":        "string",
		"maxLength":   mcpBufferMaxLength,
		"minLength":   1,
		"pattern":     `^[^\u0000-\u001F\u007F-\u009F]+$`,
		"description": "Exact, case-sensitive output buffer/window name (1-128 Unicode characters, no control characters; whitespace is preserved). Optional; defaults to main. Filters only returned output, never command routing.",
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
						"session_id":       sessionIDProp,
						"buffer":           bufferProp,
						"limit":            map[string]any{"type": "integer", "description": "Number of lines to return (default 200)."},
						"tz":               tzProp,
						"last_seen_log_id": lastSeenProp,
					},
				},
			},
			map[string]any{
				"name":        "mud_get_output_range",
				"description": "Get output lines before a specific log entry ID (paginate upward).",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id":       sessionIDProp,
						"buffer":           bufferProp,
						"before_id":        map[string]any{"type": "integer", "description": "Return lines with ID less than this value."},
						"limit":            map[string]any{"type": "integer", "description": "Number of lines to return (default 50)."},
						"tz":               tzProp,
						"last_seen_log_id": lastSeenProp,
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
						"session_id":       sessionIDProp,
						"buffer":           bufferProp,
						"query":            map[string]any{"type": "string", "description": "Case-insensitive search string."},
						"context":          map[string]any{"type": "integer", "minimum": 0, "description": "Lines of context around each match (default 15)."},
						"max_groups":       map[string]any{"type": "integer", "description": "Maximum number of match groups to return (default 10)."},
						"before_id":        map[string]any{"type": "integer", "description": "Search only in log entries with ID less than this value. Use to paginate deeper into history."},
						"tz":               tzProp,
						"last_seen_log_id": lastSeenProp,
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
						"session_id":       sessionIDProp,
						"buffer":           bufferProp,
						"command":          map[string]any{"type": "string", "description": "The command to send. Aliases are expanded: use alias name to trigger expansion (e.g. 'gre corpse'). Variables are substituted via $varname. Positional args via %1, %2, etc."},
						"sync":             map[string]any{"type": "number", "description": "Seconds to wait for MUD response after sending (default 0.5, max 10). Set to 0 for fire-and-forget without waiting for output."},
						"last_seen_log_id": lastSeenProp,
					},
					"required": []string{"command"},
				},
			},
			map[string]any{
				"name":        "mud_get_variables",
				"description": "List session variables resolved across active profile layers, showing the effective value or marking it as a profile default or unset.",
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
				"description": "List one zone in bounded compact text. Each room shows its basic fields plus ONLY non-default topology/provenance: manual_open/manual_blocked grid faces; grid_parsed/grid_fallback faces; exact seam selectors (including malformed raw entries); and directed attrs (weight, phrase, door, locked, open_cmd, no_route). Shown seam selectors and seam attrs include target_zone/nullable target_tag/command; pathological per-room lists are capped with topology_truncated. Defaults to the session's active map set; the result notes room truncation. An omitted session_id defaults to the first session.",
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
				"description": "Describe the current room from the authoritative materialized topology: each shown outgoing grid edge and seam is separate, with source, passable state, resolution (mapped, vetoed, dangling, dead_target, or blocked), target coordinate when known, and non-default directed attrs. Seam lines retain the exact occurrence order, kind/source, target_zone/nullable target_tag/command selector, and resolved target when known. Pathological edge lists are capped with a truncation note. Includes confidence. Alias: mud_room. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_path",
				"description": "Route from the current position (or a given 'from') to a target: to_zone (a lowest-cost room in a zone), to:{zone,x,y,l}, or to_hint (a lowest-cost room whose hint contains the text). Output is an ordered list of canonical English direction/seam commands for mud_send_command, command count, total weight, seam-crossing flags, pipe-run cell annotations, and known death-trap/door warnings. Refuses with isError when position is lost (red) — re-anchor with mud_anchor_here first. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"to_zone":    map[string]any{"type": "string", "description": "Target zone name (routes to a lowest-cost matching room)."},
						"to_hint":    map[string]any{"type": "string", "description": "Target by room hint substring (routes to a lowest-cost match)."},
						"to": map[string]any{
							"type":        "object",
							"description": "Exact target cell {zone,x,y,l}.",
							"properties": map[string]any{
								"zone": map[string]any{"type": "string"},
								"x":    map[string]any{"type": "integer"},
								"y":    map[string]any{"type": "integer"},
								"l":    map[string]any{"type": "integer"},
							},
							"required": []string{"zone", "x", "y", "l"},
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
							"required": []string{"zone", "x", "y", "l"},
						},
					},
				},
			},
			map[string]any{
				"name":        "mud_map_walk",
				"description": "Start a server-owned, confirmation-paced map walk to one exact cell. The host plans and sends one hop at a time; do not use mud_send_command for route directions while it is active. Replaces an existing walk using the same in-flight-hop barrier as the map UI. Input requires to:{zone,x,y,l}; ambiguous hint/zone targets are intentionally not accepted. Soft failures return isError. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"to": map[string]any{
							"type":        "object",
							"description": "Exact target cell.",
							"properties": map[string]any{
								"zone": map[string]any{"type": "string"},
								"x":    map[string]any{"type": "integer"},
								"y":    map[string]any{"type": "integer"},
								"l":    map[string]any{"type": "integer"},
							},
							"required": []string{"zone", "x", "y", "l"},
						},
					},
					"required": []string{"to"},
				},
			},
			map[string]any{
				"name":        "mud_map_walk_status",
				"description": "Get the current server-owned map-walk snapshot: active, state, completed/total hops, reason, resume eligibility, transport epoch, job id, and revision. Epoch distinguishes snapshots across reconnects. Once Session no longer retains a job, status reports inactive/stopped with its high-water identity; transition-specific terminal detail is available on the transition response/event. A stopped route is a normal status result. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_map_walk_stop",
				"description": "Cancel the active server-owned map walk. An already-sent hop is allowed to confirm before the route becomes stopped; manual movement remains behind the same barrier. Repeating stop after a terminal route is an idempotent no-op. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
				},
			},
			map[string]any{
				"name":        "mud_map_walk_resume",
				"description": "Resume a paused server-owned map walk. The host replans from the latest green tracker position; it refuses while movement is pending, position is lost, or there is no paused route. Soft failures return isError. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"session_id": sessionIDProp},
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
			map[string]any{
				"name":        "mud_map_undo",
				"description": "Undo the last topology write (e.g. an exit add/remove) on the session's ACTIVE map set. The undo journal is per map set (a write on set A is not undoable while set B is active) and in-memory (lost on host restart). Input {session_id?}. Returns what was undone, or \"nothing to undo\" when the set's journal is empty. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
					},
				},
			},
			map[string]any{
				"name":        "mud_room_upsert",
				"description": "Create or partially update a room of the session's ACTIVE map set, keyed by the cell {zone,x,y,l}. Topology write — if the active set is frozen (imported) it is forked copy-on-write to an editable copy and the session switches to it (forked_to reported). Partial: only the fields you pass change; omitted fields are preserved on update / defaulted on create. Exits may be given as a display string (exits, e.g. \"N (S) U\") OR as canonical dir letters (edirs, e.g. [\"N\",\"S\"]); whichever is provided rewrites edirs/ch/exits together (exits wins if both). The fingerprint is recomputed from hint/desc/exits so tracker auto-resync stays consistent. Reversible with mud_map_undo (undo of a create deletes the room). Soft-fails (isError) with no active set. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
						"hint":       map[string]any{"type": "string", "description": "Room hint (short name). Recomputes fingerprint."},
						"desc":       map[string]any{"type": "string", "description": "Room description. Recomputes fingerprint."},
						"exits":      map[string]any{"type": "string", "description": "Display exits string with optional door markers, e.g. \"N (S) U\". Rewrites edirs/ch/exits."},
						"edirs":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Canonical exit dir letters, e.g. [\"N\",\"S\",\"U\"]. Alternative to exits; pass [] to clear all exits."},
						"ch":         map[string]any{"type": "integer", "description": "Connectivity bitmask (accepted but derived from the resolved edirs — the mask never drifts from the exit set)."},
						"is_dt":      map[string]any{"type": "boolean", "description": "Mark the cell a death trap."},
						"pipe":       map[string]any{"type": "boolean", "description": "Mark the cell a pipe corridor."},
						"imageindex": map[string]any{"type": "integer", "description": "Room image index."},
						"note":       map[string]any{"type": "string"},
						"dx":         map[string]any{"type": "integer", "description": "Display X offset (layout)."},
						"dy":         map[string]any{"type": "integer", "description": "Display Y offset (layout)."},
						"dl":         map[string]any{"type": "integer", "description": "Display level offset (layout)."},
					},
					"required": []string{"zone", "x", "y", "l"},
				},
			},
			map[string]any{
				"name":        "mud_room_link",
				"description": "Add an exit-edge in direction dir from the cell {zone,x,y,l} of the session's ACTIVE map set. Bidirectional: if a room exists at the grid neighbor (+delta(dir)) the reverse exit is added there too; otherwise a one-sided exit is recorded (still valid). Topology write — forks a frozen set (forked_to reported). dir is a canonical letter N/S/E/W/U/D. Reversible with mud_map_undo (restores BOTH cells). Soft-fails (isError) with no active set, no room at the cell, or a bad direction. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
						"dir":        map[string]any{"type": "string", "description": "Exit direction: one of N,S,E,W,U,D (canonical letters)."},
					},
					"required": []string{"zone", "x", "y", "l", "dir"},
				},
			},
			map[string]any{
				"name":        "mud_room_unlink",
				"description": "Remove the exit-edge in direction dir from the cell {zone,x,y,l} of the session's ACTIVE map set, and the reverse on the grid neighbor if that neighbor room advertises it. Topology write — forks a frozen set (forked_to reported). dir is a canonical letter N/S/E/W/U/D. Reversible with mud_map_undo (restores BOTH cells). Soft-fails (isError) with no active set, no room at the cell, or a bad direction. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
						"dir":        map[string]any{"type": "string", "description": "Exit direction: one of N,S,E,W,U,D (canonical letters)."},
					},
					"required": []string{"zone", "x", "y", "l", "dir"},
				},
			},
			map[string]any{
				"name": "mud_room_mark_edge",
				"description": "HAND-DRAW the topology of one face of a cell — the layer that is NOT parsed from room text and therefore SURVIVES re-recording the room. " +
					"mark=\"open\": there IS a way out of {zone,x,y,l} in dir even though the room's exits line does not say so (a secret passage). " +
					"mark=\"blocked\": there is NO way out in dir even though the exits line (or the visual grid) claims one — this is how a route into a wrongly-adjacent room is taken back. " +
					"mark=\"auto\": erase the hand-drawn mark and let the room's own exits decide this face again. " +
					"NOT the same tool as mud_room_link, and not interchangeable with it: mud_room_link edits the ROOM'S EXITS (the parsed layer), so it shows up in the room's exit list and the next mud_room_upsert / «Записать комнату» overwrites it. This tool writes no room field at all — it writes the edge itself, and re-parsing the room cannot undo or resurrect it. Use mud_room_link when the map's record of the exits is simply wrong; use this when the map is right about the text and the text is not the whole truth. " +
					"Edges are DIRECTED: by default both directions of the face are marked, one_way=true marks only the way OUT of this cell (a one-way passage or a one-way wall). Both cells must hold a room and be grid-adjacent (jump links are not supported yet). " +
					"THE SAME TOOL ALSO DESCRIBES HOW THE PASSAGE IS TAKEN — phrase / door / locked / open_cmd / no_route. Those describe an EXISTING passage rather than creating one, so they need no `mark` (omit it) and always apply to exactly ONE face: every one of them is a statement about the side you approach from, and marking the pair would put words in your mouth about the other side. Setting any of them makes the face hand-drawn, so re-recording the room does not erase it. They are a SEPARATE undo entry from a mark passed in the same call. " +
					"Topology write — forks a frozen set (forked_to reported). Reversible with mud_map_undo. Soft-fails (isError) with no active set, a missing room on either side, a bad direction, or attributes aimed at a face with no passage on it. An omitted session_id defaults to the first session.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"zone":       map[string]any{"type": "string"},
						"x":          map[string]any{"type": "integer"},
						"y":          map[string]any{"type": "integer"},
						"l":          map[string]any{"type": "integer"},
						"dir":        map[string]any{"type": "string", "description": "Face to mark: one of N,S,E,W,U,D (canonical letters)."},
						"mark": map[string]any{"type": "string", "enum": []string{"open", "blocked", "auto"},
							"description": "open = hidden exit (there is a way); blocked = negation (there is no way); auto = drop the hand-drawn mark and go back to what the room's exits say. Omit it to only set the attributes below."},
						"one_way": map[string]any{"type": "boolean", "description": "Mark only the direction OUT of this cell. Default false: both directions of the face are marked. Applies to `mark` only — the attributes are always one face."},
						"weight":  map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "Positive finite routing cost of this directed face. Default 1; fractions such as 0.5 are allowed. The router minimizes total weight, not cell count."},
						"phrase": map[string]any{"type": "string",
							"description": "The command that TAKES this passage instead of the direction (\"двигать ящик\"). A route emits it in place of the direction letter. One-sided by nature: the same hole may be \"двигать ящик\" going down and \"лезть ковёр\" coming back up. \"\" clears it."},
						"door":   map[string]any{"type": "boolean", "description": "A door stands on this face. Routes then emit an opening action BEFORE the hop instead of stopping at it."},
						"locked": map[string]any{"type": "boolean", "description": "That door is LOCKED BY DEFAULT from this side (a door is routinely locked one way and open the other). It does not block routing — the route reports it, because the player may hold the key."},
						"open_cmd": map[string]any{"type": "string",
							"description": "The command that opens the door FROM THIS SIDE (\"откр две север\", \"дёрнуть рычаг\", \"say friend\"). Free text so the markup is not tied to one MUD's verbs. \"\" falls back to the mapper's default template."},
						"no_route": map[string]any{"type": "boolean",
							"description": "\"Passable, but do not plan a route through me.\" DIFFERENT from mark=\"blocked\": the way exists and walking it is normal (the tracker accepts the step as usual), the ROUTER just must not choose it — for dubious or DT-like transitions. When a target becomes unreachable only because of such marks, mud_path says so instead of \"no route\"."},
					},
					"required": []string{"zone", "x", "y", "l", "dir"},
				},
			},
			map[string]any{
				"name":        "mud_room_set_seam_attrs",
				"description": "Set directed routing attributes on one existing automap seam. The seam selector is source {zone,x,y,l} plus target_zone, target_tag, command and optional exact occurrence order; it must identify exactly one transition. Attributes are weight, phrase, door, locked, open_cmd, and no_route. This is a topology write, forks a frozen set, survives room re-recording, and is reversible with mud_map_undo.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id":  sessionIDProp,
						"zone":        map[string]any{"type": "string"},
						"x":           map[string]any{"type": "integer"},
						"y":           map[string]any{"type": "integer"},
						"l":           map[string]any{"type": "integer"},
						"target_zone": map[string]any{"type": "string"},
						"target_tag":  map[string]any{"type": "integer"},
						"command":     map[string]any{"type": "string", "description": "Exact parsed seam command."},
						"order":       map[string]any{"type": "integer", "minimum": 0, "description": "Exact persisted occurrence, required when otherwise-identical seam selectors repeat."},
						"weight":      map[string]any{"type": "number", "exclusiveMinimum": 0},
						"phrase":      map[string]any{"type": "string"},
						"door":        map[string]any{"type": "boolean"},
						"locked":      map[string]any{"type": "boolean"},
						"open_cmd":    map[string]any{"type": "string"},
						"no_route":    map[string]any{"type": "boolean"},
					},
					"required": []string{"zone", "x", "y", "l", "target_zone", "target_tag", "command"},
				},
			},
			map[string]any{
				"name":        "mud_room_portal",
				"description": "Create, update, or delete one DIRECTED exact-target portal from source {zone,x,y,l}. This is not a grid edge and not a legacy cross-zone zone/tag seam: its target is the exact existing {target_zone,target_x,target_y,target_l} cell, and same-zone portals are valid. action=create needs target coordinates and a nonblank traversal command; action=update/delete selects one stable source occurrence with order. Updates may retarget, change command, and/or patch any routing attributes without erasing omitted fields. Create/update promote manual ownership, so room re-recording never resurrects old imported data. Portal orders stay sparse after delete. Topology write: forks frozen sets, reloads routing/tracker once, and is reversible with mud_map_undo.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"session_id": sessionIDProp,
						"action":     map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}},
						"zone":       map[string]any{"type": "string"}, "x": map[string]any{"type": "integer"}, "y": map[string]any{"type": "integer"}, "l": map[string]any{"type": "integer"},
						"order":       map[string]any{"type": "integer", "minimum": 0, "description": "Required for update/delete; stable source occurrence selector."},
						"target_zone": map[string]any{"type": "string", "description": "Exact target zone; required with target coordinates for create, optional for retargeting update."},
						"target_x":    map[string]any{"type": "integer"}, "target_y": map[string]any{"type": "integer"}, "target_l": map[string]any{"type": "integer"},
						"command": map[string]any{"type": "string", "maxLength": 4096, "description": "Raw traversal command; may contain scripts such as semicolon-separated commands and may be a direction when that is safe for this MUD."},
						"weight":  map[string]any{"type": "number", "exclusiveMinimum": 0}, "phrase": map[string]any{"type": "string"}, "door": map[string]any{"type": "boolean"}, "locked": map[string]any{"type": "boolean"}, "open_cmd": map[string]any{"type": "string"}, "no_route": map[string]any{"type": "boolean"},
					},
					"required": []string{"action", "zone", "x", "y", "l"},
				},
			},
			map[string]any{
				"name":        "mud_map_move_rooms",
				"description": "Preview or atomically move up to 8000 selected exact room coordinates from one zone namespace to another. dry_run=true is read-only and returns revision; apply requires that expected_revision, rejects stale topology, preserves coordinates and endpoint edges, and is undoable. Duplicate coordinates are canonicalized.",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
					"session_id": sessionIDProp, "source_zone": map[string]any{"type": "string"}, "target_zone": map[string]any{"type": "string"},
					"rooms":   map[string]any{"type": "array", "minItems": 1, "maxItems": 8000, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"x": map[string]any{"type": "integer"}, "y": map[string]any{"type": "integer"}, "l": map[string]any{"type": "integer"}}, "required": []string{"x", "y", "l"}}},
					"dry_run": map[string]any{"type": "boolean"}, "expected_revision": map[string]any{"type": "integer", "minimum": 0},
				}, "required": []string{"source_zone", "target_zone", "rooms"}},
			},
			map[string]any{
				"name":        "mud_map_rename_zone",
				"description": "Preview or atomically rename a whole non-empty zone of at most 8000 rooms. dry_run=true is read-only; apply requires expected_revision from preview. This moves every room in the source zone, never deletes rooms, and is undoable.",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"session_id": sessionIDProp, "source_zone": map[string]any{"type": "string"}, "target_zone": map[string]any{"type": "string"}, "dry_run": map[string]any{"type": "boolean"}, "expected_revision": map[string]any{"type": "integer", "minimum": 0}}, "required": []string{"source_zone", "target_zone"}},
			},
			map[string]any{
				"name":        "mud_room_delete",
				"description": "Delete the room at the cell {zone,x,y,l} of the session's ACTIVE map set. Topology write — forks a frozen set (forked_to reported). The cell's room_annotations / room_images are left dangling (the annotation overlay is keyed by logical coord and outlives the room). Reversible with mud_map_undo (recreates the room with all fields, exits, and fingerprint intact). Soft-fails (isError) with no active set or no room at the cell. An omitted session_id defaults to the first session.",
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
		},
	}
}

// mcpMapZoneDefaultLimit / Max bound the slim-room result — a zone can be
// hundreds of rooms, so an unbounded dump would blow the response.
const (
	mcpMapZoneDefaultLimit = 300
	mcpMapZoneMaxLimit     = 1000
	// A single imported room can contain a pathological seam list. Cap each
	// repeated topology category so a room cannot defeat the room-count bound.
	mcpTopologyListLimit = 64
)

func (s *Server) mcpCallTool(ctx context.Context, params json.RawMessage, mcpSessionID string) (any, error) {
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
			SessionID     int64           `json:"session_id"`
			Buffer        json.RawMessage `json:"buffer"`
			Limit         int             `json:"limit"`
			Tz            string          `json:"tz"`
			LastSeenLogID *int64          `json:"last_seen_log_id"`
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
		buffer, err := mcpResolveBuffer(args.Buffer)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		if _, err := s.mcpEffectiveCursor(mcpSessionID, sid, buffer, args.LastSeenLogID); err != nil {
			content, isError = err.Error(), true
			break
		}
		entries, err := s.store.RecentLogsForBuffer(sid, buffer, args.Limit)
		if err != nil {
			return nil, err
		}
		content = s.formatMcpEntries(entries, "", s.resolveRequestLocation(sid, args.Tz))
		s.mcpRememberCursor(mcpSessionID, sid, buffer, mcpMaxEntryID(entries))

	case "mud_get_output_range":
		var args struct {
			SessionID     int64           `json:"session_id"`
			Buffer        json.RawMessage `json:"buffer"`
			BeforeID      *int64          `json:"before_id"`
			Limit         int             `json:"limit"`
			Tz            string          `json:"tz"`
			LastSeenLogID *int64          `json:"last_seen_log_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.BeforeID == nil {
			content, isError = "before_id is required.", true
			break
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
		buffer, err := mcpResolveBuffer(args.Buffer)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		if _, err := s.mcpEffectiveCursor(mcpSessionID, sid, buffer, args.LastSeenLogID); err != nil {
			content, isError = err.Error(), true
			break
		}
		entries, err := s.store.LogRangeDetailedForBuffer(sid, buffer, *args.BeforeID, args.Limit)
		if err != nil {
			return nil, err
		}
		content = s.formatMcpEntries(entries, "", s.resolveRequestLocation(sid, args.Tz))
		s.mcpRememberCursor(mcpSessionID, sid, buffer, mcpMaxEntryID(entries))

	case "mud_search":
		var args struct {
			SessionID     int64           `json:"session_id"`
			Buffer        json.RawMessage `json:"buffer"`
			Query         *string         `json:"query"`
			Context       *int            `json:"context"`
			MaxGroups     int             `json:"max_groups"`
			BeforeID      int64           `json:"before_id"`
			Tz            string          `json:"tz"`
			LastSeenLogID *int64          `json:"last_seen_log_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Query == nil {
			content, isError = "query is required.", true
			break
		}
		contextLines := 15
		if args.Context != nil {
			contextLines = *args.Context
			if contextLines < 0 {
				content, isError = "context must be nonnegative", true
				break
			}
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
		buffer, err := mcpResolveBuffer(args.Buffer)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		if _, err := s.mcpEffectiveCursor(mcpSessionID, sid, buffer, args.LastSeenLogID); err != nil {
			content, isError = err.Error(), true
			break
		}
		groups, _, err := s.store.SearchLogsDetailedForBuffer(sid, buffer, *args.Query, contextLines, args.BeforeID)
		if err != nil {
			return nil, err
		}

		if len(groups) == 0 {
			content = "No matches found."
		} else {
			totalGroups := len(groups)
			// SearchLogsDetailed returns newest groups first.
			if len(groups) > args.MaxGroups {
				groups = groups[:args.MaxGroups]
			}
			content = formatMcpSearch(groups, *args.Query, totalGroups, args.MaxGroups, s.resolveRequestLocation(sid, args.Tz))
			var shown []storage.LogEntry
			for _, group := range groups {
				shown = append(shown, group...)
			}
			s.mcpRememberCursor(mcpSessionID, sid, buffer, mcpMaxEntryID(shown))
		}

	case "mud_send_command":
		var args struct {
			SessionID     int64           `json:"session_id"`
			Buffer        json.RawMessage `json:"buffer"`
			Command       *string         `json:"command"`
			Sync          *float64        `json:"sync"`
			LastSeenLogID *int64          `json:"last_seen_log_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Command == nil {
			content, isError = "command is required.", true
			break
		}
		command := *args.Command
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
		buffer, err := mcpResolveBuffer(args.Buffer)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		cursor, err := s.mcpEffectiveCursor(mcpSessionID, sid, buffer, args.LastSeenLogID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		// Capture the latest visible entry before the write. Both continuity and
		// command response queries are bounded by this ID, so concurrent output
		// cannot migrate across the command boundary.
		lastID, err := s.store.LatestVisibleLogIDForBuffer(sid, buffer)
		if err != nil {
			return nil, err
		}
		var unseen []storage.LogEntry
		var unseenCount int64
		if lastID > cursor {
			unseenCount, err = s.store.CountVisibleLogsBetweenIDsForBuffer(sid, buffer, cursor, lastID)
			if err != nil {
				return nil, err
			}
			if unseenCount <= 1000 {
				unseen, err = s.store.VisibleLogsBetweenIDsForBuffer(sid, buffer, cursor, lastID, 1000)
			} else {
				unseen, err = s.store.RecentVisibleLogsBetweenIDsForBuffer(sid, buffer, cursor, lastID, 200)
			}
			if err != nil {
				return nil, err
			}
		}
		if err := sess.SendCommand(command, "mcp"); err != nil {
			content = fmt.Sprintf("Error sending command: %v", err)
			isError = true
			break
		}
		continuity := formatMcpContinuity(unseen, unseenCount)
		if syncSecs <= 0 {
			content = continuity + fmt.Sprintf("Command '%s' sent to session %d", command, sid)
			s.mcpRememberCursor(mcpSessionID, sid, buffer, mcpMaxEntryID(unseen))
			break
		}
		timer := time.NewTimer(time.Duration(syncSecs * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			content, isError = fmt.Sprintf("Command '%s' sent, but response wait was cancelled", command), true
			break
		}
		if isError {
			break
		}
		postCommandID, err := s.store.LatestVisibleLogIDForBuffer(sid, buffer)
		if err != nil {
			return nil, err
		}
		entries, err := s.store.VisibleLogsBetweenIDsForBuffer(sid, buffer, lastID, postCommandID, 200)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			content = continuity + fmt.Sprintf("Command '%s' sent. No output received in %.1fs.", command, syncSecs)
		} else {
			content = continuity + fmt.Sprintf("Command '%s' sent. Response (%.1fs window):\n\n", command, syncSecs) +
				formatMcpGroup(entries, "")
		}
		s.mcpRememberCursor(mcpSessionID, sid, buffer, maxInt64(mcpMaxEntryID(unseen), mcpMaxEntryID(entries)))

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
			SessionID int64   `json:"session_id"`
			Key       *string `json:"key"`
			Value     *string `json:"value"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Key == nil || args.Value == nil {
			content, isError = "key and value are required.", true
			break
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content = err.Error()
			isError = true
			break
		}
		if err := s.store.SetVariable(sid, *args.Key, *args.Value); err != nil {
			content, isError = err.Error(), true
			break
		}
		if sess, ok := s.manager.GetSession(sid); ok {
			sess.NotifySettingsChanged("variables")
		}
		content = fmt.Sprintf("$%s set to %q", *args.Key, *args.Value)

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
			SessionID int64             `json:"session_id"`
			ToZone    string            `json:"to_zone"`
			ToHint    string            `json:"to_hint"`
			To        *mcpRequiredCoord `json:"to"`
			From      *mcpRequiredCoord `json:"from"`
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
		var to, from *mcpCoordArg
		if args.To != nil {
			coord, ok := args.To.value()
			if !ok {
				content, isError = "to:{zone,x,y,l} requires all four fields.", true
				break
			}
			to = &coord
		}
		if args.From != nil {
			coord, ok := args.From.value()
			if !ok {
				content, isError = "from:{zone,x,y,l} requires all four fields.", true
				break
			}
			from = &coord
		}
		content, isError = s.mcpPath(sid, mcpPathArgs{
			ToZone: args.ToZone,
			ToHint: args.ToHint,
			To:     to,
			From:   from,
		})

	case "mud_map_walk":
		var args struct {
			SessionID int64             `json:"session_id"`
			To        *mcpRequiredCoord `json:"to"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		if args.To == nil {
			content, isError = "to:{zone,x,y,l} is required.", true
			break
		}
		to, ok := args.To.value()
		if !ok {
			content, isError = "to:{zone,x,y,l} is required.", true
			break
		}
		content, isError = s.mcpMapWalkStart(sid, to)

	case "mud_map_walk_status":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		content, isError = s.mcpMapWalkStatus(sid)

	case "mud_map_walk_stop":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		content, isError = s.mcpMapWalkStop(sid)

	case "mud_map_walk_resume":
		var args struct {
			SessionID int64 `json:"session_id"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		content, isError = s.mcpMapWalkResume(sid)

	case "mud_anchor_here":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
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
		if args.Zone == nil || strings.TrimSpace(*args.Zone) == "" {
			content, isError = "zone is required.", true
			break
		}
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		content, isError = s.mcpAnchorHere(sid, coord)

	case "mud_room_annotate":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
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
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		content, isError = s.mcpRoomAnnotate(sid,
			coord,
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

	case "mud_map_undo":
		var args struct {
			SessionID int64 `json:"session_id"`
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
		content, isError = s.mcpMapUndo(sid)

	case "mud_room_upsert":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
			Hint       *string   `json:"hint"`
			Desc       *string   `json:"desc"`
			Exits      *string   `json:"exits"`
			EDirs      *[]string `json:"edirs"`
			Ch         *int      `json:"ch"`
			IsDT       *bool     `json:"is_dt"`
			Pipe       *bool     `json:"pipe"`
			ImageIndex *int      `json:"imageindex"`
			Note       *string   `json:"note"`
			DX         *int      `json:"dx"`
			DY         *int      `json:"dy"`
			DL         *int      `json:"dl"`
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
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		f := storage.RoomFields{
			Hint: args.Hint, Desc: args.Desc, Exits: args.Exits, Ch: args.Ch,
			IsDT: args.IsDT, Pipe: args.Pipe, ImageIndex: args.ImageIndex,
			Note: args.Note, DX: args.DX, DY: args.DY, DL: args.DL,
		}
		if args.EDirs != nil {
			f.SetEDirs(*args.EDirs)
		}
		content, isError = s.mcpRoomUpsert(sid, coord, f)

	case "mud_room_link", "mud_room_unlink":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
			Dir string `json:"dir"`
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
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		dir := strings.ToUpper(strings.TrimSpace(args.Dir))
		if !isCanonicalDir(dir) {
			content = fmt.Sprintf("dir %q is not a canonical direction (want one of N,S,E,W,U,D).", args.Dir)
			isError = true
			break
		}
		content, isError = s.mcpRoomLink(sid, coord, dir, call.Name == "mud_room_link")

	case "mud_room_mark_edge":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
			Dir     string   `json:"dir"`
			Mark    string   `json:"mark"`
			OneWay  bool     `json:"one_way"`
			Weight  *float64 `json:"weight"`
			Phrase  *string  `json:"phrase"`
			Door    *bool    `json:"door"`
			Locked  *bool    `json:"locked"`
			OpenCmd *string  `json:"open_cmd"`
			NoRoute *bool    `json:"no_route"`
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
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		dir := strings.ToUpper(strings.TrimSpace(args.Dir))
		if !isCanonicalDir(dir) {
			content = fmt.Sprintf("dir %q is not a canonical direction (want one of N,S,E,W,U,D).", args.Dir)
			isError = true
			break
		}
		patch := storage.EdgeAttrPatch{
			Weight: args.Weight, Phrase: mcpTrimmedPtr(args.Phrase), Door: args.Door, Locked: args.Locked,
			OpenCmd: mcpTrimmedPtr(args.OpenCmd), NoRoute: args.NoRoute,
		}
		// `mark` is now optional: a call that only describes an existing passage
		// (phrase/door/…) has nothing to mark. A call with neither is a no-op the agent
		// should hear about rather than have silently succeed.
		// A phrase or an opening command that is a bare direction would be pushed onto
		// the tracker's pending queue when the route emits it — a phantom step that
		// moves the player's recorded position. Same check as the REST surface.
		if reason := edgeCommandsRefusal(patch); reason != "" {
			content = reason
			isError = true
			break
		}
		if err := patch.Validate(); err != nil {
			content, isError = err.Error(), true
			break
		}
		markGiven := strings.TrimSpace(args.Mark) != ""
		if !markGiven && patch.Empty() {
			content = "nothing to do: pass mark (open/blocked/auto) to draw the passage, and/or " +
				"phrase/door/locked/open_cmd/no_route to describe how it is taken."
			isError = true
			break
		}
		if markGiven {
			kind, ok := manualMarkKind(args.Mark)
			if !ok {
				content = fmt.Sprintf("mark %q is not understood (want \"open\", \"blocked\" or \"auto\").", args.Mark)
				isError = true
				break
			}
			content, isError = s.mcpRoomMarkEdge(sid, coord, dir, kind, args.OneWay)
			if isError || patch.Empty() {
				break
			}
			// The mark landed; now the attributes, as their own write and its own undo
			// entry. Order matters: mark="open" may be what CREATES the passage the
			// attributes then describe.
			attrText, attrErr := s.mcpRoomEdgeAttrs(sid, coord, dir, patch)
			content += "\n" + attrText
			isError = attrErr
			break
		}
		content, isError = s.mcpRoomEdgeAttrs(sid, coord, dir, patch)

	case "mud_room_set_seam_attrs":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
			TargetZone string   `json:"target_zone"`
			TargetTag  *int     `json:"target_tag"`
			Command    *string  `json:"command"`
			Order      *int     `json:"order"`
			Weight     *float64 `json:"weight"`
			Phrase     *string  `json:"phrase"`
			Door       *bool    `json:"door"`
			Locked     *bool    `json:"locked"`
			OpenCmd    *string  `json:"open_cmd"`
			NoRoute    *bool    `json:"no_route"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		if args.Command == nil {
			content, isError = "zone plus target_zone, target_tag, and command are required.", true
			break
		}
		seam := storage.SeamSelector{TargetZone: args.TargetZone, TargetTag: args.TargetTag, Command: *args.Command, Order: args.Order}
		if !seam.Valid() {
			content, isError = "zone plus target_zone, target_tag, and command are required.", true
			break
		}
		patch := storage.EdgeAttrPatch{Weight: args.Weight, Phrase: mcpTrimmedPtr(args.Phrase), Door: args.Door, Locked: args.Locked, OpenCmd: mcpTrimmedPtr(args.OpenCmd), NoRoute: args.NoRoute}
		if patch.Empty() {
			content, isError = "provide at least one seam attribute to set.", true
			break
		}
		if reason := edgeCommandsRefusal(patch); reason != "" {
			content, isError = reason, true
			break
		}
		if err := patch.Validate(); err != nil {
			content, isError = err.Error(), true
			break
		}
		content, isError = s.mcpRoomSeamAttrs(sid, coord, seam, patch)

	case "mud_room_portal":
		var args struct {
			SessionID int64  `json:"session_id"`
			Action    string `json:"action"`
			mcpRequiredCoord
			Order      *int     `json:"order"`
			TargetZone *string  `json:"target_zone"`
			TargetX    *int     `json:"target_x"`
			TargetY    *int     `json:"target_y"`
			TargetL    *int     `json:"target_l"`
			Command    *string  `json:"command"`
			Weight     *float64 `json:"weight"`
			Phrase     *string  `json:"phrase"`
			Door       *bool    `json:"door"`
			Locked     *bool    `json:"locked"`
			OpenCmd    *string  `json:"open_cmd"`
			NoRoute    *bool    `json:"no_route"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		attrs := storage.EdgeAttrPatch{Weight: args.Weight, Phrase: mcpTrimmedPtr(args.Phrase), Door: args.Door, Locked: args.Locked, OpenCmd: mcpTrimmedPtr(args.OpenCmd), NoRoute: args.NoRoute}
		if err := attrs.Validate(); err != nil {
			content, isError = err.Error(), true
			break
		}
		if reason := edgeCommandsRefusal(attrs); reason != "" {
			content, isError = reason, true
			break
		}
		action := strings.ToLower(strings.TrimSpace(args.Action))
		var target *storage.PortalTarget
		if args.TargetZone != nil || args.TargetX != nil || args.TargetY != nil || args.TargetL != nil {
			if args.TargetZone == nil || args.TargetX == nil || args.TargetY == nil || args.TargetL == nil || strings.TrimSpace(*args.TargetZone) == "" {
				content, isError = "target_zone,target_x,target_y,target_l must be supplied together.", true
				break
			}
			target = &storage.PortalTarget{Zone: strings.TrimSpace(*args.TargetZone), X: *args.TargetX, Y: *args.TargetY, L: *args.TargetL}
		}
		switch action {
		case "create":
			if args.Order != nil {
				content, isError = "create does not accept order.", true
				break
			}
			if target == nil || args.Command == nil {
				content, isError = "create requires target_zone,target_x,target_y,target_l and command.", true
				break
			}
			content, isError = s.mcpRoomPortal(sid, coord, storage.TopologyOp{Kind: storage.TopoCreatePortal, Zone: coord.Zone, X: coord.X, Y: coord.Y, L: coord.L, Portal: target, PortalCommand: *args.Command, PortalAttrs: attrs})
		case "update":
			if args.Order == nil || *args.Order < 0 {
				content, isError = "update requires non-negative order.", true
				break
			}
			content, isError = s.mcpRoomPortal(sid, coord, storage.TopologyOp{Kind: storage.TopoUpdatePortal, Zone: coord.Zone, X: coord.X, Y: coord.Y, L: coord.L, PortalSelector: &storage.PortalSelector{Order: *args.Order}, PortalPatch: storage.PortalPatch{Target: target, Command: args.Command, Attrs: attrs}})
		case "delete":
			if args.Order == nil || *args.Order < 0 {
				content, isError = "delete requires non-negative order.", true
				break
			}
			if target != nil || args.Command != nil || !attrs.Empty() {
				content, isError = "delete accepts only source coordinates and order.", true
				break
			}
			content, isError = s.mcpRoomPortal(sid, coord, storage.TopologyOp{Kind: storage.TopoDeletePortal, Zone: coord.Zone, X: coord.X, Y: coord.Y, L: coord.L, PortalSelector: &storage.PortalSelector{Order: *args.Order}})
		default:
			content, isError = "action must be create, update, or delete.", true
		}

	case "mud_map_move_rooms", "mud_map_rename_zone":
		var args struct {
			SessionID        int64               `json:"session_id"`
			SourceZone       string              `json:"source_zone"`
			TargetZone       string              `json:"target_zone"`
			Rooms            []storage.ZoneCoord `json:"rooms"`
			DryRun           bool                `json:"dry_run"`
			ExpectedRevision *int64              `json:"expected_revision"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, err
		}
		sid, err := s.mcpResolveSessionID(args.SessionID)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		in := storage.ZoneMoveInput{SourceZone: args.SourceZone, TargetZone: args.TargetZone, Rooms: args.Rooms, AllSource: call.Name == "mud_map_rename_zone"}
		if !args.DryRun {
			if args.ExpectedRevision == nil {
				content, isError = "expected_revision from a dry run is required for apply.", true
				break
			}
			in.ExpectedRevision = *args.ExpectedRevision
		}
		in, err = storage.NormalizeZoneMove(in)
		if err != nil {
			content, isError = err.Error(), true
			break
		}
		content, isError = s.mcpZoneMove(sid, in, args.DryRun)

	case "mud_room_delete":
		var args struct {
			SessionID int64 `json:"session_id"`
			mcpRequiredCoord
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
		coord, ok := args.mcpRequiredCoord.value()
		if !ok {
			content, isError = "zone,x,y,l are required.", true
			break
		}
		content, isError = s.mcpRoomDelete(sid, coord)

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
		exits := strings.Join(r.E, "")
		doors := ""
		if len(r.D) > 0 {
			doors = " doors=" + strings.Join(r.D, "")
		}
		sb.WriteString(fmt.Sprintf("(%d,%d,L%d) tag=%s ch=%d exits=[%s]%s %q%s",
			r.X, r.Y, r.L, tag, r.Ch, exits, doors, r.H, flags))
		if topology := formatSlimTopology(r); topology != "" {
			sb.WriteString("  " + topology)
		}
		sb.WriteString("\n")
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n... truncated: %d more room(s) not shown. Increase limit (max %d) to see more.\n",
			len(rooms)-limit, mcpMapZoneMaxLimit))
	}
	return sb.String()
}

// formatSlimTopology mirrors the REST wire fields without making the common room
// pay for default topology text. Grid direction sets are normalized here; seams
// already arrive in persisted occurrence order from ListSlimRooms.
func formatSlimTopology(r storage.SlimRoom) string {
	var parts []string
	omitted := 0
	appendDirections := func(label string, dirs []string) {
		value, n := formatDirectionSet(dirs)
		omitted += n
		if value != "" {
			parts = append(parts, label+"=["+value+"]")
		}
	}
	appendDirections("manual_open", r.M)
	appendDirections("manual_blocked", r.MB)
	appendDirections("grid_parsed", r.G)
	appendDirections("grid_fallback", r.GF)
	seams := append([]storage.SlimSeam(nil), r.SE...)
	if len(seams) > mcpTopologyListLimit {
		omitted += len(seams) - mcpTopologyListLimit
		seams = seams[:mcpTopologyListLimit]
	}
	for _, seam := range seams {
		parts = append(parts, "seam={"+formatSlimSeam(seam)+"}")
	}
	attrs := make([]storage.SlimEdgeAttr, 0, len(r.EA))
	for _, attr := range r.EA {
		if slimAttrsNonDefault(attr.Weight, attr.Phrase, attr.Door, attr.Locked, attr.OpenCmd, attr.NoRoute) {
			attrs = append(attrs, attr)
		}
	}
	sort.Slice(attrs, func(i, j int) bool {
		left, right := mcpDirectionRank(attrs[i].Dir), mcpDirectionRank(attrs[j].Dir)
		if left != right {
			return left < right
		}
		return attrs[i].Dir < attrs[j].Dir
	})
	if len(attrs) > mcpTopologyListLimit {
		omitted += len(attrs) - mcpTopologyListLimit
		attrs = attrs[:mcpTopologyListLimit]
	}
	for _, a := range attrs {
		parts = append(parts, "attrs={"+formatSlimEdgeAttr(a)+"}")
	}
	if omitted > 0 {
		parts = append(parts, fmt.Sprintf("topology_truncated=%d", omitted))
	}
	return strings.Join(parts, " ")
}

func formatDirectionSet(dirs []string) (string, int) {
	if len(dirs) == 0 {
		return "", 0
	}
	out := append([]string(nil), dirs...)
	sort.Slice(out, func(i, j int) bool {
		left, right := mcpDirectionRank(out[i]), mcpDirectionRank(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	omitted := 0
	if len(out) > mcpTopologyListLimit {
		omitted = len(out) - mcpTopologyListLimit
		out = out[:mcpTopologyListLimit]
	}
	return strings.Join(out, ""), omitted
}

func mcpDirectionRank(dir string) int {
	for i, d := range []string{"N", "S", "E", "W", "U", "D"} {
		if dir == d {
			return i
		}
	}
	return 100
}

func formatSlimEdgeAttr(a storage.SlimEdgeAttr) string {
	parts := []string{"dir=" + a.Dir}
	if weight, ok := mcpNonDefaultWeight(a.Weight); ok {
		parts = append(parts, fmt.Sprintf("weight=%g", weight))
	}
	parts = appendNonDefaultAttrParts(parts, a.Phrase, a.Door, a.Locked, a.OpenCmd, a.NoRoute)
	return strings.Join(parts, " ")
}

func formatSlimSeam(a storage.SlimSeam) string {
	tag := "null"
	if a.TargetTag != nil {
		tag = fmt.Sprintf("%d", *a.TargetTag)
	}
	parts := []string{fmt.Sprintf("order=%d", a.Order), "kind=" + a.Kind, "source=" + a.Source,
		fmt.Sprintf("passable=%t", a.Passable), fmt.Sprintf("target_zone=%q", a.TargetZone), "target_tag=" + tag, fmt.Sprintf("command=%q", a.Command)}
	if a.ToZone != nil && a.ToX != nil && a.ToY != nil && a.ToL != nil {
		parts = append(parts, fmt.Sprintf("to={zone=%q x=%d y=%d l=%d}", *a.ToZone, *a.ToX, *a.ToY, *a.ToL))
	}
	if weight, ok := mcpNonDefaultWeight(a.Weight); ok {
		parts = append(parts, fmt.Sprintf("weight=%g", weight))
	}
	parts = appendNonDefaultAttrParts(parts, a.Phrase, a.Door, a.Locked, a.OpenCmd, a.NoRoute)
	return strings.Join(parts, " ")
}

func mcpNonDefaultWeight(weight *float64) (float64, bool) {
	if weight == nil || *weight <= 0 || math.IsNaN(*weight) || math.IsInf(*weight, 0) {
		return 1, false
	}
	return *weight, *weight != 1
}

func slimAttrsNonDefault(weight *float64, phrase string, door, locked bool, openCmd string, noRoute bool) bool {
	_, weighted := mcpNonDefaultWeight(weight)
	return weighted || phrase != "" || door || locked || openCmd != "" || noRoute
}

func appendNonDefaultAttrParts(parts []string, phrase string, door, locked bool, openCmd string, noRoute bool) []string {
	if phrase != "" {
		parts = append(parts, fmt.Sprintf("phrase=%q", phrase))
	}
	if door {
		parts = append(parts, "door=true")
	}
	if locked {
		parts = append(parts, "locked=true")
	}
	if openCmd != "" {
		parts = append(parts, fmt.Sprintf("open_cmd=%q", openCmd))
	}
	if noRoute {
		parts = append(parts, "no_route=true")
	}
	return parts
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

func formatMcpContinuity(entries []storage.LogEntry, count int64) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Messages since your last seen output:\n")
	if count > 1000 {
		sb.WriteString(fmt.Sprintf("More than 1000 unseen messages arrived; showing the most recent %d.\n", len(entries)))
	}
	sb.WriteString("\n")
	sb.WriteString(formatMcpGroup(entries, ""))
	sb.WriteString("\n")
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
