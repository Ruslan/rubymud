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

---

## Current Architecture Baseline

**What already exists:**

- `log_entries` table — single flat log per session (no buffer column)
- `ServerMsg.Entries []ClientLogEntry` — no buffer name field
- `sendRestoreState` — loads last 500 entries from main log only
- Frontend renders to a single `elements.output` div
- Trigger execution is server-side (Go VM); command dispatch pipeline is clean

**What needs to be added:**

1. `buffer` column on `log_entries` (default `"main"`)
2. `Buffer` field on `ClientLogEntry` and `ServerMsg`
3. Restore logic per buffer
4. Trigger effects `move_to_buffer` / `copy_to_buffer` / `echo_to_buffer`
5. UI pane/layout management with pane-to-buffer binding

---

## Scope

### Named Buffers

1. Add a `buffer` column to `log_entries` (TEXT, default `"main"`, NOT NULL).
2. Keep `"main"` as the default buffer for normal gameplay output.
3. Allow trigger effects to target a named buffer.
4. Each UI pane can be pointed at any named buffer.

### Pane Layouts

1. Support vertical split: left/right.
2. Support horizontal split: top/bottom.
3. Panes must be resizable with the mouse (CSS resize or drag divider).
4. Pane-to-buffer binding must be visible and changeable in the UI.
5. Recursive splits deferred; first implementation is max two panes.

### Buffer Routing Semantics

1. **move**: matched line is removed from `"main"` and written only to the target buffer.
2. **copy**: matched line stays in `"main"` and is also appended to the target buffer.
3. **echo**: trigger emits a newly formatted line directly into a named buffer (does not copy the original).

Initial product stance:
- Single-target move must exist.
- Single-target copy must exist.
- Trigger echo into a named buffer must exist.
- Multi-target copy deferred.

### Architecture Direction

Buffer routing is a runtime/storage concern, not a browser trick.

1. Trigger effects may mutate persisted state (`log_entries.buffer`).
2. New output routed to buffers must be written to SQLite before or alongside broadcast.
3. WebSocket carries output events that already know their target buffer (`"buffer"` field on entry).
4. The browser decides only which pane shows which named buffer.
5. The browser does not re-run trigger logic or invent routing rules locally.

This matches the older Ruby direction (`act(..., window: ...)`, `wecho buffer, text`).

### Client Buffer State

1. Browser state: `Map<bufferName, ClientLogEntry[]>`
2. Each entry is structured JSON data, not pre-owned DOM nodes.
3. Panes render from those arrays on demand.
4. If an incoming line targets a buffer not currently visible, client still appends to its in-memory array.

This keeps `0.0.7` simple:
- Initial restore loads the latest N entries per buffer.
- Live updates append to the tail of the in-memory array.
- Future lazy scroll can prepend older entries for one buffer without changing the model.

### Scroll Behavior

1. Each pane independently maintains live-follow vs. manual-reading mode.
2. Scrolling one pane must not affect other panes.

---

## Data Model Changes

### SQLite

```sql
-- Add buffer column to existing log_entries table
ALTER TABLE log_entries ADD COLUMN buffer TEXT NOT NULL DEFAULT 'main';
CREATE INDEX idx_log_entries_session_buffer ON log_entries (session_id, buffer, id);
```

GORM AutoMigrate: add `Buffer string` field to `LogRecord`.

### TriggerRule

Add `TargetBuffer string` and `BufferAction string` (values: `"move"`, `"copy"`, `"echo"`) to `trigger_rules`.

```go
type TriggerRule struct {
    // ... existing fields ...
    TargetBuffer string `json:"target_buffer"`  // "" = no routing
    BufferAction string `json:"buffer_action"`  // "move" | "copy" | "echo"
}
```

---

## Protocol Changes

### ServerMsg

```go
type ServerMsg struct {
    // ... existing fields ...
    Buffers map[string][]ClientLogEntry `json:"buffers,omitempty"` // restore_begin: all buffers
}
```

### ClientLogEntry

```go
type ClientLogEntry struct {
    Text     string          `json:"text"`
    Buffer   string          `json:"buffer,omitempty"` // "" = "main"
    Commands []string        `json:"commands,omitempty"`
    Buttons  []ButtonOverlay `json:"buttons,omitempty"`
}
```

### Message flow

