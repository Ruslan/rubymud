# Release 0.0.3

## Summary

`0.0.3` adds the built-in scripting VM to the Go session host.

The focus of this version was:

1. TinTin-compatible command syntax (`#alias`, `#var`, `#action`, `#highlight`)
2. Full alias expansion with variable substitution
3. Regex triggers with capture groups and button overlays
4. Highlight rules with complete ANSI SGR injection
5. Speedwalk support
6. Persistent storage for all rule types in SQLite

This version makes the Go client playable with real scripting power, no longer just a raw terminal relay.

## What Exists In 0.0.3

### Built-in VM

The VM (`go/internal/vm/vm.go`) implements a TinTin-compatible command engine inside the Go process.

#### Alias System

1. `#alias {name} {template}` — create alias with brace-delimited args
2. `#alias {name}` — show alias definition
3. `#alias` — list all aliases
4. `#unalias {name}` — delete alias
5. Abbreviation: `#ali`
6. `%0` — all arguments as single string (TinTin standard)
7. `%1`–`%9` — positional parameters in template
8. Semicolon splitting: `#alias {kk} {kick;kill}` → sends `kick` then `kill`
9. Recursive expansion up to depth 10 (alias → alias → …)
10. `$varname` substitution inside alias templates

#### Variable System

1. `#variable {name} {value}` / `#var {name} {value}` — set variable
2. `#var {name}` — show value
3. `#var` — list all variables
4. `#unvariable {name}` / `#unvar {name}` — delete variable
5. `$varname` substitution (Unicode-aware: works with Cyrillic names like `$двуруч`)
6. Built-in variables: `$DATE`, `$TIME`, `$HOUR`, `$MINUTE`, `$SECOND`, `$TIMESTAMP`
7. Variables persist in SQLite (`variables` table, scope = `session`)
8. Variable substitution happens before alias expansion

#### Trigger System

1. `#action {regex} {command} [group] [button]` / `#act` — create trigger
2. `#action` — list all triggers
3. `#unaction {pattern}` / `#unact` — delete trigger
4. Pattern = Go regex (native), `%1`–`%9` auto-converted from capture groups
5. Match runs on ANSI-stripped plain text (not raw MUD output)
6. Two effect types:
   - `send` — auto-send command to MUD immediately
   - `button` — create clickable overlay, no auto-send
7. Group field for future `#group enable/disable`
8. `StopAfterMatch` field on trigger rules (not yet exposed in command syntax)

#### Highlight System

1. `#highlight {color} {pattern} [group]` / `#high` — create highlight
2. `#highlight` — list all highlights
3. `#unhighlight {pattern}` / `#unhigh` — delete highlight
4. Color spec parser supports full ANSI SGR:
   - Named colors: `red`, `light cyan`, `brown`, `purple`, `charcoal`, etc.
   - Attributes: `bold`, `faint`, `italic`, `underline`, `strikethrough`, `blink`, `reverse`
   - Background: `b red`, `b light blue`
   - RGB: `rgb120,100,80`
   - 256-color: `256:42`
5. ANSI injection approach: match on plain text, inject ANSI codes into raw text
6. Browser renders injected ANSI via ansi_up + CSS classes

#### Speedwalk

1. `3nw2e` → `n;n;n;w;e;e`
2. Only `nsewud` direction chars + digits
3. Max repeat capped at 100 per direction
4. `look`, `n s`, `say north` do NOT trigger speedwalk
5. Activates only when every character is a direction or digit

#### Other Commands

1. `#nop` — comment, ignored
2. `#N {command}` — repeat command N times (e.g. `#3 север`)
3. Unknown `#commands` pass through as literal text (not eaten)

### Session Integration

