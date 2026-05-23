# Version 0.0.8.6 — Timer Declaration Layering Across Profiles (Done)

## Current State

- **Layered merge**: `session.go:302-382` (`loadProfileTimerDeclaration`) iterates profiles from lowest to highest priority. Scalar fields (icon, cycle_ms, repeat_mode) overridden by non-zero/non-empty values from higher-priority profiles.
- **Subscription merge**: `session.go:246-300` (`resolveTimerSubscriptions`) merges subscriptions from all profiles. Supports `IsRemoval` (exact command removal) and `IsBulk` (clear entire second) markers.
- **Restore**: `session.go:732-859` (`restoreTimers`) merges persisted runtime state with profile declarations. Subscriptions are always overridden by profile declarations on restore.
- **Tests**: `declaration_test.go:543-711` (`TestLayeredTimerDeclarations0086`) — 3 profiles (base/raid/boss) with icon override, exact removal, bulk removal, restore consistency.
- **Timers are session-layer** (not VM). `reloadLocked()` in `runtime.go` does not load timers. `NotifySettingsChanged("timers"|"profiles")` triggers both VM reload and timer restore.
