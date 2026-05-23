# Version 0.0.6.2 — Profile Export/Import (Done)

## Current State

- **`profile_script.go`**: `ExportProfileScript`, `ParseProfileScript`, `ImportProfileScript` — serializes/deserializes `.tt` format with aliases, triggers, highlights, substitutes, hotkeys, timers, declared variables.
- **Endpoints**: `POST /api/profiles/export/all`, `POST /api/profiles/import/all`, `POST /api/profiles/import`, `GET /api/profiles/files`, `POST /api/profiles/{id}/export`.
- **`--config-dir` flag** in `main.go` defaults to `config/` next to DB. Directory auto-created.
- **`#hotkey` cmd**: registered in `commands.go:150`, handler in `commands_alias_variable.go:142`.
- **UI**: Export/Import buttons in `SettingsApp.svelte:859-902`, "Files in config/" section with per-file Import.
- **Tests**: API tests in `api_test.go:463-524`.
- **`.tt` format**: `#nop Profile:`, `#nop rubymud:rule {json}` metadata, standard TinTin-compatible lines.
