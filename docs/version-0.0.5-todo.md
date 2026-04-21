# Version 0.0.5 Todo

## Goal

Prepare the project for a real browser settings UI without breaking the current single-binary local product shape.

`0.0.5` is the milestone where we add CRUD-oriented backend APIs and a separate settings frontend, while keeping the game screen stable.

## Product Direction

We keep the current delivery model:

1. User downloads one Go executable.
2. User runs it locally.
3. Go serves browser UI over localhost.
4. Frontend assets are embedded into the Go binary.

There is no requirement for Node.js on the end-user machine.

Node/Vite/Svelte are build-time tools only.

## Frontend Direction

### Game Zone

Keep the current game UI in native JavaScript for now.

Reason:

1. It already works.
2. It is connected to the live WebSocket runtime.
3. Migrating terminal behavior and building settings UI at the same time would increase risk.

Possible future direction:

1. Keep game UI native permanently, or
2. Wrap parts of it later as a Svelte component

This is intentionally deferred.

### Settings Zone

Build the settings/admin area in `Svelte`.

This zone is for non-game CRUD screens:

1. Variables
2. Aliases
3. Triggers
4. Highlights
5. Sessions
6. Later: import/export, profiles, plugin settings, etc.

Reason:

1. These screens are form-heavy and list-heavy.
2. They benefit from component structure.
3. Svelte is a good fit with low boilerplate.

## Backend Direction

### API Style

Use `REST JSON CRUD` for settings data.

Examples:

1. `GET /api/variables`
2. `POST /api/variables`
3. `PUT /api/variables/:id` or key-based update variant
4. `DELETE /api/variables/:id`
5. Same pattern for aliases, triggers, highlights, sessions

The exact shape can be finalized during implementation, but the principle is fixed:

1. CRUD goes through HTTP JSON API
2. not through the game WebSocket protocol

### WebSocket Role

Keep WebSocket for:

1. live game output
2. live game input side effects
3. invalidation/update notifications for settings

Do not use WebSocket as the primary CRUD transport.

## Sync Model

### Source Of Truth

The database is the source of truth for runtime state.

This is already the intended direction after runtime compaction work:

1. aliases live in SQLite
2. variables live in SQLite
3. triggers live in SQLite
4. highlights live in SQLite

The runtime must tolerate restart at any moment.

### Settings Refresh Strategy

When settings change, the settings UI should not trust stale local state forever.

Preferred initial strategy:

1. backend changes DB
2. backend sends WebSocket notification that a domain changed
3. frontend receives notification
4. frontend re-fetches that domain via REST

Do not build complicated optimistic real-time merging yet.

### Notification Model

Do not make this variables-only.

Use domain-level notifications, for example:

1. `settings.changed` + `{ "domain": "variables" }`
2. `settings.changed` + `{ "domain": "aliases" }`
3. `settings.changed` + `{ "domain": "triggers" }`
4. `settings.changed` + `{ "domain": "highlights" }`
5. `settings.changed` + `{ "domain": "sessions" }`

This covers:

1. changes made in settings UI
2. changes made in game UI via `#var`, `#alias`, `#action`, etc.
3. changes caused indirectly by future trigger logic

## Game UI Data Loading Rules

If game-side panels later show aliases/triggers/highlights/variables, load them on demand.

Pattern:

1. user opens panel
2. frontend fetches current domain from REST API
3. panel shows latest state

Do not preload every settings domain into the game UI permanently unless there is a clear need.

## Build Direction

### Tooling

Use:

1. `Vite`
2. `Svelte`
3. embedded static assets in Go binary

### Packaging

Build flow:

1. frontend is compiled by Vite
2. output goes into embedded asset directory
3. Go binary is built with those assets via `//go:embed`

The final release remains a single local executable.

### Dev Workflow

We are intentionally **not** spending time on hot reload / dev server setup right now.

Reason:

1. full rebuilds are acceptable for now
2. the build may take up to ~20 seconds
3. current priority is product architecture, not frontend DX

Document this clearly in code comments or build docs.

## Suggested Make Targets

Expected direction:

1. `make ui-build`
2. `make run`
3. `make build`

`make run` and `make build` should ensure frontend assets are built before embedding.

## Planned Scope For 0.0.5

### Backend

1. Add REST JSON endpoints for variables
2. Add REST JSON endpoints for aliases
3. Add REST JSON endpoints for triggers
4. Add REST JSON endpoints for highlights
5. Add REST JSON endpoints for sessions
6. Add WebSocket notifications for settings invalidation

### Frontend

1. Add Svelte settings app
2. Add settings pages for variables
3. Add settings pages for aliases
4. Add settings pages for triggers
5. Add settings pages for highlights
6. Add settings pages for sessions
7. Keep game page native for now

### Integration

1. Embed built frontend assets into Go binary
2. Wire build pipeline into Makefile
3. Keep one-exe local-first UX intact

## Explicit Non-Goals For 0.0.5

1. Rewriting the game terminal in Svelte
2. Building hot-module reload tooling
3. Building advanced real-time conflict resolution in settings UI
4. Reworking the whole WebSocket protocol into a CRUD transport

## Follow-Up Notes

If settings become editable from both:

1. game runtime commands, and
2. settings UI pages

then stale-page protection is mandatory.

The first acceptable implementation is:

1. WebSocket domain-changed notification
2. re-fetch on receipt

That is enough for now.

## Revised Scope Snapshot

This file originally treated `sessions` as full CRUD scope for `0.0.5`.

After implementation, the honest `0.0.5` cutoff is narrower:

1. Settings foundation is in scope.
2. Real session lifecycle management is out of scope.

### What Counts As Done For 0.0.5

1. Vite + Svelte settings frontend exists and is embedded into the Go binary.
2. Game UI remains native and working.
3. REST JSON CRUD works for:
   - variables
   - aliases
   - triggers
   - highlights
4. Settings pages exist for those four editable domains.
5. Sessions page exists as a read-only status page.
6. Settings invalidation uses `settings.changed` notifications and REST re-fetch.
7. `make run`, `make build`, and `make test` work with the current frontend/embed flow.

### Explicitly Deferred From 0.0.5 To 0.0.6

1. Creating sessions from the settings UI.
2. Editing session host/port/name from the settings UI.
3. Real connect/disconnect/reconnect lifecycle from the browser.
4. Session manager/runtime orchestration for multiple persisted sessions.
5. First-run product flow where a downloaded `.exe` can bootstrap its own local DB and let the user create the first session entirely from the UI.

### Closure Checklist For 0.0.5

1. Sessions page must not expose fake connect/disconnect controls.
2. Settings routes must reliably open the Svelte settings page.
3. Variables delete must handle arbitrary keys safely.
4. Embedded assets in `go/internal/web/static/` must be rebuilt from `ui/`.
5. Manual smoke test should confirm game page + settings page + CRUD for the four editable settings domains.
