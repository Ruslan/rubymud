# MCP

`mudhost` exposes an optional Model Context Protocol endpoint for a generic MCP
client. This document is the current user and developer contract. The server
returns text tool results; clients should treat that text as the authoritative
presentation format rather than parsing it as a stable JSON data model.

## Connection

- Endpoint: `POST /mcp`.
- Transport: MCP Streamable HTTP request/response only. The server supports MCP
  protocol versions `2025-03-26` and `2025-06-18`. `initialize` echoes either
  supported requested version; an omitted or other requested version selects
  `2025-06-18`.
- The endpoint does not implement SSE, a `GET` stream, WebSocket MCP transport,
  or browser CORS. Use an MCP client that can connect to an HTTP URL and issue
  JSON-RPC requests to that URL.
- `initialize` must not carry `Mcp-Session-Id`. It returns the negotiated
  version, tools-only capabilities, server information, and a new opaque
  `Mcp-Session-Id` response header. Save and reuse that id for best-effort output
  continuity. Later methods also work without it as stateless calls; in that
  mode there is no implicit cursor, but an explicit `last_seen_log_id` still
  works. Any supplied malformed, unknown, or expired id is rejected; it is not
  a login credential. Generated ids are 64 lowercase hexadecimal characters;
  a supplied id is limited to 1-128 printable ASCII characters.
- `MCP-Protocol-Version` is optional on requests. When present it must be one
  of the two versions above. Notifications are accepted with no id and return
  `202 Accepted`; ordinary requests require a JSON-RPC id.

Well-formed JSON-RPC requests, including tool-level `isError` results, return
`200 OK`. Notifications return `202`. Invalid request JSON, headers, protocol
versions, or request/notification id shapes return `400`; a missing or wrong
bearer credential returns `401`; any `Origin` header returns `403`; disabled MCP
and unknown/expired MCP session ids return `404`; and an otherwise admissible
non-POST request returns `405` with `Allow: POST`. Authentication and Origin
checks run before the method check.

For a generic client configuration, enable HTTP/Streamable HTTP transport and
set its URL to `http://HOST:PORT/mcp`. Configure an `Authorization` header only
when a token was configured on the host:

```text
Authorization: Bearer YOUR_CONFIGURED_TOKEN
```

Do not put a real token in source control, shared client configuration, logs,
or prompt text. The MCP bearer credential is independent of the browser/basic
authentication credential.

## Enabling And Security

MCP is disabled by default. A disabled endpoint responds as not found.

```text
# Tokenless local listener:
mudhost --mcp --listen 127.0.0.1:8080

# Remote-capable listener, after a service manager injects MUDHOST_MCP_TOKEN:
mudhost --mcp --listen 0.0.0.0:8080
```

Configuration inputs, in precedence order:

1. An explicit `--mcp` / `--mcp=false` takes ownership of enabled state;
   otherwise an explicit `MUDHOST_MCP_ENABLED` does. With neither, the
   persistent Settings UI state owns it. An environment value that is not a Go
   boolean is a startup error unless the CLI flag overrides it.
2. An explicit `--mcp-token` takes ownership of the bearer credential;
   otherwise an explicit `MUDHOST_MCP_TOKEN` does. With neither, the persistent
   token digest owns it. An explicitly empty value is still an override.

Whitespace around a configured token is trimmed. When MCP is enabled, a
non-empty token uses Bearer `b64token` grammar: one or more ASCII letters,
digits, or `-._~+/`, followed only by optional `=` padding. With a token, every
MCP request needs exactly one matching `Authorization` header with the
case-sensitive `Bearer` scheme, one space, and no extra text or whitespace.
Without a token, MCP can become active only when `--listen` resolves and binds
to an explicit loopback address such as `127.0.0.1`, `::1`, or a loopback-only
`localhost`; wildcard and remote binds remain inactive. Legacy embedding code
which supplies a complete tokenless `MCPConfig{Enabled: true}` is rejected.
Prefer injecting
`MUDHOST_MCP_TOKEN` through a service manager or secret store: putting a real
`--mcp-token` value in a shell command exposes it through command history and
may expose it through the process list.

Requests carrying an `Origin` header are rejected. This is intentional: MCP is
not a browser API, and the policy protects a tokenless loopback listener from
DNS-rebinding browser requests. Do not expose a tokenless listener through a
reverse proxy or tunnel. For any remote or shared deployment, configure a
strong bearer token, restrict network access independently, and understand that
MCP tools can read game output and send game commands.

### Settings UI and precedence

