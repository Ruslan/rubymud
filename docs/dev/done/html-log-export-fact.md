# Colored HTML Log Export + Replay Viewer — Done

## Status

Done. Closes the `html-log-export.md` plan (both Phase 1 — static colored HTML
export — and Phase 2 — the in-file replay viewer). Exports a session's logs as a
**self-contained colored HTML file** rendered through the session's `ansi_theme`
palette: the raw server stream (no highlights/substitutions/gags), scoped to the
main game buffer, with canonical sent commands, plus an embedded vanilla-JS
replay viewer. The plain `.txt` download is unchanged (still the greppable
"everything" format).

Implements `log-browsing.md` §5/§6 for export with one intentional deviation:
colors are the native server ANSI **mapped through the `ansi_theme` palette**
(themeable), not raw network ANSI (§5.6). Related follow-up — suppressing input
logged at a telnet echo-off password prompt — stays open in
`password-echo-off-guard.md`.

## Implemented

### ANSI→HTML converter — `go/internal/ansihtml`
- Isolated package converting a MUD ANSI/SGR string to HTML that matches
  `ansi_up` (`use_classes=true`) **byte-for-byte**, so the embedded CSS colors it
  identically to the live pane.
- `ToHTML(input, theme)` (fresh state) and a persistent `Converter`
  (`NewConverter`/`Convert`) that carries SGR state across output rows, matching
  the live pane's single persistent `ansi_up`. Standard/bright 16 colors → CSS
  classes; 256-cube/grayscale/truecolor → inline `rgb()`; bold/faint/italic/
  underline → inline styles; blink/reverse/strikethrough ignored (ansi_up
  parity); OSC sequences (incl. OSC-8 hyperlinks) stripped so no URL leaks into
  the file. High-contrast theme applies the `promoteBoldAnsiForeground` mapping.
- Table-driven parity tests captured against the real `ansi_up` 6.0.6.

### Streaming endpoint — `go/internal/web/export_html.go`
- `GET /api/sessions/{id}/logs/export-html?from&to&buffer&commands&tz&title&theme`.
  Streams `text/html` with `Content-Disposition: attachment` via a single ordered
  `Rows()` cursor — no pagination, **no size cap, no browser-side assembly**
  (a ~19 MB-class range exports like the `.txt`), `http.Flusher` every 256 rows.
- Emits `<!DOCTYPE>` + `<head>` with ONE embedded `<style>` (classic base +
  selected theme + ansi-classes + export-base + replay CSS), the replay control
  bar, `<pre class="log-export__body">`, then per row
  `<div class="log-line"[ log-input]" data-source="output|command" data-t="<ms>">`.
  `data-t` is ms from the first row. Commands are prefixed `> ` and escaped.
- At end of `<body>`: injects `window.__REPLAY_T0=<first-row-epoch-ms>` then the
  replay `<script>`. The document above is a complete, valid static log without JS.

### Export query — `go/internal/storage/log_store.go`
- `StreamExportLog(opts, fn)` runs a merged UNION as a single ordered cursor
  (never buffering the whole result): `source_type='mud'` server output +
  optional `command_hint` canonical outgoing commands, with a normalized
  lexicographic sort key (`STRFTIME(...) || PRINTF('%09d', ...)`) so whole-second
  and fractional timestamps order correctly.
- **Local echo excluded from export.** `AppendLogEntry` now tags rows
  `source_type="echo"` (all three callers are local echo: `#showme`/`#woutput`/
  command echo); `AppendLogEntryWithOverlays` tags genuine server output
  `source_type="mud"`. The export selects only `mud`.
- Auto-login (`source="connect"`) commands get no `command_hint` overlay, so they
  never reach the export (regression-tested).

### Buffer scoping
- The export is scoped to `buffer=main` (the game screen). Canonical command
  echoes anchored to side buffers would otherwise leak in; both the output and
  `command_hint` branches filter `window_name='main'`.