| Message | Change |
|---------|--------|
| `restore_begin` | Add `buffers` map: `{bufferName → []entry}` for all non-empty buffers |
| `restore_chunk` | Each entry has `buffer` field |
| `output` | Each entry has `buffer` field |

Client routes each incoming entry to `buffers[entry.buffer ?? "main"]`.

---

## VM / Trigger Changes

In `go/internal/vm/`, trigger execution (`applyTrigger`) needs to:

1. Check `trigger.TargetBuffer` — if non-empty, apply buffer routing.
2. `"move"`: write log entry with `buffer = targetBuffer`, skip writing to `"main"`.
3. `"copy"`: write log entry to `"main"`, then write a second entry with `buffer = targetBuffer`.
4. `"echo"`: after trigger command fires, write a synthetic entry (`[trigger: <label>] <result>`) with `buffer = targetBuffer`.

---

## UI Changes

### Layout

```
┌─────────────────────┬──────────────────┐
│  pane A (main)      │  pane B (chat)   │
│  [live follow]      │  [fixed read]    │
│                     │                  │
└─────────────────────┴──────────────────┘
          [ input bar ]
```

Each pane:
- Header bar showing current buffer name + buffer picker dropdown
- Scroll area rendering entries from `buffers[bufferName]`
- Independent scroll state (live-follow vs. manual)

### Split creation

- UI button (e.g. "⊞ Split") in the toolbar opens a pane configuration panel.
- No command-based split in first version; UI first.

### Pane layout persistence

- Deferred from first implementation — layout is ephemeral (in-memory only).
- On reconnect, client starts with single `"main"` pane.
- Persistence (SQLite `session_layouts` table) planned for 0.0.7.1.

---

## Phased Implementation

### Phase 1 — Data layer (Go)

1. Add `Buffer` to `LogRecord`, run `AutoMigrate`.
2. Add `TargetBuffer`, `BufferAction` to `TriggerRule`.
3. Update `AppendLogEntry` to accept buffer parameter.
4. Add `LogEntriesForBuffer(sessionID int64, buffer string, limit int)` to Store.
5. Update trigger execution to apply buffer routing.

### Phase 2 — Protocol (Go)

1. Add `Buffer` to `ClientLogEntry`.
2. Update `sendRestoreState`: query distinct buffers, send `restore_begin` with `buffers` map.
3. Update live broadcast: include `buffer` on each outgoing entry.

### Phase 3 — Frontend

1. Replace single `output` div model with `Map<string, HTMLElement>` panes.
2. Implement split layout (two panes, CSS flex or grid, draggable divider).
3. Buffer picker dropdown per pane.
4. Per-pane live-follow scroll logic.

### Phase 4 — Settings UI

1. Add `target_buffer` and `buffer_action` fields to trigger editor in settings.

---

## Acceptance Criteria

1. User can create at least one additional pane from the UI.
2. User can switch a pane between named buffers.
3. User can resize split panes with the mouse.
4. A trigger with `buffer_action = "move"` routes a line away from `"main"` into a named buffer.
5. A trigger with `buffer_action = "copy"` keeps the line in `"main"` and copies it to a named buffer.
6. A trigger with `buffer_action = "echo"` emits a formatted line into a named buffer.
7. One pane can remain live-following while another is used for reading older content.
8. On WebSocket reconnect, each pane restores its buffer content independently.

---

## Non-Goals

1. Full tmux-level layout management in the first pass.
2. Arbitrary floating windows.
3. Detachable OS-native windows.
4. Persistent pane layouts (deferred to 0.0.7.1).
5. Multi-target copy (deferred).
6. MCP tools for buffers (can be added later alongside MCP cartography in 0.0.7.x).

---

## Open Questions (Resolved)

| Question | Decision |
|----------|----------|
| Persist pane layouts? | No — deferred to 0.0.7.1 |
| Multi-target copy in 0.0.7? | No — deferred |
| Split creation: command or UI? | UI button first, command deferred |
| Separate history retention per buffer? | No — all buffers share the main log model, just filtered by `buffer` column |

### Commands for Compatibility
Added for compatibility with third-party scripts:
* `#showme {text}` — outputs text to the main window.
* `#woutput {window_name} {text}` — outputs text to the specified additional window (analogous to `wecho`).
If text needs to be output with colors, it uses format similar to highlights: `#woutput {window_name} {text with ANSI/highlight tags}`. We will use our existing ANSI/highlight coloring format for this.
