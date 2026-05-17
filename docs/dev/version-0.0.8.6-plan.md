# Version 0.0.8.6 Plan

## Goal

Finish timer declaration layering across multiple active profiles, without expanding the runtime model introduced in `0.0.8.5`.

`0.0.8.6` should make timer declarations behave like ordered profile code:

1. later profiles override earlier scalar timer fields
2. timer subscriptions are merged sequentially
3. later profiles can remove earlier subscriptions precisely
4. named timer startup resolves declaration from the full active profile stack, not only the primary profile

---

## Current Status

### Done (in this cycle)

1. `ticker` subscriptions are no longer strictly primary-only in the common active-profile path.
2. `ticker` subscriptions are merged from active profiles in deterministic profile order (base -> later), with per-second command dedupe.
3. restore path now reapplies profile-derived `ticker` subscriptions even when persisted runtime ticker state exists.
4. regression coverage added for:
   1. two-profile `ticker` subscription merge/order behavior
   2. primary profile without `ticker` declaration
   3. persisted runtime stale-subscription override by profile declarations
   4. clearing stale runtime subscriptions when profile merge is empty (for active-profile stacks)

### Intentionally Not Changed Yet

1. zero-active-profile edge path keeps legacy behavior to avoid runtime persistence regressions.
2. no broad runtime model redesign was introduced.

### Release Completion Status

`0.0.8.6` goals are implemented and validated in code/tests.

Completed in this cycle:

1. full layered declaration resolution for named timers (not only `ticker`) across all active profiles.
2. layered scalar override semantics (`icon`, `cycle_ms`, `repeat_mode`) where later profile wins.
3. layered subscription mutation model including exact removal:
   1. `#untickat {name} {second} {command}` exact tuple removal
   2. existing bulk form `#untickat {name} {second}` remains supported
4. declaration-aware validation/startup paths use resolved layered declarations for named timers.
5. runtime-originated declaration writes remain primary-profile targeted, with explicit behavior and coverage.
6. acceptance-test matrix completed for layered merge/order/remove/restart consistency.

Additional post-release hardening completed:

1. fixed a runtime race window in timer restore/reload path by rebuilding into a temporary map and atomically swapping session timer state.
2. fixed admin UI repeat-mode value mismatch (`once` -> `one_shot`) to match runtime/parser contract.
3. added server-side validation/normalization for timer admin API writes (`cycle_ms`, `repeat_mode`, subscription removal forms).
4. ensured timer/profile settings reload triggers immediate timer snapshot broadcast so icon/declaration updates become visible on frontend without waiting for the next scheduler tick.

Deferred tradeoffs (intentional for urgent HIGH-first stabilization):

1. TODO: define and implement explicit zero-active-profile subscription policy (currently legacy behavior may preserve runtime subscriptions when layered declaration source is empty).
2. TODO: add dedicated HTTP API tests for profile timer CRUD and validation matrix (`POST/DELETE /profiles/{id}/timers`, subscription create/delete validation/normalization).
3. TODO: improve timer admin UX for bulk removal rows (hide/disable/clear command input when `is_bulk=true`).
4. TODO: revisit default `cycle_ms` for newly created timers in admin UI (currently aggressive default may be surprising).

### Known Pitfalls (from review + implementation)

1. primary-gated declaration loading can silently drop lower-profile timer behavior.
   1. if startup/declaration resolution requires the primary profile to declare a timer before merge runs, lower/base profile declarations may never be applied.
   2. tests must include "primary has no timer declaration, lower profile does" scenarios.

2. persisted runtime state can mask declaration-layer fixes.
   1. if restore loads session-scoped timer subscriptions first and layered merge is skipped/partial, stale runtime subscriptions can survive.
   2. restore-path tests must include preexisting runtime rows and verify override/clear semantics.

3. empty layered result must be treated as authoritative, not as "no-op".
   1. when active profile merge resolves to no subscriptions, runtime subscriptions should be cleared for that timer in layered paths.
   2. otherwise stale subscriptions continue firing even though declaration layers removed them.

4. zero-active-profile sessions are a special risk boundary.
   1. behavior differs depending on whether the product guarantees at least one active profile.
   2. if zero-profile sessions are allowed, merge/clear logic and tests must explicitly define expected fallback behavior.

5. runtime writeback can unintentionally collapse layering provenance.
   1. persisting full resolved runtime subscriptions back into the primary profile may copy lower-profile contributions upward.
   2. this can change future behavior after profile reorder/remove and should be either constrained or explicitly accepted/documented.

6. "contains-only" scheduler tests can hide ordering bugs.
   1. asserting only that commands eventually appear does not prove deterministic merge order.
   2. tests should assert resolved in-memory subscription order (or precise execution order with deterministic timing control).

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

Resolved for `0.0.8.6`:

1. export includes exact-unsubscribe lines when they are part of stored declaration-layer behavior.

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

Status: met for `0.0.8.6`.

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
