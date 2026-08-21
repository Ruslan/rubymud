# Profiles & Layering Guide

Use this when you need to understand how configuration rules (aliases, triggers, variables, timers) are resolved across multiple profiles.

## Not to be confused with: mapper profiles (room formats)

`mapper_profiles` is a **different entity** that merely shares the word. It describes one server's ROOM TEXT FORMAT for the mapper (exits regex, block boundaries, left-column strip, header rule, and optional server-specific scan command/prefixes) and is **not layered**: a session selects exactly one via `sessions.mapper_format`, the same deliberate exception the mapper's `active_map_set_id` makes. The rationale is identical — a room format is a property of the MUD you are connected to, not of the user's alias/trigger stack, so there is nothing sensible to merge across layers. See `go/internal/mapper/profile.go` and `docs/agents/sqlite.md`.

The optional mapper `scan` contract is intentionally narrow. During active
single-step recording, a confirmed frontier room may send the configured command
once per visit and capture only the configured `N/E/S/W/U/D` line prefixes,
bounded by the format's existing block limit/boundaries and a short timeout.
Results are ephemeral UI hints: they are never persisted and never participate
in room fingerprints or reconciliation. The bounded response is isolated from
the room detector; any later game command cancels publication and the remaining
response is discarded through its boundary. A missing `scan` object disables the feature.

Everything below is about the layered configuration profiles only.

## Layering Logic

A session in `rubymud` can have multiple active profiles. This is a "layered" system where rules from higher-priority profiles can override or augment rules from lower-priority ones.

- **Primary Profile**: The main profile attached to the session (highest priority).
- **Base Profiles**: Additional profiles (e.g., shared macros, global settings) attached to the session with a specific order.

### Resolution Order
The system resolves rules by iterating through profile IDs in a specific order (usually from highest priority to lowest, or vice versa depending on the context).
- **Variables**: The value in the primary profile wins.
- **Aliases/Triggers**: All active rules from all layers are checked. If there's a conflict, the one from the higher-priority profile usually takes precedence.
- **Timers**: Scalar fields (icon, cycle) are resolved by "later profile wins" (highest priority).

## Critical Implementation Rule
When adding any new entity or logic that depends on configuration, **always consider layering**. Never assume there is only one profile. Use `GetOrderedProfileIDs` from the storage layer to get the correct stack of profiles.
