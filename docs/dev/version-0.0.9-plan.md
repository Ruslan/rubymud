# Version 0.0.9 Plan

## Goal

Add scalable log browsing for long-running sessions:

1. virtual scrolling / windowed rendering for log output
2. server-side search across session logs
3. a dedicated searchable log view that does not overload the live game UI
4. export of selected historical logs to self-contained HTML

This milestone is about making long sessions and historical investigation practical without turning the main play screen into a heavy management interface or a general observability product.

---

## Product Problem

Current output rendering is intentionally simple and optimized for live play, but it has two hard limits:

1. browser DOM is pruned aggressively in the live UI
2. restore currently loads only a small recent slice per buffer
3. there is no user-facing way to search old logs even though storage already has the beginnings of server-side search support

As a result, users can play live, but cannot comfortably:

1. scroll far back in session history
2. search for past combat lines, loot, commands, or chat
3. inspect old context without loading too much DOM into the main game screen
4. save memorable runs, raids, or roleplay sessions as a portable archive

---

## Existing Context

Relevant current behavior:

1. live UI renders only the last `maxRenderedLines` entries and prunes older DOM aggressively in `ui/src/render.ts`
2. websocket restore currently sends recent logs via `RecentLogsPerBuffer(500)` in `go/internal/web/server.go`
3. storage already includes `SearchLogsDetailed(...)` in `go/internal/storage/log_store.go`, returning grouped contextual search results
4. settings already has a richer Svelte UI (`ui/src/SettingsApp.svelte`) that is a better fit for data-heavy tooling than the minimal native-JS game client

---

## Product Direction

### Main recommendation

Log search should live in the Settings UI, not in the live game UI.

Reasoning:

1. the live game UI should stay focused on low-latency play, input, hotkeys, buttons, and immediate visibility
2. log search is exploratory, stateful, filter-heavy, and likely to grow pagination/context controls over time
3. Settings already uses Svelte and is structurally better suited to forms, result lists, tabs, filters, and richer interaction
4. this separation keeps the game client fast and visually minimal while still giving power users full log tooling

### Optional later convenience

The live game UI may later get a tiny convenience entry point such as:

1. an "Open Log Search" button
2. a shortcut that opens Settings directly on the log-search tab

But the actual search workflow should live in Settings for `0.0.9`.

The same recommendation applies to log export: it belongs in Settings, next to search/history tooling, not in the live play surface.

### Search UX level

`0.0.9` should target practical grep-style search with context, not Kibana-level analytics.

Target UX:

1. search query input
2. match list similar to `grep -n`
3. click a match to open `View in context`
4. load more above / below around the selected match
5. server-side pagination for older matches

Not the target:

1. observability dashboards
2. query language design
3. faceted analytics
4. large split-pane analytics workspace

---

## Scope

### 1. Server-side log search API

Add an HTTP API for searching session logs.

Recommended endpoint shape:

```text
GET /api/sessions/:id/logs/search?q=dragon&buffer=main&context=2&before_id=12345
```

Recommended parameters:

1. `q` - required search text
2. `buffer` - optional buffer filter
3. `context` - optional number of surrounding lines, default small such as `2`
4. `before_id` - optional pagination cursor for older results
5. later-compatible room for `limit`, `source`, or date-range filters

Behavior:

1. search is session-scoped
2. server performs the search; browser does not fetch the entire log corpus
3. primary response should be a match list, not a fully expanded archive view
4. each match should include enough metadata for a `grep -n` style result row: id, time, buffer, and text preview
5. response should expose whether more results are available
6. matching by both plain log text and command overlays is in scope because storage already trends that way

Recommended follow-up endpoint:

```text
GET /api/sessions/:id/logs/:entry_id/context?before=20&after=50
```

Context behavior:

1. returns the selected entry plus surrounding lines
2. indicates which line is the direct match
3. supports loading more above / below around that anchor

### 2. Dedicated Settings tab for Logs

Add a new Settings tab such as `Logs` or `Log Search`.

Initial UI behavior:

1. choose session
2. optional buffer filter
3. enter search query
4. show a compact match list first
5. open selected result in a context viewer
6. show which lines are direct matches vs surrounding context
7. allow loading older result pages

This should be a read-only investigation tool, not an editing surface.

### 3. Virtualized log results in Settings

Result lists in Settings should use windowed rendering / virtual scrolling.

Why:

1. search results can become large
2. context viewers may contain many rows
3. Svelte UI should stay responsive even on long sessions

Implementation goals:

1. render only the visible window plus overscan
2. preserve stable scroll behavior while loading more results
3. support variable-height rows if possible; if too expensive, use a simple block-based virtualization strategy first
4. prefer incremental pragmatism over trying to replicate a full analytics suite

