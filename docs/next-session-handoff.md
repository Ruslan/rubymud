# Next Session Handoff

## Current Decision

We are not evolving the current Ruby app as the long-term runtime.

The chosen direction is:

1. Build a local-first Go session host.
2. Keep the browser UI model.
3. Keep SQLite.
4. Move most user scripting into out-of-process plugins.
5. Ship the first Go version with a Ruby compatibility plugin so scripting power is not lost.

**Update (v0.0.3):** The built-in Go VM now handles aliases, triggers, variables, and highlights directly. This covers 90%+ of daily scripting needs without plugins. The out-of-process plugin path remains for future advanced use (TTS, LLM, custom windows).

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

Current active milestone: `v0.0.3 — built-in VM with TinTin syntax`.

Completed milestones:
- `v0.0.1` — Go session host, TCP+WebSocket, basic relay
- `v0.0.2` — SQLite persistence, restore, hotkeys, ANSI rendering, history
- `v0.0.3` — Built-in VM: aliases, triggers, highlights, variables, speedwalk, button overlays

## Files To Read First Tomorrow

Read these first before doing any design or code work:

1. `docs/go-migration-plan.md`
2. `docs/next-session-handoff.md` (this file)
3. `docs/release-0.0.3.md` — latest release notes
4. `docs/vm-design-v2.md` — current VM design
5. `docs/vm-example-jmc.md` — JMC reference
6. `docs/vm-example-tortilla.md` — Tortilla reference

## What The Go App Does Today (v0.0.3)

### Runtime

1. One Go binary session host
2. Direct TCP connection to the MUD
3. Browser UI served by Go over localhost
4. Shared live session across multiple browser clients
5. Built-in VM for aliases, triggers, highlights, variables, speedwalk

### VM Commands (TinTin Syntax)

1. `#alias {name} {template}` — aliases with `%0`–`%9`, semicolons, recursion
2. `#var {name} {value}` — variables with `$varname` substitution (Cyrillic OK)
3. `#action {regex} {cmd} [group] [button]` — regex triggers, capture groups, button overlays
4. `#highlight {color} {pattern} [group]` — full ANSI SGR injection
5. `#nop`, `#N {cmd}` — comment and repeat
6. Speedwalk: `3nw2e` → `n;n;n;w;e;e`
7. Abbreviations: `#ali`, `#act`, `#high`, `#unact`, etc.

### Storage

1. SQLite at `data/mudhost.db`
2. Tables: `sessions`, `log_entries`, `log_overlays`, `history_entries`, `variables`, `alias_rules`, `trigger_rules`, `highlight_rules`
3. All rules persisted per session
4. Button overlays persisted in `log_overlays`

### Browser UI

1. Dark theme, ANSI rendering via ansi_up
2. Restore on reconnect (logs + history + hotkeys)
3. Inline command hints
4. Trigger buttons as clickable green chips
5. Hotkeys panel
6. Connection status indicator

## Discoveries From JMC/Tortilla Source

1. JMC = C++/Win32 DLL by "Jaba" (nickname), derived from TinTin++
2. Tortilla = JMC3 + extensions (PCRE regex, `#if`, `#math`, `#wait`, `#antisub`, `%(...)` ops)
3. JMC processing order (default): antisub → sub → action → highlight
4. JMC gag = `#substitute {pattern} .` — lines equal "." are suppressed
5. JMC has two alias modes: simple (exact word) and regex (`/pattern/`)
6. JMC has 27 built-in variables (`$DATE`, `$TIME`, `$INPUT`, `$COMMAND`, etc.)
7. `%%1` inside alias = outer alias argument (different from `%1` in triggers)
8. `splitBraceArg` needs double call for `#alias {name} {template}` — template retains braces otherwise
9. In Go raw string literals (backticks), `\\.` = two backslashes + dot, not `\.` for regex
10. ansi_up v5.1+ supports bold, faint, italic, underline natively
11. `$var` substitution must be Unicode-aware: `\$([\p{L}\p{N}_]+)` for Cyrillic
12. Profile vs Session: profile = persistent config, session = ephemeral connection. Currently all bound to session_id, migrate to profile_id in v0.0.10

## What Is Still Missing

### VM Commands (Priority Order)

1. `#substitute` / `#gag` / `#antisub` — replace/suppress MUD text
2. `#group enable/disable` — toggle rule groups
3. `#if` / `#else` — conditionals (JMC-style expression evaluator)
4. `#math` — arithmetic
5. `#wait` — delayed commands
6. `#read` — load TinTin-compatible script files
7. `#output` / `#woutput` — echo/window output
8. `#timer` / `#tickset` — timer scheduler

### Infrastructure

1. Profile entity (profile vs session separation) — v0.0.10
2. CRUD admin UI in browser
3. JMC `.set` file importer
4. Tortilla XML importer
5. Out-of-process plugin protocol (still needed for TTS, LLM, advanced)

### Advanced Features

1. `%%1` — nested alias argument references
2. `/regex/` — PCRE aliases
3. `$var*` — multi-variable matching
4. `%(...)` — substring operations (Tortilla)
5. Raw/Color action types (JMC `Action_RAW`, `Action_COLOR`)

## Chosen Architecture Direction

### Go Core Owns

1. TCP/Telnet transport
2. Browser HTTP/WebSocket server
3. Shared session runtime
4. Attached client management
5. SQLite persistence
6. Built-in VM (aliases, triggers, highlights, variables, speedwalk)
7. Event bus
8. Timer scheduler infrastructure
9. Plugin lifecycle
10. Effect execution
11. Session restore behavior

### Plugins Own

1. TTS
2. LLM features
3. Most custom automation beyond built-in VM
4. Optional timer logic built on top of core scheduler support
5. Custom window routing beyond defaults

**Key insight:** The built-in VM covers the daily scripting needs of most players. Plugins are for power-user extensions, not basic scripting.

## Storage Decision

Keep `SQLite`.

Reason:

1. Already used.
2. Good for Windows local execution.
3. Single-file DB.
4. Good fit for history, events, variables, and snapshots.

Do not switch storage stacks unless there is a concrete need.

Go DB approach (confirmed working):

1. `database/sql` with `modernc.org/sqlite` (pure Go, no CGO)
2. Explicit SQL queries
3. Explicit migrations in `go/sqlite/migrations/`

## Event Model Decision

We want typed events, not only raw text pipes.

But we do not want full event sourcing in v1.

Chosen direction:

1. In-memory current state.
2. Persisted event log.
3. SQLite projections/tables for fast reads.
4. Optional snapshots.

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

## Build Sequence

Do not try to build all of this at once. Sequence matters.

1. ~~docs~~ ✅
2. ~~Go session host~~ ✅ (v0.0.1)
3. ~~SQLite + restore + ANSI~~ ✅ (v0.0.2)
4. ~~Built-in VM~~ ✅ (v0.0.3)
5. VM expansion: `#sub`, `#gag`, `#group`, `#if`, `#math`, `#wait`, `#read`, `#timer` ← **current**
6. Plugin protocol
7. Ruby compatibility plugin
8. Windows packaging
9. JS plugins
10. LLM advisor
11. Hosted/cloud control plane

## Short Reminder To Future Session

Do not reopen the language debate first.

The decisions already made are:

1. Go core.
2. Browser UI.
3. SQLite stays.
4. Plugins are out-of-process.
5. Built-in VM covers TinTin syntax natively.
6. Typed events, but not full event sourcing.

Start from the next VM command (`#substitute`), not from re-arguing fundamentals.
