# Version 0.0.7.3 Plan

## Goal

Add a built-in tick timer system that feels familiar to JMC users:

1. a visible always-on ticker in the client chrome
2. simple VM commands to control it
3. reset/sync from triggers on known tick-marker lines
4. state that survives UI reconnect/restore for the current session

This milestone is about practical MUD play ergonomics, not a general-purpose scheduler.

---

## Product Problem

Real MUD play often depends on server tick timing:

1. food/drink timers
2. buff expiration prediction
3. periodic mob/world events
4. coordinated group actions timed to the next tick

Users explicitly asked for:

1. a ticker
2. reset by trigger
3. always visible

Today RubyMUD has triggers, local commands, variables, and named buffers, but no built-in ticking clock or persistent UI indicator.

---

## Scope

### 1. VM Commands

Add these commands:

```text
#ticksize {seconds}
#tickon
#tickoff
#tickset
#tickset {seconds}
```

Behavior:

1. `#ticksize {seconds}`
   sets the configured tick period for the current session runtime
2. `#tickon`
   starts countdown using the current tick size
3. `#tickoff`
   stops countdown and hides/marks it inactive in UI
4. `#tickset`
   resets countdown to the current tick size immediately
5. `#tickset {seconds}`
   sets tick size and resets countdown in one step

Recommended defaults:

1. default tick size: `60`
2. if `#tickon` runs before explicit size is set, use default `60`

### 2. Trigger-driven sync

The main intended use is:

```text
#action {^Вы хотите есть\.} {#tickset}
#action {^Начался дождь\.} {#tickset}
#action {^Ощущение праведности прошло\.} {#tickset}
```

Because trigger commands already pass through the VM pipeline, no special trigger-only implementation should be needed.

### 3. Always-visible UI ticker

Show ticker in the bottom toolbar meta area, next to connection status.

Current UI already has an always-visible area here:

1. `ui/src/index.html` -> `.bottom-toolbar__meta`
2. existing `#connection-status`

Add a dedicated ticker element there, for example:

1. inactive: `tick off`
2. active: `tick 42`
3. near-expiry styling when low, for example under 10 seconds

This should stay visible regardless of pane layout, active panel, or scroll position.

### 4. Session runtime state

Ticker is session runtime state, not profile rule state.

Store in Go session runtime something equivalent to:

```go
type TickState struct {
    Enabled      bool
    SizeSeconds  int
    NextTickAt   time.Time
}
```

Derived values such as remaining seconds should be computed from wall clock, not decremented by mutating state every second.

### 5. WebSocket/UI updates

Expose ticker state to the browser so the UI can render it continuously.

Recommended shape:

```json
{
  "type": "tick",
  "enabled": true,
  "size_seconds": 60,
  "remaining_seconds": 42
}
```

Two acceptable implementation directions:

1. server pushes periodic tick updates while enabled
2. server pushes canonical state (`enabled`, `size_seconds`, `next_tick_at`) and browser renders local countdown

Preferred direction:

1. server owns canonical timer state
2. browser renders the visual countdown locally from `next_tick_at`
3. server sends updates only when ticker is changed or when UI restores

This keeps UI smooth without requiring per-second broadcast spam.

### 6. Restore behavior

When a client reconnects/restores an existing session tab, ticker state should be included so the indicator appears immediately with the correct remaining time.

---

## Explicit Non-Goals

### Not in this milestone

1. generic multi-timer scheduler
2. named timers
3. cron-like commands
4. repeating arbitrary callbacks
5. JMC/TinTin full `#timer` compatibility
6. status bar scripting/layout language

`0.0.7.3` should solve the practical tick/ticker problem first.

---

## Why Not Full `#timer` Yet

There are two different problems:

1. game tick tracking
2. arbitrary delayed automation

The user request is specifically about tick tracking with visible countdown and trigger reset.

So this milestone should ship the narrow, high-value feature first:

1. `#ticksize`
2. `#tickon`
3. `#tickoff`
4. `#tickset`
5. always-visible ticker UI

General `#timer` can come later once there is a clearer model for delayed command execution and cancellation.

---

## Implementation Direction

### VM

Add local commands:

1. `cmdTicksize`
2. `cmdTickon`
3. `cmdTickoff`
4. `cmdTickset`

They should:

1. execute locally
2. send nothing to MUD
3. return local echo only when useful for diagnostics or usage errors

### Session/runtime

Session should own canonical tick state and expose a method to snapshot it for websocket restore.

Prefer `time.Time`-based math over background decrement counters.

### UI

Add one small ticker pill in bottom toolbar meta.

Minimal UI behavior:

1. hidden or dim when disabled
2. visible with remaining seconds when enabled
3. updates once per second client-side
4. resyncs cleanly when a new tick-state message arrives

### API / socket protocol

Add a websocket message for ticker state.

Send it on:

1. restore begin / restore payload
2. `#tickon`
3. `#tickoff`
4. `#ticksize`
5. `#tickset`

---

## Acceptance Criteria

1. `#tickon` starts a 60-second ticker if no custom size was set.
2. `#ticksize {45}` followed by `#tickon` starts a 45-second ticker.
3. `#tickset` resets the remaining time back to the configured tick size.
4. `#tickset {30}` changes size to 30 and resets immediately.
5. `#tickoff` disables the ticker and the always-visible UI indicator reflects that state.
6. `#action {^Вы хотите есть\.} {#tickset}` works and resets ticker locally from a trigger.
7. Ticker remains visible in the UI regardless of active pane/panel state.
8. Reconnected/restored clients see the current ticker state immediately.
9. No ticker command sends raw text to the MUD.
10. Invalid tick command usage returns a clear local diagnostic message.

---

## Suggested First Example

```text
#ticksize {60}
#tickon
#action {^Вы хотите есть\.} {#tickset}
#action {^Начался дождь\.} {#tickset}
#action {^Ощущение праведности прошло\.} {#tickset}
```

This gives the classic JMC-style workflow without requiring a full status bar subsystem.
