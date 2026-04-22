# Plan 0.0.6.4 — MCP Server

## Context

Expose the MUD session to Claude (or any MCP client) via the Model Context Protocol.
An AI can read game screen output and send commands — useful for automation, assistance,
or just letting Claude play alongside you.

Implemented as **Streamable HTTP** transport at `/mcp`.
No auth for the initial experiment (local access only).

---

## Transport

**MCP Streamable HTTP (spec 2025-03-26)** — single `POST /mcp` endpoint.

- `Content-Type: application/json` for request + response
- No SSE streaming needed — all tools are synchronous request/response
- No `Mcp-Session-Id` header (stateless, no server-side session tracking)
- Responds to both individual JSON-RPC objects and notification-style messages

---

## JSON-RPC 2.0 Methods

| Method | Behaviour |
|--------|-----------|
| `initialize` | Return server info + tool capability declaration |
| `notifications/initialized` | No-op (notification, no `id`) |
| `tools/list` | Return array of tool schemas |
| `tools/call` | Dispatch to tool, return `content` array |

`initialize` response:
```json
{
  "protocolVersion": "2024-11-05",
  "capabilities": { "tools": {} },
  "serverInfo": { "name": "mudhost-mcp", "version": "0.0.6.4" }
}
```

---

## Tools

| Tool | Parameters | Description |
|------|-----------|-------------|
| `mud_get_output` | `session_id: int, limit?: int = 100` | Last N lines, chronological |
| `mud_get_output_range` | `session_id: int, before_id: int, limit?: int = 50` | Paginate upward from an ID |
| `mud_search` | `session_id: int, query: string, context?: int = 30` | Case-insensitive LIKE + ±N context rows |
| `mud_send_command` | `session_id: int, command: string` | Send command into the game session |

### Output format (text content)

Each line:
```
[#1234] The orc attacks you!
        > kill orc
```
- ID prefix allows the model to reference specific lines for `mud_get_output_range`
- Command hints indented under the line they belong to
- Search results: groups of lines separated by `---`, matched line prefixed with `***`

---

## Storage Additions

**File: `go/internal/storage/log_store.go`**

Two new methods (work directly with `LogRecord` — no overlay loading needed for MCP):

```go
// LogRange returns up to limit records with id < beforeID, in chronological order.
// Used for upward pagination ("show me more").
func (s *Store) LogRange(sessionID, beforeID int64, limit int) ([]LogRecord, error)

// SearchLogs finds records matching plain_text LIKE %query% and returns groups
// of contextLines rows before+after each match (deduped, sorted by id).
// Returns [][]LogRecord where each inner slice is one context window.
func (s *Store) SearchLogs(sessionID int64, query string, contextLines int) ([][]LogRecord, error)
```

`RecentLogs` already exists — reused for `mud_get_output` but querying `LogRecord` directly
(not `LogEntry` with overlays) to get clean `PlainText` without button/trigger noise.

---

## Implementation

**New file: `go/internal/web/mcp.go`**

```
handleMCP(w, r)
  ├── decode JSON-RPC request
  ├── switch method:
  │   ├── "initialize"                → writeResult(initResponse)
  │   ├── "notifications/initialized" → write empty (notification, no id)
  │   ├── "tools/list"                → writeResult(toolList)
  │   └── "tools/call"                → dispatchTool(name, args)
  │       ├── mud_get_output          → LogRange(sessionID, maxInt64, limit)
  │       ├── mud_get_output_range    → LogRange(sessionID, beforeID, limit)
  │       ├── mud_search              → SearchLogs(sessionID, query, context)
  │       └── mud_send_command        → manager.GetSession + sess.SendCommand
  └── unknown method → JSON-RPC error -32601
```

Helper `formatRecords(records []LogRecord) string` — produces `[#id] text` lines.

**Modified: `go/internal/web/server.go`**

Add `/mcp` **outside** the token-protected router group:
```go
r.Post("/mcp", s.handleMCP)
```

---

## Auth

`/mcp` is outside the `X-Session-Token` middleware for the first experiment.
Can move inside later or add a dedicated MCP token.

---

## No External Dependencies

Pure Go stdlib: `encoding/json`, `strings`, `fmt`. No MCP SDK needed.

---

## Critical Files

| File | Change |
|------|--------|
| `go/internal/storage/log_store.go` | Add `LogRange`, `SearchLogs` |
| `go/internal/web/mcp.go` | **New** — full MCP handler |
| `go/internal/web/server.go` | Add `r.Post("/mcp", s.handleMCP)` outside auth group |

---

## Verification

1. `go build ./...` — no errors
2. `go test ./...` — existing tests pass
3. Add MCP server to Claude Code / Claude Desktop config:
   ```json
   { "mcpServers": { "mud": { "type": "http", "url": "http://localhost:PORT/mcp" } } }
   ```
4. Ask Claude: "get output from session 1" → verify last 100 lines appear
5. Ask Claude: "send 'look' to session 1" → verify command appears in game
6. Ask Claude: "search for 'orc' in session 1" → verify matches with context