The Settings UI has an `App` / `Integration: MCP` control. Its configuration is
stored as one SQLite row and applies to new requests immediately; restarting the
host is not required. The API behind it is authenticated with the regular
`X-Session-Token` browser/admin token:

- `GET /api/mcp/settings` returns state only: `enabled`,
  `requested_enabled`, `effective_enabled`, `token_configured`, listener
  exposure, and any external management reason. `enabled` is the stored value,
  `requested_enabled` includes an operator override, and `effective_enabled`
  also includes listener safety. It never returns a bearer token or its digest.
- `PATCH` (or `POST`) `/api/mcp/settings` with `{"enabled": true|false}`
  changes the database-owned enabled state. On a network-capable listener,
  enabling without a configured credential atomically generates a new 256-bit
  URL-safe bearer token and returns it in that response only. The endpoint
  requires `application/json`, a small single object with no unknown fields,
  and a same-origin `Origin` when that header is present.
- `POST /api/mcp/settings/token` generates or rotates the database credential.
  The raw credential appears only in that successful response. Copy it then;
  it cannot be fetched later. Rotation is allowed while MCP is disabled, so a
  credential can be prepared before enabling. SQLite stores only its SHA-256
  digest. Settings responses use `Cache-Control: no-store`.

Do not retry a write blindly after losing its HTTP response: the write may have
committed even though its one-time credential did not arrive. Fetch current
status, then rotate to obtain a new usable credential. A successful rotate
invalidates any credential returned by an earlier concurrent rotate.

Disabling immediately rejects new MCP calls with the ordinary `404 page not
found` response and drops all implicit MCP continuity sessions. Rotating a
credential immediately rejects the old credential and drops those sessions.
Requests are checked before their body is read and again at the dispatch
boundary. Disable or rotation completed while a request is still supplying its
body rejects it before it can create or touch MCP session state. A tool call
which already passed dispatch may complete; runtime locks are not held while a
tool, database, or network operation executes.

`--mcp`, `MUDHOST_MCP_ENABLED`, `--mcp-token`, and `MUDHOST_MCP_TOKEN` are
operator-managed overrides. An explicitly supplied enabled value owns the
effective enabled state; an explicitly supplied token owns the effective
credential. The Settings UI shows these controls as locked and leaves the
stored digest untouched while an external token is active. An explicit empty
token is also an override. With neither override, SQLite owns runtime state.
For source compatibility, an embedding which constructs `MCPConfig{Enabled:
true}` is treated as an enabled override; command-line parsing uses explicit
set markers so `--mcp=false` remains distinguishable from no flag.

At startup persisted tokenless enabled state is reconciled against the actual
configured listener. A non-loopback/wildcard listener is fail-closed until a
credential is configured; no tokenless MCP endpoint is exposed. This check does
not trust `Host`, `X-Forwarded-*`, or `Origin` headers.

`/mcp` accepts no URL query parameters. Bearer credentials are accepted only in
the `Authorization` header, so an accidentally query-encoded secret is rejected
before request logging.

## Common Conventions

Every successful `tools/call` response has one text `content` item. Expected
operational failures are normally returned as `isError: true` with explanatory
text, rather than a JSON-RPC transport error. Examples include no selected map,
lost map position, invalid topology targets, and an unavailable MUD session.

Unless stated otherwise, `session_id` is optional and defaults to the first
available MUD session. It is safer for multi-session clients to pass it
explicitly. Coordinates always use `{zone, x, y, l}`. Direction letters are
`N`, `S`, `E`, `W`, `U`, and `D`. Every advertised coordinate object requires
all four members; `zone` must be nonblank, while zero is a valid value for each
integer coordinate. Direction inputs are trimmed and accepted case-insensitively.

Text-output tools accept `buffer` as an optional exact, case-sensitive buffer
name. It defaults to `main`, must be 1-128 Unicode characters with no control
characters, and filters returned output only: it never changes where a command
is sent. `tz` is an optional IANA timezone; it defaults to the session timezone
and then UTC.

`last_seen_log_id` is an optional nonnegative visible log id on text tools. It
overrides the cursor remembered for the MCP session, MUD session, and buffer.
`mud_send_command` uses the effective cursor to select unseen pre-command
output. The three read/search tools do not turn it into a `since` filter: they
validate it, perform their documented recent/range/search query, and advance
continuity only through rows actually shown. A hidden, deleted, wrong-session,
or wrong-buffer cursor is rejected. Cursor `0` means the start of retained
visible history.

Implicit state is memory-only and is lost on host restart. An MCP session
expires after 30 minutes without a request carrying its id. The store retains
at most 512 MCP sessions and 64 MUD-session/buffer cursor streams per MCP
session; capacity pressure evicts the least recently used state. A client that
needs durable or exact control should pass its own cursor and tolerate losing
implicit state.

