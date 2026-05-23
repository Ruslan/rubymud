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

## Echo Behavior

When command echo is enabled:

1. every command actually sent to the MUD server is echoed once
2. echo text is the exact outgoing command after VM evaluation
3. multiple outgoing commands produce multiple echo rows
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

1. hotkey with `#if` echoes only the selected outgoing branch
2. variables are expanded in echoed outgoing command
3. alias invocation echoes expanded server command, not alias source
4. multi-command chain echoes each outgoing command separately
5. local-only commands do not create sent-command echo entries
6. command history/log entries use final outgoing command text

## Acceptance Criteria

1. Normal echo shows the command text actually sent to the MUD server.
2. Unevaluated `#if`, alias bodies, variables, and command-chain source text are not shown as outgoing echo.
3. Multiple outgoing commands are echoed as multiple sent-command entries.
4. Local VM commands do not appear as sent-to-server echo.
5. Aliases follow the same final-command echo policy as hotkeys/actions/buttons.
6. Admin log viewing, search, and export can rely on canonical sent-command entries.
7. Behavior is covered by regression tests.
