# Input History Navigation — Done

## Status

Done. Arrow navigation and safe `Ctrl+R` reverse search are implemented and
tested. The one remaining open item — scoping local history by session ID — is
spun out into its own plan: `docs/dev/planned/session-scoped-command-history.md`.

## Product Decision

Command recall must be predictable and shell-like, and must never send an old
command by accident (a wrong recalled command can have real in-game
consequences). Arrows do prefix-scoped history navigation; `Ctrl+R` does safe
substring reverse search with a deliberate two-step Enter.

## Implemented Behavior

- **Arrow navigation** (`ui/src/history.ts`): a single focused navigation
  session with `active`, `query`, and `index`.
  - `query` is captured once, on the first `ArrowUp`/`ArrowDown` after typing.
  - `ArrowUp` scans older matches; `ArrowDown` scans newer.
  - Oldest match clamps (extra `ArrowUp` keeps the same command); past the newest
    match `ArrowDown` restores the original draft/query.
  - Prefix search is case-insensitive.
  - Manual typing and successful command submission reset the session.
- **Ctrl+R reverse search** (`ui/src/reverseSearchController.ts`): substring
  match, newest-to-older with clamp at the oldest, no wrap.
  - `Enter` accepts the match into the input without sending; a second `Enter`
    sends it through the normal path.
  - `Escape` restores the original draft.
  - `ArrowUp`/`ArrowDown` exit search first, then run normal prefix navigation.
  - Active search shows a visible affordance: `reverse-search-hint` button plus
    the `input-wrap_reverse-search-active` state class on the input wrap.

## Resolved Follow-ups

- **Visible reverse-search indicator**: done (`reverse-search-hint` + active
  state class).
- **Raw input recall vs canonical sent-command logs**: kept as an explicit
  product choice — arrows recall raw input; canonical sent-command logging stays
  separate by design.
- **Duplicate/recency policy for `merge(remoteHistory)`**: local + remote are
  merged and de-duplicated by first occurrence (`merge`/`mergeValues`,
  `hasMergedRemoteHistory`, `pendingLocalBeforeRemoteRestore`).

## Spun Out

- **Session-scoped local history** (was follow-up #1): the local storage key is
  still the global `commandHistory`, which mixes recall across MUD sessions.
  Tracked separately in `docs/dev/planned/session-scoped-command-history.md`.

## Verification

- `ui: npm test` (`history.test.ts` — stale-index, prefix, empty-input,
  boundary, case-insensitive; `reverseSearchController.test.ts` — reverse search)
