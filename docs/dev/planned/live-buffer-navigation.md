# Live Buffer Navigation

## Context

Current live output is optimized for the newest messages, but day-to-day play still needs better in-buffer navigation.

The main current pain points are:

1. no PageUp-style scrollback mode
2. browser `Ctrl+F` searches the whole page instead of the active MUD buffer
3. log/search tooling in Settings is useful for investigation, but too heavy for quick live-play lookup
4. several related live-buffer usability issues are already tracked in GitHub issues

This plan is separate from `log-browsing.md`.

`log-browsing.md` is for admin/history tooling in Settings. This plan is for the live game buffer while playing.

## Goals

1. make quick scrollback usable without disconnecting the player from live output
2. add buffer-local search inside the active live buffer
3. keep the live UI lightweight and low-latency
4. avoid turning the live screen into the full admin log browser

## Status

- Stage 1 (pane-based temporary split) was implemented, shipped, and has been replaced.
- Buffer-local search v1 (live per-keystroke search + auto-split) was implemented, shipped, and has been replaced.
- Stage 2 (in-pane scrollback region + explicit search execution) is implemented. Remaining planned work: `PageUp` scrollback shortcuts.

## Stage 1: Toggle-Only Temporary Split (implemented, superseded)

Shipped model: the pane header exposes a split toggle; toggle on creates a temporary duplicate live pane (`isTemporaryLive`, `temporaryLiveFor`) below the source pane as a real `PaneNode` in the layout; the source pane above becomes the scrollback view; toggle off collapses the split with the live pane surviving. The temporary pane is in-memory only and never saved to `pane-layout`.

### Why it is superseded

Every layout mutation goes through a full `rebuildDOM()` (`panesContainer.innerHTML = ''` + `renderedPanes.clear()`). Opening or closing the temporary split therefore destroys and recreates the live output DOM, re-parses ANSI for thousands of lines, and visibly flickers. Reordering the panes (scrollback above, live below) would not fix this: as long as the split is a layout mutation, `rebuildDOM()` recreates the live pane anyway.

The fix is not to patch the rebuild but to change the model: the temporary scrollback is not a pane, it is ephemeral state of one pane.

## Stage 2: In-Pane Scrollback Region (current plan)

### Model

The scrollback/search view is a region inserted **inside the existing pane element**, between the header and the live output:

```
before:                       after open:
+- .pane ---------+           +- .pane ----------------+
| header          |           | header (+search UI)    |
|                 |           +------------------------+
| outputEl        |           | scrollbackEl           |  <- flex-basis from saved ratio
| flex: 1         |           | overflow: auto         |
|                 |           +------------------------+
|                 |           | outputEl  flex: 1      |  <- same DOM node, just shrinks
+-----------------+           +------------------------+
```

`.pane` is already a flex column and `.pane-output` is `flex: 1`, so inserting `scrollbackEl` with a `flex-basis` shrinks the live output geometrically without recreating it. No layout-model mutation happens at all.

### Behavior

1. open: `insertBefore(scrollbackEl, outputEl)`, then re-stick the live output to bottom (`scrollTop = scrollHeight`)
2. close: `scrollbackEl.remove()`, then re-stick the live output to bottom
3. the live output element is never rebuilt, re-rendered, or detached — no flicker by construction
4. the single pane header stays on top and controls both regions
5. the region is in-memory only; the layout model and `pane-layout` in `localStorage` are untouched by design
6. the region height ratio is remembered per buffer via the existing app-level preference key (`readLiveSplitRatio`)
7. v1 may ship with a fixed ratio; a drag divider between the regions is a follow-up
8. a temporary-live region cannot be nested (the region has no header/controls of its own)

### Performance rules

1. the scrollback region renders a window capped by `maxRenderedLines`, extended backward only as far as the oldest match of an executed query (bounded overall by the in-memory buffer cap `maxRenderedLines + pruneRenderedLines`); match counting scans the full in-memory buffer, but the DOM holds only the window
2. new entries are appended to the scrollback region incrementally via the existing `createEntryDOM` path, with match highlighting applied only to the new entries; `current/total` is updated by addition, not recount
3. a full scrollback re-render happens only when the search query is (re-)executed
4. the live region below never participates in search: no highlighting, no re-render on message — the hot append path stays untouched

### Migration / removal

Replacing Stage 1 deletes code rather than adding it:

1. remove `isTemporaryLive` / `temporaryLiveFor` from `PaneNode` and all special-casing in `rebuildDOM`, `renderPane`, `removePane`
2. remove `toggleTemporaryLiveSplit`, `collapseTemporaryLiveSplit`, `findTemporaryLivePane`, `hasTemporaryLiveSplit` survivor logic
3. remove `maybeAutoEnableSearchSplit` and its nested `setTimeout` re-checks (obsoleted by explicit search execution, see below)
4. the split toggle button in the pane header now opens/closes the scrollback region instead of creating a pane
5. port existing temporary-split and search tests to the region model; behaviors like "live view survives close" become trivially true

## PageUp Scrollback Mode

`PageUp` opens the same in-pane scrollback region (no search query) and scrolls it upward.

Behavior:

1. `PageUp` opens the region if closed and scrolls the scrollback region upward
2. `PageDown` scrolls the scrollback region downward
3. the lower live region stays attached to the live stream
4. `End`, `Esc`, or the header toggle closes the region and returns to the normal single live view

Shortcuts must not override active profile hotkeys. If a current profile binds `PageUp`, `PageDown`, `End`, or `Esc`, the profile hotkey wins and the app scrollback shortcut is disabled for that key. The explicit pane toggle remains available even when a shortcut is occupied.

## Buffer-Local Search

Search inside the active buffer instead of relying on browser `Ctrl+F`. Runs in the scrollback region.

### Execution model: explicit, not live

Typing in the search input changes only the query string — it triggers no buffer scan and no render. Search executes explicitly:

1. `⌕` in the pane header opens the search input (UI only; nothing is scanned or split yet)
2. `Enter` or the search button executes the query: matches are counted, and if there are matches the scrollback region opens (if closed), highlights render, and the view jumps to the newest match
3. after execution, `Enter` moves to the next older match (search walks backward through history); `Shift+Enter` moves to the next newer match; the `↑`/`↓` buttons navigate the same way with wrapping (`↑` = older/upper, `↓` = newer/lower); navigation only swaps the strong-highlight class — the scrollback DOM is not re-rendered
4. `Esc` closes search
5. an empty query or a query with zero matches shows `0/0` and does not open the region
6. editing the query after an executed search marks the current count/highlights as stale (e.g., dimmed count) until the next `Enter`
7. closing the search UI closes a search-opened region; a manually-toggled region stays open

Live-as-you-type search (with debounce) is a possible later enhancement; nothing in the model prevents it.

### Semantics (unchanged from v1)

1. search is scoped to one pane's current MUD buffer, not the whole browser page
2. matches are found within in-memory live buffer history only; older archive search remains in Settings -> Logs
3. search is always case-insensitive, including Cyrillic
4. search is based on ANSI-stripped MUD output text from `LogEntry.text` after runtime substitutions/replacements, and matches across ANSI style boundaries
5. search does not match command hints or trigger button labels appended to output lines
6. executing a query selects the newest/lower match by default and shows a `current/total` count
7. all matches are weakly highlighted and the current match is strongly highlighted
8. browser `Ctrl+F` may remain available, but the app provides its own buffer search control in the pane header

## Relationship To Admin Log Browsing

This feature should not duplicate the full Settings log tooling.

Differences:

1. live buffer navigation is fast, local, and play-oriented
2. Settings log browsing is historical, paginated, exportable, and server-searchable
3. live buffer search can start with currently loaded buffer contents
4. deeper historical search belongs to Settings

If the user searches beyond loaded live history, the UI may offer an "Open full log search" action later.

## Non-Goals

1. full historical archive browsing in the live game screen
2. replacing Settings log search/export
3. loading the entire session log into the live DOM
4. complex search query language
5. analytics-style filtering
6. keyed/incremental `rebuildDOM()` for persistent layout operations — valuable on its own (pane add/remove and column drag also flicker today) but orthogonal to this plan; tracked separately

## Acceptance Criteria

1. Opening and closing the scrollback region (via toggle, search, or `PageUp`) does not rebuild, re-render, or visibly flicker the live output; the live output element is the same DOM node before and after.
2. The scrollback region lets the user review older live-buffer lines while new output keeps arriving in the live region below.
3. Typing in the search input causes no rendering or buffer scanning; search work happens only on explicit execution.
4. Executed search shows weak/strong highlighting, a `current/total` count, and supports wrapping next/previous navigation.
5. Case-insensitive matching works for Cyrillic and across ANSI style boundaries.
6. While search is active, new messages append incrementally to both regions; no full pane re-render occurs per message.
7. The scrollback region DOM stays within the in-memory buffer cap (`maxRenderedLines`, extended only to reach older matches); pruning/virtualization safeguards remain in force.
8. The layout model and `pane-layout` persistence are unaffected by opening/closing the region.
