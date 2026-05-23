# Version 0.0.8.1 — Default Session-Shared Ticker (Done)

## Current State

- **Commands**: `#ticksize {sec}`, `#tickon`, `#tickoff`, `#tickset`, `#tickset {sec}` — default timer name `ticker`, 60s default cycle.
- **Behavior**: Session-scoped, cyclic (`cycle_ms > 0`), never negative. `#tickset` resets to cycle. `#tickon`/`#tickoff` enable/disable.
- **UI**: Ticker pill in bottom toolbar (`index.html:44`), shows remaining seconds. Inactive dimmed.
- **Restore**: State included in WebSocket restore payload, browser renders local countdown from `next_tick_at` + `cycle_ms`.
- **Trigger sync**: `#action {pattern} {#tickset}` works through standard VM pipeline.
- **Architecture**: Runtime state is a map keyed by name (ready for named expansion).