1. VM instantiated per session, receives `ProcessInput()` for all user input
2. `SendCommand()` routes through VM: alias expansion → speedwalk → raw send
3. Read loop: each MUD line → trigger matching → highlight injection → broadcast
4. Echo messages from `#alias`/`#var`/`#act`/`#high` commands broadcast as `ServerMsg`
5. Trigger `send` effects execute via `sendTriggerCommand` (logs + writes to MUD)
6. Trigger `button` effects stored as `log_overlays` rows + sent as `buttons` array in JSON

### Storage

#### New Tables

1. `alias_rules` — session_id, name, template, enabled (unique on session_id + name)
2. `trigger_rules` — session_id, pattern, command, is_button, enabled, stop_after_match, group_name
3. `highlight_rules` — session_id, pattern, fg, bg, bold, faint, italic, underline, strikethrough, blink, reverse, enabled, group_name (unique on session_id + pattern)

#### Migrations

1. `go/sqlite/migrations/002_alias_rules.sql`
2. `go/sqlite/migrations/003_trigger_rules.sql`
3. `go/sqlite/migrations/004_highlight_rules.sql`
4. `Makefile` updated: `db-init` now runs all `*.sql` files from migration directory

#### Storage Code Split

1. `go/internal/storage/storage.go` — Store core, sessions, logs, overlays, history
2. `go/internal/storage/alias.go` — AliasRule + CRUD
3. `go/internal/storage/trigger.go` — TriggerRule + CRUD
4. `go/internal/storage/highlight.go` — HighlightRule + CRUD
5. `go/internal/storage/variable.go` — variable CRUD (LoadVariables, SetVariable, DeleteVariable)
6. `ButtonOverlay` type added to storage package
7. `AppendButtonOverlay` method for button persistence

### WebSocket Protocol Changes

1. `ServerMsg` type moved from `web` package to `session` package (shared by both)
2. `ClientLogEntry` gains `buttons` field (array of `{label, command}`)
3. `HotkeyJSON` type replaces direct `config.Hotkey` serialization
4. Restore protocol now includes button overlays from `log_overlays`
5. `BroadcastEcho()` method for VM response messages

### Browser UI

1. Trigger buttons rendered as green clickable chips after the log line
2. Button click sends `sendCommand(btn.command, 'button')` to server
3. CSS for trigger buttons: pill-shaped, dark green, hover/active states
4. Additional ANSI CSS classes:
   - `.ansi-italic` — `font-style: italic`
   - `.ansi-faint` — `opacity: 0.7`
   - `.ansi-blink` — CSS animation (1s step-end)
   - `.ansi-conceal` — `visibility: hidden`

### Tests

1. `go/internal/vm/alias_test.go` — template substitution, semicolons, recursion, %0, positional args
2. `go/internal/vm/command_test.go` — #commands, speedwalk, repeat, brace args, Arctic alias patterns
3. `go/internal/vm/trigger_test.go` — anchored/unanchored, capture groups, buttons, multi-match
4. `go/internal/vm/variable_test.go` — $var substitution, Cyrillic, built-in vars, vars in aliases/triggers

### Documentation

1. `docs/vm-design-v2.md` — updated VM design with TinTin motivation, JMC source findings, processing order
2. `docs/vm-example-jmc.md` — JMC reference (commands, syntax, architecture from source)
3. `docs/vm-example-tortilla.md` — Tortilla reference (extensions over JMC)

## Important Files In 0.0.3

### New Files

1. `go/internal/vm/vm.go` — VM engine (864 lines)
2. `go/internal/vm/alias_test.go`
3. `go/internal/vm/command_test.go`
4. `go/internal/vm/trigger_test.go`
5. `go/internal/vm/variable_test.go`
6. `go/internal/storage/alias.go`
7. `go/internal/storage/trigger.go`
8. `go/internal/storage/highlight.go`
9. `go/internal/storage/variable.go`
10. `go/sqlite/migrations/002_alias_rules.sql`
11. `go/sqlite/migrations/003_trigger_rules.sql`
12. `go/sqlite/migrations/004_highlight_rules.sql`
13. `docs/vm-design-v2.md`
14. `docs/vm-example-jmc.md`
15. `docs/vm-example-tortilla.md`

