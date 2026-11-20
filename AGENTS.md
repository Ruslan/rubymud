# Agent Notes

This repository is `rubymud`, a local-first MUD host and browser client.

## Core Runtime & Data
- **Backend**: Go (located in `go/`). Primary entry point: `go/cmd/mudhost/main.go`.
- **Runtime Data**: Lives in `data/`.
- **Main DB file**: `data/mudhost.db` (SQLite).
- **Migration**: The project was migrated from Ruby to Go. Legacy Ruby files are in `legacy_ruby/` — ignore for current development unless specifically asked.

## Domain Documentation
Read these guides to understand specific project domains:

- **[SQLite & Storage](docs/agents/sqlite.md)**: Extraction queries, schema overview, and debugging workflow.
- **[Engine & VM](docs/agents/vm.md)**: TinTin++ compatibility, command processing pipeline, triggers, and aliases.
- **[Profiles & Layering](docs/agents/profiles.md)**: Multiple profile support and rule resolution logic.
- **[Colors & Overlays](docs/agents/colors.md)**: ANSI processing and the structured overlay model.
- **[UI & Frontend](docs/agents/ui.md)**: Vanilla TS rendering, Svelte 5 settings app, and WebSocket communication.
- **[Transport Layer](docs/agents/transport.md)**: Telnet, MCCP2 compression, WebSockets, and session restoration.
- **[Performance & Batching](docs/agents/performance.md)**: Low-latency design, backend batching, and UI pruning.
- **[Testing & Quality](docs/agents/testing.md)**: TDD approach, repro-first rule, and test layers.
- **[MCP (Model Context Protocol)](docs/agents/mcp.md)**: Tools and integration for AI agents.

## Critical Principles
When working on this codebase, you **MUST** follow these rules:

1. **Consider Layering**: Always account for multiple active profiles. Rules, aliases, and variables are resolved across layers. Use `GetOrderedProfileIDs`.
2. **Admin/Game Sync**: New entities must be manageable via the Settings UI. Be mindful of the synchronization between in-memory state (game activity) and SQLite state (admin changes).
3. **Performance First**: Maintain low latency and high throughput. Avoid blocking the main game loop or bloating the browser DOM.
4. **Repro-First TDD**: Fix bugs by first writing a failing reproduction test case. Only then implement the fix.

## Development Workflow
- Use `make run` to build the UI and start the server in the foreground (not on a machine where the live service below is installed: both want the same listener and DB).
- The UI source of truth is in `ui/`. Compiled assets in `go/internal/web/static/` are generated and should not be edited directly.
- Prefer inspecting `mudhost.db` directly for runtime state.

## Live Service (systemd --user)
On the maintainer's machine the live server runs as the `rubymud` systemd **user** unit (`make service-install`, template in `scripts/systemd/rubymud.service.in`). It runs a deployed copy at `~/.local/lib/rubymud/mudhost` against `data/mudhost.db`, and logs go to the journal (`make service-logs`).
- **Shipping a new version** = `make deploy` (build, atomic copy to `DEPLOY_BIN`, `systemctl --user restart rubymud`). Deploy only a finished, accepted change, and say so: every restart drops and re-logs the player's game session.
- **Tests, E2E, headless checks never touch the service**: no `systemctl`, no `make deploy`, no writes to `data/mudhost.db`. `make build` and the E2E wrappers rebuild `bin/mudhost` freely; the service never runs that file. Throwaway instances use their own port and a DB copy (`docs/agents/testing.md`).
