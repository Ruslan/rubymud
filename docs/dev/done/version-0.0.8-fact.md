# Version 0.0.8 — Timer System (Done)

All subversions 0.0.8.1 through 0.0.8.6 implemented. This umbrella roadmap is now historical.

## Current State

- **Commands**: `#ticksize`, `#tickon`, `#tickoff`, `#tickset` (with `+N`/`-N` delta), `#tickat`, `#untickat`, `#delay`, `#undelay`, `#tickicon`, `#tickmode`, `#ticker` — all in `commands_timer.go`, dispatched from `commands.go:152-173`.
- **Session runtime**: `session.go:47-50` owns `timers map[string]*Timer` and `delays map[string]*delayTask`. `timer.go:9-258` has full state machine (On/Off/Reset/Set/Size/Adjust/Check/CheckSubscriptions).
- **Scheduler**: `runTimerLoop()` at `session.go:127` ticks at 100ms, checks subscriptions + timer bounds. `runCommandDispatcher()` paces commands at 50ms spacing. Guardrails: 100ms min delay, 50 max pending.
- **UI ticker pill**: `index.html:44-46` — `#ticker` shows `tick off` or `{icon} tick {remaining}`. `#secondary-timers` renders named timer pills. `render.ts:700-738` handles low-time styling (<5s).
- **Named timers**: All commands accept optional `{name}`, default `"ticker"`. `isValidTimerName()` rejects numeric/delta-like names.
- **Persistence**: `timers`/`timer_subscriptions` (session-level, `storage/timer.go`) + `profile_timers`/`profile_timer_subscriptions` (profile-level declarations, `storage/profile_timer.go`). API routes at `server.go:209-214`.
- **Timer declarations UI**: `SettingsApp.svelte` "Timers" tab at line 1431 with full CRUD.
- **Tests**: `scheduler_real_test.go`, `scheduler_test.go`, `ticker_correctness_test.go`, `persistence_test.go`, `declaration_test.go`.