### Modified Files

1. `go/internal/session/session.go` — VM integration, trigger/highlight pipeline, button overlays, ServerMsg refactoring
2. `go/internal/storage/storage.go` — ButtonOverlay type, AppendButtonOverlay, boolToInt helper
3. `go/internal/web/server.go` — ServerMsg/ClientLogEntry moved to session, button restore, removed redundant logs
4. `go/internal/web/static/index.html` — trigger button UI, ANSI CSS additions
5. `Makefile` — multi-migration db-init

## Commands Available

Run the Go client:

```bash
make run MUD=rmud.org:4000
```

Initialize database (now runs all migrations):

```bash
make db-init
```

Run tests:

```bash
cd go && go test ./internal/vm/...
```

## Architectural Decisions Confirmed In 0.0.3

1. Built-in VM is the right first step before out-of-process plugins.
2. TinTin `#command` syntax is the standard for JMC/Tortilla compatibility.
3. Highlight injection via ANSI codes (approach B) is simpler and faster than DOM mutation.
4. Trigger matching on ANSI-stripped plain text is correct (matches JMC `Action_TEXT` behavior).
5. Processing order: alias expansion → speedwalk → MUD send; MUD output → trigger → highlight → broadcast.
6. Variables use Unicode-aware regex `\$([\p{L}\p{N}_]+)` for Cyrillic support.
7. Storage split by domain (alias.go, trigger.go, highlight.go, variable.go) keeps code navigable.
8. `splitBraceArg` handles `{…}`, `"…"`, `'…'` and bare-word arguments.

## What Is Still Missing

### VM Commands (Next Iteration)

1. `#substitute` / `#sub` — replace matched text with different text
2. `#gag` — suppress line (gag = substitute with ".")
3. `#antisub` — exclude pattern from substitution
4. `#group enable/disable` — group management
5. `#if` / `#else` — conditionals
6. `#math` — arithmetic expressions
7. `#wait` — delayed command execution
8. `#read` — load TinTin-compatible script files
9. `#output` / `#woutput` — echo/window output

### Infrastructure

1. `#timer` / `#tickset` — timer scheduler
2. Profile entity (profile vs session separation) — v0.0.10
3. CRUD admin UI in browser
4. JMC `.set` file importer
5. Tortilla XML importer

### Advanced Features

1. `%%1` — nested alias argument references
2. `/regex/` — PCRE aliases (regex-mode aliases)
3. `$var*` — multi-variable matching
4. `%(...)` — substring operations (Tortilla extension)
5. Raw/Color action types (`Action_RAW`, `Action_COLOR` from JMC)

## Why 0.0.3 Matters

This version proves that the Go client can be a real scripting environment, not just a terminal mirror.

A player can now:

1. Define aliases for common actions (`#alias {kk} {kick;kill}`)
2. Set variables for equipment names (`#var {оружие} {фламберг}`)
3. Create triggers for game events (`#action {^You are thirsty.} {drink all}`)
4. Highlight important text (`#highlight {light red} {^You are hungry.}`)
5. Get lazy trigger-buttons for safe actions (`#action {^R\.I\.P\.} {взя все тру} {default} {button}`)
6. Use speedwalk (`3nw2e` → walk north×3, west, east×2)

All of this uses the same TinTin syntax that JMC and Tortilla players already know.

## Immediate Next Priority After 0.0.3

1. `#substitute` / `#gag` — essential for MUD play (suppress spam, replace prompt text)
2. `#group enable/disable` — needed for toggleable trigger sets
3. `#if` / `#math` — conditionals unlock complex automation
4. `#read` — script loading from files (enables config migration from JMC/Tortilla)
5. `#timer` — timed commands (tick tracking, periodic actions)
