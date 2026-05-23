# Version 0.0.8.4.2 — Mobile Hotkey Layout (Done)

## Current State

- **DB columns**: `mobile_row` / `mobile_order` on `hotkey_rules` (migration `003_hotkey_mobile_layout.sql`).
- **Model**: `HotkeyRule.MobileRow`/`MobileOrder` in `types.go:126-134`, `HotkeyJSON.MobileRow`/`MobileOrder` in `session/types.go:36-41`.
- **Storage**: `CreateHotkey`/`UpdateHotkey` in `hotkey.go` accept mobile params.
- **UI rendering** (`render.ts:768-847`): On mobile (`<=640px`), groups hotkeys by `mobile_row`, sorts by `mobile_order`, auto-fills free hotkeys into existing rows if space permits.
- **CSS** (`main.css:758-784`): Mobile media query — larger touch targets (34px min height, 44px min button), hides `<kbd>` hints.
- **Settings UI** (`SettingsApp.svelte:1398-1400`): Editor fields for Mob Row / Order.
- **Export/import** (`profile_script.go:202-208`): `mobile_row`/`mobile_order` in `#nop rubymud:rule` metadata.
