# Version 0.0.8.9.8.b Plan - `$var` in Trigger Patterns

## Problem

Trigger patterns can contain variables, for example:

```text
^$lider сказа(л|ла) группе: "сост"$
```

These patterns currently do not work reliably when loaded from DB/scripts or when the variable changes after the trigger was created.

## Root Cause

Trigger patterns are handled differently from `#sub` and `#highlight`.

`#sub` and `#highlight` preserve the original pattern template and build an effective regex from current variable values when the rule is applied.

`#action` currently has two problems:

1. `cmdAction` expands variables too early when the trigger is created.
2. `rebuildCaches` compiles the raw stored trigger pattern without resolving variables.

As a result, a stored pattern like this:

```text
^$lider сказа(л|ла) группе: "сост"$
```

is compiled as raw regex. In regex syntax, `$` means end-of-line, so `^$lider` effectively means: start of line, immediately end of line, then literal `lider`. That cannot match a normal non-empty MUD line.

## Target Behavior

Trigger patterns should follow the same template-compilation model as `#sub` and `#highlight`.

The stored trigger pattern must remain a template:

```text
^$lider сказа(л|ла) группе: "сост"$
```

The compiled matcher should be built from current variable values:

```text
^Игрок сказа(л|ла) группе: "сост"$
```

Changing `$lider` should affect trigger matching without recreating the trigger.

Variable values inside patterns should be treated as literals, not regex fragments.

## Matcher Architecture

Add a shared compiled matcher layer inside `go/internal/vm`.

This layer should be used by `#action`, `#sub`, `#gag`, and `#highlight` so variable expansion, escaping, compilation, and cache invalidation are consistent.

The initial implementation should support regex matchers only, but the API should be shaped so future MUD-style matchers can be added without rewriting trigger/sub/highlight logic.

Future MUD-style matcher example:

```tintin
#act {%1 ударил %2} {помочь %1;пнуть %2}
```

This should later compile to a non-regex matcher that captures `%1` and `%2` from plain text.

Suggested internal types for the first implementation:

```go
type MatcherKind string

const (
    MatcherRegex MatcherKind = "regex"
    MatcherMud   MatcherKind = "mud"
)

type CompiledMatcher struct {
    Kind             MatcherKind
    Template         string
    EffectivePattern string
    Regex            *regexp.Regexp
}
```

Keep the first implementation minimal. Do not introduce a broad matcher interface unless it is needed by the current code. Future MUD-style matchers can replace or extend the `Regex` field with a real matcher interface later.

Consumers should not call `regexp.Compile` or perform `$var` expansion directly.

## Implementation Plan

### 1. Add shared matcher compiler

Create a new file such as `go/internal/vm/pattern_matcher.go`.

Move pattern-template logic out of substitution-specific code:

```go
func (v *VM) effectivePattern(template string) string
func (v *VM) compileMatcherTemplate(template string, cache map[string]*regexp.Regexp) CompiledMatcher
```

The cache is a local rebuild-time dedupe map passed by `rebuildCaches`, not a long-lived VM runtime cache.

The compiler should:

1. Resolve `$var` using current VM variables.
2. Quote variable values with `regexp.QuoteMeta`.
3. Expand undefined `$var` to an empty string for pattern templates.
4. Compile the current implementation as a regex matcher.
5. Log compile errors with both `Template` and `EffectivePattern`.

Do not use generic `substituteVars` for regex patterns, because it does not quote regex metacharacters.

Example:

```tintin
#var {lider} {A.B}
#action {^$lider says$} {echo ok}
```

This should match literal `A.B says`, not `AxB says`.

### 2. Preserve trigger pattern templates

Update `go/internal/vm/commands_trigger_highlight.go`.

Remove variable substitution from `cmdAction` for the trigger pattern.

Change this behavior:

```go
pattern = v.substituteVars(pattern)
```

to preserving `pattern` as-is.

Keep variable substitution for metadata such as `group`, because group names are not pattern templates.

### 3. Compile all pattern-based rules in `rebuildCaches`

Update `go/internal/vm/runtime.go`.

`rebuildCaches` should build compiled matchers for all pattern-based runtime rules:

1. Triggers.
2. Substitutions.
3. Gags.
4. Highlights.

Triggers should no longer compile raw `t.Pattern` directly.

`#sub`, `#gag`, and `#highlight` should stop expanding variables and looking up regex cache on every incoming line. They should use the precompiled matchers built during `rebuildCaches`.

This keeps the hot path fast: incoming MUD lines use ready matchers, while variable/rule changes rebuild compiled caches.

### 4. Keep full cache rebuild on variable changes

Do not implement per-variable dependency tracking in this version.

Even if a player has around 200 regex rules, a full rebuild on variable changes is acceptable because variable changes are expected to be much less frequent than incoming MUD lines.

The compiled matcher type may store metadata such as `Template` and `EffectivePattern`, but it should not require a variable-to-rule dependency index yet.

If profiling later shows variable changes are hot, dependency-based recompilation can be added as a follow-up optimization.

### 5. Invalidate compiled matchers when variables change

Trigger, substitute, gag, and highlight matchers will depend on variables, so variable changes must rebuild compiled matchers.

Update `#var` and `#unvar` paths in `go/internal/vm/commands_alias_variable.go`.

For no-store VM mode, increment `rulesVersion` when variables change so the next `ensureFresh` rebuilds compiled matchers.

For store-backed VM mode, the existing `ReloadFromStore` path should rebuild caches, but this behavior needs a regression test.

### 6. Keep replacement expansion separate

Pattern compilation and replacement expansion are different concerns.

`#sub` replacement text should continue to expand variables at apply time:

```go
replacementTemplate := v.substituteVars(rule.Replacement)
```

This preserves current behavior where replacement output can use current variable values and captures.

### 7. Update tests

Replace the current misleading bug test with behavior-oriented tests.

Add or update tests for:

1. `#action` stores trigger pattern templates without early substitution.
2. A stored trigger with `$lider` matches after `$lider` is defined.
3. Changing `$lider` changes trigger matching without recreating the trigger.
4. Variable values in trigger, substitute, gag, and highlight patterns are quoted literally.
5. Undefined variable behavior in pattern templates is explicit and tested.
6. `#sub`, `#gag`, and `#highlight` use compiled matchers and still behave as before.
7. A variable change in no-store VM mode rebuilds compiled trigger/sub/highlight/gag matchers.
8. A variable change through store-backed session/UI reload rebuilds compiled matchers.

## Verification

Run focused VM tests first:

```bash
cd go && go test ./internal/vm -run 'TestTriggerVarInPattern|TestSubstituteVars|TestApplySubs|TestHighlight|TestGag' -count=1 -v
```

Then run the full VM package:

```bash
cd go && go test ./internal/vm
```

If session/UI variable changes are touched, also run relevant session tests:

```bash
cd go && go test ./internal/session -run 'Variable|SettingsChanged' -count=1 -v
```

## Non-Goals

Do not implement MUD-style `%1` matchers in this task. Only shape the `CompiledMatcher` layer so they can be added next.

Do not implement per-variable dependency tracking for selective recompilation in this task.

Do not make variable values act as regex snippets inside current regex patterns.

Do not solve time-based builtin variables such as `$TIME` in compiled pattern templates.

Do not edit generated frontend assets in `go/internal/web/static/`.
