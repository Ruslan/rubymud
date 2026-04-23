# Version 0.0.7.1 Plan

## Goal

Add drag-resizable panes and two-axis split so the screen can be divided into
columns (left/right) and each column can be sub-divided into rows (top/bottom).

This covers the target layout:

```
┌──────────────────┬──────────┐
│                  │  chat    │
│   main           ├──────────┤
│                  │  combat  │
│                  ├──────────┤
│                  │  map     │
└──────────────────┴──────────┘
```

---

## Layout Model

Two strict levels — no arbitrary recursion needed:

```
Level 1 (root): flex-row   — horizontal split into columns
Level 2:        flex-column — vertical split within one column
```

A **column** contains one or more **panes** stacked top-to-bottom.
A single pane that has not been split vertically is just a column of one.

### Data structure (TypeScript)

```ts
interface PaneNode {
  id: string;
  buffer: string;
}

interface ColumnNode {
  id: string;
  panes: PaneNode[];
  rowSizes: number[];   // % heights, sum = 100, length = panes.length
}

interface Layout {
  columns: ColumnNode[];
  colSizes: number[];   // % widths, sum = 100, length = columns.length
}
```

### Example — target layout

```ts
{
  columns: [
    { id: 'c1', panes: [{ id: 'p1', buffer: 'main' }],   rowSizes: [100] },
    { id: 'c2', panes: [
        { id: 'p2', buffer: 'chat'   },
        { id: 'p3', buffer: 'combat' },
        { id: 'p4', buffer: 'map'    },
      ], rowSizes: [33, 33, 34] },
  ],
  colSizes: [60, 40],
}
```

---

## DOM Structure

```
div#panes-container  (flex-row)
  div.column          (flex-column, flex-basis: 60%)
    div.pane
      div.pane-header
      div.pane-output
  div.col-resize-handle
  div.column          (flex-column, flex-basis: 40%)
    div.pane          (flex-basis: 33%)
      ...
    div.row-resize-handle
    div.pane          (flex-basis: 33%)
      ...
    div.row-resize-handle
    div.pane          (flex-basis: 34%)
      ...
```

Resize handles are `4px` wide/tall divs with `cursor: col-resize` or `cursor: row-resize`.

---

## Resize Interaction

No libraries. Pure pointer events on `document`.

```
handle.addEventListener('pointerdown', e => {
  handle.setPointerCapture(e.pointerId);
  // record start position + current sizes of the two neighbours
});
handle.addEventListener('pointermove', e => {
  // compute delta as % of container size
  // clamp each neighbour to min 10%
  // update sizes[i] and sizes[i+1]
  // apply via flex-basis on the two elements
});
handle.addEventListener('pointerup', () => saveLayout());
```

`setPointerCapture` means drag works even if the pointer leaves the handle.

---

## Split Operations

### Toolbar button ⊞ Split

Adds a new column to the right of the last column. New column starts showing `"main"`.

### Pane header context menu (right-click or ⋮ button)

| Action | Effect |
|--------|--------|
| Split down | Add a new pane below this one in the same column |
| Split right | Add a new column to the right of this column |
| Close | Remove this pane; if last pane in column, remove column |

If removing a column would leave zero columns, the action is blocked.

---

## Persistence

Layout is saved to `localStorage['pane-layout']` as JSON after every structural
change and after every resize drag end.

On page load:
1. Read and parse `localStorage['pane-layout']`.
2. Reconstruct the DOM from the layout tree.
3. Wait for `restore_begin` — buffer data fills in via `appendEntry`.

If `localStorage` has no layout, default to a single `main` pane (current behavior).

---

## Changes Required

### `ui/src/render.ts`

- Replace `panes: Pane[]` with `Layout` struct above.
- `createColumn(initialBuffer)` — builds column DOM with one pane.
- `addColumnRight(afterColumnId)` — inserts column + col-resize-handle.
- `addPaneBelow(paneId)` — inserts pane + row-resize-handle inside column.
- `removePane(paneId)` — removes pane; if column empty, removes column.
- `makeDragHandle(kind, indexA, indexB)` — drag logic above.
- `saveLayout() / loadLayout()` — localStorage serialization.
- `appendEntry` — unchanged (iterates all panes, matches `pane.buffer`).
- `clearOutput` — unchanged (preserves pane.buffer, clears DOM content).

### `ui/src/main.ts`

- Remove `renderer.createPane('main')` initial call.
- Replace with `renderer.loadLayout()` (falls back to single-pane default).
- `elements.splitPaneToggle.addEventListener` → calls `renderer.addColumnRight()`.

### `ui/src/styles/main.css`

- `.column` — `display: flex; flex-direction: column; overflow: hidden`.
- `.col-resize-handle` — `width: 4px; cursor: col-resize; background: #232a31`.
- `.row-resize-handle` — `height: 4px; cursor: row-resize; background: #232a31`.
- Handle hover: highlight to `#3d4f61`.
- Remove old `.pane` `flex: 1` (flex-basis now controls size).

### `ui/src/index.html`

- No changes needed.

---

## Acceptance Criteria

1. Drag a column divider left/right — columns resize live.
2. Drag a row divider up/down — panes in a column resize live.
3. Right-click a pane header → "Split down" adds a pane in the same column.
4. Right-click a pane header → "Split right" adds a column.
5. "×" closes a pane; if last pane in column, closes the column.
6. Layout survives page reload (localStorage).
7. Layout survives WebSocket reconnect (pane buffers restored, structure intact).
8. Minimum pane size: 10% of container — cannot drag to zero.
9. Single-pane layout shows no resize handles and no close button.

---

## Non-Goals

- Recursive splits beyond 2 levels.
- Server-side layout persistence (deferred to 0.0.7.2).
- Floating/detachable windows.
- Animated transitions.
