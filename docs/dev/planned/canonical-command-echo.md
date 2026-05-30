# Canonical Command Echo

## Context

Current command echo can show the source script text instead of the actual command sent to the MUD server.

Example problem:

```text
Hotkey source:
#if {$fbash == 1} {сби $target1} {пну $target1}

Current echo:
#if {$fbash == 1} {сби $target1} {пну $target1}

Desired echo when `$fbash = 1` and `$target1 = гоблин`:
сби гоблин
```

For normal play, echo should answer the user question: "What command did RubyMUD actually send to the server?"

## Decision

Echo should display canonical outgoing MUD commands after VM evaluation.

That means echo should show the final wire-level commands after:

1. variable expansion
2. `#if` branch selection
3. alias expansion, unless a later explicit debug mode needs otherwise
4. action/hotkey/button command evaluation
5. command-chain splitting

Recommended default: follow the JMC-style rule where echo always shows server commands, including commands produced through aliases.

TinTin++-style alias-source echo can be useful for debugging, but should not be the default play/log/export behavior.

## Product Semantics

There are two different things:

1. command source: the script/hotkey/alias/action text the user authored
2. outgoing command: the final text sent to the MUD connection

Echo should show outgoing commands.

Command source may be exposed later only through debug/trace tooling.

## Implementation Status

Status: partially implemented, keep this plan open.

Implemented in the first trace slice:

1. client sends non-empty commands with `client_command_id`
2. server returns `command_trace` with canonical outgoing `commands`
3. live client reconciles pending optimistic hints to server canonical output
4. `bash $t1` can resolve live to `bash orc` instead of remaining as source text
5. aliases, `#if` branches, variable expansion, and command chains are covered by session tests
6. local-only commands resolve to an empty outgoing chain and remove pending sent-command hints
7. input history recall still keeps raw user input for editing convenience

Not fully implemented yet:

1. no separate accepted/queued event exists; the first slice uses local pending state plus resolved `command_trace`
2. the VM has not been refactored to emit a first-class `send_to_mud` effect; session code collects commands actually written
3. Settings History still displays raw `input` rows alongside canonical expanded rows, so admin history/source-vs-outgoing presentation still needs product cleanup
4. log export/search should be reviewed as part of the broader log-browsing work before claiming the whole storage/export model complete
5. no browser/integration test covers multiple connected clients or pending hint placement when the client has no current log line
6. if a connection write fails before a trace response is sent, the pending optimistic hint can remain until reconnect/restore

## Two-Source Drift

Before trace reconciliation, the live UI could treat the client-side source command as the sent command, while storage/restore later used server-side truth.

Example:

```text
User hotkey/input source:
bash $t1

Client optimistic hint:
bash $t1

Server VM output actually sent to MUD:
bash orc

After refresh/history:
bash orc
```

This creates drift: live play says "I sent source", restored play says "server sent canonical output". The fix should make live and restored UI use the same server canonical trace data.

## Command Trace Protocol Model

Use a small reconciliation protocol rather than treating optimistic client text as final.

Recommended flow:

1. client sends command source with a `client_command_id` or equivalent trace id
2. server acknowledges the id as accepted/queued, so the client may show a pending hint
3. server runs the VM and writes final outgoing commands to the MUD
4. server emits a resolved trace event for that id with the canonical outgoing chain

Resolved trace payload can be compact or structured, for example:

```json
{"client_command_id":"abc123","commands":["c1","c2","c3","c4"]}
```

The canonical command list, not the raw source string, is the source of truth for final sent-command UI, history, restore, logs, search, and export.

## UI Reconciliation

The client may show a pending optimistic hint while waiting for server resolution, but it must not keep raw source text as the final sent command.

Required behavior:

1. pending hint can show the source command as "pending" if useful
2. accepted event ties the pending hint to the server trace id
3. resolved trace replaces/reconciles the pending hint with canonical outgoing command text
4. `bash $t1` should not remain visible as the final sent-to-server command when the server actually sent `bash orc`
5. local-only commands resolve to an empty outgoing chain and should disappear or remain only as local/debug feedback, not as sent-to-MUD commands

## History And Restore Parity

Restored UI should match live UI because both are derived from canonical server trace data.

Acceptance direction:

1. live command hints use resolved canonical traces
2. persisted history/log entries use the same canonical outgoing commands
3. refresh/restore does not change what the user believes was sent
4. source command metadata may be retained for debugging, but must not be rendered as normal sent-command history

## Echo Behavior

When command echo is enabled:

1. every command actually sent to the MUD server is echoed once
2. echo text is the exact outgoing command after VM evaluation
3. multiple outgoing commands produce multiple echo rows by default
4. local/meta commands are not echoed as if they were sent to the MUD
5. command source text is not shown in normal echo

