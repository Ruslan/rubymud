# Version 0.0.8.6 Plan

## Goal

Finish timer declaration layering across multiple active profiles, without expanding the runtime model introduced in `0.0.8.5`.

`0.0.8.6` should make timer declarations behave like ordered profile code:

1. later profiles override earlier scalar timer fields
2. timer subscriptions are merged sequentially
3. later profiles can remove earlier subscriptions precisely
4. named timer startup resolves declaration from the full active profile stack, not only the primary profile

---

## Scope

### Multi-Profile Declaration Resolution

Timer declarations should no longer be loaded only from the session's primary profile.

Instead, named timer declaration resolution should use all active profiles in profile order.

Model:

1. profiles behave like sequential program layers
2. earlier profiles provide base declaration
3. later profiles override or mutate that declaration
4. runtime uses the final resolved declaration for the session

This affects at least:

1. `#tickon {name}` startup from declaration
2. validation paths that need declared cycle information
3. any declaration-aware named timer command path using current declaration lookup

### Scalar Field Override Rules

For named timer declaration fields:

1. `icon`
2. `cycle_ms`
3. `repeat_mode`

the rule should be:

1. later profile wins

Example:

Profile `base`:

```text
#ticksize {herb} {58}
#tickicon {herb} {🪴}
```

Profile `raid`:

```text
#tickicon {herb} {⚗️}
```

Final resolved declaration:

```text
cycle = 58
icon = ⚗️
```

### Subscription Merge Rules

Timer subscriptions should not use winner-takes-all replacement.

They should be built by sequential application across active profiles.

Rules:

1. later profiles may add subscriptions
2. later profiles may remove earlier subscriptions
3. identical `name + second + command` entries should still dedupe
4. different commands on the same `name + second` still coexist
5. when multiple profiles contribute commands on the same timer second, execution order is base-profile first, then later-profile additions in profile order

Example:

Profile `default`:

```text
#tickat {tick} {3} {sit}
```

Profile `agro`:

```text
#untickat {tick} {3} {sit}
#tickat {tick} {3} {kill all}
```

Final resolved subscriptions at second `3`:

```text
kill all
```

### Exact Subscription Removal

`0.0.8.6` should add exact-removal support for layered declarations:

```text
#untickat {name} {second} {command}
```

This form should remove only the exact matching subscription tuple.

For `0.0.8.6`, this exact-removal form is primarily a declaration-layer operation used by profile scripts, import/export, and layered resolution.

It does not need to change the live runtime command semantics unless implementation naturally supports both paths safely.

The existing bulk-clear form should remain supported:

```text
#untickat {name} {second}
```

Bulk-clear semantics should not be removed in this release.

### Import/Export and Layering Compatibility

Import/export added in `0.0.8.5` should remain declaration-only.

For `0.0.8.6`, the main new requirement is that imported declarations participate correctly in layered resolution.

That means:

1. imported timer declarations must behave the same as manually declared profile timer commands
2. exact subscription removal must be representable in profile scripts when needed for layering
3. export/import must preserve command order where order affects final layered result

### Mutation Attribution

Layered declaration resolution affects how named timer declarations are read.

For `0.0.8.6`, runtime-originated declaration writes should continue to target the current session's primary profile unless a stronger provenance model is explicitly added later.

This keeps write behavior simple even while read behavior becomes layered.

### Runtime Boundaries Remain Intact

`0.0.8.6` should not collapse declaration and runtime back together.

Declaration stays:

1. profile-scoped
2. portable
3. layered across active profiles

Runtime stays:

1. session-scoped
2. responsible for live phase and enabled state
3. separate from profile portability
4. restart behavior must remain consistent with the resolved declaration that was used to start the timer

---

## Open Questions

### Export of Exact Unsubscribe

If a profile contains layered removal intent, exported scripts may need to include:

```text
#untickat {name} {second} {command}
```

Question:

1. should `0.0.8.6` export exact-unsubscribe lines when they are part of the stored declaration layer behavior?

### Default `ticker`

The likely direction remains:

1. named timers get full layered declaration behavior
2. default `ticker` stays on its current special path for now

Unless needed for consistency, `0.0.8.6` should avoid pulling default `ticker` into the layered declaration model.

---

## Non-Goals

1. do not redesign the runtime timer loop in this release
2. do not merge default `ticker` into full declaration layering unless explicitly needed
3. do not remove existing bulk `#untickat {name} {second}` behavior
4. do not add admin/UI CRUD for layered timer declarations in this release
5. do not export/import live runtime phase
6. do not redesign one-shot `#delay`

---

## Acceptance Criteria

1. named timer declaration resolution uses all active profiles, not only the primary profile
2. later profiles override earlier scalar fields: `icon`, `cycle_ms`, `repeat_mode`
3. timer subscriptions merge sequentially across profiles
4. exact duplicate subscriptions still dedupe
5. different commands on the same `name + second` still coexist
6. `#untickat {name} {second} {command}` removes only the exact matching subscription
7. existing `#untickat {name} {second}` bulk-clear behavior remains supported
8. `#tickon {name}` starts from the final resolved declaration built from all active profiles
9. declaration-aware validation paths use the final resolved declaration rather than primary-profile-only lookup
10. imported timer declarations participate in layered resolution the same as manually declared profile commands
11. declaration layering does not reintroduce runtime/profile entanglement
12. when multiple profiles contribute commands on the same timer second, resolved execution order is deterministic and tested
13. runtime-originated declaration updates continue to write into the primary profile and this behavior is documented
14. restart behavior remains consistent with the resolved declaration rather than duplicating or diverging from it
15. tests cover layered scalar override, layered subscription merge, exact unsubscribe, resolved startup behavior, and restart consistency
