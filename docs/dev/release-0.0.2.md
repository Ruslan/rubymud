# Release 0.0.2

## Summary

`0.0.2` is the first serious playable Go-based session host milestone.

The focus of this version was:

1. transport correctness
2. browser session UX
3. SQLite foundation
4. restore/history/hotkeys quality-of-life

This version is still pre-scripting for aliases and triggers, but it already proves the new architecture well enough to play and iterate inside the Go runtime.

## What Exists In 0.0.2

### Runtime

1. one Go binary session host
2. direct TCP connection to the MUD
3. browser UI served by Go over localhost
4. shared live session across multiple browser clients

### Transport

1. direct MUD TCP connection from Go
2. packet handling based on `IAC GA` behavior copied from the Ruby client
3. normalization of invalid UTF-8 bytes into printable form
4. ANSI color rendering in the browser

### Storage

1. new SQLite database: `data/mudhost.db`
2. schema created by SQL migration
3. `default` session auto-created/updated from CLI flags
4. incoming output persisted as `log_entries`
5. command history persisted as `history_entries`
6. command hints persisted as `log_overlays`

### Browser UI

1. dark theme with readable ANSI black
2. restore of recent logs on reconnect
3. restore of command history on reconnect
4. inline `-> command` hints rendered on the last line
5. command history search by prefix on `ArrowUp` / `ArrowDown`
6. command history merged with localStorage
7. clickable hotkey panel
8. collapsible hotkey panel at the bottom
9. connection status indicator

### Core UX Polish

1. autoscroll that respects when the user is reading old output
2. DOM line retention to avoid infinite browser growth
3. chunked restore protocol
4. hotkeys working in Russian keyboard layout via `event.code`

## Major Documents Added Before Or During 0.0.2

1. `docs/go-migration-plan.md`
2. `docs/next-session-handoff.md`
3. `docs/sqlite-schema.md`
4. `docs/vm-design.md`

These docs capture the architecture direction so compaction or future sessions do not lose the plan.

## Commands Available

Run the Go client:

```bash
make run MUD=rmud.org:4000
```

Initialize or update the Go SQLite schema:

```bash
make db-init
```

Inspect the schema:

```bash
make db-schema
```

## Important Files In 0.0.2

### Go Runtime

1. `go/cmd/mudhost/main.go`
2. `go/internal/session/session.go`
3. `go/internal/storage/storage.go`
4. `go/internal/web/server.go`
5. `go/internal/web/static/index.html`
6. `go/internal/config/config.go`

### Database

1. `go/sqlite/migrations/001_init.sql`
2. `data/mudhost.db`

### Build Entry Points

1. `Makefile`

## Architectural Decisions Confirmed In 0.0.2

1. Go is the correct runtime for the shared session host.
2. Browser UI remains the primary client interface.
3. SQLite remains the local storage engine.
4. New Go schema should be separate from the old Ruby schema.
5. Structured logs are first-class data, not plain text files.
6. Built-in client power matters, not only plugin power.

## What Is Still Missing

### Not Yet Implemented

1. aliases
2. triggers
3. variables in live client UX
4. buttons from trigger rules
5. multi-window routing
6. plugin protocol
7. Ruby compatibility plugin
8. JS plugin SDK

### Known Scope Boundary

This release intentionally does not solve the scripting layer yet.

The current focus is the playable core.

## Immediate Next Priority After 0.0.2

Next work should focus on the built-in scripting layer for playability:

1. aliases
2. variables
3. simple deterministic triggers

This should likely be implemented as a built-in Go rule engine, not as arbitrary embedded Ruby.

Reference document:

1. `docs/vm-design.md`

## Why 0.0.2 Matters

This version proves that the project can move away from the Ruby runtime without losing the product's strongest ideas:

1. one shared session
2. browser-based control
3. history and restore
4. practical MUD UX

It is the first version that feels like the new client is real, not just a migration experiment.
