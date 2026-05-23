# Version 0.0.8.9.3 — #sub Text Substitution (Done)

## Current State

- **Storage**: `storage/substitute.go` — CRUD for substitute rules, `LoadSubstitutesForProfiles`.
- **Commands**: `#sub`, `#substitute`, `#gag`, `#unsub` in `commands_substitute.go`. Dispatched at `commands.go:144`.
- **Application**: `substitution_apply.go` — compiled regex matching, text replacement via overlays, gag support. Canonical log text preserved.
- **Tests**: `substitution_apply_test.go`, `substitution_extra_test.go`, `runtime_cache_test.go`.
