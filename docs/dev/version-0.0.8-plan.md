# Version 0.0.8 Plan

## Goal

Define the full `0.0.8` timer roadmap and split it into small deliverable subversions, each with concrete user-visible value.

The final destination remains the same:

1. a session-shared ticker/timer system
2. visible UI state
3. trigger-driven sync
4. per-second automation
5. optional multi-timer support
6. later drift-correction polish

This document is now the umbrella roadmap. Detailed scope lives in:

1. `docs/dev/version-0.0.8.1-plan.md`
2. `docs/dev/version-0.0.8.2-plan.md`
3. `docs/dev/version-0.0.8.3-plan.md`
4. `docs/dev/version-0.0.8.4-plan.md`

---

## Delivery Strategy

### 0.0.8.1

Deliver one default session-shared ticker with visible UI, restore support, and trigger-driven reset.

Why first:

1. smallest end-to-end slice with immediate player value
2. validates runtime state, websocket restore, and client rendering
3. avoids premature complexity from named timers and automation

### 0.0.8.2

Add `#tickat`, `#delay`, `#undelay`, and scheduler safety for automation around the default ticker.

Why second:

1. builds directly on the already-visible ticker
2. introduces central scheduler once there is real automation value to justify it
3. keeps automation bugs isolated before multi-timer complexity arrives

### 0.0.8.3

Expose named timers and compact secondary timer pills.

Why third:

1. product value is clear only after single-ticker workflow already works
2. uses the timer-map architecture reserved in earlier releases
3. avoids mixing first-time timer UX with multi-timer UI complexity

### 0.0.8.4

Add delta sync and final ergonomics polish.

Why last:

1. useful but not required to unlock core timer workflow
2. easier to implement after reset semantics and named timers are stable
3. best treated as polish on top of a working system

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

Today RubyMUD has triggers, local commands, variables, and named buffers, but no built-in cyclic session timer system or persistent UI indicator.

---

## Cross-Version Architectural Rules

These should be respected from `0.0.8.1`, even when the product surface is still intentionally smaller:

1. session remains the canonical owner of timer runtime state
2. reconnect/restore must remain first-class, not bolted on later
3. implementation should allow future move from one default ticker to a named timer map without rewriting core storage shape
4. delay ids and timer names must stay in separate namespaces
5. websocket payloads should evolve in a forward-compatible direction toward multi-timer state
6. command parsing should remain explicit and deterministic; no ambiguous guessing based on loose heuristics

---

## Final 0.0.8 Target Scope

### 1. VM Commands

Add these commands:

```text
#ticksize {seconds}
#ticksize {name} {seconds}
#tickon
#tickon {name}
#tickoff
#tickoff {name}
#tickset
#tickset {seconds}
#tickset {+seconds}
#tickset {-seconds}
#tickset {name}
#tickset {name} {seconds}
#tickset {name} {+seconds}
#tickset {name} {-seconds}
#tickat {second} {command}
#tickat {name} {second} {command}
#untickat {second}
#untickat {name} {second}
#delay {seconds} {command}
#delay {id} {seconds} {command}
#undelay {id}
```

Argument model:

1. timer-oriented commands use positional arguments with `name` first when present
2. forms without `name` target the default timer `ticker`
3. `seconds` means an absolute cycle value such as `60`
4. `delta` means a signed adjustment such as `+2` or `-2`
5. timer names are plain runtime identifiers and must not be parsed as numbers or signed deltas

Parsing rules:

1. `#ticksize`
   - `#ticksize {seconds}`
   - `#ticksize {name} {seconds}`
2. `#tickon`
   - `#tickon`
   - `#tickon {name}`
3. `#tickoff`
   - `#tickoff`
   - `#tickoff {name}`
4. `#tickset`
   - `#tickset` -> reset default timer `ticker` to its current cycle
   - `#tickset {value}`
   - `#tickset {name}`
   - `#tickset {name} {value}`
5. for `#tickset`, `value` is interpreted as:
   - absolute cycle seconds if it matches `^\d+$`
   - delta if it matches `^[+-]\d+$`
6. `#tickset {value}` therefore means:
   - reset default timer cycle to `{value}` and restart if `{value}` is absolute seconds
   - adjust default timer by `{value}` if `{value}` is a signed delta
7. `#tickset {name}` means reset that named timer to its current cycle
8. `#tickset {name} {value}` means:
   - set named timer cycle and reset if `{value}` is absolute seconds
   - delta-adjust named timer if `{value}` is a signed delta
