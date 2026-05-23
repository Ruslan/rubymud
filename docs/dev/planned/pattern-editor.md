# Regex-First Pattern Editor

## Problem

Users want to write common trigger patterns in a simpler MUD-client style:

```tintin
#act {%1 ударил %2} {помочь %1;пнуть %2}
```

RubyMUD already has a regex-backed `CompiledMatcher` layer. Adding a second native matcher language would increase runtime complexity, storage complexity, and compatibility risk.

## Decision

Regex remains the canonical runtime language.

The user-facing syntax must be explicit and persisted. RubyMUD should not guess whether a rule should be shown as regex or as a simple pattern.

The UI may provide a WYSIWYG/simple pattern editor, but it must compile to the same runtime regex template that the VM already uses.

```text
User-facing simple pattern: %1 ударил %2
Stored pattern template:    ^(.*?) ударил (.*)$
Runtime matcher:            Go regexp after $var expansion
```

This is not a commitment to full TinTin++ syntax. It is a small editor DSL that generates Go-compatible regex.

Key product rule:

1. If the user entered a simple pattern, keep showing it as a simple pattern.
2. If the user entered a regex, keep showing it as a regex.
3. Do not silently convert `(.+)` into `%1` for display.
4. Conversion previews are allowed, but representation changes must be user-driven.

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

Runtime storage remains regex-backed, but authoring metadata should be persisted:

