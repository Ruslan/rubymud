# UI & Frontend Guide

Use this when you need to understand the browser-based interface, its rendering logic, or its communication with the Go backend.

## Architecture

The frontend is split into two main parts:

### 1. Main Game UI (`ui/src/main.ts`, `render.ts`)
- **Tech**: Vanilla TypeScript + Direct DOM manipulation.
- **Reason**: High-performance requirements for rendering thousands of lines of MUD output with low latency.
- **Features**:
    - **Multi-pane layout**: Users can split the screen into columns and panes.
    - **Buffer Routing**: Different types of logs (chat, combat, main) can be routed to specific panes.
    - **History & Hotkeys**: Client-side command history and key bindings.
    - **Overlays**: Renders buttons and highlights layered on top of canonical text.

### 2. Settings App (`ui/src/SettingsApp.svelte`)
- **Tech**: Svelte 5 + Vite.
- **Reason**: Easier management of complex state and forms for configuration.
- **Features**: Profile management, rule editing (aliases, triggers), and session settings.

## Communication

- **WebSocket**: Used for real-time bidirectional communication.
    - Incoming: `LogEntry` objects, status updates, event notifications, and the
      instance-wide `app_language_changed` event.
    - Outgoing: Commands, input history requests.
- **REST API**: Used for static data and settings.
    - `/api/variables`, `/api/profiles`, etc.
- **Auth**: Uses a `%TOKEN%` injection during build or a shared secret.

## Mapper Cross-Zone Navigation

The room payload exposes two distinct kinds of sparse cross-zone topology. Keep
them distinct in the canvas and cell menu:

- `se` contains directed seams and exact portals. Their menu actions use the
  resolved `to` endpoint when present; unresolved legacy seams remain visible
  but disabled so they can be inspected or repaired.
- `xz` contains directed grid edges whose exact endpoint is `z,x,y,l`. An `xz`
  contact gets its own amber navigation action even when the same room also has
  a dead `se` entry. The reverse direction exists only when the backend returns
  a separate reverse contact.

Grid navigation must match the target zone exactly after NFC normalization; it
must not use the fuzzy zone-name lookup intended for legacy seam selectors.
Blocked contacts and contacts whose exact zone or room no longer exists are not
treated as successful jumps. Async jumps use the pane load generation guard so
a late response cannot replace a newer zone selection.

## Mapper Scan Frontier

During active room recording the server may emit an ephemeral `room_scan` for
the exact confirmed position. `MapPaneController` accepts it only for the active
map set and a `valid + green + eventDescribesCell` current coordinate; movement,
confidence loss, recording stop, reconnect reset, and stale coordinates clear
it. `MapRenderer` draws cardinal results as an extended corridor ending in only
the near half of an unknown room outline, and vertical `U/D` results as compact
arrows. This layer is canvas-only presentation: it must never enter `SlimRoom`,
hit-testing, routing, selection, or map persistence. The raw server hint is
displayed without translation.

For a scan scheduled by a newly parsed room, the server reserves the probe on
the exits event but writes the command only after that room format's trailing
blank or prompt boundary. Otherwise RMUD's blank after `[ Exits: ... ]` can race
the detached writer and be mistaken for an empty scan response. When OBS must
first commit a new room, that SQLite commit and the trailing boundary may finish
in either order; a small `mapMu` rendezvous records which won and either writes
immediately or leaves the scan reserved for the boundary. Scans started later
from an already settled anchor do not need this boundary gate.

An exact player-entered scan command is captured by the same bounded parser while
OBS is active, so a command-triggered exit can become a frontier hint before the
stored room text changes. Room identity remains strict by default. A room may
persist a small `mutable_exits` direction set; only differences wholly contained
in that set may keep the exact current/predicted room green. When name and cleaned
description match but an unmarked exit differs, OBS pauses and offers the differing
directions for confirmation, never automatic mutation. The cell menu exposes the
same six-direction property editor. `U/D` scan hints use the same green frontier
color as cardinal half-room hints.

When Follow receives a position on a level absent from the browser's cached
zone (the normal autorecord order is `room_position` before `rooms_changed`),
the renderer retains that prospective level. The subsequent bounded zone reload
preserves it and exposes the new floor immediately; it must not require a page
reload or snap back to the old level.

Rare/destructive room actions live under the cell menu's collapsed **Room
properties** section. **Delete leaf room** is intentionally narrower than the MCP
room-delete tool: the server refuses the player's current coordinate and, in the
same topology transaction as deletion, refuses any room with more than one unique
existing graph neighbour (incoming and outgoing faces both count). A successful
delete stops OBS, refreshes every session sharing the set, and uses the normal
topology journal, so the visible Undo action recreates the exact room and edges.

## Obscured Mapper Rooms

