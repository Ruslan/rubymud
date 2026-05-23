# Version 0.0.6.1 — Profiles Feature (Done)

## Current State

- **Structs**: `Profile`, `SessionProfile`, `HotkeyRule` in `types.go:112-126`.
- **Profile CRUD**: `profile.go` — 15 functions: `ListProfiles`, `CreateProfile`, `GetProfile`, `UpdateProfile`, `DeleteProfile`, `GetOrderedProfileIDs`, `GetSessionProfiles`, `GetPrimaryProfileID`, `AddProfileToSession`, `RemoveProfileFromSession`, `ReorderSessionProfiles`, `GetSessionIDsForProfile`, `EnsureProfileInSession`, `EnsureSessionProfiles`, `BackfillMissingSessionProfiles`.
- **Hotkey CRUD**: `hotkey.go` — profile-scoped List/Create/Update/Delete/LoadForProfiles.
- **ProfileID on rules**: `AliasRule`, `TriggerRule`, `HighlightRule` all have `ProfileID` + `LoadXxxForProfiles(profileIDs []int64)`.
- **VM reload**: `runtime.go:65` calls `GetOrderedProfileIDs` then loads rules per profile in priority order.
- **API routes**: `GET/POST /profiles`, `PUT/DELETE /profiles/{id}`, `/profiles/{id}/aliases|triggers|highlights|subs|hotkeys|timers|groups|variables`, `/sessions/{id}/profiles`.
- **UI**: Profile selector in `SettingsApp.svelte`, tab for Profiles, profile assignment per session.
- **No Go-level MigrateToProfiles**: SQL versioned migrations used instead.
