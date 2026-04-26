# Version 0.0.8.4 Plan

## Goal

1. Add manual drift-correction so the timer system feels better than classic JMC in day-to-day play.
2. Add timer persistence in SQLite so timer configuration and current phase survive server restart — removing the worst part of current UX.

Timing representation rules remain unchanged:

1. delta adjustments operate on millisecond-based runtime values
2. scheduler precision target in early `0.0.8.x` remains within 100ms
3. tighter precision can be considered later only if there is a concrete feature request
4. if decimal-second command syntax is enabled by this phase or later, values should be rounded to the nearest millisecond during parsing

---

## User Value

After `0.0.8.4`:

1. a player can fine-tune a timer without fully resetting it when the server and client drift slightly out of sync
2. after server restart, timer configuration and current phase are restored — no manual re-entry of `#tickat`/`#tickicon`/`#ticksize`

External baseline from competitor research:

1. JMC persists only `tick_size`; tick on/off state and phase are lost on restart
2. Tortilla persists timer definitions, but active state and current phase are lost on restart
3. TinTin++ restores ticker definitions only via explicit save; runtime phase is lost; one-shot delays remain volatile
4. therefore RubyMUD `0.0.8.4` should intentionally exceed competitor UX by restoring repeating timer phase, while keeping one-shot delays volatile

Example delta workflow:

```text
#tickset {-2}
#tickset {herb} {+1}
```

---

## Scope

### 1. Delta Sync Commands

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

### 2. Timer Persistence in SQLite

Persistence must cover both:

1. timer configuration: cycle, icon, subscriptions
2. runtime phase: whether the timer is running, and how much time remained when it was last persisted

**What to store** — full timer configuration + runtime state:

| Field | Notes |
|-------|-------|
| `name` | timer id, `"ticker"` for default |
| `cycle_ms` | cycle length |
| `next_tick_at` | absolute wall-clock time, UTC, used when timer is enabled |
| `remaining_ms` | paused or last-known remaining time, used when timer is disabled |
| `enabled` | true/false |
| `icon` | emoji string, empty = no icon |
| subscriptions | stored in separate `timer_subscriptions` table |

**Write strategy** (avoid per-second DB writes):

1. Write to DB **only on state change**: `TickSet`, `TickSize`, delta `TickSet`, `TickOn`, `TickOff`, `TickIcon`, `SubscribeTimer`, `UnsubscribeTimer`
2. One final flush at server graceful shutdown
3. NO writes during per-second tick broadcasts or ordinary scheduler countdown updates
4. State-change writes should be coalesced only if we already have a safe batching primitive; otherwise prefer correctness over premature write optimization

**Restore strategy**:

1. On session init, load all timers from DB
2. If `enabled=true` and `next_tick_at` is present: compute `elapsed = now - next_tick_at`, then restore phase by modular arithmetic against `cycle_ms`; set fresh runtime `next_tick_at = now + remaining`
3. If `enabled=false`: restore paused timer with persisted `remaining_ms`; it must stay paused until explicit `#tickon`
4. If stored data is partially missing or invalid, restore the timer conservatively with `cycle_ms` clamped to a valid value and `enabled=false`, plus a local diagnostic log entry
5. Missing timers get default 60s cycle, no subscriptions

**Runtime model note**:

1. enabled timers can be reconstructed from `next_tick_at` + wall clock without per-second writes
2. disabled timers cannot be reconstructed from `next_tick_at` alone, so paused `remaining_ms` must be stored explicitly
3. persistence is for timer state, not for replaying already missed timer actions during downtime

**DB schema** — new table:

```sql
CREATE TABLE IF NOT EXISTS timers (
    session_id INTEGER NOT NULL,
    name       TEXT NOT NULL,
    cycle_ms   INTEGER NOT NULL DEFAULT 60000,
    next_tick_at TEXT,      -- ISO8601, NULL when disabled or uninitialized
    remaining_ms INTEGER NOT NULL DEFAULT 0,
    enabled    INTEGER NOT NULL DEFAULT 0,
    icon       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, name)
);
```

