# Version 0.0.8.1 Plan

## Goal

Ship one practical, always-visible, session-shared default ticker that users can start, stop, size, reset, and re-sync from triggers.

This is the smallest release that already solves a real MUD play problem end-to-end.

---

## User Value

After `0.0.8.1`, a player can:

1. see the current tick countdown all the time
2. reset it from known tick-marker triggers such as `Начался день.`
3. keep it alive across UI reconnect/restore within the same session

Example workflow:

```text
#ticksize {60}
#tickon
#action {^Начался день\.} {#tickset}
```

---

## Scope

### Commands

Implement only the default timer `ticker` commands:

```text
#ticksize {seconds}
#tickon
#tickoff
#tickset
#tickset {seconds}
```

Behavior:

1. timer is session-scoped and shared by all attached clients of the same session
2. default cycle length is `60`
3. `#tickon` starts the ticker using the current cycle length
4. `#tickoff` stops the ticker
5. `#tickset` resets countdown to the current cycle length
6. `#tickset {seconds}` sets cycle length and resets immediately
7. ticker is cyclic when `cycle_seconds > 0`
8. when it reaches `0`, it returns to the full cycle length
9. ticker never goes negative
10. if cycle length is `0`, ticker becomes inactive/stopped

### UI

Add one ticker pill in the bottom toolbar meta area next to connection status.

Minimal behavior:

1. visible with remaining seconds when active
2. dimmed or `tick off` when inactive
3. updates client-side from canonical server state
4. remains visible regardless of active pane/panel state

### Runtime / protocol

1. session owns canonical ticker state
2. restore payload includes current ticker state
3. browser renders countdown from canonical `next_tick_at`
4. websocket updates are sent only on state changes and restore

---

## Architecture Reserved For Later

Even though `0.0.8.1` exposes only one ticker in product/API terms, implementation should leave room for later expansion:

1. runtime state should be representable as a timer map keyed by name, even if only `ticker` is used initially
2. websocket payload should allow future extension to multiple timers without breaking shape badly
3. command parsing should stay isolated so named variants can be added later without rewriting the pipeline

Not yet in scope:

1. named timers
2. `#tickat` / `#untickat`
3. `#delay` / `#undelay`
4. delta sync `#tickset {+N}` / `#tickset {-N}`
5. secondary timer pills
6. burst protection queue

---

## Acceptance Criteria

1. `#tickon` starts a 60-second ticker if no custom size was set.
2. `#ticksize {45}` followed by `#tickon` starts a 45-second ticker.
3. `#tickset` resets the countdown back to the configured cycle length.
4. `#tickset {30}` changes cycle length to 30 and resets immediately.
5. `#tickoff` disables the ticker and the UI reflects that state.
6. `#action {^Начался день\.} {#tickset}` works and resets ticker locally from a trigger.
7. reconnecting/restoring the page shows the current ticker state immediately.
8. no tick command sends raw text to the MUD.
9. invalid usage returns a clear local diagnostic.
