# Version 0.0.8.5 — Timer Declaration vs Runtime, Dedupe, Export (Done)

## Current State

- **Profile timer declarations**: `profile_timers` + `profile_timer_subscriptions` tables (`types.go:173-197`). CRUD in `storage/profile_timer.go`.
- **Duplicate dedupe**: `session.go:490-500` (`SubscribeTimer`) skips identical command already at same second. `resolveTimerSubscriptions()` (line 283) deduplicates across profile merges. Tested in `declaration_test.go:87-100`.
- **Timer export/import** in `.tt` format: `profile_script.go:213-276` (export `#tickicon`/`#ticksize`/`#tickmode`/`#tickat`/`#untickat`), `:281-704` (import parsing), `:706-840` (import persistence).
- **Layering**: `resolveTimerSubscriptions()` (`session.go:246-300`) merges subscriptions across all profiles. `loadProfileTimerDeclaration()` (`session.go:302-382`) merges scalar fields (icon, cycle_ms, repeat_mode) with later-profile-wins.
- **Tests**: 6 test files across storage (`profile_timer_test.go`, `profile_timer_import_test.go`, etc.) and session (`declaration_test.go` with `TestLayeredTimerDeclarations0086`).
