# SQLite Schema

## Goal

This document defines the first serious SQLite schema for the Go runtime.

It is intentionally not a direct port of the Ruby schema.

The new schema must support:

1. persistent MUD sessions
2. browser reconnect and restore
3. structured logs as first-class data
4. future full-text search
5. trigger/plugin enrichments such as buttons, styling, and annotations
6. plugin and event-driven architecture

## Core Design Decision

Logs are not just text blobs.

Each log entry should be stored as:

1. a raw message record
2. optional derived metadata
3. optional overlays/enrichments layered on top of the raw text

This is similar to the idea used in chat systems where the canonical message is stored once and formatting or actions are attached separately.

## Why This Is Better Than Storing Final Rendered Output

If we store only final rendered output:

1. we lose a clean canonical source
2. search becomes harder or less trustworthy
3. we couple storage to one UI rendering model
4. plugin-generated actions become harder to inspect, diff, and re-render

If we store raw text plus overlays:

1. the original message remains stable
2. rendering can evolve without rewriting old rows
3. search can target canonical text
4. enrichments can be added by plugins without mutating the original message
5. multiple clients can render the same message differently if needed

## Can SQLite Handle This?

Yes.

SQLite is a good fit for this design if we keep the model pragmatic.

Why it is fine:

1. the data is append-heavy, which SQLite handles well
2. one local runtime means low write contention
3. JSON payload columns are fine for flexible metadata
4. FTS5 can be used for full-text search over canonical text
5. the expected scale for a local MUD client is reasonable for SQLite

The main rule is:

1. keep canonical columns queryable
2. use JSON for flexible details, not for every important field

## High-Level Model

The schema is divided into these concepts:

1. sessions
2. clients
3. log entries
4. log overlays
5. history entries
6. variables
7. events
8. snapshots
9. plugins

## Canonical Principle For Logs

Each visible message in the client should have one canonical `log_entries` row.

That row stores the source text and stable metadata.

Anything that decorates or augments the message belongs in `log_overlays`.

Examples of overlays:

1. underline characters 5 through 10
2. attach one or more buttons
3. annotate a regex match
4. route the line to a named window
5. mark a span as clickable
6. attach plugin-produced semantic tags

## Tables

### 1. sessions

One runtime MUD session.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `name TEXT`
3. `mud_host TEXT NOT NULL`
4. `mud_port INTEGER NOT NULL`
5. `status TEXT NOT NULL`
6. `created_at TEXT NOT NULL`
7. `updated_at TEXT NOT NULL`
8. `last_connected_at TEXT`
9. `last_disconnected_at TEXT`

Notes:

1. `status` can be `new`, `connecting`, `connected`, `disconnected`, `error`
2. v1 can run one session, but the schema should not hardcode that

Indexes:

1. `INDEX sessions_status_idx(status)`
2. `INDEX sessions_updated_at_idx(updated_at)`

### 2. clients

Browser or controller attached to a session.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `client_uid TEXT NOT NULL`
4. `display_name TEXT`
5. `mode TEXT NOT NULL`
6. `user_agent TEXT`
7. `created_at TEXT NOT NULL`
8. `last_seen_at TEXT NOT NULL`

Notes:

1. `mode` can be `controller`, `spectator`, `unknown`
2. `client_uid` should be stable enough to identify one browser/device session

Indexes:

1. `INDEX clients_session_id_idx(session_id)`
2. `UNIQUE INDEX clients_session_uid_idx(session_id, client_uid)`

### 3. log_entries

Canonical visible output line or message fragment.

This is the central table for output history.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `stream TEXT NOT NULL`
4. `window_name TEXT`
5. `raw_text TEXT NOT NULL`
6. `plain_text TEXT NOT NULL`
7. `raw_bytes BLOB`
8. `encoding TEXT`
9. `source_type TEXT NOT NULL`
10. `source_id TEXT`
11. `received_at TEXT NOT NULL`
12. `created_at TEXT NOT NULL`

Meaning:

1. `raw_text`: canonical text as normalized by transport layer
2. `plain_text`: text without ANSI/control decorations, ready for search and matching
3. `raw_bytes`: optional original packet bytes for debugging or future reprocessing
4. `stream`: usually `mud`, later can also be `local`, `plugin`, `system`
5. `window_name`: default window or named target window
6. `source_type`: `mud`, `plugin`, `system`, `replay`

Notes:

1. `raw_text` and `plain_text` should not be length-limited to 255
2. `raw_bytes` is optional in v1 but useful when transport edge cases matter

Indexes:

1. `INDEX log_entries_session_id_idx(session_id)`
2. `INDEX log_entries_session_created_idx(session_id, created_at)`
3. `INDEX log_entries_session_window_created_idx(session_id, window_name, created_at)`
4. `INDEX log_entries_source_idx(session_id, source_type, created_at)`

### 4. log_overlays

Derived enrichments layered over one canonical log entry.

This is the key table that makes logs first-class structured objects.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `log_entry_id INTEGER NOT NULL`
3. `overlay_type TEXT NOT NULL`
4. `layer INTEGER NOT NULL DEFAULT 0`
5. `start_offset INTEGER`
6. `end_offset INTEGER`
7. `payload_json TEXT NOT NULL`
8. `source_type TEXT NOT NULL`
9. `source_id TEXT`
10. `created_at TEXT NOT NULL`

Overlay examples:

1. `style_span`
2. `button`
3. `annotation`
4. `link`
5. `route`
6. `tag`
7. `match`

How offsets work:

