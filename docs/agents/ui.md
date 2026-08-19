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
    - Incoming: `LogEntry` objects, status updates, event notifications.
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

## Mapper Room Knowledge

The cell menu action `Knowledge…` opens a room-scoped dialog. Its `Inhabitants` tab
loads lazily from `/api/map-sets/{id}/inhabitants`; the full catalogue must not
be added to the hot slim-room/zone payload. Cards and forms use DOM `textContent`
and native controls rather than HTML interpolation.

Knowledge writes are overlays: they do not reload the zone or tracker. The
server broadcasts `room_knowledge_changed`, and only an open dialog for the
affected room refetches. Abort/generation guards preserve unsaved forms across
late GET responses and discard responses after closing or switching rooms.
Entity fields are shared across placements, while `presence_text` and
`resident` belong to one room. `Place existing` searches a bounded catalogue
endpoint and attaches the selected entity by id, so shared fields are not
duplicated; entities already attached to the room are excluded. Live NPC
detection remains intentionally out of scope.

## Development

- **Build**: Vite compiles everything into `go/internal/web/static/`.
- **Dev Mode**: `npm run dev` in the `ui/` directory.
- **CSS**: Custom CSS variables for theme and font size.
