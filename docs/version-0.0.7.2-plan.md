# Version 0.0.7.2 Plan

## Goal

Close the biggest scripting-compatibility gaps left after `0.0.7`:

1. allow aliases to expand into local engine commands starting with `#`
2. make local output-to-buffer usable from imported/shared configs
3. make buffer routing round-trip through `.tt` export/import
4. add one cheap local notification primitive for speech on macOS

This milestone is about making real JMC/Tortilla-style configs feel native in RubyMUD instead of "UI-only configurable".

---

## Product Problem

After `0.0.7`, named buffers and routing exist at runtime, but several practical config patterns are still awkward:

1. alias expansion currently produces commands to send, but does not re-parse expanded `#commands`
2. this blocks patterns like:

```text
#alias {ц1} {#var {t1} {%1}}
```

3. trigger routing exists in DB/UI/runtime, but `.tt` does not yet preserve `target_buffer` / `buffer_action`
4. local output commands exist as `#showme` and `#woutput`, but they are not yet first-class building blocks for migrated configs

---

## Scope

### 1. Alias -> local command execution

If alias expansion produces a line beginning with `#`, that line should be executed by the client VM, not sent to MUD.

Example:

```text
#alias {ц1} {#var {t1} {%1}}
```

Input:

```text
ц1 орк
```

Behavior:

1. alias expands to `#var {t1} {орк}`
2. VM re-parses it as a local command
3. variable `t1` is updated in the client/session
4. nothing is sent to MUD for that expanded line

### 2. Recursive local command handling

This should work not only for `#var`, but for all existing local VM commands:

- `#alias`
- `#unalias`
- `#var`
- `#unvar`
- `#action`
- `#unaction`
- `#highlight`
- `#unhighlight`
- `#hotkey`
- `#showme`
- `#woutput`
- `#nop`
- `#N` repeat

### 3. `.tt` support for trigger buffer routing

Extend `rubymud:rule` metadata for `#action` export/import so these fields round-trip:

- `target_buffer`
- `buffer_action`

Example:

```text
#nop rubymud:rule {"buffer_action":"copy","target_buffer":"chat"}
#action {^Guild: (.*)$} {#woutput {chat} {%1}}
```

or, if using direct trigger routing semantics:

```text
#nop rubymud:rule {"buffer_action":"copy","target_buffer":"chat"}
#action {^Guild: (.*)$} {say %1}
```

The important part is that routing metadata survives export/import.

### 4. Config-facing local output

`#showme` and `#woutput` should become usable inside migrated configs, not only as manually typed commands.

This falls out naturally from alias recursive re-parse.

Examples:

```text
#alias {чатыкл} {#woutput {chat} {Chat pane ready}}
#alias {время} {#showme {Now: $TIME}}
```

### 5. Local speech notification command

Add one local command for system speech synthesis.

Recommended command name:

```text
#tts {text}
```

Examples:

```text
#tts {ready}
#alias {озвучь} {#tts {%0}}
```

Initial implementation target:

- macOS only
- use the system `say` command under the hood

Behavior:

1. command executes locally in the client
2. nothing is sent to MUD
3. on macOS it invokes system speech
4. on other platforms it should fail gracefully with a local diagnostic message

This is intentionally a small primitive for notifications. More media commands such as `#beep` or `#mp3` can come later.

---

## Explicit Non-Goals

### Not in this milestone

- embedded Ruby/Lua/JS scripting
- block aliases with arbitrary host-language logic
- `transform:` lambdas from old Ruby config
- CSS/styled `wecho(..., css: ...)`
- `#beep`, `#mp3`, and broader media automation

Speech is in scope here, but only as a dedicated `#command`, not bare `say`.

---

## Why Bare `say` Is Different

In old Ruby config, `say` was used as a local helper from Ruby code.

In RubyMUD VM, bare `say hello` is already just an ordinary outgoing MUD command.

So `say` cannot safely become a client-local built-in without breaking user expectations and game commands.

Conclusion:

- keep bare `say ...` as MUD traffic
- add local speech as a distinct `#command` such as `#tts`

---

## Why `wecho` Is Only Partially Covered By `0.0.7`

`0.0.7` already delivered the product capability of named buffers:

- trigger routing via `move` / `copy` / `echo`
- UI panes bound to buffers
- local command `#woutput {buffer} {text}`

But it did not fully deliver old-script parity with Ruby `wecho` because:

1. `.tt` did not yet preserve trigger routing metadata
2. alias-expanded local `#woutput` was still blocked by non-recursive parsing
3. styled output like `css:` is not supported

So `0.0.7` solved the runtime/product problem, while `0.0.7.2` should solve the config/script compatibility problem.

---

## Implementation Direction

### VM

Refactor input processing so expanded lines are classified per line:

1. if expanded line begins with `#` -> dispatch as local VM command
2. else -> emit as outgoing MUD command

This classification must happen after alias expansion and `;` splitting, not only on the original raw input.

### Result model

Current `ResultKind` already separates:

- outgoing command
- local echo

Keep that model and extend the expansion pipeline so alias-produced local commands can return proper `Result`s.

For `#tts`, VM can return a dedicated local-effect result or trigger a platform helper from command dispatch, but it must not be emitted as an outgoing MUD command.

### Profile script format

Update `ExportProfileScript` and `ParseProfileScript` for triggers:

- export `target_buffer` / `buffer_action` inside `#nop rubymud:rule {...}`
- import them back into `TriggerRule`

---

## Acceptance Criteria

1. `#alias {ц1} {#var {t1} {%1}}` works and updates the session variable locally.
2. `#alias {note} {#showme {hello}}` writes to local output and sends nothing to MUD.
3. `#alias {chatnote} {#woutput {chat} {hello}}` writes to buffer `chat` and sends nothing to MUD.
4. `.tt` export preserves `buffer_action` and `target_buffer` for triggers.
5. `.tt` import restores those trigger routing settings correctly.
6. Existing normal aliases that expand to game commands still work unchanged.
7. Bare `say hello` still goes to MUD exactly as before.
8. `#tts {ready}` speaks locally on macOS and sends nothing to MUD.
9. `#tts {ready}` on unsupported OSes fails gracefully without crashing the client.
