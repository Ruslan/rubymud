# Version 0.0.8.9.8.c Plan - MUD Pattern Editor Projection

## Problem

Users want to write common trigger patterns in a simple MUD-client style:

```tintin
#act {%1 ударил %2} {помочь %1;пнуть %2}
```

But the VM runtime already has a fast regex-based `CompiledMatcher` layer. We do not want to introduce a second native matching engine unless profiling proves it is necessary.

## Product Direction

Keep storage and runtime regex-only.

Add a UI/editor projection that lets compatible regex patterns be edited as MUD patterns.

This is similar to a WYSIWYG editor:

```text
User-facing MUD pattern: %1 ударил %2
Stored/runtime regex:    ^(.+) ударил (.+)$
```

The user can edit a friendly pattern language, while the engine stores and executes a regex.

## Target Behavior

Storage remains unchanged:

```text
trigger_rules.pattern = regex string
substitute_rules.pattern = regex string
highlight_rules.pattern = regex string
```

Runtime remains unchanged:

```text
regex string -> CompiledMatcher -> *regexp.Regexp
```

The UI decides whether a stored regex can be shown as a MUD pattern.

Compatible regex example:

```regex
^(.+) ударил (.+)$
```

MUD projection:

```text
%1 ударил %2
```

Incompatible regex example:

```regex
^$lider сказа(л|ла) группе: "сост"$
```

This contains alternation and cannot be represented by the simple MUD projection.

## Compatible Regex Subset

The first version should support only a conservative reversible subset.

Allowed:

1. Optional `^` at the start.
2. Optional `$` at the end.
3. Literal text.
4. Capture groups exactly matching `(.+)`.
5. Escaped literal characters produced by `regexp.QuoteMeta`.

Not compatible:

1. Alternation: `(л|ла)`.
2. Character classes: `[а-я]`.
3. Optional groups: `(foo)?`.
4. Repetition outside the canonical capture: `.*`, `.+?`, `{1,3}`.
5. Non-capturing groups: `(?:...)`.
6. Nested groups.
7. Lookarounds or PCRE-only syntax.

If a regex is not compatible, the UI should show regex mode only.

## MUD Pattern Syntax

Supported in the first version:

```text
%1 .. %9  capture placeholders
%%        literal percent sign
everything else is literal text
```

Captures must be sequential and in occurrence order.

Valid:

```text
%1 ударил %2
Вы получили %1 опыта.
```

Invalid or unsupported for the first version:

```text
%2 ударил %1
%10 ударил %11
%* ударил %w
```

The UI should validate this before converting to regex.

## MUD To Regex Conversion

Convert MUD pattern to canonical regex:

```text
%1 ударил %2
```

becomes:

```regex
^(.+) ударил (.+)$
```

Rules:

1. Literal text is escaped with `regexp.QuoteMeta`.
2. `%N` becomes `(.+)`.
3. `%%` becomes literal `%`.
4. Add `^` and `$` anchors by default.
5. Reject non-sequential capture numbers.

Generated regex should be stable. Repeated save without edits should not produce regex churn.

## Regex To MUD Projection

Convert compatible regex back to MUD pattern for display.

Example:

```regex
^(.+) ударил (.+)$
```

becomes:

```text
%1 ударил %2
```

Implementation options:

1. Use `regexp/syntax` to parse the regex and inspect the AST.
2. Or implement a small scanner for the conservative subset.

Recommended: start with `regexp/syntax` if it makes compatibility detection clearer. Fall back to a small scanner only if the AST representation makes escaped literals awkward.

The converter must be conservative. If unsure, return incompatible and keep regex mode.

## UI Behavior

For each pattern rule:

1. Try `regex -> MUD` projection.
2. If compatible, show both MUD and Regex tabs.
3. If incompatible, show Regex tab and explain why MUD mode is unavailable.
4. When editing MUD mode, save only the generated regex to storage.
5. Show generated regex preview before save.

No generated regex needs to be stored separately.

No `pattern_syntax` column is required for this version.

## Backend Helpers

Add shared conversion helpers, preferably in a package usable by both backend tests and UI API code.

Suggested functions:

```go
func MudPatternToRegex(pattern string) (string, error)
func RegexToMudPattern(pattern string) (string, bool)
```

If conversion is implemented in TypeScript for immediate UI use, mirror it with Go tests or keep a small backend endpoint to avoid drift.

Preferred long-term approach: one backend conversion implementation with tests, exposed to UI through an API if needed.

## Runtime Impact

No matcher runtime changes are required.

`CompiledMatcher` stays regex-backed:

```text
stored regex -> variable expansion -> regexp.Compile -> hot-path matching
```

This feature is about editing and compatibility, not a new matching engine.

## Tests

Add conversion tests for:

1. `%1 ударил %2` -> `^(.+) ударил (.+)$`.
2. `Вы получили %1 опыта.` -> `^Вы получил...$` with correct escaping.
3. `%% загрузки: %1` handles literal `%`.
4. `^(.+) ударил (.+)$` -> `%1 ударил %2`.
5. Escaped regex literals are unescaped correctly in MUD projection.
6. Alternation `(л|ла)` is rejected as incompatible.
7. Character classes are rejected as incompatible.
8. Non-sequential captures in MUD input are rejected.

Add UI tests if the Settings UI exposes this editor in the same task.

## Performance Notes

This approach does not make matching faster than the current precompiled regex path.

The previous performance win came from moving regex compilation and variable expansion out of the hot path. This plan keeps that benefit and only changes how users edit compatible patterns.

Native MUD matchers might be faster for simple templates, but that is not the goal of this version. The goal is simple UX and script compatibility with minimal backend risk.

If matching speed becomes a bottleneck again, add benchmarks first:

```text
BenchmarkRegexMatcher200Rules
BenchmarkNativeMudMatcher200Rules
```

Only add a native MUD matcher if benchmark data justifies a second runtime matching engine.

## Non-Goals

Do not add a new storage column.

Do not add a second native matcher runtime.

Do not try to convert arbitrary regex into MUD patterns.

Do not support full TinTin++ pattern syntax in this version.

Do not make alternation like `(л|ла)` editable in MUD mode.
