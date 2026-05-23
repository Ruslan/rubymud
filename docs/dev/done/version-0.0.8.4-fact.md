# Version 0.0.8.4 — Timer Persistence & Delta Sync (Done)

See 0.0.8 umbrella fact for full details.

## Current State

- **Delta sync**: `#tickset {+N}`/`{-N}` — parsed in `commands_timer.go:82-106`. `Timer.Adjust()` clamps to `[0, cycle]`.
- **Session persistence**: `timers` + `timer_subscriptions` tables, CRUD in `storage/timer.go`. `persistTimer()`/`persistSubscriptions()` in `session.go:666-730`.
- **Profile declarations**: `profile_timers` + `profile_timer_subscriptions` tables, CRUD in `storage/profile_timer.go`.
- **API routes**: 5 endpoints at `server.go:209-214`.
