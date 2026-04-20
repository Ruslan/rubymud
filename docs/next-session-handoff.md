# Next Session Handoff

## Current Decision

We are not evolving the current Ruby app as the long-term runtime.

The chosen direction is:

1. Build a local-first Go session host.
2. Keep the browser UI model.
3. Keep SQLite.
4. Move most user scripting into out-of-process plugins.
5. Ship the first Go version with a Ruby compatibility plugin so scripting power is not lost.

## Main Product Shape

Target product:

1. A Go runtime that runs locally, including on Windows as a `.exe`.
2. The runtime serves a browser client over localhost.
3. One live MUD session can have multiple attached browser clients/devices.
4. Plugins may be written in Ruby first, then JavaScript and later other languages.

Important product angle already present in the Ruby version:

1. Shared live session.
2. Multi-device attach.
3. Persistent log/history.
4. Trigger-generated buttons.
5. Browser-based control of one character from multiple devices.

This is a differentiator versus classic desktop clients.

## Current Active Milestone

Current active milestone: `Iteration 1`.

Iteration 1 is not feature parity. It is the smallest Go runtime that proves the shared-session architecture.

Iteration 1 scope:

1. one Go binary
2. one direct TCP connection to a MUD
3. one HTTP server
4. one WebSocket endpoint
5. one shared live session
6. multiple attached browser clients
7. MUD output broadcast to all clients
8. browser input sent back to the same MUD session

Iteration 1 explicitly excludes:

1. plugins
2. SQLite
3. aliases
4. triggers
5. buttons
6. windows/buffers
7. history restore
8. reconnect logic
9. multi-session
10. external mux

Why no mux in Iteration 1:

1. The Go host itself already acts as the session host.
2. One TCP connection plus many browser attachments is the behavior we want to prove first.

Iteration 1 success means:

1. Go process starts.
2. Browser opens localhost UI.
3. MUD output appears.
4. Browser input reaches the MUD.
5. A second browser tab sees the same live stream.

## Files To Read First Tomorrow

Read these first before doing any design or code work:

1. `docs/go-migration-plan.md`
2. `docs/next-session-handoff.md`
3. `server.rb`
4. `server/web_server.rb`
5. `server/mud_client.rb`
6. `server/game_engine/player_commands.rb`
7. `server/game_engine/server_commands.rb`
8. `server/game_engine/vm.rb`
9. `public/index.html`

## What Exists In The Ruby App Today

The current Ruby app already does all of this:

1. Runs a browser client over WebSocket.
2. Holds one shared live MUD connection in the backend.
3. Lets more than one browser attach to the same session.
4. Stores logs and input history in SQLite.
5. Supports variables, aliases, regex triggers, buttons, and window routing.
6. Restores history and logs when a browser reconnects.

Important implementation detail:

1. The current app is effectively `one MUD session runtime -> many browser clients`.
2. This should be preserved in the Go design.

## Important Problems In The Ruby App

These are not to be copied directly into Go:

1. Global mutable config class.
2. Unsafe reload behavior.
3. Tight coupling between runtime and arbitrary Ruby execution.
4. Weak separation of transport, state, UI sync, and scripting.
5. Hard distribution story for Windows users.

## Chosen Architecture Direction

### Go Core Owns

1. TCP/Telnet transport.
2. Browser HTTP/WebSocket server.
3. Shared session runtime.
4. Attached client management.
5. SQLite persistence.
6. Event bus.
7. Timer scheduler infrastructure.
8. Plugin lifecycle.
9. Effect execution.
10. Session restore behavior.

### Plugins Own

1. User aliases.
2. User triggers.
3. User variable logic.
4. TTS.
5. LLM features.
6. Most custom automation.
7. Optional timer logic built on top of core scheduler support.

Important nuance:

1. Scheduler infrastructure should stay in Go.
2. Plugin logic may request timers.
3. Timer firing comes back as an event.

## Storage Decision

Keep `SQLite`.

Reason:

1. Already used.
2. Good for Windows local execution.
3. Single-file DB.
4. Good fit for history, events, variables, and snapshots.

Do not switch storage stacks unless there is a concrete need.

Likely Go DB approach:

1. `database/sql` or `sqlx`
2. explicit SQL
3. explicit migrations

Avoid defaulting to GORM unless a concrete benefit appears.

## Event Model Decision

We want typed events, not only raw text pipes.

But we do not want full event sourcing in v1.

Chosen direction:

1. In-memory current state.
2. Persisted event log.
3. SQLite projections/tables for fast reads.
4. Optional snapshots.

This is event-driven, but not pure replay-only architecture.

## Plugin Runtime Decision

Do not embed Ruby inside Go.

Chosen direction:

1. Out-of-process plugins.
2. Stable protocol.
3. Restartable plugins.
4. Core survives plugin crashes.

Preferred transport order:

1. `stdio` subprocess protocol first.
2. Unix sockets / named pipes later.
3. TCP only if remote plugins become necessary.

## First Release Constraints

The first Go release should be considered successful only if:

1. It runs locally as a Windows `.exe`.
2. It serves the browser UI.
3. It keeps one live MUD session.
4. More than one browser can attach to the same session.
5. SQLite persists log/history.
6. It supports out-of-process plugins.
7. It ships with a Ruby compatibility plugin path.

## Why Ruby Compatibility Plugin Matters

The Ruby implementation itself is not the main asset.

The real asset is the scripting behavior users already rely on:

1. aliases
2. triggers
3. variables
4. buttons
5. windows
6. local output helpers

If the first Go version ships without this power, it will feel weaker than the current app.

## Immediate Next Documents To Write

When continuing, write these next:

1. `docs/go-architecture.md`
2. `docs/plugin-protocol.md`
3. `docs/sqlite-schema.md`

## What `docs/go-architecture.md` Should Contain

Define the Go runtime modules.

Expected sections:

1. package layout
2. session lifecycle
3. websocket client lifecycle
4. mud connection lifecycle
5. event bus responsibilities
6. timer scheduler responsibilities
7. plugin host responsibilities
8. storage layer responsibilities

## What `docs/plugin-protocol.md` Should Contain

Define the protocol between Go core and plugins.

Expected sections:

1. plugin startup handshake
2. capability declaration
3. event envelope JSON
4. effect envelope JSON
5. lifecycle messages
6. error handling
7. restart behavior
8. minimum Ruby plugin support target

## What `docs/sqlite-schema.md` Should Contain

Define the first-pass schema.

Expected tables:

1. `sessions`
2. `clients`
3. `events`
4. `history_entries`
5. `variables`
6. `snapshots`
7. `plugins`

## First Engineering Milestone After Docs

The first implementation milestone should be a tiny Go session host with no real plugin logic yet.

It should support:

1. one MUD TCP connection
2. one browser websocket endpoint
3. multiple attached browser clients
4. broadcast output
5. input from browser to MUD

Only after that should plugin protocol work begin.

## Important Scope Reminder

Do not try to build all of this at once:

1. local runtime
2. plugin platform
3. LLM advisor
4. Windows packaging
5. Rails control plane
6. cloud orchestration

Sequence matters.

Build in this order:

1. docs
2. Go session host
3. plugin protocol
4. Ruby compatibility plugin
5. Windows packaging
6. JS plugins
7. LLM advisor
8. hosted/cloud control plane

## Short Reminder To Future Session

Tomorrow, do not reopen the language debate first.

The decisions already made are:

1. Go core.
2. Browser UI.
3. SQLite stays.
4. Plugins are out-of-process.
5. Ruby compatibility plugin for v1.
6. Typed events, but not full event sourcing.

Start from architecture and protocol design, not from re-arguing fundamentals.
