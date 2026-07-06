# Live Buffer Navigation — Done

## Status

Done. This closes the `live-buffer-navigation.md` plan; the final outstanding
piece was the `PageUp` scrollback shortcuts.

## Product Decision

Day-to-day play needs quick scrollback and buffer-local search without
disconnecting from live output and without pulling in the heavy Settings log
browser. The chosen model is an **in-pane scrollback region** (not a layout
pane), so opening/closing it never rebuilds or flickers the live output.

## Implemented Behavior

- **In-pane scrollback region** (Stage 2): a region inserted between the pane
  header and the live output, sized from a per-buffer split ratio
  (`liveSplitRatio:v1:<buffer>`) with a drag divider. The live output element is
  the same DOM node before and after — no rebuild, no flicker. In-memory only;
  `pane-layout` persistence is untouched.
- **Buffer-local search**: explicit execution (typing only sets the query),
  `current/total` count, weak/strong highlighting, wrapping next/older &
  prev/newer navigation, case-insensitive incl. Cyrillic, matching across ANSI
  boundaries. Header `⌕` opens it; `Esc` closes.
- **PageUp scrollback shortcuts** (final piece):
  - `PageUp` opens the region on the active pane (if closed) and pages up.
  - `PageDown` pages down while the region is open.
  - `End` / `Esc` close the region and return to the single live view.
  - The header `⇵` toggle stays available regardless.
- **Active pane**: shortcuts act on the most recently interacted-with pane
  (tracked via `pointerdown`), falling back to the first pane in the layout.
- **Profile-hotkey precedence**: scrollback keys are handled in
  `main.ts` *after* `matchHotkey`, so a profile binding on
  `PageUp/PageDown/End/Esc` always wins. The pane search input keeps its own
  Enter/Escape navigation (scrollback shortcuts defer to it).

## Key Code

- `ui/src/render.ts`: `handleScrollbackKey`, `getActivePaneId`,
  `scrollScrollbackByPage`, `open/close/toggleScrollbackRegion`,
  `renderScrollbackRegion`.
- `ui/src/main.ts`: global keydown wiring after hotkey matching.

## Deferred / Non-Goals

- Live-as-you-type search (debounced) — possible later; model allows it.
- Full historical archive browsing stays in Settings → Logs.
- Keyed/incremental `rebuildDOM()` for persistent layout ops — tracked separately.

## Verification

- `ui: npm test` (118 tests pass, incl. `renderer scrollback keyboard shortcuts`)
- `ui: npm run build`
