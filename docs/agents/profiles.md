# Profiles & Layering Guide

Use this when you need to understand how configuration rules (aliases, triggers, variables, timers) are resolved across multiple profiles.

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
