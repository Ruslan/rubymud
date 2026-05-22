# Version 0.0.8.9.8.c Plan - Regex-First Pattern Editor

## Problem

Users want to write common trigger patterns in a simpler MUD-client style:

```tintin
#act {%1 ударил %2} {помочь %1;пнуть %2}
```

RubyMUD already has a regex-backed `CompiledMatcher` layer. Adding a second native matcher language would increase runtime complexity, storage complexity, and compatibility risk.

## Decision

Regex remains the canonical storage and runtime language.

The UI may provide a WYSIWYG/simple pattern editor, but it must compile to the same stored regex template that the VM already uses.

```text
User-facing simple pattern: %1 ударил %2
Stored pattern template:    ^(.*?) ударил (.*)$
Runtime matcher:            Go regexp after $var expansion
```

This is not a commitment to full TinTin++ syntax. It is a small editor DSL that generates Go-compatible regex.

## Research Findings

`jmc_explorer` compared MUD clients and found a consistent pattern:

1. TinTin++ uses a simplified pattern syntax, then compiles it to PCRE/PCRE2.
2. Tortilla also compiles classic placeholders to PCRE.
3. Mudlet exposes explicit pattern modes: substring, exact match, Perl regex, color, etc.
4. JMC and zMUD/CMUD have classic custom matchers plus a regex mode.
5. Many clients use PCRE features, but common MUD trigger usage mostly fits regular regex capture patterns.

Design conclusion for RubyMUD:

1. Keep regex as the canonical layer.
2. Do not implement full TinTin++ as the runtime language.
3. Add a UI-only DSL only when it can compile cleanly to Go `regexp`.
4. Keep raw regex mode as the escape hatch for anything complex.

## PCRE vs Go Regexp

TinTin++ embedded `{...}` fragments are PCRE. Go `regexp` is RE2-like, not PCRE.

PCRE features that do not compile to Go `regexp` include:

```regex
(?=foo)      lookahead
(?<=foo)     lookbehind
(\w+)\1      backreference
(?(1)a|b)    conditional
```

Therefore full TinTin++ pattern compatibility cannot be promised while runtime stays on Go `regexp`.

RubyMUD should support only the subset it can compile to safe, linear-time Go regex.

## Storage And Runtime

Storage remains unchanged:

```text
trigger_rules.pattern = regex template string
substitute_rules.pattern = regex template string
highlight_rules.pattern = regex template string
```

Runtime remains unchanged:

```text
stored regex template -> $var expansion -> regexp.Compile -> hot-path matching
```

`$var` pattern templates from `0.0.8.9.8.b` remain valid. The simple pattern editor must preserve `$var` tokens as pattern-template variables, not escape them as literal dollars.

Example:

```text
Simple pattern: $lider сказал %1
Stored regex:   ^$lider сказал (.*)$
Runtime regex:  ^<quoted lider value> сказал (.*)$
```

## Simple Pattern Syntax V1

Supported:

```text
%1 .. %9  capture placeholders
%%        literal percent sign
$name     existing RubyMUD pattern variable token
text      literal text
```

Captures must be sequential and in occurrence order.

Valid:

```text
%1 ударил %2
$lider сказал %1
Вы получили %1 опыта.
```

Invalid or unsupported in V1:

```text
%2 ударил %1
%10 ударил %11
%* ударил %w
сказа{л|ла}
```

The UI should validate this before conversion.

## Simple Pattern To Regex

Convert simple pattern to a stable regex template.

Example:

```text
%1 ударил %2
```

becomes:

```regex
^(.*?) ударил (.*)$
```

Rules:

1. Literal text is escaped for Go regex.
2. `$name` variable tokens are preserved for `CompiledMatcher` variable expansion.
3. `%%` becomes literal `%`.
4. `%N` before later pattern content becomes `(.*?)`.
5. Final `%N` before the end anchor becomes `(.*)`.
6. Add `^` and `$` anchors by default.
7. Reject non-sequential capture numbers.

The TinTin++-style lazy/greedy split matters:

```text
%1 ударил %2
```

If both wildcards are greedy, the first one can consume too much. The middle wildcard should be lazy so it stops at the next literal separator. The final wildcard can be greedy because nothing follows it.

If RubyMUD later decides captures must be non-empty, use `(.+?)` for middle captures and `(.+)` for the final capture. The important rule is still lazy in the middle, greedy at the end.

## Regex To Simple Projection

The UI may project compatible regex templates back to simple patterns.