9. `#tickat`
   - `#tickat {second} {command}`
   - `#tickat {name} {second} {command}`
10. `#untickat`
   - `#untickat {second}`
   - `#untickat {name} {second}`
11. `#delay`
   - `#delay {seconds} {command}`
   - `#delay {id} {seconds} {command}`
12. `#undelay`
   - `#undelay {id}`

Disambiguation examples:

```text
#tickset            -> reset default timer `ticker`
#tickset {30}       -> set default timer cycle to 30 and reset
#tickset {-2}       -> adjust default timer by -2 seconds
#tickset {herb}     -> reset named timer `herb`
#tickset {herb} {45} -> set timer `herb` cycle to 45 and reset
#tickset {herb} {-2} -> adjust timer `herb` by -2 seconds
```

Reserved-name guidance:

1. timer names should not be purely numeric
2. timer names should not begin with `+` or `-`
3. if a user tries to create or target a timer name that collides with numeric/delta syntax, return a clear diagnostic instead of guessing

Behavior:

1. timers are session-scoped runtime objects shared by all attached clients of the same session
2. the default timer name is `ticker`
3. forms without `{name}` target `ticker`
4. `#ticksize {seconds}` / `#ticksize {name} {seconds}`
   sets the configured cycle length for the timer
5. `#tickon` / `#tickon {name}`
   starts countdown using the current timer cycle
6. `#tickoff` / `#tickoff {name}`
   stops countdown and hides/marks it inactive in UI for the default ticker
7. `#tickset` / `#tickset {name}`
   resets countdown to the current timer cycle immediately
8. `#tickset {seconds}` / `#tickset {name} {seconds}`
   sets cycle length and resets countdown in one step
9. `#tickset {+seconds}` / `#tickset {-seconds}` and named variants
   adjust the current countdown by a small delta without changing the configured cycle length
10. `#tickat {second} {command}` / `#tickat {name} {second} {command}`
   subscribes a command to fire once per cycle when remaining time reaches that exact second
11. `#untickat {second}` / `#untickat {name} {second}`
   removes subscriptions for that exact timer-second slot
12. `#delay {seconds} {command}`
   schedules a one-shot delayed command in the current session runtime
13. `#delay {id} {seconds} {command}`
   schedules a one-shot delayed command with a cancelable runtime identifier
14. `#undelay {id}`
   cancels a pending delayed command by id

Recommended defaults:

1. default timer name: `ticker`
2. default cycle length: `60`
3. if `#tickon` runs before explicit size is set, use default `60`
4. timers are cyclic when `cycle_seconds > 0`
5. when a timer reaches `0`, it fires any `0`-second subscriptions, then resets to the full cycle length
6. timers never go negative
7. if `cycle_seconds == 0`, the timer becomes inactive and stops ticking until reconfigured
8. subscriptions are re-armed on every new cycle
9. delayed commands are one-shot and do not repeat automatically
10. delta sync should clamp within the valid countdown range and never produce a negative remaining time
11. positive delta sync that would exceed the configured cycle length should clamp to the cycle length rather than wrap

Validation:

1. timer names are case-sensitive runtime identifiers
2. `seconds` for `#ticksize`, `#tickset`, and `#delay` must be integer seconds
3. negative values are invalid for absolute duration commands; `+N`/`-N` deltas are valid only for `#tickset`
4. subscription `second` must be an integer in the inclusive range `0..cycle_seconds`
5. `0` is allowed only for timer cycle commands and means stop/inactive
6. delay ids and timer names are case-sensitive runtime identifiers
7. delay ids and timer names live in separate runtime namespaces
8. very small or recursive delays must not be able to cause unbounded immediate rescheduling or queue explosion
9. invalid usage returns a clear local diagnostic and sends nothing to MUD
10. timer names that are purely numeric or look like signed deltas are invalid and must be rejected explicitly

### 2. Trigger-driven sync

The main intended use is:

```text
#action {^Вы хотите есть\.} {#tickset}
#action {^Начался дождь\.} {#tickset}
#action {^Ощущение праведности прошло\.} {#tickset}
```

Because trigger commands already pass through the VM pipeline, no special trigger-only implementation should be needed.

Practical subscription examples:

```text
#ticksize {60}
#tickon
#tickat {3} {stand;wear shield}
#tickat {0} {bash target}
#tickat {0} {#delay {ready} {2} {report ready}}
```

