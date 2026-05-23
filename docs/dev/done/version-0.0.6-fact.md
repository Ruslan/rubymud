# Version 0.0.6 — Session Management (Done)

## Current State

- **Auto-bootstrap**: `main.go:24-28` creates `data/` dir; `gorm.Open` auto-creates SQLite DB.
- **SessionManager**: `go/internal/session/manager.go` — `Manager` struct with `ListSessions`, `GetSession`, `Connect`, `Disconnect`, `CreateSession`, `UpdateSession`, `DeleteSession`.
- **CRUD endpoints**: `GET/POST /api/sessions`, `PUT/DELETE /api/sessions/{id}`, `POST connect/disconnect`.
- **Decoupled server**: `Server` holds `*session.Manager`, not a single session. WebSocket attaches by `?session_id=` param.
- **API token**: random 16-byte hex, stored in `app_settings` table, checked via `X-Session-Token` header. Rotatable via `POST /api/app/settings/rotate-api-token`.
- **Connect/reconnect from UI**: `ui/src/main.ts` — exponential backoff reconnect (1s–10s), click-to-reconnect.
- **Editable sessions page**: `SettingsApp.svelte:1656-1754` — name/host/port/initial commands/MCCP fields, connect/disconnect/edit/delete buttons.
- **Migrations**: 8 `.sql` files in `go/internal/storage/migrations/`, auto-applied.
