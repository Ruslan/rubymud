# Version 0.0.8.4 Plan

## Goal

Add manual drift-correction and final polish so the timer system feels better than classic JMC in day-to-day play.

---

## User Value

After `0.0.8.4`, a player can fine-tune a timer without fully resetting it when the server and client drift slightly out of sync.

Example workflow:

```text
#tickset {-2}
#tickset {herb} {+1}
```

---

## Scope

### Commands

Add delta sync variants:

```text
#tickset {+seconds}
#tickset {-seconds}
#tickset {name} {+seconds}
#tickset {name} {-seconds}
```

Behavior:

1. delta adjusts current remaining time without changing configured cycle length
2. negative delta never drives remaining time below `0`
3. positive delta never exceeds the configured cycle length; it clamps instead of wrapping
4. delta sync is intended for manual correction, not as a replacement for trigger-based reset

### Polish

1. finalize low-time styling for primary and secondary timers
2. tighten diagnostics and edge-case messages
3. verify restore/resync behavior after delta adjustments

---

## Acceptance Criteria

1. `#tickset {-2}` moves the default ticker 2 seconds closer to the next boundary without changing its cycle length.
2. `#tickset {herb} {+1}` increases remaining time on named timer `herb` by 1 second without changing its cycle length.
3. `#tickset {+10}` at 55 seconds remaining on a 60-second cycle clamps to 60 remaining instead of wrapping.
4. delta-adjusted timers continue to restore correctly after reconnect.
5. invalid delta usage returns a clear local diagnostic.
