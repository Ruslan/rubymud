# MCP (Model Context Protocol) Guide

Use this when you need to interact with the project via MCP or modify the MCP server implementation.

## Server Implementation

- **Location**: `go/internal/web/mcp.go`
- **Endpoint**: `/mcp` (accessible via HTTP POST).

## Available Tools

### MUD Interaction
- `mud_send_command`: Sends a command to the active MUD session and returns the synchronous response.
- `mud_get_output`: Returns the most recent N lines of output from a specific buffer.
- `mud_get_output_range`: Returns a specific range of log entries by ID.
- `mud_search`: Searches log entries using plain text or FTS5.

### State Management
- `mud_get_variables`: Lists all current variables for a session.
- `mud_set_variable`: Updates or creates a variable.
- `mud_get_session_info`: Returns current connection status and session details.

## Continuity Support

The MCP server supports `last_seen_log_id` to provide continuity. When the model sends a command, it can see what happened in the game since its last turn.

## Authentication

If configured, MCP requests require a `Authorization: Bearer <token>` header.

## Usage in Clients

To connect from Claude Desktop or other MCP clients:
1. Ensure `mudhost` is running.
2. Use the `/mcp` URL as the server transport (Note: standard MCP usually uses stdio or SSE; this implementation uses a custom HTTP-based bridge).
