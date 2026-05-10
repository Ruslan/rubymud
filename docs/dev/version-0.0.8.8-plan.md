# Version 0.0.8.8 Plan: `#if` and Expression Layer MVP

## Goal

Add the first conditional runtime command while laying down a reusable expression layer for future math features.

`0.0.8.8` should implement:

1. `#if {expression} {then}`
2. `#if {expression} {then} {else}`
3. a small internal expression evaluator API that `#if` uses to obtain a boolean result
4. enough arithmetic support to make the expression layer useful later for `#math`-style commands

The important design choice is that this release should not build a one-off `#if` parser. It should introduce a reusable VM expression layer, with `#if` as the first caller.

---

## Product Problem

RubyMUD currently has aliases, variables, triggers, timers, and delayed commands, but no local conditional execution. This blocks common migrated scripts such as:

```text
#alias {k} {#if {$target == ""} {#showme {No target}} {kill $target}}
#action {^You are ready\.$} {#if {$auto == 1} {stand;bash $target}}
```

Existing documentation explicitly lists `#if` as not implemented. That is correct today, but it is now a compatibility gap for JMC/TinTin++-style scripting.

---

## Scope

### Runtime Command

Add `#if` as a VM-local command.

Supported forms:

```text
#if {expression} {then-command}
#if {expression} {then-command} {else-command}
```

Behavior:

1. evaluate `expression` through the new expression layer
2. require the result to be boolean
3. execute only the selected branch through the existing VM pipeline
4. return no output when the condition is false and no `else` branch exists
5. return a local diagnostic echo when usage or expression evaluation fails

Branch execution must preserve all current pipeline behavior:

1. `;` splits multiple statements inside the chosen branch
2. aliases work inside branches
3. local commands work inside branches
4. variable substitution in branches happens only when the chosen branch is executed
5. speedwalk behavior remains unchanged

### Expression Layer MVP

Introduce a reusable expression package/file inside `go/internal/vm`, not an `if`-specific implementation.

Recommended internal shape:

```go
type ExprValue struct {
    // internal representation for bool, number, string
}

func evalExpression(input string, env ExprEnv) (ExprValue, error)

type ExprEnv interface {
    GetVar(name string) (string, bool)
}
```

The exact type names can differ, but the boundary should stay narrow:

1. expression input string in
2. variable lookup abstraction in
3. typed value or error out

`#if` should call this layer and then require `ExprValue` to be boolean.

### Operators For This Release

Keep the first version deliberately small.

Supported expression syntax:

1. variables: `$name`
2. numeric literals: `1`, `1.5`, `0.25`
3. string literals: `"text"` and optionally `'text'` if the backend supports it cleanly
4. equality: `==`
5. arithmetic: `+`, `-`, `*`, `/`
6. parentheses for arithmetic grouping: `( ... )`

Examples that must work:

```text
#if {$auto == 1} {stand}
#if {$target == ""} {#showme {No target}} {kill $target}
#if {$hp + 10 == $maxhp / 2} {#showme {half}}
#if {($a + $b) / 2 == 10} {say average ten}
```

### Backend Choice

The expression layer may use `expr-lang/expr` as its internal backend, but VM code must not call that library directly.

Recommended implementation approach:

1. add `expr-lang/expr` only behind the new expression wrapper
2. preprocess RubyMUD expression syntax into backend syntax
3. extract only variables actually used in the expression
4. pass only those variables to the backend environment
5. normalize backend results into `ExprValue`
6. normalize backend errors into stable RubyMUD diagnostics

The wrapper must preserve RubyMUD syntax even if the backend has different syntax rules.

Example rewrite:

```text
$hp + 10 == $maxhp / 2
```

may become internally:

```text
__var_hp + 10 == __var_maxhp / 2
```

with an environment containing only:

```text
__var_hp
__var_maxhp
```

### Variable Handling

Expression variables are read from the VM runtime variable map plus builtin time variables where appropriate.