### Embedded theme CSS (single source of truth)
- `ui/src/styles/{ansi-classes.css, export-base.css, ansi-themes/*.css}` are the
  single source, shared with the live app via `main.css`. A `make ui`
  `sync-styles` step mirrors them into `go/internal/web/styles/` (committed) for
  `go:embed`. `buildExportStyle` always prepends `classic.css` as the base layer
  so non-classic themes (which define only the vars they change) resolve every
  `--ansi-*`. Drift guard test + "all referenced vars defined" test.

### Replay viewer (Phase 2, embedded vanilla JS)
- Media-player controls in a **sticky** bar: **Play / Pause / Stop**, speeds
  **0.25× / 0.5× / 1× / 2× / 4×**, a **next-line progress bar** (fills as the next
  reveal approaches, on the compressed timeline), and a **wall-clock readout** of
  the current line (`__REPLAY_T0` + `data-t`). No message counter.
- **Play** starts replay from the **topmost visible line** (via
  `elementFromPoint`); Pause freezes; Stop restores the full static log.
- **Silence compression:** `SILENCE_CAP_MS = 2000` — any idle gap in `data-t`
  longer than 2 s is compressed to 2 s so AFK stretches never stall replay; speed
  divides the already-compressed delay.
- **Scales to 100k+ lines:** a precomputed compressed virtual timeline + a single
  `requestAnimationFrame` clock (batch-reveal per frame, one scroll/frame) instead
  of a timer per line; a **sliding hide-window** (`.replay-pending`,
  `visibility:hidden`) covering ~2 viewport-heights around the cursor instead of
  hiding all lines — O(1) amortized per reveal, no full-document reflow. Uses
  `visibility` (not `display`) so the layout is stable and the scroll never jumps.
- **Scroll behaviour:** auto-follow keeps the newest line ~2 lines
  (`scroll-padding-bottom: 2lh`) off the bottom. Detects user intent via
  `wheel`/`touch`/scroll keys (never the `scroll` event, so programmatic
  auto-scroll isn't mistaken for the user): scrolling **up** detaches follow
  (keeps playing, no yank); scrolling **down** past the hidden window (an unhidden
  future line reaches the bottom) **stops** replay; scrolling back to the cursor
  re-attaches.
- Tests assert the replay layer is present and self-contained, the cap
  name/value, the `.replay-pending` visibility gate is JS-gated (lines visible by
  default), and Phase-1 `data-source`/`data-t` + full text survive.

### Frontend — `ui/src/SettingsApp.svelte`, `ui/src/settings/*`
- Logs tab export form: a **date+time range to the minute** (`datetime-local`,
  for exporting a single raid/event), an **Include sent commands** toggle, and two
  buttons — **Download .txt** and **Download .html**. Search is a separate block.
- `window.open` of the streaming endpoint (mirrors the `.txt` download) — no
  client-side assembly, no caps.
- **URL-persisted filter:** `?from&to&q` are mirrored into the URL via
  `replaceState` (preserving the `#logs` hash) so a refresh restores the view and
  tabs are shareable; a `?q=` auto-runs the search once on load.
- **Timezone-correct ranges:** `logRange.ts` — `localDayStartISO`/`localDayEndISO`
  (date) and `localDateTimeISO` (datetime) convert the viewer's LOCAL wall-clock
  to the correct UTC instant (malformed input → open bound), so "yesterday" means
  the viewer's local yesterday, not UTC's. The `tz` param is sent to
  download/export/search for server-side timestamp presentation.

## Tests
- `ansihtml` parity vs `ansi_up`; `export_html` (self-contained doc, echo
  excluded, command toggle, monotonic `data-t`, replay layer, lines-visible-by-
  default); `log_store` export source/order/buffer; session connect/echo
  exclusion; embedded-styles drift + var-resolution; `logRange` (local-day and
  local-datetime → UTC, incl. TZ-pinned and malformed cases).

## Non-Goals (unchanged)
- Client-side HTML assembly and per-request size caps (streaming makes both
  unnecessary).
- Applying client highlights/substitutions/gags (RAW only).
- Changing `.txt` export semantics.
- The password/echo-off leak — tracked in `password-echo-off-guard.md`.
