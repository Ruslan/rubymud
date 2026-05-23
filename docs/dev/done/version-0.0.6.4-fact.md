# Version 0.0.6.4 — MCP Server (Done)

## Current State

- **`mcp.go`** (587 lines): Full Streamable HTTP MCP endpoint at `POST /mcp`. Handles `initialize`, `notifications/initialized`, `tools/list`, `tools/call`.
- **8 tools**: `mud_get_output`, `mud_get_output_range`, `mud_search`, `mud_send_command`, `mud_list_sessions`, `mud_get_variables`, `mud_set_variable`, `mud_get_aliases`, `mud_get_triggers`.
- **Storage**: `LogRangeDetailed` and `SearchLogsDetailed` in `log_store.go`.
- **Auth**: Outside token middleware for local-first experiment.
- **Tests**: `mcp_test.go` (5 tests: initialize, tools/list, get_output, send_command, search).
- **Docs**: `docs/agents/mcp.md`.
- **No external MCP SDK**: pure Go stdlib `encoding/json`.
