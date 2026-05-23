# Version 0.0.7.1 — Drag-Resizable Panes & Two-Axis Split (Done)

## Current State

- **Layout model**: `PaneNode`, `ColumnNode`, `Layout` in `render.ts:27-41` with `rowSizes`/`colSizes` percentage arrays.
- **Resize handles**: `col-resize-handle` (4px, `cursor: col-resize`) and `row-resize-handle` (4px, `cursor: row-resize`) in CSS. Pure pointer events with `setPointerCapture`, no libraries.
- **Split operations**: `addColumnRight()` (splits column), `addPaneBelow()` (splits pane), `removePane()` (with size redistribution). Accessed via pane header `⋮` dropdown.
- **Persistence**: `localStorage['pane-layout']` JSON. `saveLayout()` on every mutation, `loadLayout()` on startup.
- **Minimum pane size**: 10%, clamped in resize handler (`render.ts:367-375`).
- **CSS**: `.column` flex-column, `.col-resize-handle`/`.row-resize-handle` with hover highlight (`main.css:872-898`).