## Tool Inventory (32 Tools)

The following block is intentionally machine-checked against `tools/list`.

<!-- MCP-TOOL-INVENTORY: begin -->
- `mud_list_sessions`
- `mud_get_output`
- `mud_get_output_range`
- `mud_search`
- `mud_send_command`
- `mud_get_variables`
- `mud_set_variable`
- `mud_get_aliases`
- `mud_get_triggers`
- `mud_map_sets`
- `mud_map_zone`
- `mud_where`
- `mud_look_map`
- `mud_path`
- `mud_map_walk`
- `mud_map_walk_status`
- `mud_map_walk_stop`
- `mud_map_walk_resume`
- `mud_anchor_here`
- `mud_room_annotate`
- `mud_room_annotations`
- `mud_set_active_map_set`
- `mud_map_undo`
- `mud_room_upsert`
- `mud_room_link`
- `mud_room_unlink`
- `mud_room_mark_edge`
- `mud_room_set_seam_attrs`
- `mud_room_portal`
- `mud_map_move_rooms`
- `mud_map_rename_zone`
- `mud_room_delete`
<!-- MCP-TOOL-INVENTORY: end -->

`mud_room` is an accepted compatibility alias for `mud_look_map`; it is not
listed by `tools/list`. New clients should use `mud_look_map`.

## Sessions, Output, And Automation

### `mud_list_sessions`

Arguments: none. Returns one line per MUD session with id, name/host, port, and
connection status, or `No sessions found.`

### `mud_get_output`

Arguments: optional `session_id`, `buffer`, `tz`, and `last_seen_log_id`;
optional `limit` defaults to 200 when zero or negative. Returns recent visible
lines from the selected buffer with ids and rendered timestamps. This tool does
not impose a maximum positive `limit`.

### `mud_get_output_range`

Arguments: required `before_id`; optional `session_id`, `buffer`, `tz`,
`last_seen_log_id`, and `limit` (default 50 when zero or negative). Returns
visible lines with ids lower than `before_id`, for upward history pagination.

### `mud_search`

Arguments: required `query`; optional `session_id`, `buffer`, `tz`,
`last_seen_log_id`, `before_id`, `context` (default 15, minimum 0), and
`max_groups` (default 10 when zero or negative). Searches visible output and
command hints case-insensitively, returns newest matching groups with context,
and says when none match. The underlying search considers at most 50 text and
50 command-hint matches per call.

### `mud_send_command`

Arguments: required `command`; optional `session_id`, `buffer`,
`last_seen_log_id`, and `sync` seconds (default 0.5, capped at 10; zero or less
is fire-and-forget). Sends through the same command and alias pipeline as user
input. It returns unseen selected-buffer output before the command boundary,
then output received in the wait window. More than 1000 unseen lines are
represented by the most recent 200 plus a notice. Command responses are capped
at 200 lines. A `buffer` filters this response but does not route the command.

### `mud_get_variables`

Arguments: optional `session_id`. Returns active variables resolved across
profile layers, showing the effective value or marking it as `(default)` or
`(unset)`.

### `mud_set_variable`

Arguments: required `key` and `value`; optional `session_id`. Sets that
session's variable override and returns a confirmation. Values participate in
alias and command `$name` substitution.

### `mud_get_aliases`

Arguments: optional `session_id`. Returns active aliases resolved across active
profiles, including their templates and expansion conventions.

### `mud_get_triggers`

Arguments: optional `session_id`. Returns active triggers resolved across active
profiles, including pattern, command, and flags.

## Maps And Position

Map tools use the active map set selected for the MUD session unless an argument
says otherwise. Read responses use the materialized topology, not a guessed
visual grid. They may return `isError: true` where no active set, live tracker,
or safe position exists.

### `mud_map_sets`

Arguments: optional `session_id`. Returns imported map sets with id, name, zone
count, room count, and the active marker. It explicitly reports no active set,
including a dangling selection.

### `mud_map_zone`

Arguments: required `zone`; optional `session_id`, `map_set`, and `limit`
(default 300, maximum 1000). Returns bounded compact room records for a zone.
Each record includes basic fields and only non-default materialized topology:
manual/parsed grid faces, exact seam selectors, provenance, and directed edge
attributes. It reports room and per-room topology truncation; an empty zone is
a normal response.

### `mud_where`

Arguments: optional `session_id`. Returns tracker position and confidence:
`green` anchored, `yellow` tracker-only dead reckoning, or `red` lost. It also
includes pending move count, active set, coordinate, tag/hint, and death-trap
or pipe state. Pending moves and confidence are independent.