Semantics:

1. subscriptions are evaluated through the normal VM command pipeline
2. they may execute local `#` commands, outgoing MUD commands, aliases, or mixed command lines
3. they should fire once per cycle for the matched second, not repeatedly while the UI redraws the same remaining value
4. `#tickset` re-syncs the cycle boundary and re-arms subscriptions for the next countdown
5. delta sync via `#tickset {+N}` / `#tickset {-N}` is intended for small manual correction when the player observes drift relative to the server tick markers

### 3. Always-visible UI ticker

Show the default ticker (`ticker`) in the bottom toolbar meta area, next to connection status.

Current UI already has an always-visible area here:

1. `ui/src/index.html` -> `.bottom-toolbar__meta`
2. existing `#connection-status`

Add a dedicated ticker element there, for example:

1. inactive: `tick off`
2. active: `tick 42`
3. near-expiry styling when low, for example under 5 seconds

This should stay visible regardless of pane layout, active panel, or scroll position.

Secondary active timers should be visible as a compact stack/list of small pills growing from the same status area when more than one timer is active.

UI requirements:

1. the default `ticker` remains the primary visible timer
2. secondary timers may be visually smaller than `ticker`
3. the status area should remain stable and not overlap output or jump awkwardly on resize
4. low-time styling should be noticeable but not noisy; color change is required, pulse is optional

### 4. Session runtime state

Timers and delayed actions are session runtime state, not profile rule state.

Store in Go session runtime something equivalent to:

```go
type TickState struct {
    Name          string
    Enabled       bool
    CycleSeconds  int
    NextTickAt    time.Time
    Subscriptions map[int][]string
}

type DelayState struct {
    ID        string
    ExecuteAt time.Time
    Command   string
}
```

Derived values such as remaining seconds should be computed from wall clock, not decremented by mutating state every second.

Session runtime should own:

1. a map of named timers
2. a queue/list of pending delayed commands
3. logic to fire timer subscriptions exactly once per cycle-second boundary
4. logic to execute delayed commands once when due
5. cancellation by delay id
6. guardrails against pathological self-rescheduling delay loops

### 5. WebSocket/UI updates

Expose timer state to the browser so the UI can render the primary and secondary timers continuously.

Recommended shape:

```json
{
  "type": "tick",
  "timers": [
    {
      "name": "ticker",
      "enabled": true,
      "cycle_seconds": 60,
      "next_tick_at": "2026-04-25T12:34:56Z"
    }
  ]
}
```

Two acceptable implementation directions:

1. server pushes periodic tick updates while enabled
2. server pushes canonical state (`name`, `enabled`, `cycle_seconds`, `next_tick_at`) and browser renders local countdown

Preferred direction:

1. server owns canonical timer state
2. browser renders the visual countdown locally from `next_tick_at`
3. server sends updates only when ticker is changed or when UI restores

This keeps UI smooth without requiring per-second broadcast spam.

### 6. Restore behavior

When a client reconnects/restores an existing session tab, timer state should be included so the indicator appears immediately with the correct remaining time.

Because timers are session-scoped, all attached clients should observe the same timer state and the same trigger-driven resets.

---

## Explicit Non-Goals

### Not in this milestone

1. cron-like commands
2. persistent jobs that survive server restart
3. arbitrary host-language callbacks
4. JMC/TinTin full `#timer` compatibility
5. arbitrary custom status bar scripting/layout language
6. rich timer grouping/filtering UI

`0.0.8` should solve the practical timer/ticker problem first.

---

## Why Not Full `#timer` Yet

There are still two different problems:

1. game tick tracking
2. fully general delayed automation

The user request is specifically about tick tracking with visible countdown, trigger reset, per-second actions, and small delayed follow-ups.

So this milestone should ship the narrow, high-value subset first:

1. `#ticksize`
2. `#tickon`
3. `#tickoff`
4. `#tickset`
5. `#tickat`
6. `#untickat`
7. `#delay`
8. `#undelay`
9. compact multi-timer UI
10. always-visible default ticker UI

Full `#timer` parity can come later once there is a clearer model for richer cancellation, persistence, and status-bar customization.

---

## Implementation Direction

### VM

Add local commands:

1. `cmdTicksize`
2. `cmdTickon`
3. `cmdTickoff`
4. `cmdTickset`
5. `cmdTickat`
6. `cmdUntickat`
7. `cmdDelay`
8. `cmdUndelay`