### 4. Better history loading path for non-search browsing

`0.0.9` should also define a path for loading older logs progressively instead of assuming restore is the only source of history.

This does not require turning the live game pane into a full archive browser yet.

Minimum product value:

1. server API should support paged historical fetch by buffer and cursor
2. Settings log view may reuse that API for ordinary historical browsing even without a query

Recommended endpoint shape:

```text
GET /api/sessions/:id/logs?buffer=main&before_id=12345&limit=200
```

### 5. HTML log export

Add a way to export a selected time range of session logs to self-contained HTML.

Recommended UI location:

1. Settings `Logs` / `Log Search` tab
2. export panel near history/search controls

Recommended export inputs:

1. session
2. optional buffer filter
3. time `from`
4. time `to`
5. optional include command overlays / buttons toggle
6. optional title for the exported page

Recommended endpoint shape:

```text
GET /api/sessions/:id/logs/export?from=2026-04-25T18:00:00Z&to=2026-04-25T23:30:00Z&buffer=main&format=html
```

Behavior:

1. export is server-side
2. output is a single self-contained HTML file
3. styling is embedded inline in the document, either via one `<style>` block or inline `style="..."` attributes
4. exported file must not depend on app assets, external CSS, or runtime API availability
5. ANSI/highlighted text should be preserved in a readable static form as closely as practical
6. export should be suitable for archiving memorable sessions such as raids, quests, or roleplay logs
7. if the selected time range is large, generation may be streamed or downloaded as an attachment without loading the whole export into the browser DOM first

---

## Explicit Non-Goals

Not in `0.0.9`:

1. full-text index tuning beyond what is needed for acceptable initial behavior
2. editing or deleting historical log entries
3. turning the live game pane into a full searchable archive browser
4. cross-session global search
5. complex saved searches
6. fuzzy search, regex search, or boolean query language
7. PDF export or rich document publishing formats
8. WYSIWYG report editing of exported logs
9. Kibana-like analytics dashboards or observability UX

---

## Implementation Direction

### Storage

Build on the existing search/storage primitives instead of replacing them blindly.

Recommended improvements:

1. add buffer-aware filtering to search if it is not already present end-to-end
2. add explicit paged history fetch helpers if missing
3. provide a compact match-list search primitive and a separate context-loading primitive
4. avoid forcing the frontend to reconstruct everything from one oversized response shape

### Web/API

Add read-only HTTP endpoints for:

1. match-list search
2. context fetch around a selected match
3. paged history fetch
4. HTML export by time range

Prefer HTTP over websocket for this feature because:

1. requests are user-driven and query-oriented
2. results can be paginated naturally
3. this keeps the live socket protocol focused on session runtime updates

### Settings UI

Add a new Svelte tab with:

1. query form
2. match list
3. selected-match context viewer
4. loading state
5. empty state
6. pagination / load more
7. virtualization/windowing
8. export controls for time-range HTML download

### Live UI

Keep the native-JS live client minimal.

Allowed scope here:

1. optional link/button opening Settings on the Logs tab
2. no heavy search panel inside the main game screen in this milestone

---

## Acceptance Criteria

1. A user can search logs for a session from a dedicated Settings tab.
2. Search runs server-side and does not require loading the full session log into the browser.
3. Search initially shows a compact match list similar in spirit to `grep -n`.
4. A user can open a selected result in a context viewer and load more lines above or below it.
5. Results can be paged to older matches using a cursor.
6. Settings log results remain responsive when many matches are present because rendering is virtualized/windowed.
7. A user can browse older paged history in Settings even without a search query.
8. A user can export a chosen time range of logs to a self-contained HTML file.
9. Exported HTML preserves readable styling without depending on external assets.
10. The live game UI remains focused on play and does not gain a large embedded search/export workflow.
11. Existing live output behavior continues to work without regression.

---

## Suggested First Slice

If this milestone needs internal sequencing, the best order is:

1. add paged history API
2. add match-list search API reusing storage primitives
3. add context endpoint around a selected match
4. add basic Settings log-search tab without virtualization
5. add HTML export endpoint and basic export controls
6. add virtualization/windowing once real result shapes are visible

---

## Open Question

Before implementation, run a small web-informed evaluation of lightweight Svelte libraries for:

1. list virtualization / windowing
2. date or date-range input controls
3. optional text highlighting helpers

The goal is not to adopt a heavy search framework, but to identify whether there are small, well-maintained libraries worth using instead of building these pieces from scratch.

This keeps the product usable early while still landing the performance work inside the same milestone.
