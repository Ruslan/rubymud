# Agent Notes

This repository is `rubymud`, a local-first MUD host and browser client.

## Core Runtime & Data
- **Backend**: Go (located in `go/`). Primary entry point: `go/cmd/mudhost/main.go`.
- **Runtime Data**: Lives in `data/`.
- **Main DB file**: `data/mudhost.db` (SQLite).
- **Migration**: The project was migrated from Ruby to Go; ignore `server.rb` and `Gemfile` for current development unless specifically asked.

## Domain Documentation
Read these guides to understand specific project domains:

- **[SQLite & Storage](file:///home/ru/rubymud/docs/agents/sqlite.md)**: Extraction queries, schema overview, and debugging workflow.
- **[Engine & VM](file:///home/ru/rubymud/docs/agents/vm.md)**: TinTin++ compatibility, command processing pipeline, triggers, and aliases.
- **[Profiles & Layering](file:///home/ru/rubymud/docs/agents/profiles.md)**: Multiple profile support and rule resolution logic.
- **[Colors & Overlays](file:///home/ru/rubymud/docs/agents/colors.md)**: ANSI processing and the structured overlay model.
- **[UI & Frontend](file:///home/ru/rubymud/docs/agents/ui.md)**: Vanilla TS rendering, Svelte 5 settings app, and WebSocket communication.
- **[Transport Layer](file:///home/ru/rubymud/docs/agents/transport.md)**: Telnet, MCCP2 compression, WebSockets, and session restoration.
- **[Performance & Batching](file:///home/ru/rubymud/docs/agents/performance.md)**: Low-latency design, backend batching, and UI pruning.
- **[Testing & Quality](file:///home/ru/rubymud/docs/agents/testing.md)**: TDD approach, repro-first rule, and test layers.
- **[MCP (Model Context Protocol)](file:///home/ru/rubymud/docs/agents/mcp.md)**: Tools and integration for AI agents.

## Critical Principles
When working on this codebase, you **MUST** follow these rules:

1. **Consider Layering**: Always account for multiple active profiles. Rules, aliases, and variables are resolved across layers. Use `GetOrderedProfileIDs`.
2. **Admin/Game Sync**: New entities must be manageable via the Settings UI. Be mindful of the synchronization between in-memory state (game activity) and SQLite state (admin changes).
3. **Performance First**: Maintain low latency and high throughput. Avoid blocking the main game loop or bloating the browser DOM.
4. **Repro-First TDD**: Fix bugs by first writing a failing reproduction test case. Only then implement the fix.

## Development Workflow
- Use `make run` to build the UI and start the server.
- The UI source of truth is in `ui/`. Compiled assets in `go/internal/web/static/` are generated and should not be edited directly.
- Prefer inspecting `mudhost.db` directly for runtime state.
