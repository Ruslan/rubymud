# Version 0.0.8.3 — Named Timers (Done)

See 0.0.8 umbrella fact for full details.

## Current State

- **Named timer commands**: All `#tick*` commands accept optional `{name}`, default `"ticker"`. `isValidTimerName()` rejects numeric/delta-like names.
- **UI pills**: Secondary timers in `#secondary-timers` container (`index.html:45`), rendered as `.timer-pill` spans in `render.ts:721-738`.
- **Repeat modes**: `repeat` (default) and `one_shot` via `#tickmode`.
- **Session state**: `timers map[string]*Timer` in `session.go:47`.
