# SQLite Extraction Guide

Use this when you need to inspect runtime data in SQLite.

## Database location

- Main DB: `data/mudhost.db`
- Side files may exist while running: `data/mudhost.db-wal`, `data/mudhost.db-shm`
- Query `mudhost.db` directly.

## Quick start

```bash
sqlite3 "data/mudhost.db"
```

One-shot query:

```bash
sqlite3 "data/mudhost.db" "SELECT 1;"
```

## Useful tables

- `log_entries`
- `log_overlays`
- `highlight_rules`
- `substitute_rules`

Also commonly useful:

- `sessions`, `profiles`, `session_profiles`
- `trigger_rules`, `alias_rules`, `hotkey_rules`
- `variables`, `profile_variables`
- `timers`, `timer_subscriptions`, `profile_timers`, `profile_timer_subscriptions`
- `history_entries`, `events`, `clients`, `plugins`, `snapshots`

List tables:

```bash
sqlite3 "data/mudhost.db" ".tables"
```

## One-shot extraction template

```bash
sqlite3 "data/mudhost.db" "
SELECT id, stream, window_name, quote(raw_text), quote(plain_text),
       length(raw_text), length(plain_text), received_at
FROM log_entries
WHERE id IN (778712, 778713)
ORDER BY id;

SELECT id, log_entry_id, overlay_type, layer, start_offset, end_offset,
       quote(payload_json), source_type, source_id, created_at
FROM log_overlays
WHERE log_entry_id IN (778712, 778713)
ORDER BY log_entry_id, layer, id;

SELECT sp.session_id, sp.profile_id, sp.order_index,
       hr.id AS highlight_id, hr.position, hr.pattern, hr.fg, hr.bg,
       hr.bold, hr.faint, hr.italic, hr.underline, hr.strikethrough, hr.blink, hr.reverse,
       hr.enabled, hr.group_name, hr.updated_at
FROM session_profiles sp
JOIN highlight_rules hr ON hr.profile_id = sp.profile_id
WHERE sp.session_id = 1
ORDER BY sp.order_index, hr.position, hr.id;

SELECT sp.session_id, sp.profile_id, sp.order_index,
       sr.id AS sub_id, sr.position, sr.pattern, sr.replacement, sr.is_gag,
       sr.enabled, sr.group_name, sr.updated_at
FROM session_profiles sp
JOIN substitute_rules sr ON sr.profile_id = sp.profile_id
WHERE sp.session_id = 1
ORDER BY sp.order_index, sr.position, sr.id;
"
```

## Interpretation notes

- `log_entries.raw_text` is stored incoming text.
- `log_overlays` stores metadata (substitutions/gag/overlays), not final rendered highlight output.
- Final client-visible text may differ after highlight/render pipeline.

## Current database snapshot (observed)

The following row counts were observed in the current `data/mudhost.db` at inspection time and can change as runtime continues.

- `sessions`: 1
- `profiles`: 1
- `session_profiles`: 1
- `log_entries`: 779240
- `log_overlays`: 67471
- `history_entries`: 59959
- `trigger_rules`: 72
- `highlight_rules`: 8
- `substitute_rules`: 1
- `alias_rules`: 16
- `hotkey_rules`: 26
- `variables`: 16
- `profile_variables`: 15
- `timers`: 3
- `profile_timers`: 2
- `timer_subscriptions`: 3
- `profile_timer_subscriptions`: 1
- `events`: 0
- `clients`: 0
- `snapshots`: 0
- `plugins`: 0

## Operational schema quick map (observed)

- `sessions`: connection target and lifecycle (`mud_host`, `mud_port`, `status`, `last_connected_at`, `mccp_enabled`).
- `profiles`: named configuration profiles; linked to sessions through `session_profiles(order_index)`.
- `trigger_rules`: regex/pattern triggers with `command`, `enabled`, optional buffer routing.
- `alias_rules`: command templates by profile (`template`, `group_name`, `enabled`).
- `hotkey_rules`: key shortcut bindings, including mobile layout fields (`mobile_row`, `mobile_order`).
- `variables`: runtime session variables keyed by (`session_id`, `scope`, `key`).
- `profile_variables`: profile-level variable definitions (`default_value`, `description`).
- `timers` and `timer_subscriptions`: runtime timer state and per-second command hooks.
- `profile_timers` and `profile_timer_subscriptions`: profile-level timer defaults and hooks.
- `history_entries`: command/input history by session/client and `kind`.
- `events`: structured event stream with optional links to `log_entries`/`clients` and JSON payload.
- `clients`: connected client identity and activity (`client_uid`, `mode`, `last_seen_at`).
- `plugins`: plugin runtime registration (`runtime`, `status`, `capabilities_json`).
- `snapshots`: persisted JSON snapshots by `kind`.

## Useful introspection commands

Show all tables:

```bash
sqlite3 "data/mudhost.db" ".tables"
```

Show indexes and triggers:

```bash
sqlite3 "data/mudhost.db" "
SELECT name, type
FROM sqlite_master
WHERE type IN ('index', 'trigger')
ORDER BY type, name;
"
```

Inspect a table schema:

```bash
sqlite3 "data/mudhost.db" "PRAGMA table_info(trigger_rules);"
```

Get quick row counts for major tables:

```bash
sqlite3 "data/mudhost.db" "
SELECT 'log_entries', count(*) FROM log_entries
UNION ALL SELECT 'log_overlays', count(*) FROM log_overlays
UNION ALL SELECT 'history_entries', count(*) FROM history_entries
UNION ALL SELECT 'trigger_rules', count(*) FROM trigger_rules;
"
```
