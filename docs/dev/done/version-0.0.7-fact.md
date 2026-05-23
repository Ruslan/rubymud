# Version 0.0.7 — Named Buffers & Split Panes (Done)

## Current State

- **Buffer field**: `LogRecord.WindowName` in `types.go:51`, mapped to `LogEntry.Buffer` at API layer. SQLite column `window_name`.
- **Trigger routing**: `TriggerRule.TargetBuffer` + `BufferAction` (move/copy/echo) in `trigger.go:16-17`. Applied in `vm/triggers.go:43-53` and `session/io.go:163-242`.
- **Protocol**: `ServerMsg.Buffer` + `Buffers map[string][]ClientLogEntry` in `session/types.go`. Frontend types match in `ui/src/types.ts`.
- **Restore**: `sendRestoreState` loads last 500 entries per buffer, sends as `buffers` map in `restore_begin`.
- **Split panes**: `PaneNode`/`ColumnNode`/`Layout` in `render.ts:27-41`. `addPaneBelow()`, `addColumnRight()`, `removePane()`. Layout persisted to `localStorage`.
- **Buffer picker**: `<select>` per pane in `createPaneDOM()`, populated by `updateSelectOptions()`.
- **Commands**: `#showme` writes to `"main"`; `#woutput {buffer} {text}` writes to named buffer (commands.go:176-190).
- **Layout persistence**: `saveLayout()` / `loadLayout()` via `localStorage`.