1. `start_offset` and `end_offset` are character offsets into `plain_text`
2. for overlays that do not target a span, offsets may be `NULL`
3. buttons can be attached to the whole line with `NULL` offsets or to a specific span if needed

Examples of `payload_json`:

For a style span:

```json
{
  "style": "underline",
  "css_class": "target-highlight"
}
```

For a button:

```json
{
  "label": "пнуть",
  "command": "пнуть крыса",
  "hotkey": "alt+1"
}
```

For a route overlay:

```json
{
  "window": "battle",
  "copy": true
}
```

Notes:

1. multiple overlays may point to one `log_entry`
2. overlays are additive by default
3. `layer` controls deterministic render ordering if needed

Indexes:

1. `INDEX log_overlays_log_entry_id_idx(log_entry_id)`
2. `INDEX log_overlays_type_idx(overlay_type)`
3. `INDEX log_overlays_source_idx(source_type, source_id)`

### 5. history_entries

Command history for the player and other client-originated actions.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `client_id INTEGER`
4. `kind TEXT NOT NULL`
5. `line TEXT NOT NULL`
6. `created_at TEXT NOT NULL`

Kinds:

1. `input`
2. `key`
3. `button`
4. `plugin`

Indexes:

1. `INDEX history_entries_session_created_idx(session_id, created_at)`
2. `INDEX history_entries_session_kind_created_idx(session_id, kind, created_at)`

### 6. variables

Current persisted variable state.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `scope TEXT NOT NULL`
4. `key TEXT NOT NULL`
5. `value TEXT`
6. `updated_at TEXT NOT NULL`

Notes:

1. `scope` can be `session`, `profile`, `plugin:<name>`

Indexes:

1. `UNIQUE INDEX variables_session_scope_key_idx(session_id, scope, key)`

### 7. events

Append-only event stream for runtime behavior.

This is not full event sourcing, but it is the durable event log.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `kind TEXT NOT NULL`
4. `source_type TEXT NOT NULL`
5. `source_id TEXT`
6. `related_log_entry_id INTEGER`
7. `related_client_id INTEGER`
8. `payload_json TEXT NOT NULL`
9. `created_at TEXT NOT NULL`

Examples:

1. `line.received`
2. `command.input`
3. `command.sent`
4. `client.attached`
5. `client.detached`
6. `plugin.started`
7. `plugin.failed`
8. `timer.fired`

Indexes:

1. `INDEX events_session_created_idx(session_id, created_at)`
2. `INDEX events_session_kind_created_idx(session_id, kind, created_at)`
3. `INDEX events_related_log_idx(related_log_entry_id)`

### 8. snapshots

Compact state snapshots for faster restore.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `kind TEXT NOT NULL`
4. `payload_json TEXT NOT NULL`
5. `created_at TEXT NOT NULL`

Kinds:

1. `buffers`
2. `recent_windows`
3. `runtime_state`

Indexes:

1. `INDEX snapshots_session_kind_created_idx(session_id, kind, created_at)`

### 9. plugins

Known plugin processes and their runtime state.

Columns:

1. `id INTEGER PRIMARY KEY`
2. `session_id INTEGER NOT NULL`
3. `name TEXT NOT NULL`
4. `runtime TEXT NOT NULL`
5. `status TEXT NOT NULL`
6. `capabilities_json TEXT`
7. `updated_at TEXT NOT NULL`
8. `created_at TEXT NOT NULL`

Indexes:

1. `UNIQUE INDEX plugins_session_name_idx(session_id, name)`

## Full-Text Search

Yes, logs should support full-text search.

Recommended approach:

1. keep canonical text in `log_entries.plain_text`
2. add an FTS5 virtual table for search
3. keep the FTS index synced from `log_entries`

Suggested FTS table:

1. `log_entries_fts`
   - `plain_text`
   - `content='log_entries'`
   - `content_rowid='id'`

Search should target canonical `plain_text`, not overlay payloads.

If later needed, tags or semantic annotations can be indexed separately.

## Why The Overlay Model Is Good

Your idea is good.

Specifically, storing one canonical message and layering formatting or actions on top is the right direction here.

Why:

1. it preserves the original record
2. it matches the way triggers and plugins enrich output
3. it avoids rewriting historical messages when rendering rules change
4. it supports search over canonical text
5. it supports UI features like buttons, underlines, and semantic hints cleanly

This is stronger than storing only plain text logs and stronger than storing only pre-rendered HTML.

## What Not To Do

1. do not store only final HTML
2. do not store only a single `logs` table with mixed text and JSON fields for everything
3. do not make overlays the canonical source of text
4. do not hide every queryable field inside JSON

## Pragmatic v1 Recommendation

For the first Go storage implementation, start with:

1. `sessions`
2. `clients`
3. `log_entries`
4. `log_overlays`
5. `history_entries`
6. `variables`

Then add:

1. `events`
2. `snapshots`
3. `plugins`

This keeps the first storage milestone focused on visible product value while preserving the long-term architecture.

## Recommended New DB File

Use a new DB file for the Go runtime:

1. `data/mudhost.db`

Do not reuse the old Ruby DB schema as the long-term schema.

Keep the Ruby DB as:

1. legacy reference
2. optional import source later

## Summary

Recommended design:

1. new SQLite database for Go
2. canonical `log_entries`
3. additive `log_overlays`
4. FTS over canonical `plain_text`
5. events and snapshots beside, not instead of, the log tables

This gives the project a much stronger foundation for:

1. browser restore
2. plugin-generated UI
3. structured logs
4. search
5. future LLM features