Examples:

```text
Input/hotkey/action:
#if {$fbash == 1} {сби $target1} {пну $target1}

Outgoing echo:
сби гоблин
```

```text
Alias:
#alias {killtarget} {убить $target1}

User input:
killtarget

Outgoing echo:
убить гоблин
```

```text
Command chain:
#if {$ready == 1} {встать;север} {отдых}

Outgoing echo:
встать
север
```

## Multi-Command Chain Display

Canonical chains may be displayed as separate sent-command rows or as one compact joined hint.

Preferred default:

1. store and trace the structured chain as `commands: [...]`
2. render separate sent-command rows where space and context allow
3. use a semicolon-joined compact hint such as `c1;c2;c3;c4` only when a single-line display is needed
4. never reconstruct canonical chains from raw source text when resolved trace data is available

## Local Commands

Local VM commands should not be represented as outgoing MUD commands.

Examples that should not produce outgoing command echo by themselves:

1. `#showme`
2. `#var`
3. `#tickset`
4. `#tickon`
5. `#alias`
6. `#action`
7. `#highlight`
8. `#sub`

If a local command indirectly sends a MUD command later, the later outgoing command should be echoed at send time.

For trace reconciliation, a local-only command can still be accepted and resolved, but its canonical outgoing chain is empty. It should not appear as a sent-to-server command.

## Storage And Logs

History/log storage should distinguish command source from outgoing command.

Recommended model:

1. store outgoing command text as the canonical sent-command log entry
2. use this canonical sent-command text for live echo, admin log viewing, search, and export
3. optionally store source/trace metadata separately if needed for debugging

This aligns with `log-browsing.md`:

1. admin search should include sent player commands
2. export should include sent player commands as network-level output
3. sent commands should be visually distinguishable from received MUD output
4. export/admin viewing should not show unevaluated script text as if it was sent to the server

## VM/API Direction

The VM should report command-send events at the point where the connection write is about to happen or has just happened.

Recommended direction:

1. make the command processor return or emit structured effects
2. one effect type should be `send_to_mud` with final command text
3. echo/history/logging should subscribe to `send_to_mud`, not to raw input or hotkey source
4. command source can remain available as metadata on the execution frame, but not as the primary echo text

This avoids guessing after the fact and keeps echo aligned with actual network writes.

## Alias Policy

Default policy:

1. aliases are expanded before echo
2. echo shows the final commands produced by aliases
3. source alias invocation is not echoed in normal mode

Rationale:

1. users care most about what reached the MUD server
2. logs/search/export become honest and useful
3. behavior is consistent across hotkeys, aliases, actions, and buttons
4. special TinTin++-style alias echo can be deferred to a debug trace mode

## Optional Debug Trace Later

A later debug feature may show execution traces, for example:

```text
F3 -> #if {$fbash == 1} {сби $target1} {пну $target1} -> сби гоблин
```

This should be separate from normal echo.

Possible locations:

1. developer/debug panel
2. command history detail view
3. optional verbose execution log

Do not block the canonical echo fix on this.

## Repro Test Requirement

Follow repro-first TDD.

Add tests before changing behavior:

1. hotkey/input source `bash $t1` resolves to canonical `bash orc`, and the client reconciles the pending source hint
2. hotkey with `#if` echoes only the selected outgoing branch
3. variables are expanded in echoed outgoing command
4. alias invocation echoes expanded server command, not alias source
5. multi-command chain resolves to a structured canonical chain and renders as separate rows or a semicolon-joined compact hint as appropriate
6. local-only commands resolve to an empty outgoing chain and do not create sent-command echo entries
7. command history/log entries use final outgoing command text
8. refresh/restore parity: restored UI shows the same canonical sent-command trace as live UI after reconciliation

## Acceptance Criteria

1. Normal echo shows the command text actually sent to the MUD server.
2. Unevaluated `#if`, alias bodies, variables, and command-chain source text are not shown as outgoing echo.
3. Multiple outgoing commands are echoed as multiple sent-command entries by default, with semicolon-joined compact display allowed for constrained hints.
4. Client optimistic hints are reconciled by `client_command_id` or equivalent trace id and replaced with canonical server output.
5. Local VM commands can resolve to an empty trace and do not appear as sent-to-server echo.
6. Aliases follow the same final-command echo policy as hotkeys/actions/buttons.
7. Live UI and refresh/restore UI show the same canonical sent-command data.
8. Admin log viewing, search, and export can rely on canonical sent-command entries.
9. Behavior is covered by regression tests.
