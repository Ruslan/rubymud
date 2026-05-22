# Engine & VM Guide

Use this when you need to understand or modify the MUD command processing engine, scripting logic (triggers, aliases, variables), or the custom VM.

## Core Philosophy

The engine aims for **TinTin++ compatibility** at the syntax level. This allows users to import legacy configurations from JMC, Tortilla, and TinTin++.

## Processing Pipeline

### Output (MUD to User)
1. **Normalization**: Transport layer handles charset conversion and line splitting.
2. **Canonical Storage**: Each line is stored as a `log_entry` in SQLite.
3. **Substitutions (`#sub`)**: Text replacement.
4. **Gag (`#gag`)**: Suppression of lines matching a pattern.
5. **Triggers (`#action`)**: Pattern matching with side effects (sending commands, echos).
6. **Highlights (`#highlight`)**: Adding color/style overlays.
7. **Overlay Layering**: Final set of `log_overlays` is sent to the UI.

### Input (User to MUD)
1. **Prefix Check**: If starts with `#`, it's a system command.
2. **Separator Split**: Split by `;` (respecting `{}` nesting).
3. **Variable Substitution**: `$varname` -> `value`.
4. **Alias Expansion**: `name %1 %2` -> `template`.
5. **Command Execution**: Send to MUD or execute locally.

## Pattern Matching

- **Wildcards**: `%0`-%`9` (capture), `%%` (no-capture), `^` (start of line).
- **Regex**: Support for PCRE-style regex via `/pattern/` (JMC style) or `$pattern` (Tortilla style).

## Pattern Template Matchers

Pattern-based rules (`#action`, `#sub`, `#gag`, `#highlight`) must store the user-authored pattern template and use the shared compiled matcher layer.

- Do not expand `$var` when saving pattern rules.
- Pattern variables are resolved during matcher compilation, not on every incoming line.
- Variable values inside patterns must be treated as literals with `regexp.QuoteMeta`.
- Undefined `$var` in pattern templates expands to an empty string.
- Variable changes rebuild compiled matchers; do not add ad-hoc regex caches in individual consumers.
- MUD-style editor patterns such as `%1 hits %2` should first be treated as a UI/editor projection to stored regex when possible.
- Do not add a native non-regex matcher unless benchmarks show the compiled regex path is still a bottleneck.

Keep the matcher abstraction minimal. Prefer `CompiledMatcher` with a regex field while storage and runtime remain regex-based.

## Key Components

- **`go/internal/vm`**: Core parser and evaluator for commands and expressions.
- **`go/internal/session`**: Manages the lifecycle of MUD connections and state.
- **`go/internal/storage`**: Handles persistence of rules (aliases, triggers) and logs.

## Scripting Features

- **Variables**: Persistent by default in SQLite.
- **Aliases**: Support for recursion (max depth 10) and positional arguments.
- **Groups**: Rules can be grouped and enabled/disabled together via `#group`.
- **Expressions**: `#if` supports a full stack-based evaluator for math and logic.
