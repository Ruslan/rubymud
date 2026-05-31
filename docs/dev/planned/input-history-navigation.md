# Input History Navigation

## Context

Input history navigation with `ArrowUp` and `ArrowDown` is currently unreliable.

Example problem:

```text
1. Press ArrowUp several times.
2. Move one command past the command you wanted.
3. Press ArrowDown once.
4. The expected command is not restored; another command can appear instead.
```

This makes command recall feel unsafe during normal play, where a wrong recalled command can have real in-game consequences.

## Implementation Status

Status: arrow navigation and safe `Ctrl+R` search fixed, keep this plan open for follow-ups.

Implemented:

1. `InputHistory` uses a focused arrow-navigation session with `active`, `query`, and `index`.
2. Manual input resets the active navigation session.
3. Successful command submission resets the active navigation session.
4. `ArrowUp` starts from the newest history item after manual typing instead of a stale index.
5. Prefix-search on arrows remains case-insensitive.
6. Oldest-match `ArrowUp` clamps on the oldest match.
7. Newest-match `ArrowDown` restores the original draft/query.
8. `Ctrl+R` opens safe reverse substring search with newest-to-older matching and no wrap.
9. `Enter` during active reverse search accepts the selected command without sending it.
10. `Escape` during active reverse search restores the original draft.
11. Manual input resets active arrow and reverse-search sessions.
12. Focused `InputHistory` unit tests cover stale-index, prefix, empty-input, boundary, case-insensitive, and reverse-search behavior.

Remaining follow-ups:

1. Decide whether local browser history should be scoped by session ID instead of global `commandHistory`.
2. Decide duplicate/recency policy for repeated commands and `merge(remoteHistory)`.
3. Keep raw input recall as an explicit product choice while canonical sent-command logs continue separately.
4. Consider a small visible indicator for active reverse-search mode if the input-only feedback feels too subtle.

## Previous Implementation

The frontend history logic lives in `ui/src/history.ts`.

The previous fragile state included:

1. `historyIndex`
2. `historyViewed`
3. `historyDirection`
4. `lastResult`
5. `arrowQuery`

The key handlers are in `ui/src/main.ts` and call `history.up(elements.input.value)` and `history.down(elements.input.value)`.

## Root Cause Fixed

The current model mixes navigation state, search query state, direction state, and duplicate suppression.

The highest-risk behavior is boundary handling:

1. `ArrowUp` can reach the oldest matching command.
2. Pressing `ArrowUp` again can return the original query/draft instead of clamping to the current history entry.
3. `historyIndex` is then left at a boundary value.
4. Pressing `ArrowDown` resumes from that shifted index and can skip the command the user expected.

`historyViewed` also makes direction changes fragile because the set is cleared when direction changes, but `historyIndex`, `lastResult`, and `arrowQuery` may no longer represent a single coherent navigation session.

## Additional Risks

The local browser history key is currently `commandHistory`, which is global across sessions. This can mix command recall between different MUD sessions.

`push()` stores duplicates locally. `merge()` later deduplicates local and remote history by first occurrence. This can distort recency semantics after restore.

The backend restore history comes from recent `input` rows only, not expanded commands. That is correct for editing convenience, but it should remain an explicit product choice.

## Desired Behavior

History navigation should be predictable and shell-like:

1. The input value before the first history navigation is preserved as the draft/query.
2. `ArrowUp` moves to the previous matching command.
3. `ArrowDown` moves to the next matching command.
4. At the oldest matching command, extra `ArrowUp` presses keep the same command.
5. At the newest matching command, extra `ArrowDown` presses restore the original draft/query.
6. If the user typed a prefix before pressing arrows, navigation only visits commands matching that prefix unless the product decision changes.
7. Manual typing should reset the active navigation session.
8. Submitting a command should reset the active navigation session.

## Proposed Fix Plan

1. Add focused tests for `InputHistory` instead of testing this only through DOM events.
2. Reproduce the reported miss-by-one scenario with a failing test.
3. Add boundary tests for oldest/newest history entries.
4. Add prefix-search tests for non-empty input values.
5. Replace the current direction/viewed/result state with a simpler navigation session model.
6. Store `query`, `index`, and `active` for the current arrow-navigation session.
7. Capture `query` exactly once, on the first `ArrowUp` or `ArrowDown` after normal typing/submission.
8. Implement `ArrowUp` as a reverse scan from the current index.
9. Implement `ArrowDown` as a forward scan from the current index.
10. Clamp at the oldest match instead of returning the draft/query.
11. Restore the draft/query after moving down past the newest match.
12. Reset navigation state on normal input changes and after successful command submission.
13. Consider scoping local storage by session ID in a separate small follow-up.

## Ctrl+R Search

Ctrl+R is implemented as a separate safe reverse-search interaction after arrow navigation stabilization.

Safe behavior for a MUD client:

1. `Ctrl+R` opens reverse history search.
2. Search should match substrings, not only prefixes.
3. Repeated `Ctrl+R` moves to older matches and clamps at the oldest match.
4. `Enter` accepts the selected match into the input without sending it immediately.
5. A second `Enter` sends the accepted command through the normal input path.
6. `Escape` closes search and restores the original draft.
7. `ArrowUp` and `ArrowDown` close search mode first, then run normal prefix history navigation from the visible input.

The two-step Enter behavior is intentionally safer than immediate execution because accidental recall of an old MUD command can be costly.

## Open Product Decision

Decide whether `ArrowUp` and `ArrowDown` should keep the current prefix-search behavior or switch to pure shell-style full-history navigation.

Option A: keep prefix-search on non-empty input.

This is faster for MUD command recall when users remember the beginning of a command.

Option B: make arrows always navigate full history and reserve search for `Ctrl+R`.

This is closer to many shells and may be easier to reason about.

Current recommendation: keep prefix-search for arrows, then add substring reverse search through `Ctrl+R`.
