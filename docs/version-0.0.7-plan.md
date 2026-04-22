# Version 0.0.7 Plan

## Goal

Add multiple named buffers and resizable split panes so one live MUD session can be viewed in several focused regions at once.

`0.0.7` is the milestone where the client stops being a single scrolling surface and becomes a windowed MUD workspace.

## Product Focus

This milestone is about classic MUD client screen management:

1. Split the screen left/right or top/bottom.
2. Attach each pane to a named buffer.
3. Route or copy selected lines into those buffers.
4. Keep one pane free for slow reading while another continues live scrolling.

This feature existed as an unfinished direction in the Ruby client and should now become a real product capability.

## Scope

### Named Buffers

1. Add persisted named buffers per session.
2. Keep a default main buffer for normal gameplay output.
3. Allow rules to target a named buffer.
4. Each UI pane can be pointed at any named buffer.

### Pane Layouts

1. Support vertical split: left/right.
2. Support horizontal split: top/bottom.
3. Allow recursive splits later, but the first implementation can be minimal if needed.
4. Panes must be resizable with the mouse.
5. Pane-to-buffer binding must be visible and changeable in the UI.

### Buffer Routing Semantics

We need explicit MUD-style routing behavior instead of vague "extra windows":

1. Move line: the matched line is removed from the source buffer and sent to a named buffer.
2. Copy line: the matched line stays in the source buffer and is also appended to a named buffer.
3. Echo to buffer: a trigger emits a newly formatted line directly into a named buffer.

Initial product stance:

1. Single-target move must exist.
2. Single-target copy must exist.
3. Trigger echo into a named buffer must exist.
4. Multi-target copy is useful but can stay an explicit follow-up if it complicates the first implementation.

### Architecture Direction

The important rule is that buffer routing is a runtime/storage concern first, not a browser-only trick.

1. Trigger effects may mutate persisted state.
2. New output routed to buffers must be written to SQLite before or alongside broadcast.
3. WebSocket should carry ready output events that already know their target buffer.
4. The browser should decide only which pane shows which named buffer.
5. The browser should not re-run trigger logic or invent routing rules locally.

This matches the older Ruby direction:

1. `act(..., window: ...)` expressed server-side line routing.
2. `wecho buffer, text` expressed server-side buffer echo.
3. The browser mostly rendered named buffers and let the user choose which one to display in a panel.

For `0.0.7`, the Go design should preserve that split of responsibilities.

Recommended first-pass model:

1. Keep one canonical output event pipeline in the Go runtime.
2. Persist enough data so restore can rebuild every named buffer.
3. Emit WebSocket output messages with explicit buffer identity.
4. Let the client append incoming lines into in-memory JSON entry arrays keyed by buffer name.
5. Let panes be just views over those buffer lists.

### Client Buffer State

For the first implementation, the browser should keep recent buffer contents in memory.

1. Client state should be `buffer_name -> entry[]`.
2. Each `entry` should stay as structured JSON data, not as pre-owned DOM nodes.
3. Panes should render from those arrays on demand.
4. If an incoming WebSocket line targets a buffer that is not currently visible, the client should still append it to that buffer's in-memory array.
5. If the client has not restored or created that buffer yet, it may ignore the live line until that buffer is known.

This keeps `0.0.7` simple and fits the future lazy-scroll direction:

1. Initial restore loads the latest `N` entries per buffer.
2. Live updates append to the tail of the in-memory array.
3. Future lazy scroll can prepend older entries for one buffer without changing the overall model.

This is close to the current architecture already, but not fully complete yet.

What is already aligned:

1. Go already owns trigger execution and storage writes.
2. Go already broadcasts output over WebSocket.
3. The browser already renders output and can maintain separate scroll state per view.

What still needs to be added:

1. Explicit multi-buffer persistence model in SQLite.
2. Runtime support for move/copy/echo effects targeting named buffers.
3. Restore logic that rehydrates all named buffers, not only the default output stream.
4. UI pane/layout management with pane-to-buffer binding.

### Scroll Behavior

1. A pane can stay in live-follow mode and auto-scroll to the bottom.
2. Another pane can stay fixed for manual reading.
3. Reading one pane must not force all other panes to lose live-follow behavior.

## Primary Use Cases

1. Right-side chat pane fed by trigger `echo` with custom formatting and timestamp.
2. Right-side important-events pane for skill gains, spells, or other high-signal messages.
3. Top/bottom split where the bottom stays live and the top is used for slow log reading.

## UX Notes

1. Split creation can be command-driven, UI-driven, or both.
2. The first implementation should strongly prefer discoverability, so at least one UI path must exist.
3. Commands remain useful for power users and scripting, but they should not be the only way to create a split.
4. Each pane should clearly show which named buffer it is displaying.

## Acceptance Criteria

1. User can create at least one additional pane from the UI.
2. User can switch a pane between named buffers.
3. User can resize split panes with the mouse.
4. A trigger can route a line away from the main buffer into a named buffer.
5. A trigger can copy a line into a named buffer while keeping it in the main buffer.
6. A trigger can emit a formatted line into a named buffer.
7. One pane can remain live-following while another is used for reading older content.

## Non-Goals

1. Full tmux-level layout management in the first pass.
2. Arbitrary floating windows.
3. Detachable OS-native windows.
4. Solving every historical Ruby-client window behavior before shipping the first useful version.

## Open Product Questions

1. Should pane layouts be persisted per session in SQLite from the first version?
2. Should multi-target copy be part of `0.0.7` or deferred?
3. Should split creation ship as both a command and a UI button, or UI first and command second?
4. Should named buffers have separate history retention rules, or share the main log model initially?

## Implementation Notes

1. Keep SQLite as the source of truth for persisted buffers and layout state if persistence is included.
2. Reuse the current browser UI model; this is still a single local Go-served app.
3. Prefer explicit buffer-routing primitives over hard-coded special panes like "chat" or "events".
4. Design routing so future commands such as `#output` or window-directed trigger actions fit naturally.