### `mud_look_map`

Arguments: optional `session_id`. Returns the current room plus each outgoing
grid edge and seam from materialized topology. Edge output distinguishes source,
passability, resolution, target when known, and non-default attrs. Seam output
preserves its exact occurrence `order`, `target_zone`, nullable `target_tag`, and
`command` selector; malformed imported seams are shown explicitly. `mud_room` is its legacy
alias.

### `mud_path`

Arguments: optional `session_id` and optional `from:{zone,x,y,l}`; provide one
target form: `to:{zone,x,y,l}`, `to_zone`, or `to_hint`. If a caller supplies
several despite that contract, precedence is `to`, then `to_hint`, then
`to_zone`. Returns an ordered command route, command count, cost, seam
crossings, pipe-run cell annotations, and known death-trap or door warnings.
Without `from`, a red/lost tracker position is a soft error. `to_zone` and
`to_hint` route to a lowest-cost matching room, so use `to` when ambiguity is
unacceptable.

Routes minimize directed edge weight, whose default is 1. `no_route=true`
vetoes planning but does not make an edge unwalkable; the response says when a
route was prevented only by vetoes. A traversal `phrase` replaces the direction
command. Door edges can add a preceding opening action; `locked` is reported,
not an automatic refusal. An edge-specific `open_cmd` wins; otherwise the
session's room-format profile may supply a template, or no opening command is
known.

### `mud_map_walk`

Arguments: required `to:{zone,x,y,l}` with all four fields; optional
`session_id`. Starts or replaces a server-owned map walk. The host plans and
sends one hop at a time, waiting for confirmation. Do not send route directions
with `mud_send_command` while it is active; manual and MCP movement share an
in-flight-hop barrier. It intentionally accepts only exact targets. Returns the
walk state fields below; unsafe starts, including lost/pending state, use soft
errors. Already at the target is a successful no-op.

### `mud_map_walk_status`

Arguments: optional `session_id`. Returns the current snapshot:
`active`, `state`, completed and total hops, `reason`, `can_resume`, transport
`epoch`, `job`, and `revision`. A stopped route is a normal result. Terminal
transition detail belongs to the transition response/event; status retains only
the high-water identity once a job is gone.

### `mud_map_walk_stop`

Arguments: optional `session_id`. Cancels the current walk and returns the same
state snapshot. An already-sent hop may finish its confirmation before stopped
state takes effect. Repeated stop after a terminal route is an idempotent normal
result.

### `mud_map_walk_resume`

Arguments: optional `session_id`. Replans a paused walk from the latest green
position and returns its state snapshot. It soft-fails without a paused route,
while a move is pending, or while the tracker is lost.

### `mud_anchor_here`

Arguments: required `zone`, `x`, `y`, `l`; optional `session_id`. Sets the
tracker position to recover after teleport, following, or loss. Returns the
new position and confidence; an unmapped cell becomes yellow rather than an
error.

## Map Annotations And Editing

Topology edits use the session's active set. Editing an imported/frozen set
creates an editable copy on write and switches the session to it; successful
write responses include `forked_to`. `mud_map_undo` is in memory and per map
set, so it is lost on host restart and cannot undo a write while another set is
active. Annotation edits are different: they are overlays, edit imported sets
in place, and never fork.

### `mud_room_annotate`

Arguments: required `zone`, `x`, `y`, `l`; optional `session_id` and any of
`dt`, `hazard`, `note`, `battle_log`, `author`. Partial overlay update: omitted
fields stay unchanged; `false` clears `dt`, and an empty supplied string clears
a text field. Text is capped at 2048 characters with a returned truncation
note. Returns the saved annotation. It may intentionally create a dangling
annotation for an unmapped logical cell; no active set is a soft error.

### `mud_room_annotations`

Arguments: optional `session_id` and `zone`. Returns annotations for all zones
of the active set, or only the selected zone, with coordinates, fields, author,
and UTC update time.

### `mud_set_active_map_set`

Arguments: required positive `map_set`; optional `session_id`. Selects an
existing set and rebuilds the live tracker's index. Returns the selected id and
name; an unknown/nonpositive id is a soft error.

### `mud_map_undo`

Arguments: optional `session_id`. Reverses the most recent topology write on
the active set using the shared write path. Returns the restored target or
`Nothing to undo`; unavailable active set or an unrestoreable target is a soft
error.

### `mud_room_upsert`

