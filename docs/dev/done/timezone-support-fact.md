# Client-Side Timezone Support — Done

## Status

Done. Closes the `timezone-support.md` plan. Per-session timezone that is
explicit/visible in session settings, auto-follows the browser on connect, and
can be manually pinned. Drives `$TIME`/`$DATE` VM expansion and makes
exported/searched log timestamps calendar-day-unambiguous. Reviewed
(ship-with-nits); all follow-up nits addressed before commit.

## Implemented

### Data model
- Migration `011_session_timezone.sql`: `sessions` gains
  `timezone TEXT NOT NULL DEFAULT 'UTC'` (concrete IANA name) and
  `tz_follow INTEGER NOT NULL DEFAULT 1` (1 = follow browser on connect,
  0 = pinned). Mirrors the `009` ansi_theme pattern.
- `SessionRecord.Timezone` / `TZFollow` threaded through storage → API → UI.
- `NormalizeTimezone` / `LoadLocationOrUTC` helpers; `UpdateSessionTimezone`
  does a column-scoped update (no full-record `Save`).

### VM `$TIME` expansion
- VM holds `loc atomic.Pointer[time.Location]` (default UTC, race-safe).
  `SetLocation`/`Location` accessors. `builtinVarAt(now, key)` is the pure
  instant-based formatter; `$DATE/$TIME/$HOUR/$MINUTE/$SECOND` render in the
  session zone, `$TIMESTAMP` stays epoch. Package `builtinVar` (host-local)
  remains for `#if` expression evaluation.

### Follow / pin sync
- On WS connect the client sends its IANA zone
  (`Intl.DateTimeFormat().resolvedOptions().timeZone`) as a `tz` query param.
  `ApplyClientTimezone`: if `tz_follow=1` and the zone parses, persist it and
  push to the live VM; pinned/invalid/empty are ignored (no error).
- `manager.Connect` seeds the VM location from the stored zone on reload.
- `manager.UpdateSession` pushes the resolved location to a live VM immediately,
  so a Settings zone change/pin affects `$TIME` without reconnect
  (Admin/Game-Sync).

### Logs / export / search
- `downloadLogs`, `/search`, `/log/context`, and the MCP log tools accept a
  per-request `tz` param and render `created_at.In(loc)` with an explicit
  offset. Fallback chain: request `tz` → session zone → UTC.
- Stored `created_at` stays UTC `RFC3339Nano` — only presentation changes.

### Settings UI
- `SessionsSection.svelte`: "Follow browser timezone" checkbox (re-enable →
  `tz_follow=1`, adopts current browser zone) + timezone `<select>` (choosing a
  zone pins → `tz_follow=0`), populated from `Intl.supportedValuesOf`.
- UI log/search/context requests append the browser `tz`.

## Tests
- `go/internal/vm/expand_test.go` — deterministic instant-based formatting in a
  fixed non-host zone (day-rollover across `+05:30`); default-UTC and nil-reset
  wiring.
- `go/internal/session/timezone_test.go` — follow persists + pushes to VM;
  pinned unchanged; invalid/empty ignored; `UpdateSession` syncs a live VM.
- `go/internal/storage/session_store_test.go` — defaults, pin round-trip,
  normalization, `LoadLocationOrUTC`.
- `go/internal/web/timezone_test.go` — export carries an offset reflecting `tz`;
  invalid `tz` → `+0000`; `resolveRequestLocation` fallback chain.
- `ui/src/settings/api.test.ts` — download URL carries the browser timezone.

## Non-Goals (unchanged)
- Stored UTC `created_at` untouched; no historical-row migration.
- No per-line live-buffer timestamps.
- No multi-viewer-of-one-session handling (assumes one player per session).

## Verification
- `go: go vet ./... && go test ./...` — all packages `ok`.
- `ui: npm test` (119 pass) + `npm run build`.
- Docs: `docs/agents/vm.md` (time variables & timezone),
  `docs/agents/sqlite.md` (`timezone`/`tz_follow` columns).

## Known / out of scope
- Pre-existing `-race` flake in `TestSchedulerTickBoundaries` (unrelated
  test-harness `bytes.Buffer` sharing); not touched.
