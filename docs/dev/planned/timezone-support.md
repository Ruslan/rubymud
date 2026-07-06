# Client-Side Timezone Support

## Context

Gameplay time and log timestamps are effectively UTC, which is not useful during
play and makes exported logs ambiguous about which calendar day a record belongs
to.

Where time is produced today:

1. **VM time variables** — `go/internal/vm/expand.go:22-40`. `$DATE`, `$TIME`,
   `$HOUR`, `$MINUTE`, `$SECOND` are expanded server-side from a bare
   `time.Now()` (the server's *local* time; UTC in the typical Docker
   deployment). `$TIMESTAMP` is Unix epoch. `builtinVar` is a package function
   with no session context, but it is already called from the `*VM` method
   `substituteVars` (`expand.go:9-20`).
2. **Log storage** — `go/internal/storage/sqlite_time.go:17,49,85` stores every
   timestamp as **UTC** `RFC3339Nano` (written via `nowSQLiteTime()` in
   `log_store.go:36`). This is the canonical form and stays UTC.
3. **Live output** — the WebSocket `ClientLogEntry`
   (`go/internal/session/types.go:26-33`) carries no timestamp; the baked `$TIME`
   text is part of the line's text.
4. **Export / search** — `downloadLogs()` (`go/internal/web/server.go:870`)
   formats `"2006-01-02 15:04:05"` with **no offset**; `/search`
   (`server.go:761`) and `/log/context` (`server.go:812`) return UTC
   `created_at`. The MCP log tool uses the same offset-less format
   (`go/internal/web/mcp.go:510`).
5. **Settings logs viewer** — already renders via `toLocaleString()` /
   `toLocaleTimeString()` (browser zone) around
   `ui/src/SettingsApp.svelte:~1450`.
6. **Session settings pattern** — `ansi_theme` shows the end-to-end path to copy:
   migration `009_session_ansi_theme.sql` → `SessionRecord.AnsiTheme`
   (`go/internal/storage/types.go:11-20`) → `PUT /api/sessions/{id}` →
   `Session.ansi_theme` (`ui/src/settings/types.ts:121-134`) → the `<select>` in
   `ui/src/settings/SessionsSection.svelte:91-99`, saved via
   `saveInlineSessionEdit` → `api.saveSession` (`ui/src/settings/api.ts:108`).

## Design Summary

A per-session timezone that is **explicit and visible** in session settings, but
**auto-follows the client** on connect so it self-corrects after the player
relocates — unless the player has pinned it.

Assumption: **one player per session.** Not built for multiple simultaneous
viewers of one session in different zones.

Canonical `created_at` stays UTC everywhere; only *presentation* changes
(`$TIME` text, and formatting of exports/search results).

### Data model (migration)

Add two columns to `sessions` (new `0NN_session_timezone.sql`, ansi_theme
pattern):

- `timezone TEXT NOT NULL DEFAULT 'UTC'` — always a concrete IANA name (e.g.
  `Europe/Kyiv`). Because it is always concrete and persisted, it is a clean
  source for `$TIME` even with no client attached or right after a restart.
- `tz_follow INTEGER NOT NULL DEFAULT 1` — 1 = follow the browser on each
  connect; 0 = pinned to a manually chosen zone.

`SessionRecord` (`go/internal/storage/types.go:11-20`) gains `Timezone string`
and `TZFollow int`, threaded through `UpdateSession`/`updateSession` like
`AnsiTheme`.

### Follow / pin behavior

- On WebSocket connect, the client sends its IANA zone
  (`Intl.DateTimeFormat().resolvedOptions().timeZone`) as a `tz` query param
  next to `session_id` (`ui/src/main.ts:200`). Connect/attach path:
  `go/internal/session/clients.go:12` (`AttachClient`) plus the WS upgrade
  handler that reads the param.
- If `tz_follow = 1` and the client zone parses (`time.LoadLocation`), write it
  into `timezone` (persist) and push the resolved `*time.Location` to the VM.
- If `tz_follow = 0` (pinned), connects do **not** overwrite `timezone`.
- In Settings, choosing a specific zone from the selector sets `tz_follow = 0`
  (pins it). A **"Follow browser timezone"** checkbox re-enables follow; when
  re-enabled it may immediately adopt the current browser zone.

### `$TIME` expansion

- The session holds the resolved `loc *time.Location` (from `timezone`, default
  `time.UTC`) and pushes it to the VM (e.g. `vm.SetLocation(loc)`).
- `builtinVar` becomes a `*VM` method (or takes `loc`) and uses
  `time.Now().In(v.loc)` for `DATE/TIME/HOUR/MINUTE/SECOND`. `$TIMESTAMP` stays
  epoch.
- **`created_at` stays UTC** — only the `$TIME` text baked into a line is local.
- **Backward compatibility**: old lines keep their previously-baked
  (server-zone) time text. Not reformatted. Accepted.

### Logs / export / search formatting

For historical timestamps rendered from the UTC `created_at`, the requesting
client passes its IANA zone on the request (`tz` param):

- `downloadLogs()`, `/search`, `/log/context` (and the MCP log tool,
  `go/internal/web/mcp.go:510`) format `created_at.In(loc)` with an explicit
  offset (e.g. `2006-01-02 15:04:05 -0700`, or RFC3339) so the calendar day is
  unambiguous.
- Missing/invalid `tz` falls back to the session's stored `timezone`, then `UTC`.

Per-request `tz` (rather than always using the session zone) keeps the Settings
page correct: it is not a game-client attach and may export a session with no
live client.

## Rejected / Superseded Alternatives

1. **Pure ephemeral (no persistence)** — capture TZ only in session runtime with
   no column. Simpler, but has no value when no client is attached (triggers
   firing on MUD output) or right after a restart, and gives the player nothing
   visible to control. Persisting one concrete zone removes those gaps.
2. **Overlay spans for baked `$TIME`** — mark the time substring in
   `LogOverlay.StartOffset/EndOffset` (`go/internal/storage/types.go:63`) +
   payload and reformat on the client. Fits the overlay model, but correctly
   anchoring the offset in the final display text after `#sub`/highlight, and
   matching the byte-vs-rune offset convention with Cyrillic, is genuinely
   fiddly. Server-side expansion in a known zone makes the problem disappear. The
   only thing lost — re-rendering an old line's time in a different viewer's zone
   — is unnecessary for a single-player client.

## Non-Goals

1. Changing stored UTC `created_at` or migrating historical rows.
2. Per-line timestamps in the live gameplay buffer (separate feature).
3. Correct display for multiple simultaneous viewers of one session in different
   zones.
4. Custom DST rules beyond Go `time.Location` / browser `Intl`.

## Acceptance Criteria

1. A new session defaults to `timezone = 'UTC'`, `tz_follow = 1`; behavior is
   unchanged until a client connects or the zone is edited.
2. With `tz_follow = 1`, connecting from `Europe/Kyiv` persists that zone, and
   `$TIME`/`$DATE` in emitted lines render in it — verified by a Go test that
   sets the VM location to a fixed `*time.Location` (not the host zone).
3. With `tz_follow = 0` (pinned), connecting from a different zone does **not**
   change the session `timezone`.
4. With no client attached, `$TIME` uses the stored `timezone` (falling back to
   UTC only if unset) — no error.
5. Exported logs and `/search` results contain an explicit UTC offset (or are
   local-day-correct); a record's calendar day is unambiguous. Missing/invalid
   `tz` falls back to the session zone, then UTC.
6. Stored SQLite `created_at` remains UTC `RFC3339Nano`.