```sql
CREATE TABLE IF NOT EXISTS timer_subscriptions (
    session_id INTEGER NOT NULL,
    timer_name TEXT NOT NULL,
    second     INTEGER NOT NULL,
    sort_order INTEGER NOT NULL,
    command    TEXT NOT NULL,
    PRIMARY KEY (session_id, timer_name, second, sort_order),
    FOREIGN KEY (session_id, timer_name)
        REFERENCES timers(session_id, name)
        ON DELETE CASCADE
);
```

**Why a separate table**:

1. `#tickat` and `#untickat` mutate subscriptions incrementally, so row-based storage matches runtime behavior better than JSON blobs
2. preserving command order on the same `second` is explicit via `sort_order`
3. future features such as deleting one specific subscription, inspect/export tooling, or SQL debugging stay straightforward

**Persistence operations**:

1. `#tickat` inserts one row into `timer_subscriptions`
2. `#untickat {name} {second}` deletes all rows for that `(session_id, timer_name, second)`
3. timer restore loads `timers` first, then attaches ordered subscriptions from `timer_subscriptions`

**Non-goals for `0.0.8.4`**:

1. no replay of missed `#tickat` commands that would have fired while the server was down
2. no cross-session/shared timer templates beyond the current session model
3. no attempt to make timer persistence editable directly as a `.tt` syntax block in this release
4. no persistence for one-shot `#delay` / `#undelay` in this release; delays remain volatile by design

### 3. UI Polish

1. MM:SS formatting for remaining time >= 60 seconds (`125` → `2:05`)
2. finalize low-time styling for primary and secondary timers
3. tighten diagnostics and edge-case messages
4. verify restore/resync behavior after delta adjustments AND after server restart
5. keep the current mental model intact: primary ticker remains special in placement, but persistence semantics must be identical for primary and named timers

### Documentation

1. user-facing command documentation must be updated together with the feature release
2. documentation must cover delta `#tickset` forms, limits, and examples for manual drift correction
3. documentation must cover timer persistence semantics: what survives restart, what resets, and what does not replay after downtime
4. documentation must explicitly state that one-shot delays do not survive restart
5. release is not complete until docs reflect the shipped command syntax and behavior

---

## Acceptance Criteria

### Delta sync
1. `#tickset {-2}` moves the default ticker 2 seconds closer to the next boundary without changing its cycle length.
2. `#tickset {herb} {+1}` increases remaining time on named timer `herb` by 1 second without changing its cycle length.
3. `#tickset {+10}` at 55 seconds remaining on a 60-second cycle clamps to 60 remaining instead of wrapping.
4. delta-adjusted timers continue to restore correctly after reconnect.
5. invalid delta usage returns a clear local diagnostic.
6. delta-adjusted timers remain corrected after server restart; the manual correction is not lost.

### Persistence
7. After `#ticksize {herb} {58}; #tickat {herb} {0} {stand}; #tickicon {herb} {🪴}; #tickon {herb}`, server restart restores timer `herb` with correct cycle, icon, subscription, and running phase.
8. After server restart with an enabled timer, remaining time is computed from `next_tick_at` and wall clock — no stale countdown.
9. Disabled timers (`#tickoff`) are restored in disabled state with their paused remaining time preserved and do not resume ticking on restart.
10. Per-second tick broadcasts do NOT trigger DB writes.
11. State-change writes (set/size/delta/on/off/icon/sub/unsub) are durable — confirmed in DB after each command.
12. Corrupt or partial persisted timer rows do not crash session startup; they degrade to a safe disabled timer or are skipped with a diagnostic.
13. One-shot `#delay` entries are not restored after restart.

### Documentation
14. User-facing command docs are updated for all shipped `0.0.8.4` delta-sync and persistence behavior.

### UI
15. Remaining time >= 60s is displayed in `M:SS` format in both primary ticker and secondary pills.
