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

## Stage 1: Toggle-Only Temporary Split

Start with an explicit toggle-only MVP to validate the interaction before adding automatic scroll behavior or shortcuts.

The pane header should expose a small scroll/split toggle button near the buffer selector/title. The icon should visually suggest splitting the pane for scrollback, for example two horizontal regions separated by a divider. The active state means a temporary duplicate live pane is currently open.

Behavior:

1. toggle on creates a temporary split below the current pane using the same buffer
2. the upper pane remains available for normal browser wheel scrolling and scrollback reading
3. the lower temporary pane is the live view and is forced to scroll to bottom
4. new entries are appended through the existing pane rendering path to both panes
5. toggle off removes the temporary split and leaves the lower/live pane as the surviving view
6. when the split is removed, the surviving live pane is forced to scroll to bottom
7. the temporary split is in-memory only and must not be saved to `pane-layout` in `localStorage`
8. the temporary split height can be resized, but the ratio is only remembered in JS memory for this session/page lifetime

This stage intentionally does not require `PageUp`, `PageDown`, `End`, or `Esc`. Those shortcuts can be layered on later after the temporary split behavior is proven.

Implementation notes:

1. do not call the existing persistent `Split Down` action directly
2. model the auto-created pane as a special temporary split type
3. prevent nested temporary splits from the temporary live pane
4. preserve the existing renderer behavior for ANSI, links, buttons, command hints, and pruning
5. accept the temporary DOM overhead because this mode only exists while the user is reading older output

## PageUp Scrollback Mode

When the user enters scrollback mode, the buffer should split horizontally into two regions:

1. upper region: scrollback/history view
2. lower region: pinned live tail that continues showing new incoming messages

Behavior:

1. `PageUp` enters scrollback mode and scrolls the upper region upward
2. `PageDown` scrolls the upper region downward
3. the lower region stays attached to the live stream
4. new MUD output continues to appear in the lower live region
5. leaving scrollback mode returns to the normal single live buffer view
6. `End`, `Esc`, or an explicit UI control may exit scrollback mode

Shortcuts must not override active profile hotkeys. If a current profile binds `PageUp`, `PageDown`, `End`, or `Esc`, the profile hotkey wins and the app scrollback shortcut is disabled for that key. The explicit pane toggle remains available even when a shortcut is occupied.

The split should preserve playability: the user can still see fresh combat/chat/output while reviewing recent history above.

## Buffer-Local Search

Add search inside the active buffer instead of relying on browser `Ctrl+F`.

Behavior:

1. search is scoped to the active MUD buffer, not the whole browser page
2. matches are found within currently loaded/rendered live buffer history
3. navigation supports next/previous match
4. search should work naturally with scrollback mode
5. browser `Ctrl+F` may remain available, but the app should provide its own buffer search shortcut/control

Possible shortcuts:

1. `/` or `Ctrl+F` opens buffer-local search if it can be done without fighting browser behavior
2. `Enter` / `Shift+Enter` move next/previous
3. `Esc` closes search

Exact shortcut choices can be decided during implementation.

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

## Acceptance Criteria

1. `PageUp` lets the user review older live-buffer lines without losing sight of new incoming output.
2. In scrollback mode, the buffer is split horizontally into scrollback and live-tail regions.
3. The lower live-tail region continues receiving new messages.
4. The user can exit scrollback mode and return to the normal live view.
5. The user can search inside the active buffer without browser-wide `Ctrl+F` noise.
6. Search supports next/previous match navigation.
7. The implementation does not disable pruning/virtualization safeguards or overload the live DOM.
