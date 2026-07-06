# Session-Scoped Command History

## Context

Spun out from `input-history-navigation.md` (now in `docs/dev/done/`), which is
complete except for this one open product decision.

Local command history is persisted under a single global `localStorage` key:

```ts
// ui/src/history.ts
export const commandHistoryStorageKey = 'commandHistory';
```

Because the key is global, command recall (arrows and `Ctrl+R`) mixes commands
across different MUD sessions. A command typed while connected to one MUD can be
recalled while connected to another. The active session is already known on the
client — the game client URL carries `session_id` (see `ui/src/main.ts`).

## The Decision

Should local browser history be scoped per session ID instead of global?

- **Pro (scope it):** recall matches the world you are actually playing; no
  cross-MUD leakage of commands; safer recall.
- **Con (keep global):** a single shared recall across sessions; simpler; some
  users may want the same muscle-memory command list everywhere.

Recommendation: scope by session ID, with a one-time migration so existing users
do not lose their current history.

## Proposed Approach

1. Derive the storage key from the active session:
   `commandHistory:<session_id>` (fall back to the global key only when no
   `session_id` is present).
2. On first load for a session with no per-session key but an existing global
   `commandHistory`, seed the per-session key from the global value (one-time
   migration). Do not delete the global key immediately; treat it as a legacy
   seed source.
3. Keep the in-memory `InputHistory` API unchanged; only the persistence key and
   load/save paths become session-aware.
4. Preserve the existing `merge(remoteHistory)` semantics; remote restore still
   applies per session.

## Non-Goals

- Changing the arrow / `Ctrl+R` navigation model (already done).
- Changing backend restore history (recent `input` rows) semantics.
- Server-side per-session history storage — this plan is about local browser
  persistence only.

## Acceptance Criteria

1. History recall in one session does not surface commands typed only in another
   session.
2. Existing users keep their current history on first load after the change
   (migration seeds the per-session key).
3. When `session_id` is absent, behavior falls back to the previous global key
   with no error.
4. `merge(remoteHistory)` still de-duplicates and restores correctly per session.
5. Focused `InputHistory` tests cover: per-session isolation, one-time migration
   from the global key, and the no-session fallback.
