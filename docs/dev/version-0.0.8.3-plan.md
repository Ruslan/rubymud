# Version 0.0.8.3 Plan

## Goal

Generalize the ticker system into named timers while preserving the simple default `ticker` workflow.

This is the release where the architecture reserve becomes real product capability.

Timing representation rules remain unchanged:

1. runtime uses `time.Duration`
2. API and storage durations use integer milliseconds
3. named timers and icon metadata should reuse the same millisecond-based representation

---

## User Value

After `0.0.8.3`, a player can track multiple independent countdowns in one session:

1. the main world tick
2. a herb/cooldown timer
3. another combat or utility timer

Example workflow:

```text
#ticksize {60}
#tickon
#ticksize {herb} {30}
#tickicon {herb} {🪴}
#tickon {herb}
#tickat {herb} {0} {use herb}
```

---

## Scope

### Commands

Add named variants:

```text
#ticksize {name} {seconds}
#tickon {name}
#tickoff {name}
#tickset {name}
#tickset {name} {seconds}
#tickicon {name} {icon}
#tickat {name} {second} {command}
#untickat {name} {second}
```

Behavior:

1. forms without `name` still target default `ticker`
2. named timers are session-scoped runtime objects
3. named subscriptions are scoped to their timer
4. timer names must not be numeric and must not start with `+` or `-`
5. `#tickicon {name} {icon}` sets optional pill metadata for that timer
6. `icon` should be a short UTF-8 glyph, typically one emoji
7. delay ids remain separate from timer namespaces

### UI

Add compact secondary timer pills in the status area.

Behavior:

1. default `ticker` remains the primary visible timer
2. additional active timers appear as smaller pills in the same status area
3. pill labels should prefer `icon + name + remaining`, for example `🪴 herb 12`
4. if `icon` is missing, fall back to `name + remaining`
5. status area should remain stable and not overlap output or jump awkwardly on resize

Suggested defaults:

1. default ticker may use `🕒`
2. combat/cooldown timers may use `⚔️`
3. herb/resource timers may use `🪴`
4. food/hunger timers may use `🍗`

### Runtime / protocol

1. restore payload includes all visible timer state
2. websocket timer messages support multiple timers with millisecond duration fields
3. websocket timer state includes optional `icon` metadata per timer
4. all attached clients observe the same named timer state

---

## Not Yet In Scope

1. delta sync on `#tickset`
2. advanced timer grouping/filtering UI
3. full JMC-compatible generic `#timer`

---

## Acceptance Criteria

1. `#tickon {herb}` creates or starts a named timer `herb`.
2. `#tickset {herb}` resets that timer without affecting default `ticker`.
3. `#ticksize {herb} {30}` followed by `#tickon {herb}` starts a 30-second cycle.
4. `#tickicon {herb} {🪴}` stores icon metadata and the timer pill renders it.
5. `#tickat {herb} {0} {use herb}` fires on the `herb` timer boundary only.
6. when multiple timers are active, secondary timers are visible in a compact non-overlapping status-area stack.
7. reconnecting/restoring the page shows all active timer state immediately, including icon metadata.