They should:

1. execute locally
2. send nothing to MUD
3. return local echo only when useful for diagnostics or usage errors

### Session/runtime

Session should own canonical tick state and expose a method to snapshot it for websocket restore.

Prefer `time.Time`-based math over background decrement counters.

Timer execution semantics:

1. use one central server-side ticker service/loop keyed off wall clock to detect due delayed commands and timer second boundaries
2. fire subscriptions on exact remaining-second transitions
3. when remaining time reaches `0`, execute `0` subscriptions, then either reset to the next cycle or stop if cycle is `0`
4. prevent duplicate fires within the same cycle-second slot
5. do not spawn one `time.Sleep` goroutine per timer; the central loop should poll at a modest cadence such as 100-200ms
6. when many commands become due at once, dispatch them through a short ordered queue with tiny spacing/jitter to avoid burst-spam to the MUD server
7. enforce sane delay scheduling guardrails, for example a minimum effective delay and/or bounded due-command queue growth, so recursive `#delay` patterns cannot explode the scheduler

### UI

Add one primary ticker pill and optional secondary timer pills in bottom toolbar meta.

Minimal UI behavior:

1. hidden or dim when no timers are active
2. visible with remaining seconds when enabled
3. updates once per second client-side
4. resyncs cleanly when a new tick-state message arrives
5. renders the default timer `ticker` plus compact pills for additional active timers
6. applies low-time styling under the configured threshold

### API / socket protocol

Add a websocket message for timer state.

Send it on:

1. restore begin / restore payload
2. `#tickon`
3. `#tickoff`
4. `#ticksize`
5. `#tickset`
6. timer creation-by-command of a new named timer
7. any named timer state change that affects the visible timer snapshot
8. `#delay` / `#undelay` do not need their own UI message unless they affect visible timer state

---

## Acceptance Criteria

1. `#tickon` starts a 60-second `ticker` timer if no custom size was set.
2. `#ticksize {45}` followed by `#tickon` starts a 45-second `ticker` cycle.
3. `#tickset` resets the remaining time back to the configured cycle length.
4. `#tickset {30}` changes cycle length to 30 and resets immediately.
5. `#tickset {-2}` moves the current countdown 2 seconds closer to the next timer boundary without changing cycle length.
6. `#tickoff` disables the default ticker and the always-visible UI indicator reflects that state.
7. `#tickon {herb}` creates or starts a named session timer `herb` without affecting the existence of `ticker`.
8. timers are shared by all attached clients of the same session.
9. when a timer reaches `0`, it fires `0`-second subscriptions and then restarts from its cycle length; it never goes negative.
10. if a timer cycle length is set to `0`, that timer stops ticking.
11. `#tickat {3} {stand}` fires once per cycle when 3 seconds remain.
12. `#tickat {0} {#delay {ready} {2} {report ready}}` works and schedules a one-shot follow-up command 2 seconds later.
13. `#undelay {ready}` cancels that delayed command before it fires.
14. `#action {^Вы хотите есть\.} {#tickset}` works and resets the default ticker locally from a trigger.
15. the default ticker remains visible in the UI regardless of active pane/panel state.
16. when multiple timers are active, secondary timers are also visible in a compact non-overlapping status-area stack.
17. reconnected/restored clients see the current timer state immediately.
18. no tick/delay command sends raw text to the MUD unless the scheduled command itself is an outgoing MUD command.
19. simultaneous due commands are paced through a burst-protection queue rather than emitted in one same-millisecond spike.
20. `#undelay {herb}` only targets delay id `herb` and cannot affect a timer named `herb`.
21. `#tickset {+10}` at 55 seconds remaining on a 60-second cycle clamps to 60 remaining instead of wrapping.
22. recursive/self-rescheduling `#delay` usage does not blow the stack and is constrained by scheduler guardrails.
23. invalid tick/delay command usage returns a clear local diagnostic message.

---

## Suggested First Example

```text
#ticksize {60}
#tickon
#action {^Вы хотите есть\.} {#tickset}
#action {^Начался дождь\.} {#tickset}
#action {^Ощущение праведности прошло\.} {#tickset}
#tickat {3} {stand;wear shield}
#tickat {0} {bash target}
#tickat {0} {#delay {ready} {2} {report ready}}
```

This gives the classic JMC-style workflow with one visible default ticker, optional named timers, and practical automation hooks without requiring a full status bar subsystem.
