# Version 0.0.8.9.1 Plan: Hide Local Command Echo Noise

## Goal

Make command echo from aliases and triggers show player-relevant output, not internal VM implementation steps.

`0.0.8.9.1` should ensure that local client commands are hidden from normal command echo when they are executed as part of aliases, triggers, timers, delayed commands, or other scripted command pipelines.

Local commands include commands such as:

1. `#var`
2. `#if`
3. `#showme`
4. `#nop`
5. `#action`
6. other recognized VM/system commands that are handled by RubyMUD itself and are not written to the MUD socket

The normal player-facing echo should show either:

1. the original user input, when echoing manual input
2. the game commands actually sent to the MUD server, when echoing expanded script output

It should not show internal local commands unless an explicit debug or verbose mode is enabled.

---

## Product Problem

RubyMUD scripts can combine local state changes, conditional logic, diagnostics, and real MUD commands in one alias or trigger body.

That is useful for scripting, but it creates a UX problem if every expanded statement is echoed as if it were a command sent to the game server.

When normal command echo displays local commands:

1. the output stream exposes script implementation details instead of player intent
2. aliases become noisy as soon as they use variables or conditionals
3. triggers can flood the UI because they are server-driven and may fire frequently
4. command logs become harder to read because local VM actions are mixed with actual MUD traffic
5. users cannot easily tell which commands were sent to the MUD and which commands only mutated local client state

The key distinction is semantic, not cosmetic:

1. MUD commands are outbound protocol actions
2. local `#` commands are client-side control flow, state mutation, diagnostics, or configuration

Normal command echo should preserve that distinction.

---

## Why Other Clients Hide This By Default

Mature MUD clients generally separate three different concepts:

1. what the user typed
2. what the client sent to the MUD server
3. what the script engine executed internally

These are related, but they are not the same stream.

### JMC-style behavior

JMC-style clients normally treat script commands as client internals. Alias expansion and trigger execution happen under the hood. Internal client commands may be visible in a command/debug message mode, but they are not treated as normal outbound game commands.

This keeps player-facing output focused on gameplay instead of script mechanics.

### TinTin++-style behavior

TinTin++ exposes separate configuration concepts for command echo, server-command echo, and verbose/debug script output.

The important compatibility lesson is not the exact option names. The important lesson is that internal script execution is a separate verbose/debug stream, not normal command echo.

### Tortilla-style behavior

Tortilla-style clients also distinguish user-visible command echo from system command tracing. System commands can be shown for debugging, but hiding them by default keeps automation usable during normal play.

### Compatibility conclusion

Hiding local commands from normal echo is not just a subjective preference. It is a common MUD-client convention because scripts are expected to be able to maintain local state and make decisions without polluting the gameplay transcript.

Verbose tracing is still valuable, but it should be opt-in.

---

## Scope

### In Scope

1. Define which commands are considered local VM/system commands.
2. Ensure local commands do not appear in normal command echo when executed through scripted pipelines.
3. Preserve existing command execution semantics.
4. Preserve normal outbound MUD command echo, if RubyMUD currently supports it.
5. Keep local diagnostics such as `#showme` visible as intentional output, not as command echo.
6. Add tests that distinguish local command execution from outbound command echo.
7. Document the behavior in `docs/engine-commands.md`.

### Out Of Scope

1. Do not redesign the entire VM pipeline.
2. Do not change which commands are sent to the MUD.
3. Do not change command history semantics unless the current history is explicitly tied to echo.
4. Do not add a full debugger UI.
5. Do not make triggers globally silent if RubyMUD already has an explicit trigger echo feature.

---

## Desired Semantics

### Manual Input

When a user enters a command manually, RubyMUD may echo the original input depending on existing UI settings.

If that input expands through an alias, the expanded local commands should not be shown as normal command echo.

Acceptable normal echo policies:

1. echo only the original input
2. echo only the expanded outbound MUD commands
3. echo both original input and expanded outbound MUD commands, if clearly separated by existing UI conventions

Unacceptable default policy:

1. echo every internal expanded statement, including local `#` commands, as ordinary command output

### Alias Expansion

