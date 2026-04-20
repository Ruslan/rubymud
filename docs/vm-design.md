# VM Design

## Purpose

This document defines the planned built-in scripting layer for the Go client.

It is not a Ruby VM port.

It is a small deterministic rule engine for the features that should feel native to the client:

1. aliases
2. variables
3. simple triggers
4. buttons
5. window routing
6. timers

Plugins remain important, but this built-in engine should cover the common everyday MUD client workflows without forcing casual players to install Ruby or JavaScript plugins.

## Core Positioning

The built-in VM should handle:

1. simple and common automations
2. deterministic rules
3. fast local execution
4. config that can be edited in UI later

Plugins should handle:

1. complex custom scripting
2. arbitrary code execution
3. LLM logic
4. TTS
5. integrations
6. experimental automation

## Non-Goals

The built-in VM should not try to be:

1. a general-purpose language
2. a Ruby interpreter
3. a JavaScript runtime
4. a sandbox for arbitrary user code

## Why Not Port Ruby VM 1:1

The Ruby implementation is a strong product reference, but not the right long-term execution model.

Things we do not want to port directly:

1. `instance_exec`
2. `method_missing`
3. `load config.rb`
4. arbitrary Ruby blocks as the core scripting contract

What we do want to keep:

1. the domain model
2. the user-facing power
3. the shape of effects
4. the separation between player input and server output rules

## Design Principles

1. deterministic execution
2. explicit data model
3. explicit effects
4. no hidden runtime magic
5. easy persistence in SQLite
6. easy future UI editor support
7. compatible with future plugin pipeline

## Main Concepts

### 1. Variables

Persisted string values.

Use cases:

1. targets like `t1`, `t2`
2. group member names
3. mode toggles such as `лид`, `вращ`, `пари`

Planned features:

1. string values in v1
2. session/profile scope
3. substitution in aliases and effects
4. set from UI and from rules

### 2. Aliases

Client input transformation before commands are sent to the MUD.

Two useful levels:

1. simple alias template
2. structured alias action list

Examples:

1. `уу -> у %1;пари`
2. `ц1 -> вар t1 %1`

The first built-in version can support template aliases only.

### 3. Triggers

Regex match on normalized server output.

Each trigger should have:

1. `pattern`
2. `enabled`
3. `stop_after_match`
4. `effects`
5. optional target window
6. optional button mode

### 4. Timers

Timer scheduling infrastructure should stay in Go core.

The built-in VM should be able to request:

1. schedule timer
2. cancel timer

Timer firing comes back as an event/effect execution point.

### 5. Effects

The VM should not mutate the client indirectly.

It should emit explicit effects.

Initial effect set:

1. `send_command`
2. `echo`
3. `write_window`
4. `show_button`
5. `set_variable`
6. `schedule_timer`
7. `cancel_timer`

## Execution Model

### Input Path

1. user enters text
2. input is split into command segments
3. variable substitution runs
4. alias expansion runs
5. final command list is produced
6. commands are sent to MUD

### Output Path

1. raw MUD text is normalized by transport layer
2. output is split into visible lines
3. each line becomes a canonical `log_entry`
4. built-in triggers run against the line
5. emitted effects are applied
6. overlays such as command hints, buttons, highlights, and routes are persisted

## Data Model

This VM should fit naturally into the SQLite schema already chosen.

Main persistence targets:

1. `variables`
2. `log_entries`
3. `log_overlays`
4. `events`

Additional tables to add later for the built-in engine:

1. `alias_rules`
2. `trigger_rules`
3. `timer_state`

## Proposed SQLite Tables For Built-In Rules

### alias_rules

Columns:

1. `id`
2. `session_id`
3. `name`
4. `template`
5. `enabled`
6. `created_at`
7. `updated_at`

### trigger_rules

Columns:

1. `id`
2. `session_id`
3. `name`
4. `pattern`
5. `flags`
6. `enabled`
7. `stop_after_match`
8. `effects_json`
9. `created_at`
10. `updated_at`

### timer_state

Columns:

1. `id`
2. `session_id`
3. `name`
4. `fire_at`
5. `payload_json`
6. `created_at`

## Minimal Built-In Feature Set

This is the smallest useful built-in engine.

### Variables v1

1. string values only
2. `$var` substitution in commands
3. set via effect or command helper later

### Aliases v1

1. exact name match
2. `%1`, `%2`, `%3` parameter substitution
3. `;` expansion into multiple commands

### Triggers v1

1. regex on `plain_text`
2. emit `send_command`
3. emit `show_button`
4. emit `write_window`
5. emit `echo`

### Effects v1

1. no arbitrary code
2. no loops
3. no condition language beyond simple trigger enable state

## Integration With Plugins

The built-in VM and plugins should coexist.

Proposed order of responsibility:

1. transport normalizes input/output
2. built-in VM handles common deterministic rules
3. plugins can subscribe to the same line and emit additional effects

Important rule:

1. plugins must not be required for basic quality-of-life scripting

## Planned UX

This is where the built-in VM becomes product value.

Later the browser UI should allow users to:

1. add alias
2. add trigger
3. add highlight
4. add button on match
5. route line to a window
6. set and inspect variables

This will make the client useful for casual players without plugin setup.

## Migration Relationship To Ruby Config

The Ruby config remains the reference for semantics.

But the built-in VM should not depend on `config.rb` forever.

Likely path:

1. short term: Ruby compatibility plugin for legacy users
2. medium term: built-in aliases/triggers/variables in SQLite
3. long term: UI editing for built-in rules and optional plugin bridge for advanced users

## Immediate Next Design Steps

1. define `alias_rules` schema precisely
2. define `trigger_rules.effects_json` format
3. define v1 effect executor contract
4. decide whether variable assignment uses a command helper or direct UI only in the first pass

## Summary

The built-in VM should be:

1. small
2. deterministic
3. data-driven
4. powerful enough for common MUD workflows
5. separate from plugin runtimes

It is not a Ruby port.

It is the native Go rule engine for the new client.