Examples:

```regex
^(.*?) ударил (.*)$
```

```text
%1 ударил %2
```

```regex
^$lider сказал (.*)$
```

```text
$lider сказал %1
```

Compatible subset:

1. Optional `^` start anchor.
2. Optional `$` end anchor.
3. Literal text.
4. `$name` variable tokens.
5. Simple capture groups: `(.*)`, `(.*?)`, `(.+)`, `(.+?)`.
6. Escaped literal characters produced by the converter.

Not compatible:

1. Alternation: `(л|ла)`.
2. Character classes: `[а-я]`.
3. Optional groups: `(foo)?`.
4. Repetition outside the canonical captures: `.*`, `.+`, `{1,3}`.
5. Non-capturing groups: `(?:...)`.
6. Nested groups.
7. Lookarounds or other PCRE-only syntax.

If a regex is not compatible, the UI should show regex mode only.

The converter must be conservative. If unsure, return incompatible and keep regex mode.

## Future DSL Growth

RubyMUD can later add convenience syntax if it still compiles to Go regex.

Possible future syntax:

```text
сказа{л|ла}
%d монет
%w сказал %1
%!{л|ла}
```

Potential compilation:

```text
{л|ла}   -> (л|ла)
%d       -> ([0-9]+)
%w       -> ([A-Za-z0-9_]+)
%!{...}  -> non-capturing compatible fragment, if Go regexp supports it
```

This should be framed as RubyMUD editor syntax, not full TinTin++ compatibility.

Do not accept embedded fragments that Go `regexp` cannot compile.

## UI Behavior

For each pattern rule:

1. Try `regex -> simple pattern` projection.
2. If compatible, show both Simple and Regex modes.
3. If incompatible, show Regex mode and explain that Simple mode cannot represent this regex.
4. When editing Simple mode, save only the generated regex template to storage.
5. Show generated regex preview before save.

No generated regex needs to be stored separately.

No `pattern_syntax` column is required for this version.

## Backend Helpers

Add shared conversion helpers, preferably in Go first so behavior is covered by backend tests.

Suggested functions:

```go
func SimplePatternToRegex(pattern string) (string, error)
func RegexToSimplePattern(pattern string) (string, bool)
```

The UI can either call a small API endpoint or use a mirrored TypeScript implementation. If mirrored, tests must keep Go and TypeScript behavior aligned.

## Runtime Impact

No matcher runtime changes are required.

`CompiledMatcher` stays regex-backed:

```text
stored regex template -> variable expansion -> regexp.Compile -> hot-path matching
```

This feature is about editing and compatibility, not a new matching engine.

## Tests

Add conversion tests for:

1. `%1 ударил %2` -> `^(.*?) ударил (.*)$`.
2. `$lider сказал %1` preserves `$lider` and converts `%1`.
3. `Вы получили %1 опыта.` escapes literal punctuation correctly.
4. `%% загрузки: %1` handles literal `%`.
5. `^(.*?) ударил (.*)$` -> `%1 ударил %2`.
6. `^(.+) ударил (.+)$` also projects to `%1 ударил %2`.
7. Escaped regex literals are unescaped correctly in Simple projection.
8. Alternation `(л|ла)` is rejected as incompatible in V1.
9. Character classes are rejected as incompatible in V1.
10. Non-sequential captures in Simple input are rejected.
11. PCRE-only syntax is rejected or fails validation before save.

Add UI tests if the Settings UI exposes the editor in the same task.

## Performance Notes

This approach keeps the current precompiled regex hot path.

The previous performance win came from moving regex compilation and variable expansion out of the hot path. This plan keeps that benefit and only changes how users edit compatible patterns.

Native simple matchers might be faster for simple templates, but that is not the goal of this version.

If matching speed becomes a bottleneck again, add benchmarks first:

```text
BenchmarkRegexMatcher200Rules
BenchmarkNativeSimpleMatcher200Rules
```

Only add a native matcher if benchmark data justifies a second runtime matching engine.

Mudlet's explicit substring/exact modes are worth considering separately as future performance features, but they should be modeled as explicit rule modes, not hidden behind the Simple editor.

## Non-Goals

Do not add a new storage column.

Do not add a second native matcher runtime.

Do not try to convert arbitrary regex into simple patterns.

Do not implement full TinTin++ pattern syntax in this version.

Do not make alternation like `(л|ла)` editable in Simple mode in V1.

Do not accept PCRE features that Go `regexp` cannot compile.