A declarative room format may list exact `obscured_lines`: server phrases that
replace the room name and description when the player cannot see. A completed
block carrying one of these lines is movement evidence, not room identity. The
tracker consumes the step at yellow confidence, room recording stops without a
topology write or reconciliation conflict, and the next visible reverse step can
restore green normally. The Room Formats editor manages the markers one exact
line per row. Do not broaden them into regexes or feed them into fingerprints.

The reconciliation dialog keeps Cancel/Escape as a non-mutating close, and also
offers an explicit “Discard observation and stop OBS” action. It uses the normal
recording-stop endpoint; a failed stop leaves the immutable conflict visible.

## Mapper Room Knowledge

The cell menu action `Knowledge…` opens a room-scoped dialog. Its `Inhabitants` tab
loads lazily from `/api/map-sets/{id}/inhabitants`; the full catalogue must not
be added to the hot slim-room/zone payload. Cards and forms use DOM `textContent`
and native controls rather than HTML interpolation.
The same lazy response carries the persisted room `hint`, cleaned `desc`, and
`exits`, which Knowledge renders above the tab; full descriptions still never
ride on the hot zone payload. The room list response is a bounded
`{room, items, truncated, next_cursor}` envelope:
the UI pages through 20 entries at a time with an opaque versioned (v2)
inhabitant-id keyset cursor (the server query follows the covering room index;
the UI sorts each page for display), keeping only the current page in the DOM
while `Load more`/`Previous page` make every placement reachable. An
unversioned historical positive-id cursor (including both the old name+id
shape and the short-lived id-only shape) is rejected with
`legacy_room_inhabitant_cursor`; the UI resets its page boundary and offers a
first-page retry rather than silently changing order and skipping entries.
During rolling upgrades it also accepts the historical bare array response
(which has no continuation page).

Knowledge writes are overlays: they do not reload the zone or tracker. The
server broadcasts `room_knowledge_changed`, and only an open dialog for the
affected room refetches. Abort/generation guards preserve unsaved forms across
late GET responses and discard responses after closing or switching rooms.
Shared entity updates use one `map_knowledge_changed` event per active session;
each mapper pane refetches only its open dialog in that map set, without an
unbounded per-placement WebSocket fanout.
Entity fields are shared across placements, while `presence_text` and
`resident` belong to one room. `Place existing` searches a bounded catalogue
endpoint and attaches the selected entity by id, so shared fields are not
duplicated; entities already attached to the room are excluded. Live NPC
detection remains intentionally out of scope.
Catalog-to-placement transitions abort any in-flight room refresh and keep its
generation stale, preserving the unsaved placement form. Back completes a
pending refresh; automatic catalog closure restores focus to `Place existing`.

Cross-zone room search matches NFC/case-folded `hint + desc` on the server and
returns at most the requested bound. Results show logical coordinates and a
bounded visual excerpt of the raw persisted description so same-named rooms
remain distinguishable; the description is not translated.

## Mapper localization

The canvas mapper uses the typed, dependency-free catalog in `ui/src/map/i18n.ts`.
Construct `MapPaneController` with the server-injected boot locale; do not fetch a
locale from the game loop. RubyMUD-authored labels, statuses, tooltips, ARIA copy,
and client-side validation belong in that catalog. Room/zone names, commands,
raw MUD output, parser direction vocabulary, and server/game values passed as
interpolation parameters must never be translated.

Keep English wording backward-compatible. Add both en/ru entries with matching
placeholders, and use the catalog plural API for counts. The room Knowledge
surface has its own typed catalogue in `ui/src/map/inhabitantsI18n.ts`; it updates
marked authored nodes in place so a language change preserves focus, unsaved
form values, the current catalogue query, and pending async work. NPC fields and
other MUD data remain raw interpolation values.

Saving the instance language reloads the Settings page. After the database
commit, the server also sends one `app_language_changed` event to each active
session. The game client accepts only a supported locale, applies it to all
active mapper controllers and degraded-pane notices, retains it for panes mounted
later, and only then updates `window.APP_LANGUAGE`. Invalid live values are a
strict no-op. This path does not reconnect the game, reload a VM, or fetch locale
data at runtime.

`MapPaneController.setLocale` closes locale-bound overlays before rebuilding
static copy. Cached server-authored recording, walk, and tracker reasons cannot
be safely replayed in their old language: controls use a localized fallback,
terminal status is cleared, and cached `position_reason` is omitted until a fresh
`room_position` event restores it in the new locale. `i18nExtraction.test.ts` and
the Knowledge extraction test guard authored literals; parser direction tokens
remain on their narrow allowlists.

## Development

- **Build**: Vite compiles everything into `go/internal/web/static/`.
- **Dev Mode**: `npm run dev` in the `ui/` directory.
- **CSS**: Custom CSS variables for theme and font size.
