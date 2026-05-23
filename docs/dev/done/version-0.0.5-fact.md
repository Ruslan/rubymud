# Version 0.0.5 — Settings Foundation (Done)

## Current State

- **Svelte settings app**: `ui/src/SettingsApp.svelte` — 2181 lines, Svelte 5.
- **REST JSON CRUD**: chi router in `go/internal/web/server.go` — endpoints for sessions, variables, aliases, triggers, highlights, profiles, groups, timers, hotkeys, subs.
- **WebSocket notifications**: `Session.NotifySettingsChanged(domain)` in `session.go:990` sends `{"type":"settings.changed","settings":{"domain":"..."}}`.
- **Embedded assets**: `//go:embed static/*` in `server.go:29`.
- **Build pipeline**: `make ui` (npm build), `make build` (ui + go build), `make run` (build + launch).
- **Storage**: `go/internal/storage/` — CRUD for variables, aliases, triggers, highlights, all entities; SQLite via GORM.
- **Migrations**: `go:embed migrations/*.sql`, auto-applied at startup.
