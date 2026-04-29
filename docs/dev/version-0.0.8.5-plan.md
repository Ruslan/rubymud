# Version 0.0.8.5 Plan

## Goal

Make timer configuration safer, more portable, and less entangled with live session state by:

1. preventing accidental exact duplicate subscriptions
2. splitting timer declaration from timer runtime state
3. adding profile export/import support for timer declarations
4. making multi-profile timer declarations behave like ordered override layers

---

## Scope

### Declaration vs Runtime Split

`0.0.8.5` should formalize two different layers:

1. **timer declaration** — profile-scoped configuration that describes what a timer means
2. **timer runtime** — session-scoped live state for a timer that is currently being used

For this release, declaration should contain:

1. `name`
2. `icon`
3. `default_cycle`
4. whether the timer is `repeating` or `one-shot`
5. `#tickat` subscriptions

Declaration should NOT contain:

1. enabled / paused state
2. current remaining time
3. start time / phase offset / any other live timing state
4. one-shot `#delay` data

Runtime should continue to own:

1. active cycle instance derived from declaration and runtime commands
2. on/off or paused state
3. current phase / remaining time model
4. manual drift-correction state after `#tickset {+seconds}` / `#tickset {-seconds}`

Design intent:

1. a player should be able to define subscriptions and icon for a timer before the timer has ever been started
2. starting a named timer should be able to require only its name when declaration already defines its default cycle
3. one-shot `#delay` remains a separate concept and is not part of this split
4. the default shared `ticker` may continue to behave like the traditional world-tick timer, while named timers behave more like declared timer objects

### Exact duplicate dedupe for `#tickat`

If a subscription already exists with the exact same tuple:

1. timer name
2. second
3. command

then a repeated `#tickat` should be a no-op instead of appending another identical subscription.

Examples:

```text
#tickat {vozl} {0} {#tickoff {vozl}}
#tickat {vozl} {0} {#tickoff {vozl}}   -> no-op
```

This should also make repeated `#ticker {name} {seconds} {command}` calls safe from silently accumulating identical `second 0` handlers.

For declaration behavior, `#tickat` should be able to target a timer that has no active runtime instance yet.
The declaration must therefore be keyed by timer name inside the owning profile, not by an existing live session timer row.

### Profile-Scoped Timer Declarations

Add new profile-scoped timer declaration storage.

Recommended shape:

1. `profile_timers`
2. `profile_timer_subscriptions`

`profile_timers` should minimally contain:

1. `profile_id`
2. `name`
3. `icon`
4. `cycle_ms`
5. `repeat_mode`

Primary key:

1. `(profile_id, name)`

`profile_timer_subscriptions` should minimally contain:

1. `profile_id`
2. `timer_name`
3. `second`
4. `sort_order`
5. `command`

Foreign key:

1. `(profile_id, timer_name)` -> `profile_timers(profile_id, name)`

This is intentionally profile-scoped, not session-scoped.

### Export/Import Timer Declarations

Add timer declarations to profile portability, but keep runtime state out of it.

What should be exported/imported:

1. `#ticksize {name} {seconds}` for declared named timer default cycle
2. `#tickat {name} {second} {command}`
3. `#tickicon {name} {icon}`
4. default timer forms where applicable (`ticker`)

What should NOT be exported/imported:

1. current remaining time
2. `next_tick_at`
3. paused runtime phase
4. enabled/on-off runtime state
5. one-shot `#delay` state

Design rule:

1. profile export/import should carry timer declaration only
2. runtime timer phase belongs to session persistence, not profile portability
3. importing a profile must not unexpectedly auto-start long-running timers unless the imported script explicitly contains that behavior as commands/triggers
4. exported declaration should remain useful even before the timer is ever started in a session

Likely implementation direction:

1. extend profile script export so timer declaration is rendered as explicit timer commands in the exported script
2. extend profile import/parser so timer declaration commands become first-class imported timer configuration, not merely ad-hoc runtime execution during import
3. preserve subscription order in export/import
4. keep the timer runtime tables for live session state separate from new profile declaration tables

### Runtime Command Behavior

Runtime commands that target a named timer should be allowed to auto-create its declaration if it does not already exist.

For `0.0.8.5`, this should apply to any named timer command, not only config-like commands.

When a declaration is auto-created by runtime command usage, it should be written into the current session's primary profile.

Examples:

```text
#tickat {herb} {0} {stand}      -> creates declaration if missing
#tickicon {herb} {🪴}           -> creates declaration if missing
#ticksize {herb} {58}           -> may create declaration and set its default cycle
#tickon {herb}                  -> may create declaration if missing, then start runtime using declared cycle
```

This preserves the natural user expectation that timer setup should not fail just because declaration and first use happen in one step.

For named timers, `#tickon {name}` should use the declared cycle when one exists.
This is especially important for timers such as long buffs where the player wants to declare a `15m` timer once and later just start it on demand.

Named timer declarations should also support a one-shot mode.
This allows a visible non-repeating timer object, distinct from `#delay`, for cases such as long buffs or cooldowns where the player wants visible UI state but no automatic repeat after expiry.

### Multi-Profile Merge Semantics

Timer declarations from multiple active profiles should be applied in profile order, as override layers.

Model:

1. profiles behave like sequential program code
2. later profile declarations may redefine earlier scalar timer properties
3. timer subscription commands are applied in profile order to produce the final declaration for the session

Scalar-field rule:

1. later declaration wins for fields such as `icon`, `cycle_ms`, and `repeat_mode`

Subscription rule:

1. subscriptions are merged by sequential application, not winner-takes-all replacement
2. later profiles may add subscriptions
3. later profiles may explicitly remove earlier subscriptions

This requires more precise unsubscribe behavior than the current bulk-clear `#untickat {name} {second}` model.

For `0.0.8.5`, declaration/import logic should support an exact-removal form:

```text
#untickat {name} {second} {command}
```

Example layering:

Profile `default`:

```text
#tickat {tick} {3} {sit}
```

Profile `agro`:

```text
#untickat {tick} {3} {sit}
#tickat {tick} {3} {kill all}
```

This gives RubyMUD an override model close to code redefinition for scalar timer fields, while still allowing targeted mutation of subscriptions.

### Relationship to JMC / Tortilla / TinTin++

This split intentionally keeps the practical strengths of the three migration targets:

1. JMC-style default ticker workflows still work through the default `ticker`
2. Tortilla-style named timers still start naturally via `#ticksize {name} {seconds}; #tickon {name}`
3. TinTin++-style simple independent ticker workflows still map naturally to `#ticker {name} {seconds} {command}`

At the same time, RubyMUD should treat icon + default cycle + repeat mode + subscriptions as declaration, and live phase as runtime.

---

## Open Questions

### Default `ticker` vs Named Timers

Named timers now clearly benefit from declared `cycle_ms` and `repeat_mode`.
The remaining product question is how far the same declaration model should be pushed onto the default shared `ticker`.

The likely direction for now:

1. named timers use the new declaration model fully
2. default `ticker` remains compatible with the existing world-tick sync workflow, including trigger-driven `#tickset`
3. user-facing docs should explain this asymmetry directly instead of hiding it

---

## Non-Goals

1. do not remove support for multiple different commands on the same second
2. do not add separate subscription ids or names in this release
3. do not remove or redefine existing `#untickat {name} {second}` bulk-clear semantics in this release
4. do not add admin/UI CRUD for timers in this release
5. do not export/import actual runtime timer phase
6. do not move one-shot `#delay` into the repeating timer declaration/runtime model
7. do not require a new explicit timer-declaration command before normal timer commands can work

---

## Acceptance Criteria

1. repeated `#tickat` with the same `name + second + command` does not create duplicates
2. different commands on the same `name + second` still coexist
3. repeated `#ticker` calls do not accumulate identical `second 0` handlers
4. a timer declaration can exist before any live runtime instance of that timer has been started
5. named timer commands can target a not-yet-started timer without failing declaration lookup
6. named timer declaration can include default cycle and one-shot vs repeating mode
7. `#tickon {name}` can start a named timer from declaration alone when its cycle is already declared
8. profile export includes timer declaration (`#ticksize`, `#tickat`, `#tickicon`) in a stable, re-importable form
9. profile import restores exported timer declaration without restoring runtime phase or one-shot delays
10. multi-profile timer declaration layering uses later-profile override for scalar fields and sequential merge for subscriptions
11. exact-removal `#untickat {name} {second} {command}` works for declaration/import layering cases
12. timer declarations are stored profile-scoped, while live timer runtime remains session-scoped
13. import/export preserves command order for multiple subscriptions on the same timer second
14. importing a profile does not auto-restore current remaining time or unexpectedly resume old timer phase
15. user-facing docs mention exact-duplicate dedupe behavior, declaration/runtime boundaries, and multi-profile override semantics
