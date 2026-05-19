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

## Development

- **Build**: Vite compiles everything into `go/internal/web/static/`.
- **Dev Mode**: `npm run dev` in the `ui/` directory.
- **CSS**: Custom CSS variables for theme and font size.
