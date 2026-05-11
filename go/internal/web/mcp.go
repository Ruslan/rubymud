package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"rubymud/go/internal/storage"
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
		},
	}
}

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
			SessionID int64 `json:"session_id"`
			Limit     int   `json:"limit"`
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
		content = s.formatMcpEntries(entries, "")

	case "mud_get_output_range":
		var args struct {
			SessionID int64 `json:"session_id"`
			BeforeID  int64 `json:"before_id"`
			Limit     int   `json:"limit"`
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
		content = s.formatMcpEntries(entries, "")

	case "mud_search":
		var args struct {
			SessionID int64  `json:"session_id"`
			Query     string `json:"query"`
			Context   int    `json:"context"`
			MaxGroups int    `json:"max_groups"`
			BeforeID  int64  `json:"before_id"`
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
		groups, err := s.store.SearchLogsDetailed(sid, args.Query, args.Context, args.BeforeID)
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
			content = formatMcpSearch(groups, args.Query, totalGroups, args.MaxGroups)
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

const mcpTimeFormat = "2006-01-02 15:04:05"

func stripAnsi(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

// mcpHeader returns a header line with the current server time and,
// if entries is non-empty, the timestamp of the first log entry.
func mcpHeader(entries []storage.LogEntry) string {
	now := time.Now().Format(mcpTimeFormat)
	if len(entries) == 0 {
		return fmt.Sprintf("Current time: %s\n\n", now)
	}
	first := entries[0].CreatedAt.Time
	if first.IsZero() {
		return fmt.Sprintf("Current time: %s\n\n", now)
	}
	return fmt.Sprintf("Current time: %s | Log from: %s\n\n", now, first.Format(mcpTimeFormat))
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
func (s *Server) formatMcpEntries(entries []storage.LogEntry, query string) string {
	return mcpHeader(entries) + formatMcpGroup(entries, query)
}

// formatMcpSearch formats search results: header + groups separated by dividers with timestamps.
// totalGroups is the total found before slicing; maxGroups is the limit applied.
func formatMcpSearch(groups [][]storage.LogEntry, query string, totalGroups, maxGroups int) string {
	now := time.Now().Format(mcpTimeFormat)
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
			sb.WriteString(fmt.Sprintf("Group %d — %s:\n", i+1, group[0].CreatedAt.Time.Format(mcpTimeFormat)))
		} else {
			sb.WriteString(fmt.Sprintf("Group %d:\n", i+1))
		}
		sb.WriteString(formatMcpGroup(group, query))
	}
	return sb.String()
}
