# Version 0.0.5.2 Plan

## Goal

Add a minimal built-in `#if` command so common Ruby-style conditional triggers can be expressed in the Go VM.

This is a small VM follow-up release after the closed `0.0.5` settings-foundation milestone.

## Motivation

The older Ruby client supported conditional trigger logic because `act` blocks executed Ruby directly.

Typical examples were:

```ruby
act(/^Вам не удалось парировать/) { 'пари' if пари == '1' }
act(/^Вы чуть не потеряли равновесие и сбились с боевого ритма/) { 'пари' if пари == '1' }
act(/удара, Вы не удержали равновесия и упали!$/) { 'вста;соск;вста;сби;вста;вскоч' if баш == '1' }
act(/на землю своим сокрушающим ударом!$/) { 'соск;сби;вста;вскоч' if баш == '1' }
act(/^Вы подняли свое оружие над головой и начали быстро его вращать/) { 'вста;вращ' if вращ == '1' }
```

The Go VM already has variables and trigger commands, but it does not yet have a built-in conditional form.

## Scope

### Syntax

1. Support `#if {cond} {true}`.
2. Support `#if {cond} {true} {false}`.

Examples:

```text
#if {$пари == 1} {пари}
#if {$баш == 1} {вста;соск;вста;сби;вста;вскоч} {гг баш=0}
```

### Execution Contexts

1. Direct user input can execute `#if`.
2. Alias bodies can execute `#if`.
3. Trigger command bodies can execute `#if`.

### Condition Model

First version should stay intentionally small:

1. `$name == value`
2. `$name != value`
3. Values are treated as strings.
4. Variable substitution should work with existing Unicode-aware variable names.

## Acceptance Criteria

1. A trigger can conditionally emit commands based on a stored variable.
2. An alias can conditionally emit commands based on a stored variable.
3. Direct manual input of `#if` works.
4. True branch executes only when the condition matches.
5. False branch executes only when the condition does not match.
6. Missing false branch is handled cleanly.
7. Existing variable substitution behavior remains intact.

## Non-Goals

1. Full expression language.
2. `&&` / `||`.
3. Arithmetic.
4. Complex precedence rules.
5. Full JMC/Tortilla condition compatibility.
6. Embedding Ruby or `mruby` as part of this patch release.

## Implementation Notes

1. Implement `#if` as a normal VM/system command, not as a special case in triggers only.
2. Reuse existing brace-argument parsing.
3. Reuse existing command execution flow for the selected branch.
4. Keep the evaluator minimal and explicit.
5. Prefer a simple parser over introducing a general scripting runtime.
