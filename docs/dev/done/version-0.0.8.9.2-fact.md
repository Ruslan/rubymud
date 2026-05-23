# Version 0.0.8.9.2 — Highlights UI (Done)

## Current State

- **Color registry**: `go/internal/colorreg/colorreg.go` — 15 named canonical colors + aliases. Tested in `colorreg_test.go`.
- **API endpoint**: `GET /api/colors` at `server.go:373`.
- **Settings UI**: Color picker in `SettingsApp.svelte:1291-1322` — `<select>` for named colors + hex/`256:X` fallback. `colorValueToHex()` resolves all formats.
