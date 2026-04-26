# Feature Request — Import JMC/Tortilla Timer Scripts

## Context

RubyMUD already exposes a timer model that is understandable to users coming from JMC, Tortilla, and partly TinTin++.

However, migration still requires manual rewriting because the command surface is similar in intent but not identical in syntax or semantics.

This feature request tracks a future import/adaptation layer specifically for timer- and delay-related script fragments from older clients.

---

## Goals

1. reduce friction when users migrate old configs into RubyMUD
2. preserve user intent even when command semantics do not match 1:1
3. document where import can be automatic and where it must be assisted

---

## Source Clients In Scope

1. JMC
2. Tortilla
3. optionally TinTin++ as a partial follow-up, especially for `#delay` / ticker-style constructs

---

## What Needs Adaptation

### 1. JMC Tick Commands

Relevant user-facing commands/patterns:

1. `#tickon`
2. `#tickoff`
3. `#ticksize <seconds>`
4. `#tickset`
5. trigger-based sync patterns such as `#action {...} {#tickset}`

Important semantic difference:

1. in JMC, `#ticksize` changes cycle size and resets the tick phase immediately
2. in RubyMUD, `#ticksize` changes only the cycle size (without resetting the phase)
3. RubyMUD `#tickset {seconds}` does both — changes cycle size and resets the phase — so it is the correct 1:1 equivalent of JMC `#ticksize`

```text
#tickset {seconds}    ;; JMC #ticksize 1:1
```

Importer note:

1. if importing a JMC script with `#ticksize {seconds}`, rewrite it to `#tickset {seconds}` — this preserves both the size change and the phase reset
2. if the JMC script uses `#ticksize` just to change the cycle without caring about phase (unlikely in practice), importer may annotate instead of guessing

### 2. Tortilla Timers

Relevant user-facing patterns:

1. repeating timers by id
2. one-shot waits via `#wait`
3. timer restart/update patterns via `#uptimer`

Recommended adaptation targets:

1. Tortilla `#wait` -> RubyMUD `#delay`
2. Tortilla repeating timer by id -> RubyMUD named timer:

```text
#ticksize {name} {seconds}
#tickon {name}
```

3. Tortilla timer reset/update -> RubyMUD `#tickset {name}` or `#tickset {name} {seconds}`

Importer note:

1. Tortilla often models multiple independent timers
2. RubyMUD can preserve that directly with named timers
3. if a Tortilla script is really expressing actions inside one shared game tick, importer may prefer one `ticker` plus several `#tickat` rules instead of many parallel timers

### 3. TinTin++ Delay / Ticker Patterns

Partial support is useful even if full script import is out of scope.

Recommended adaptation notes:

1. TinTin++ `#delay {seconds} {command}` -> RubyMUD `#delay {seconds} {command}`
2. TinTin++ named delay -> RubyMUD `#delay {id} {seconds} {command}`
3. TinTin++ delete-style ticker lifecycle may require explicit choice during import:
   - stop timer (pause cycle but keep subscriptions) -> `#tickoff`
   - remove slot actions (keep cycle but clear subscriptions) -> `#untickat`
   - RubyMUD currently has no single command that both stops the cycle AND clears all subscriptions (no full "delete timer")

Importer note:

1. RubyMUD separates the clock lifecycle (pause/resume via `#tickon`/`#tickoff`) from subscription lifecycle (add/remove via `#tickat`/`#untickat`)
2. if the original TinTin++ intent was pure deletion of a temporary ticker, the importer should emit `#tickoff` + `#untickat {0}` plus a comment, or consider a future `#tickdrop` command
3. imported scripts may need comments explaining whether the original intent was pause, reset, or full removal

---

## Suggested Import Strategy

### Automatic Rewrites That Are Usually Safe

1. `#wait X cmd` -> `#delay {X} {cmd}`
2. simple JMC `#tickon/#tickoff/#tickset` -> direct command mapping
3. JMC `#ticksize {seconds}` -> `#tickset {seconds}` (1:1, preserves size change + phase reset)
4. simple named repeating timer patterns -> `#ticksize {name} {seconds}; #tickon {name}`
5. if a single-action repeating timer is intended, consider `#ticker {name} {seconds} {command}` shorthand

### Rewrites That Need Caution

1. JMC `#ticksize` used without implicit reset expectation (rare) — may need `#ticksize` instead of `#tickset`
2. Tortilla timer graphs where several ids are orchestrated relative to one hidden cycle
3. TinTin++ ticker removal where intent may be stop vs delete vs unschedule

### Assisted Migration Output

Importer should be able to emit comments or notes such as:

```text
#showme {Imported from JMC: added #tickset after #ticksize to preserve reset semantics}
```

or produce a side report for the user.

---

## User-Facing Notes Worth Preserving

Any eventual import tool or migration guide should explain these RubyMUD-specific ideas clearly:

1. unnamed tick commands operate on default `ticker`
2. named timers are first-class and session-scoped
3. `#tickat` is often a better translation target than creating many separate timers
4. `#delay` is one-shot, while `#tick...` describes cycle-based behavior
5. `#tickicon` is UI metadata, not a classical MUD-client timer primitive

---

## Acceptance Direction

This feature request should be considered meaningfully addressed when:

1. there is a migration guide for JMC and Tortilla timer scripts
2. common timer/wait idioms can be rewritten into RubyMUD syntax with low surprise
3. semantic mismatches are documented instead of hidden
4. importer output, if added, prefers explicitness over risky silent conversion
