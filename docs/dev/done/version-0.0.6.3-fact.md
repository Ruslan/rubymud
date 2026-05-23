# Version 0.0.6.3 — Variables Management (Done)

## Current State

- **`ProfileVariable`** struct in `types.go:136` — profileID, position, name, defaultValue, description. Unique on (profile_id, name).
- **`profile_variable.go`**: CRUD functions + `LoadProfileVariablesForProfiles` + `ListResolvedVariablesForSession` (full merge: session values override declared defaults).
- **Endpoints**: `GET/POST /api/profiles/{id}/variables`, `PUT/DELETE /{id}`, `GET /api/sessions/{id}/variables/resolved`.
- **Game client**: `main.ts:314` fetches `/resolved`. `render.ts:903` renders bottom panel with input fields, placeholder for defaults, × clear button. Inline edit via `POST/DELETE` session variables.
- **Settings UI**: "Declared Vars" tab in `SettingsApp.svelte:187` with full CRUD table.
- **Gap**: VM's `$var` expansion does NOT fall back to profile defaults (session vars only). Only the UI/resolved API does full resolution.
- **No "Fill Missing From Defaults" button** yet; defaults shown as placeholders only.
