# Version 0.0.6 Plan

## Goal

Make the product usable from a downloaded `.exe` without manual setup.

Target user flow:

1. Download the executable.
2. Run it.
3. The app creates its local data directory and SQLite DB if needed.
4. Open settings.
5. Create a MUD session with host/port/name.
6. Connect from the browser client.
7. Play.

## Product Focus

`0.0.6` is the real session-management milestone.

`0.0.5` prepared the settings/admin foundation.

`0.0.6` turns sessions from a placeholder status page into a real product entrypoint.

## Scope

### First-Run Bootstrap

1. Create local data directory automatically on first run.
2. Create SQLite DB automatically if it does not exist.
3. Apply schema/migrations automatically at startup.
4. Start cleanly without requiring the user to run helper commands first.
5. Generate a persisted per-installation API key for external REST/WebSocket access.

### Sessions Backend

1. Add persisted session CRUD in the backend.
2. Add runtime `SessionManager` responsible for active connections.
3. Decouple `web.Server` from a single hard-wired `*session.Session`.
4. Support listing persisted sessions plus current runtime status.
5. Support real connect/disconnect/reconnect operations.

### Sessions Frontend

1. Sessions page becomes editable, not read-only.
2. User can create a session with at least:
   - name
   - host
   - port
3. User can edit saved session configuration.
4. User can connect/disconnect/reconnect from the UI.
5. UI clearly shows when no session is currently connected.

### API Access

1. Keep the embedded browser UI working without manual auth setup.
2. Add a persisted per-installation API key stored locally, not hardcoded in the binary.
3. Allow external REST clients to authenticate with that API key.
4. Allow external WebSocket clients to authenticate with that API key.
5. Expose the current API key in settings UI with an obvious copy path.
6. Allow rotating the API key from the settings UI.

### Game UI Integration

1. Game page must tolerate startup with no active MUD connection.
2. Connection status must reflect runtime state correctly.
3. If a session connects later, the game page should attach cleanly.

## Acceptance Criteria

1. Fresh machine / empty data directory startup works.
2. No pre-created SQLite file is required.
3. User can create a session from the settings UI.
4. User can connect to that session without editing CLI flags or files.
5. Disconnect and reconnect are real operations, not UI stubs.
6. Existing restore/log/history behavior still works after reconnect.
7. User can retrieve and use the local API key for direct REST access when needed.

## Non-Goals

1. Rewriting the game UI in Svelte.
2. Profiles vs sessions split.
3. Cloud sync or hosted control plane.
4. Multi-profile onboarding polish beyond what is needed for the first usable local product flow.

## Implementation Notes

1. Keep SQLite as the source of truth.
2. Keep the final release as a single executable serving embedded frontend assets.
3. Prefer a minimal `SessionManager` rather than scattering lifecycle logic across web handlers.
4. Do not fake connect/reconnect behavior in the UI before the runtime actually supports it.
5. The API key should be installation-specific and persisted locally, not shared across all binaries.
