# MCP (Model Context Protocol) Guide

Use this when you need to interact with the project via MCP or modify the MCP server implementation.

## Server Implementation

- **Location**: `go/internal/web/mcp.go`
- **Endpoint**: `POST /mcp` (Streamable HTTP, no auth for local access).

## Available Tools (9 total)

### Session Interaction
- `mud_list_sessions`: Lists all configured sessions with connection status.
- `mud_get_output`: Returns the most recent N lines of output (default 100).
- `mud_get_output_range`: Returns log entries before a given ID (upward pagination).
- `mud_search`: Full-text search across session logs with surrounding context.
- `mud_send_command`: Sends a command to a MUD session.

### State Management
- `mud_get_variables`: Lists all resolved variables for a session.
- `mud_set_variable`: Updates or creates a session variable.
- `mud_get_aliases`: Lists all aliases active for a session.
- `mud_get_triggers`: Lists all triggers active for a session.

## Continuity Support

The server tracks `last_seen_log_id` per session. After sending a command, the model can request output since its last turn.

## Authentication

MCP endpoint is outside the `X-Session-Token` middleware for local-first access.

## Usage in Clients

To connect from Claude Desktop or other MCP clients:
1. Ensure `mudhost` is running.
2. Configure HTTP transport pointing to `http://localhost:PORT/mcp`.