Aliases may execute both local commands and MUD commands.

Normal echo should include only player-relevant command output:

1. local state mutations stay hidden
2. condition checks stay hidden
3. local no-op statements stay hidden
4. outbound MUD commands may be echoed
5. explicit local display commands still display their message as display output

### Trigger Execution

Triggers are server-driven, not direct user input.

Normal trigger execution should be quieter than manual alias execution because triggers can fire many times from ordinary server output.

Recommended default:

1. do not echo local commands from triggers
2. consider not echoing outbound trigger commands unless RubyMUD already has a configured trigger-command echo mode
3. keep explicit display commands visible because they are intentional user-facing output

### Timers And Delayed Commands

Timers and delayed commands should follow the same scripted-pipeline rules as triggers:

1. hide local commands by default
2. keep outbound command behavior unchanged
3. expose internal steps only through verbose/debug tracing

---

## Terminology

### Local Command

A local command is a recognized command handled by the RubyMUD VM or engine without being sent to the MUD socket.

Examples:

1. variable mutation (#var, #unvar)
2. conditional branching (#if)
3. local display output (#showme, #woutput, #tts)
4. action/alias/hotkey configuration (#action, #alias, #hotkey)
5. no-op or comment commands (#nop)
6. timer and delay management (#ticksize, #tickon, #tickoff, #tickset, #tickicon, #tickat, #untickat, #delay, #undelay, #ticker)
7. session and profile management commands

### Outbound Command

An outbound command is a final string written to the MUD socket.

Normal command echo should be about outbound commands, original player input, or intentional display output. It should not be about every VM step required to produce them.

### Verbose Trace

A verbose trace is an opt-in diagnostic stream that can show internal script execution.

Verbose trace is useful for debugging imported scripts, but it should not be enabled by default.

---

## Proposed Solution Paths

There are three viable implementation directions. They differ mainly in how much structure they add to the VM pipeline.

### Option A: Filter Echo At The Echo Source

The minimal approach is to keep the current pipeline mostly intact and filter local commands before command echo is emitted.

Implementation shape:

1. identify the code path that emits command echo for expanded alias/trigger statements
2. before echoing a statement, classify it as local or outbound
3. suppress local statements from normal echo
4. leave actual command execution unchanged

Pros:

1. smallest patch
2. low risk to execution semantics
3. easy to ship as a bugfix release

Cons:

1. classification may duplicate parser logic
2. risk of filtering based on raw string shape instead of actual execution result
3. may miss future local commands unless classification is centralized

Best fit:

1. quick patch if the current echo path already has access to individual statements

### Option B: Classify Parse Results As Local Or Outbound

The cleaner approach is to make the command pipeline return structured execution results instead of bare strings.

Implementation shape:

```text
ParsedCommand {
  text
  kind: local | outbound | display | diagnostic
  source: input | alias | trigger | timer | delay
}
```

The exact type names can differ. The important part is that echo behavior is based on command kind, not on string guessing.

Pros:

1. semantically correct
2. makes future commands safer
3. gives UI/logging code enough context to make good decisions
4. can support verbose trace cleanly later

Cons:

1. broader refactor
2. more tests required
3. may touch VM, session, web, and UI boundaries

Best fit:

1. preferred if the current command pipeline is already being refactored around local commands and script execution

### Option C: Add Execution Events

The most extensible approach is to emit explicit execution events from the VM.

Implementation shape:

1. `command_input` for user-entered text
2. `command_outbound` for strings written to the MUD socket
3. `command_local` for recognized local VM commands
4. `display_output` for intentional local display
5. `script_trace` for debug-only internal steps

Normal UI subscribes only to player-facing events. Verbose/debug UI can subscribe to trace events.

Pros:

1. clean long-term architecture
2. separates logging, UI echo, and execution clearly
3. supports future debugger/trace features without changing execution semantics

Cons:

1. largest change
2. may be too much for a patch release
3. needs careful compatibility testing around logs and websocket payloads

Best fit:

1. future direction if RubyMUD needs richer script debugging and audit trails

---

## Recommended Approach

Use Option A only if the current echo bug can be fixed at a single well-defined boundary without stringly reimplementing the VM parser.

Otherwise, use a small version of Option B:

1. centralize command classification in the VM pipeline
2. return outbound commands separately from local execution steps
3. let normal echo consume only original input, outbound commands, and intentional display output
4. leave a future extension point for verbose trace

Avoid a broad Option C event-system refactor in `0.0.8.9.1` unless the existing architecture already points that way.

The patch release should prioritize correctness and compatibility over building a debugger.

---

## Verbose Mode Direction

This release does not need to implement verbose tracing, but the design should avoid blocking it.

Future verbose behavior could be:

```text
#config {verbose} {on}
```

or a settings UI toggle.

When enabled, RubyMUD may display internal script steps with a distinct style or prefix.

Verbose output must be clearly different from normal command echo so users can tell that a line is a client-side trace, not something sent to the MUD.

---

## Edge Cases

### Unknown `#` Commands

RubyMUD may intentionally pass unknown `#` commands through to the MUD for compatibility (e.g., if a specific MUD uses `#` for server-side commands).

If an unknown `#` command is sent to the MUD socket, it is outbound and should not be hidden solely because it starts with `#`.

Classification must be based on recognized local command handling, not on the first character alone. This ensures that any unrecognized command sent to the server remains visible in the echo, which is essential for troubleshooting server-side command rejection.

### Explicit Display Commands

Commands that intentionally print local output should still print their message.

The command invocation itself should be hidden from normal command echo, but the resulting display output should remain visible.

### Conditional Branches

Only the selected branch should execute.

Normal echo should never reveal non-selected branch contents.

Verbose mode may trace branch selection later, but normal echo should not.

### Nested Aliases

If an alias expands into another alias, local commands at every expansion level should be hidden from normal echo.

Only final outbound commands or intentional display output should be visible.

### Trigger Storms

For triggers that fire repeatedly, hidden local commands are essential. A trigger that updates local state on every prompt or combat line should not generate user-visible command noise.

---

## Testing Plan

### Unit Tests

Add command pipeline tests for:

1. recognized local commands execute but are not returned as outbound commands
2. unknown `#` commands preserve existing pass-through behavior
3. aliases that contain local commands still execute local effects
4. aliases that contain local commands return only outbound commands for socket writes
5. nested aliases hide local commands at every level
6. explicit display commands produce display output but are not echoed as command invocations

### Integration Tests

Add end-to-end tests for:

1. manual alias execution with local state mutation and outbound command
2. trigger command execution with local state mutation and outbound command
3. timer or delayed command execution with local state mutation and outbound command
4. command echo payloads do not include recognized local command invocations
5. outbound socket writes remain unchanged

### Regression Tests

Ensure existing behavior remains unchanged for:

1. direct MUD commands
2. variable substitution
3. alias argument substitution
4. trigger capture substitution
5. semicolon splitting inside command groups
6. local diagnostics
7. command history, if history records user input rather than expanded commands

---

## Documentation Updates

Update `docs/engine-commands.md` to explain:

1. local `#` commands are client-side script commands
2. normal command echo does not show local script commands executed inside aliases, triggers, timers, or delayed commands
3. outbound MUD commands may still be echoed according to command echo settings
4. explicit local display output remains visible
5. verbose/debug tracing, if added later, is the place where internal script steps will appear

Avoid documenting this as a special case for one command. It is a general command-pipeline rule.

---

## Acceptance Criteria

1. Recognized local commands executed from aliases do not appear in normal command echo.
2. Recognized local commands executed from triggers do not appear in normal command echo.
3. Recognized local commands executed from timers or delayed commands do not appear in normal command echo.
4. Outbound MUD commands produced by the same scripts are still sent exactly as before.
5. Explicit local display output remains visible.
6. Unknown `#` commands preserve the current pass-through behavior if they are not recognized as local commands.
7. Normal command echo no longer implies that local VM commands were sent to the MUD.
8. Tests cover aliases, triggers, unknown `#` commands, display output, and nested expansion.
9. Documentation describes local-command hiding as default behavior and reserves internal step visibility for verbose/debug tracing.
