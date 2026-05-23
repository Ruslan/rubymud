# Feature Request — MCP Improvements

## Context

Current MCP support exists and is useful, but it still behaves like an early experimental integration rather than a fully productized interface.

The current gaps are:

1. documentation is outdated relative to the actual tool surface
2. MCP exposure is not configurable enough for normal day-to-day use
3. tool responses do not fully account for output that arrived while the model was thinking between calls
4. multi-buffer usage is not exposed consistently through MCP text tools

This document collects the next-round MCP improvements in one place.

---

## Goals

1. make MCP safer to run in normal local usage
2. make MCP easier to configure for external clients
3. improve continuity between MCP calls so the model can see what happened since its previous interaction
4. expose buffer-aware log access for multi-buffer workflows
5. bring MCP documentation in sync with the real implementation

---

## Scope

### 1. Documentation Refresh

Add an up-to-date user/developer-facing MCP document.

Required outcomes:

1. document the `/mcp` endpoint and transport model
2. document all currently shipped MCP tools
3. document real parameters, defaults, and response shapes
4. document how Claude Code / Claude Desktop or other MCP clients should connect
5. clearly distinguish stable current behavior from future/planned behavior

Suggested target:

- `docs/mcp.md`

The old `docs/dev/version-0.0.6.4-mcp.md` should remain as historical planning context, not as the main current spec.

### 2. MCP Enable / Disable Setting

Add an explicit setting to turn MCP on or off.

Desired behavior:

1. MCP can be disabled entirely
2. MCP can be enabled intentionally when needed
3. default policy should favor safety over convenience

Recommended policy:

1. MCP disabled by default
2. when disabled, `/mcp` is not exposed for normal use

Open design question:

1. whether MCP should be controlled only by config/env, or also from Settings UI

### 3. Bearer Token Authentication

Add support for a configurable Bearer API token for MCP requests.

Desired behavior:

1. MCP requests may include `Authorization: Bearer <token>`
2. server validates the configured MCP token before serving requests
3. token should be independently configurable from existing web/session auth if needed

Recommended policy:

1. if MCP is enabled for anything beyond tightly local experimental usage, bearer auth should be available
2. if future non-local binding is allowed, bearer auth should become mandatory

Possible future extension:

1. localhost-only mode when no token is configured

### 4. Unseen Output Continuity Between Calls

Add support for continuity context so the model can learn what happened between its previous request and the current command.

This is especially useful for:

1. combat logs that arrived while the model was thinking
2. actions performed manually by the player between model turns
3. trigger-driven automation or autonomous game activity
4. chat or party updates that affect the next decision

#### Proposed API

Add `last_seen_log_id` as a recommended but optional argument on relevant tools.

Key rule:

1. clients are encouraged to pass `last_seen_log_id` explicitly
2. if omitted, MCP may fall back to the last log id previously seen by that MCP client, stored in MCP server memory
3. in-memory tracking is acceptable for this feature; it does not need durable persistence

This means the feature is:

1. explicit when the client wants full control
2. convenient by default when the client does not provide a cursor each time

#### State model

The MCP server may keep per-client in-memory state representing the latest log id already shown to that MCP client.

Notes:

1. this state is best-effort and ephemeral
2. server restart may forget it
3. explicit `last_seen_log_id` always overrides implicit remembered state
4. this is acceptable because the purpose is continuity assistance, not strict transactional guarantees

#### `mud_send_command` behavior

Enhance `mud_send_command` so that, before showing the command response window, it may include output that arrived after the client's previously seen log id but before the command was sent.

Desired behavior:

1. determine the effective `last_seen_log_id`
2. gather unseen messages between that cursor and the pre-command latest log id
3. if unseen count is small enough, prepend a block such as:
   `Messages since your last seen output:`
4. then perform current command send behavior
5. then append the existing sync response block as today
6. update remembered last-seen state after the response is returned

Suggested limits:

1. if unseen message count is `<= 1000`, include the unseen block directly
2. if unseen message count is larger, do not dump everything blindly
3. instead return a compact notice such as:
   `More than 1000 unseen messages arrived; showing the most recent subset.`

This keeps the benefit without exploding context windows.

#### Other text tools

`last_seen_log_id` may also be useful on read-oriented tools where continuity matters.

Candidates:

1. `mud_get_output`
2. `mud_get_output_range`
3. `mud_search`
4. `mud_send_command`

At minimum, `mud_send_command` is the highest-value target.

### 5. Buffer Filter for Text Tools

Add optional `buffer` support to text-oriented MCP tools.

Motivation:

1. the app already supports multiple buffers
2. MCP should be able to read only `main`, `chat`, `combat`, `buffs`, or another target buffer when desired
3. this reduces noise and helps models focus on the relevant stream

Recommended behavior:

1. add optional `buffer` argument to text retrieval tools
2. default buffer should remain `main`
3. optionally consider a future special value such as `all`, but default behavior should stay simple

Primary candidates:

1. `mud_get_output`
2. `mud_get_output_range`
3. `mud_search`
4. `mud_send_command` response filtering, if we want to scope returned output by buffer

Clarification:

1. `buffer` should filter what output is read/returned
2. `buffer` should not change where commands are sent

---

## Suggested Rollout Order

1. finish current `0.0.8.x` work first
2. complete `0.0.9.0` main planned work
3. then take MCP as a dedicated follow-up scope

Reasoning:

1. MCP improvements are valuable but are a separate product surface
2. they touch auth, configuration, tool contracts, formatting, and docs
3. mixing them into the current timer roadmap risks scope drift

---

## Acceptance Direction

This feature request should be considered satisfied only when:

1. current MCP behavior is documented in a stable doc
2. MCP can be enabled/disabled intentionally
3. bearer token auth is supported
4. `last_seen_log_id` continuity is implemented as a recommended optional parameter with in-memory fallback to the previously seen log id
5. text tools support buffer-aware reads with default `main`
6. tests cover the new MCP behaviors and defaults

---

## Non-Goals For The First Pass

1. durable persistence of per-client MCP seen-state across server restarts
2. complex access control or user identity layers beyond bearer auth
3. broad MCP protocol expansion unrelated to current MUD session workflows
