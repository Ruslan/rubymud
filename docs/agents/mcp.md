# MCP Engineering Guide

The user and client contract is [MCP](../mcp.md). Keep that document current
when changing the shipped tool surface, endpoint behavior, or security model.

## Implementation

- HTTP JSON-RPC handler and tool registration: `go/internal/web/mcp.go`.
- Mapper tools and their text formatting: `go/internal/web/mcp_mapper.go`.
- Ephemeral MCP session/cursor store: `go/internal/web/mcp_session_store.go`.
- Enable/token override resolution: `go/cmd/mudhost/main.go`.
- Persistent singleton state: `go/internal/storage/migrations/022_mcp_settings.sql`
  and `go/internal/storage/settings_store.go`. It contains only the SHA-256
  digest, never the raw bearer secret.
- Runtime gate and admin API: `go/internal/web/mcp_runtime.go` and
  `go/internal/web/server.go`; focused coverage is `mcp_admin_test.go`.
- Endpoint routing, token validation, loopback restriction, and Origin policy:
  `go/internal/web/server.go` and `go/internal/web/mcp_auth_test.go`.

The endpoint is optional, `POST /mcp`, Streamable HTTP request/response. It is
not SSE, browser CORS, or a WebSocket MCP transport. `initialize` creates an
opaque `Mcp-Session-Id`; the id carries only best-effort continuity state, not
authentication. The listed surface currently contains 29 tools. `mud_room` is
a handler-only compatibility alias for `mud_look_map`, not a `tools/list` item.

## Invariants

- MCP is disabled by default. Tokenless enabled MCP must bind to a real loopback
  listener; a remote-capable bind requires the independent bearer token. The
  admin enable endpoint generates a 256-bit URL-safe token atomically when a
  remote-capable listener would otherwise become tokenless.
- Reject all Origin-bearing requests. Never log, persist, or expose the MCP
  token through settings reads. A generated token is returned only once by its
  successful write response; the SQLite singleton keeps its digest only.
- The request gate is in-memory and snapshot based; do not add a DB read to MCP
  request handling. Requests revalidate after body parsing and hold the runtime
  read lock only through continuity-session validation/creation, never through
  tool, database, or network work. Disable/rotate invalidates the ephemeral
  session store.
- Store post-commit notifications keep multiple `Server` instances sharing one
  `Store` synchronized. The persisted revision prevents out-of-order callbacks
  from restoring older credentials.
- CLI/environment values are explicit operator overrides. Reflect locked state
  through the admin API rather than letting the UI contradict an operator.
- Text continuity is per MCP session, MUD session, and exact buffer. It is
  ephemeral, buffer-scoped, and advances only through visible output returned
  to the caller.
- Omitted text `buffer` means `main`; it filters reads and command responses,
  never command routing.
- Expected operational failures are text tool results with `isError: true`.
  Reserve JSON-RPC errors for malformed calls and server failures.
- Mapper reads must use materialized topology. Directed grid faces and seams
  retain independent attributes; `no_route` vetoes planning but not walking.
- Map-walk commands are server-owned and confirmation-paced. They share the
  in-flight-hop barrier with manual movement and the map UI.
- Topology writes use the session write path, fork frozen sets, refresh live
  state, broadcast changes, and are undone per active set. Annotation overlays
  do not fork and may intentionally be dangling.

## Change And Debug Workflow

1. Add a focused failing test before correcting a real MCP behavior defect.
2. Update the schema in `mcpListTools`, the corresponding `mcpCallTool` decoder,
   handler validation, and user documentation as one contract change.
3. For a new listed tool, add it to the bounded inventory in `docs/mcp.md`.
   `TestMCPDocumentationToolInventoryMatchesShippedTools` rejects duplicates,
   undocumented shipped tools, and non-shipped inventory entries.
4. Run focused MCP tests with `go test ./internal/web -run MCP`, then `go test
   ./...` from `go/` and `go vet ./...`.

Useful tests include `mcp_test.go` (protocol/basic tools), `mcp_auth_test.go`
(configuration/security), `mcp_continuity_test.go` (buffers/cursors), and the
mapper-specific `mcp_*_test.go` files. Inspect `data/mudhost.db` directly when
debugging map or log state; do not infer it from a stale browser view.
