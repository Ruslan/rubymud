# Version 0.0.8.5 Plan

## Goal

Make timer configuration safer and more portable by:

1. preventing accidental exact duplicate subscriptions
2. adding profile export/import support for timer settings

---

## Scope

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

### Export/Import timer settings

Add timer settings to profile portability, but keep runtime state out of it.

What should be exported/imported:

1. `#ticksize {name} {seconds}`
2. `#tickat {name} {second} {command}`
3. `#tickicon {name} {icon}`
4. default timer forms where applicable (`ticker`)

What should NOT be exported/imported:

1. current remaining time
2. `next_tick_at`
3. paused runtime phase
4. one-shot `#delay` state
5. active on/off runtime state as an implicit auto-start side effect

Design rule:

1. profile export/import should carry timer configuration only
2. runtime timer phase belongs to session persistence, not profile portability
3. importing a profile must not unexpectedly auto-start long-running timers unless the imported script explicitly contains that behavior as commands/triggers

Likely implementation direction:

1. extend profile script export so timer configuration is rendered as explicit timer commands in the exported script
2. extend profile import/parser so these timer commands become first-class imported timer configuration, not merely ad-hoc runtime execution during import
3. preserve subscription order in export/import

---

## Non-Goals

1. do not remove support for multiple different commands on the same second
2. do not add remove-one-subscription syntax in this release
3. do not change `#untickat` bulk-clear semantics in this release
4. do not add admin/UI CRUD for timers in this release
5. do not export/import actual runtime timer phase

---

## Acceptance Criteria

1. repeated `#tickat` with the same `name + second + command` does not create duplicates
2. different commands on the same `name + second` still coexist
3. repeated `#ticker` calls do not accumulate identical `second 0` handlers
4. profile export includes timer configuration (`#ticksize`, `#tickat`, `#tickicon`) in a stable, re-importable form
5. profile import restores exported timer configuration without restoring runtime phase or one-shot delays
6. import/export preserves command order for multiple subscriptions on the same timer second
7. importing a profile does not auto-restore current remaining time or unexpectedly resume old timer phase
8. user-facing docs mention exact-duplicate dedupe behavior and timer config portability boundaries