Rules:

1. `$name` inside expressions is an expression variable, not eager text substitution
2. `$name` inside `then` / `else` branches keeps the existing VM substitution behavior
3. unknown variables should evaluate as the literal string `""` or produce a stable error; choose one behavior and document it in tests
4. numeric-looking runtime values may be passed as numbers for arithmetic
5. non-numeric runtime values remain strings

Recommended choice for this release: unknown expression variables evaluate to `""`.

Reasoning:

1. this makes `$target == ""` useful even when `target` has not been set
2. it avoids turning common alias guards into hard errors
3. arithmetic using an unknown or non-numeric variable should still fail clearly when the backend cannot evaluate it numerically

### Lazy Branch Semantics

`#if` must be lazy.

Only the selected branch may be evaluated by the VM pipeline.

This matters for scripts like:

```text
#if {$mode == "combat"} {#var {target} {orc}} {#var {target} {}}
```

The non-selected branch must not:

1. substitute variables
2. execute local commands
3. mutate variables
4. send commands to the MUD

---

## Required VM Pipeline Change

The current pipeline substitutes `$variables` before local command dispatch.

That cannot be used for `#if`, because it would eagerly substitute variables across the whole command, including both branches.

Required behavior:

1. detect `#if` before generic `substituteVars(...)`
2. dispatch it without eager substitution
3. let the expression layer resolve variables inside the condition
4. let the normal VM pipeline substitute variables inside the selected branch only

This should be a narrow special case for `#if`, not a broad rewrite of the command pipeline.

Unknown `#...` command fallback behavior must remain unchanged.

---

## Files Likely Involved

Expected backend files:

1. `go/go.mod`
2. `go/go.sum`
3. `go/internal/vm/commands.go`
4. `go/internal/vm/commands_if.go`
5. `go/internal/vm/expr.go`
6. `go/internal/vm/expr_test.go`
7. `go/internal/vm/command_test.go` or `go/internal/vm/if_command_test.go`

Expected docs:

1. `docs/engine-commands.md`

`commands_if.go` is recommended so the command implementation does not grow `commands.go` further.

---

## Non-Goals

Do not implement these in `0.0.8.8`:

1. `#math`
2. `#set` / computed variable assignment
3. comparison operators beyond `==`
4. `!=`, `<`, `>`, `<=`, `>=`
5. logical operators `&&`, `||`, `!`
6. regex operators `=~`, `!~`
7. regex capture variables
8. functions such as `min`, `max`, `abs`, `round`, `sqrt`, `log`, `sin`
9. modulo `%`
10. arrays, maps, object access, or method calls
11. TinTin++ exact semantics for string pattern matching
12. JMC `#match` / `#strcmp`
13. eager branch evaluation compatibility with JMC

These are intentionally left for later expression-layer expansion.

---

## Error Behavior

Use local echo diagnostics, not MUD commands, for `#if` failures.

Recommended messages:

```text
#if: usage: #if {expression} {then} [{else}]
#if: expression must evaluate to boolean
#if: expression error: <details>
```

Rules:

1. usage errors do not execute either branch
2. expression parse errors do not execute either branch
3. expression runtime errors do not execute either branch
4. division by zero should be reported as an expression error if the backend exposes it that way
5. malformed braces should follow existing `splitBraceArg` behavior unless a safer local validation is added

---

## Testing Plan

### Expression Layer Unit Tests

Add focused tests that do not go through the full VM command pipeline.

Required cases:

1. `$a == 1` with numeric variable
2. `$target == ""` with missing variable
3. `$target == "orc"` with string variable
4. `$hp + 10 == 50`
5. `$hp / 2 == 25`
6. `($a + $b) / 2 == 10`
7. string literal containing `$name` is not rewritten as a variable if quoted interpolation is not supported
8. invalid expression returns an error
9. non-boolean expression result is distinguishable from boolean result

### `#if` Command Tests

Required cases:

1. true branch sends command
2. false condition with no else sends nothing
3. false condition executes else branch
4. chosen branch may contain multiple `;` statements
5. chosen branch may execute local commands such as `#var` and `#showme`
6. aliases work inside selected branch
7. non-selected branch is not executed
8. non-selected branch does not mutate variables
9. branch variable substitution is lazy
10. expression variables are resolved from current runtime variables
11. usage error returns local diagnostic
12. expression error returns local diagnostic and sends nothing to MUD

### Regression Tests

Ensure existing behavior remains unchanged:

1. normal input still substitutes `$variables`
2. aliases still substitute `$variables` at execution time
3. unknown `#foo` commands still fall through as MUD commands
4. `#N` repeat still works
5. `#nop` remains silent

---

## Documentation Updates

Update `docs/engine-commands.md`:

1. add `#if` under commands
2. document supported forms
3. document the minimal expression syntax
4. document lazy branch behavior
5. add examples
6. remove `#if` from the "not implemented as runtime commands" list
7. explicitly state that `#math`, regex matching, logical operators, and non-equality comparisons are not in this release

Recommended examples:

```text
#if {$auto == 1} {stand}
#if {$target == ""} {#showme {No target}} {kill $target}
#if {$hp + 10 == $maxhp / 2} {#showme {half hp}}
```

---

## Future Expansion Path

The expression layer should be designed so later releases can add operators without changing `#if` integration.

Likely next steps:

1. `!=`, `<`, `>`, `<=`, `>=`
2. logical operators `&&`, `||`, `!`
3. modulo `%`
4. regex match operators `=~`, `!~` using Go RE2
5. small whitelisted math functions: `abs`, `min`, `max`, `round`, `floor`, `ceil`
6. `#math {name} {expression}` to store numeric expression results in session variables
7. optional exact string/number conversion rules if migration scripts need them

The important constraint: all future features should enter through the expression layer, not through ad hoc parsing in individual commands.

---

## Open Questions

### Backend Commitment

Should `0.0.8.8` commit to `expr-lang/expr`, or should the first implementation spike verify it against the MVP test list before the dependency is accepted?

Recommended answer: use `expr-lang/expr` only if the wrapper can keep RubyMUD semantics stable and the MVP tests remain simple. If the wrapper becomes larger than a small scanner/rewrite layer, reconsider a hand-written evaluator.

### Unknown Variables

Should unknown expression variables evaluate to `""` or produce an error?

Recommended answer: evaluate to `""` for string/equality guards, but make arithmetic with non-numeric values fail clearly.

### Single-Quoted Strings

Should expression strings support both `'text'` and `"text"`?

Recommended answer: support only what the selected backend supports cleanly in this release. Do not add a separate quote-normalization layer unless tests require it.

---

## Acceptance Criteria

1. `#if {expression} {then}` is recognized as a local VM command.
2. `#if {expression} {then} {else}` is recognized as a local VM command.
3. `#if` uses a reusable expression layer rather than command-local expression parsing.
4. The expression layer supports `$variables`, `==`, `+`, `-`, `*`, `/`, numeric literals, string literals, and parentheses.
5. `#if` requires the expression result to be boolean.
6. The selected branch is executed through the existing VM pipeline.
7. The non-selected branch is never evaluated or executed.
8. Branches may contain semicolon-separated commands inside braces.
9. Local commands, aliases, and variable substitution work inside selected branches.
10. Usage and expression errors produce local diagnostics and do not send commands to the MUD.
11. Unknown non-`#if` system commands still fall through to the MUD as before.
12. Existing command, alias, variable, trigger-command, repeat, and speedwalk tests continue passing.
13. New expression-layer unit tests cover numeric equality, string equality, arithmetic, grouping, missing variables, and invalid expressions.
14. New `#if` integration tests cover true branch, false branch, else branch, laziness, local commands, aliases, and diagnostics.
15. `docs/engine-commands.md` documents `#if` and no longer lists it as missing.