Arguments: required `zone`, `x`, `y`, `l`; optional `session_id` and partial
fields `hint`, `desc`, `exits`, `edirs`, `ch`, `is_dt`, `pipe`, `imageindex`,
`note`, `dx`, `dy`, `dl`. Creates or partially updates the room. On update,
omitted fields are preserved; on create they receive defaults. `exits` (display
text) or `edirs` (canonical direction list) rewrites exits, directions, and
connectivity together; `exits` wins if both are supplied. The fingerprint is
recomputed. Returns confirmation and optional fork/truncation notes.
The `hint`, `desc`, and `note` fields are each truncated to 2048 Unicode
characters with a returned note.

### `mud_room_link` And `mud_room_unlink`

Arguments: required `zone`, `x`, `y`, `l`, `dir`; optional `session_id`.
`mud_room_link` adds a parsed exit and `mud_room_unlink` removes it. If the grid
neighbor exists, the reverse exit is changed too; otherwise the operation is
one-sided. These change the room exit layer and can later be overwritten by a
room re-record/upsert. Both return a confirmation or a soft error for bad
direction, missing room, or no active set.

### `mud_room_mark_edge`

Arguments: required `zone`, `x`, `y`, `l`, `dir`; optional `session_id`,
`mark`, `one_way`, `weight`, `phrase`, `door`, `locked`, `open_cmd`, and
`no_route`. `mark` is `open`, `blocked`, or `auto` and is optional when setting
attributes only. `open` adds a hand-drawn hidden passage; `blocked` negates one;
`auto` returns the face to derived room exits and drops its attrs. Marks affect
both directed sides unless `one_way=true`; both neighboring rooms must exist.

Attributes apply to exactly one directed face and persist across re-recording.
`weight` must be positive and finite (default routing weight is 1); `phrase`
replaces the route direction command; `door`, `locked`, and `open_cmd` describe
door handling; `no_route` vetoes routing but leaves traversal valid. Empty
`phrase` clears it and empty `open_cmd` restores profile fallback. A bare
direction is rejected for `phrase` and `open_cmd` because it would corrupt
tracker movement. The call can combine a mark and attributes, creating separate
undo entries. It returns a soft error if there is no passage for attributes or
no neighboring room for a mark.

### `mud_room_set_seam_attrs`

Arguments: required source `zone`, `x`, `y`, `l` and complete seam selector
`target_zone`, `target_tag`, `command`; optional exact occurrence `order`, optional
`session_id`, and at least one of `weight`, `phrase`, `door`, `locked`, `open_cmd`,
`no_route`. The selector
must identify exactly one existing automap seam. Attributes have the same
directed routing semantics and validation as `mud_room_mark_edge`, but do not
create or delete seams. Successful writes are manual seam ownership, survive
room re-recording, are undoable, and may fork a frozen set. Missing or ambiguous
seams are soft errors.

### `mud_room_portal`

Arguments: required `action` (`create`, `update`, or `delete`) and source
`zone`, `x`, `y`, `l`; optional `session_id`. A portal is a directed exact-target
jump (`kind=seam`), not a grid direction and not a symbolic legacy zone/tag
seam. `create` requires `target_zone`, `target_x`, `target_y`, `target_l`, and a
nonblank raw `command`; the target room must exist and may be in the same zone.
`update` and `delete` require stable source occurrence `order`; update can patch
the exact target, command, and any routing attrs without erasing omitted fields.
Orders are intentionally sparse after deletion. Exact portals are manual-owned,
survive room re-recording, fork frozen sets, and are reversible with
`mud_map_undo`; absent endpoints/occurrences are soft errors.

### `mud_map_move_rooms` And `mud_map_rename_zone`

`mud_map_move_rooms` takes source/target zone names and up to 8,000 selected
`{x,y,l}` cells. `mud_map_rename_zone` selects every room in its non-empty
source server-side and refuses zones larger than the same 8,000-room bound.
Use `dry_run:true` first: it is read-only and returns an aggregate report with a
topology revision. Apply with that `expected_revision`; a changed topology is a
soft stale-revision error. Coordinates, room images, annotations, incident grid
edges, and exact portals move atomically. Boundary grid contacts become manual
authoritative cross-zone edges, so later room recording cannot erase them. Both
operations fork frozen sets, preserve the tracker position when its exact room
moved, cancel active map-walk planning, and have one `mud_map_undo` entry.

### `mud_room_delete`

Arguments: required `zone`, `x`, `y`, `l`; optional `session_id`. Deletes the
room and returns confirmation, possibly with `forked_to`. Logical-coordinate
annotations and images can remain dangling by design. `mud_map_undo` restores
the room's fields, exits, and fingerprint. No active set or missing room is a
soft error.
