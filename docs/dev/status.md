# Development Status

## Purpose

This file is the normalized handoff/status snapshot for active work.

Update rules:

1. update this file incrementally
2. do not re-copy old compaction/history dumps if the same information is already recorded here
3. keep only current state, material decisions, open questions, and immediate next steps
4. when a topic is resolved, move it into the relevant version plan or release note and trim it here

---

## Current Focus

1. finish the timer line after `0.0.8.2` and `0.0.8.3`
2. keep timer API/documentation consistent for users coming from JMC, Tortilla, and TinTin++
3. define a strong `0.0.8.4` scope around delta sync and timer persistence

---

## Recently Completed

### `0.0.8.2`

1. shipped `#tickat`, `#untickat`, `#delay`, `#undelay`
2. fixed tick boundary math and scheduler goroutine leak
3. restored `#ts` as alias for `#tts`
4. added real-time scheduler tests and verified `1s` cycle behavior
5. implemented empty-Enter send to MUD from input

### `0.0.8.3`

1. shipped named timers for `#ticksize`, `#tickon`, `#tickoff`, `#tickset`, `#tickat`, `#untickat`
2. shipped `#tickicon`
3. added multi-timer UI with primary ticker and secondary timer pills
4. fixed review findings:
   - auto-create missing named timers for subscriptions/icon updates
   - default cycle fallback `60` for unknown/uninitialized timers
   - correct `#tickicon {name} {}` parsing
   - filter disabled secondary timers from snapshots/broadcasts
5. updated user docs in `docs/engine-commands.md`

### Docs and Migration

1. added migration section in `docs/engine-commands.md` for JMC / Tortilla / TinTin++
2. corrected JMC mapping: JMC `#ticksize` maps to RubyMUD `#tickset {seconds}` when reset semantics are intended
3. documented `#delay` named-id overwrite behavior as deliberate debounce-friendly behavior
4. added `#ticker` docs and clarified TinTin++ delete-vs-pause differences
5. added `docs/dev/feature-request-import-jmc-tortilla.md`
6. added `docs/dev/feature-request-mcp.md`

### UX polish already landed

1. secondary timer pills now omit timer name when icon is set; tooltip still shows the timer name
2. UTF-8 / emoji timer icons are already supported in UI

---

## Current Decisions

1. `#tickoff` remains pause/resume semantics; do not redefine it as full timer deletion
2. if a delete-style command is ever needed, it should be a separate command, not a behavior change for `#tickoff`
3. `#ticker` is the correct shorthand for simple independent repeating timers
4. timer docs are part of definition of done for this feature line
5. compatibility notes should document intentional differences instead of hiding them
6. timer persistence should prefer correct UX over strict competitor parity

---

## `0.0.8.4` Direction

Planned in `docs/dev/version-0.0.8.4-plan.md`:

1. delta sync forms for `#tickset {+seconds}` / `#tickset {-seconds}` and named equivalents
2. timer persistence in SQLite
3. MM:SS formatting for timer values >= 60 seconds
4. documentation for delta sync and persistence semantics

Important persistence design choices currently agreed:

1. persist timer configuration and current phase
2. keep timer metadata in `timers`
3. keep subscriptions in separate `timer_subscriptions` table, not JSON blobs
4. store paused `remaining_ms` explicitly for disabled timers
5. write on state changes only, not on every scheduler tick
6. do not replay missed timer actions after downtime in `0.0.8.4`

---

## Open Questions

1. whether `0.0.8.4` is still the right release bucket for both delta sync and persistence, or whether persistence should move to `0.0.8.5`
2. whether one-shot delays should also become persistent, or whether `0.0.8.4` should cover repeating timers only
3. whether we eventually want a full timer-delete command in addition to pause/subscription removal
4. final confirmation from `competitor_explorer` on automatic timer persistence semantics in JMC / Tortilla / TinTin++

---

## In Flight Tasks

1. `576a1b89062c02355eec6f6e32de6363` — coder: format timer values >60s as `M:SS` in UI
2. `7f67232da65e9a84042b28baa0a5ea8e` — competitor follow-up: clarify restart persistence evidence for JMC / Tortilla / TinTin++

---

## Relevant Files

1. `docs/engine-commands.md`
2. `docs/dev/version-0.0.8.3-plan.md`
3. `docs/dev/version-0.0.8.4-plan.md`
4. `docs/dev/feature-request-import-jmc-tortilla.md`
5. `go/internal/vm/commands_timer.go`
6. `go/internal/session/session.go`
7. `go/internal/session/timer.go`
8. `ui/src/render.ts`

---

## Short Handoff

The timer feature line is in good shape after `0.0.8.3`: named timers, icons, UI pills, migration docs, and `#ticker` are in place. The next meaningful step is not another small syntax tweak, but making timers resilient across restart and adding manual drift correction without losing the current API clarity.