```text
trigger_rules.pattern = compiled regex template string
substitute_rules.pattern = compiled regex template string
highlight_rules.pattern = compiled regex template string

pattern_syntax = "regex" | "simple"
pattern_source = original user-authored regex or simple pattern
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

## Explicit Syntax Modes

RubyMUD should expose two explicit authoring modes:

1. `simple` - `%1`/`%2` style pattern syntax for common MUD triggers
2. `regex` - raw Go-compatible regex template syntax

Settings UI behavior:

1. use a visible mode selector, such as `Pattern` / `Regex`
2. keep the user's selected mode when reopening a rule
3. show the compiled regex preview for `simple` mode before save
4. do not auto-switch modes based on whether a regex is projectable to a simple pattern
5. allow an explicit "Convert to Pattern" / "Convert to Regex" action later if useful, but never do it silently

Console/import/export behavior:

1. plain pattern text means `simple` syntax
2. `/.../` means `regex` syntax
3. `/.../` is primarily for console commands and `.tt` import/export, not required as the main Settings UI affordance
4. literal `/` escaping rules are needed only inside `/.../` regex syntax

Examples:

```text
#act {%1 ударил %2} {помочь %1}       ;; simple pattern
#act {/^(.*?) ударил (.*)$/} {помочь %1} ;; regex
```

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
6. Anchoring must be explicit in the product model, not hidden.
7. Reject non-sequential capture numbers.

Initial recommendation for V1:

1. expose `match from start` and `match to end` checkboxes in Settings for simple patterns
2. default `match from start` to enabled for new simple patterns because broad MUD triggers are risky and surprising
3. default `match to end` to disabled unless user asks for strict full-line matching
4. store the resulting compiled regex in `pattern`, while preserving the original pattern and mode in `pattern_source` / `pattern_syntax`
5. when importing JMC-style patterns, do not assume start anchoring unless the imported pattern explicitly contains `^`

Open compatibility question:

1. verify TinTin++ default anchoring behavior for `%1`-style patterns before finalizing import semantics
2. JMC requires explicit `^` when the user wants start-of-line matching, so JMC import should respect that

The TinTin++-style lazy/greedy split matters:

```text
%1 ударил %2
```

If both wildcards are greedy, the first one can consume too much. The middle wildcard should be lazy so it stops at the next literal separator. The final wildcard can be greedy because nothing follows it.

If RubyMUD later decides captures must be non-empty, use `(.+?)` for middle captures and `(.+)` for the final capture. The important rule is still lazy in the middle, greedy at the end.

## Regex To Simple Projection

Projection is optional and must not control normal display mode.

The UI may offer an explicit conversion action for compatible regex templates, but it should not automatically project regex templates back to simple patterns just because it can.

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

If a regex is not compatible, explicit conversion to Simple mode should be disabled with an explanation.

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

1. Load and display the persisted `pattern_syntax` and `pattern_source`.
2. If `pattern_syntax = simple`, show Pattern mode and a generated regex preview.
3. If `pattern_syntax = regex`, show Regex mode.
4. Do not infer display mode from the stored regex.
5. When editing Simple mode, save both the generated regex template and the original simple source.
6. When editing Regex mode, save the regex as both runtime pattern and regex source.

The generated regex remains the runtime pattern.

`pattern_syntax` and `pattern_source` are required to preserve user intent and avoid surprising display changes.

## Backend Helpers

Add shared conversion helpers, preferably in Go first so behavior is covered by backend tests.

Suggested functions:

```go
func SimplePatternToRegex(pattern string) (string, error)
func RegexToSimplePattern(pattern string) (string, bool)
```

`RegexToSimplePattern` is for explicit conversion only. It is not for deciding how an existing rule should be displayed.

The UI can either call a small API endpoint or use a mirrored TypeScript implementation. If mirrored, tests must keep Go and TypeScript behavior aligned.

## Preview Semantics

Settings preview must use RubyMUD runtime pattern semantics, not browser JavaScript `RegExp` semantics.

Current risk example:

```text
(?i)$target1
```

This is valid for Go `regexp` after RubyMUD `$target1` expansion, but browser-side `new RegExp("(?i)$target1", "g")` is invalid because JavaScript does not support Go-style inline `(?i)` flags. A UI preview column that reports `(invalid regex)` for this pattern is misleading if the backend/runtime accepts it.

Preview requirements:

1. do not use JS `RegExp` as the source of truth for RubyMUD pattern validity
2. preview must account for `$var` pattern-template expansion
3. preview must accept Go regexp syntax that RubyMUD runtime accepts, including inline flags such as `(?i)`
4. if the UI cannot run the real matcher locally, it should call a backend preview/validation endpoint
5. if backend preview is not available yet, show `Preview unavailable` or `Cannot preview in browser`, not `(invalid regex)`, for patterns that may be valid in RubyMUD but invalid in JS

Recommended backend endpoint shape:

```text
POST /api/patterns/preview
```

Request:

```json
{
  "syntax": "regex",
  "pattern": "(?i)$target1",
  "sample": "Тартис стоит здесь.",
  "profile_id": 1,
  "rule_type": "substitution"
}
```

Response:

```json
{
  "valid": true,
  "matched": true,
  "effective_pattern": "(?i)Тартис",
  "captures": []
}
```

Exact API shape can change during implementation, but validation/preview must run through the same Go-side pattern compiler/matcher used by live rules.

## Runtime Impact

No matcher runtime changes are required.

`CompiledMatcher` stays regex-backed:

```text
stored regex template -> variable expansion -> regexp.Compile -> hot-path matching
```

This feature is about editing and compatibility, not a new matching engine.

## Tests

Add conversion tests for:

1. simple `%1 ударил %2` compiles to the expected regex according to selected anchor options.
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

Add persistence/display tests for:

1. a rule authored as regex `(.+)` reopens in Regex mode, not Pattern mode
2. a rule authored as simple `%1` reopens in Pattern mode
3. `/.../` console/import syntax is stored as `pattern_syntax = regex`
4. non-`/.../` console/import syntax is stored as `pattern_syntax = simple`

Add preview/runtime parity tests for:

1. `(?i)$target1` is not reported as invalid only because JS `RegExp` cannot parse it
2. `$target1` expansion is included in preview matching
3. backend preview validity matches live `CompiledMatcher` validity
4. UI displays `Preview unavailable` instead of `(invalid regex)` when browser-only preview cannot safely evaluate a RubyMUD pattern

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

Do not add a second native matcher runtime.

Do not try to convert arbitrary regex into simple patterns.

Do not implement full TinTin++ pattern syntax in this version.

Do not make alternation like `(л|ла)` editable in Simple mode in V1.

Do not accept PCRE features that Go `regexp` cannot compile.
