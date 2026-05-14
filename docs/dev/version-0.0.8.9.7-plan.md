# Plan: Support Operators and Substitutions in #if Expressions (v0.0.8.9.7)

## Goal
Fix the issue where `#if` expressions were overly restrictive and failed when using comparison operators (like `>`, `<`) or capture group substitutions (`%0`, `%1`).

## Tasks Accomplished

### 1. Expression Evaluator Enhancements
- **Modified `go/internal/vm/expr.go`**:
    - Removed the restrictive character whitelist in `preprocessAndValidate` that was blocking `>, <, !, &, |, %, *, /, -, +`.
    - Added support for `%0`, `%1`, etc. natively in the expression evaluator by mapping them to internal identifiers (`__cap_N`).
    - Updated `isAllowedIdentifier` to allow `__cap_` prefix and standard keywords like `and`, `or`, `not`, `nil`, `null`.
    - Updated `EvalExpression` signature to accept a `captures []string` argument.

### 2. Capture Context Propagation (Context-Aware Logic)
Instead of storing captures in a global VM state, they are propagated as function arguments to ensure thread-safety and correct behavior in nested calls (e.g., an alias calling another alias).

- **Stateless Flow**: Updated `ProcessInputDetailed`, `evalLine`, `evalStatement`, and `dispatchIf` to accept and pass a `captures []string` slice. This ensures that every command execution has its own local "scope" for `%0..%N`.
- **Alias Integration**: When an alias is expanded, its arguments are passed as the new capture context for the commands inside the alias template.
- **Trigger/Action Integration**: 
    - Updated `MatchTriggers` to store regex match groups in the `Effect` struct.
    - Updated `ApplyEffects` to use `ProcessInputWithCaptures`. When a trigger fires, its regex match groups become the `%0..%N` variables for any `#if` commands executed by that trigger.
- **Modified `go/internal/vm/types.go`**: Added `Captures []string` field to the `Effect` struct.
- **Modified `go/internal/vm/commands_timer.go`**: Updated `cmdDelay` signature for API consistency.

## Technical Details: Capture Group Mechanism

The implementation avoids global state to prevent "capture bleeding" between different execution levels.

1. **Preprocessing**: The `#if` expression is pre-scanned. Any occurrence of `%N` is replaced with a safe internal identifier (e.g., `__cap_0`).
2. **Environment Mapping**: At evaluation time, the current local `captures` slice is mapped to these internal identifiers.
3. **Capture Semantics**:
   - Missing/absent captures default to `""` (empty string).
   - In **string/equality context** (`== ""`, `!= ""`, `== "..."`) an empty capture is a normal empty string.
   - In **numeric context** (`>`, `<`, `>=`, `<=`, `+`, `-`, `*`, `/`, `%`, `== 0`) an empty capture is automatically coerced to `0`, so `#if {%0 > 0}` safely evaluates to false when no argument is given.
   - Numeric-looking captures like `"5"` remain valid string values for guards like `%1 != ""`, and are coerced to integers/floats only when used in numeric context.
   - Non-empty non-numeric captures in a numeric comparison context (e.g. `"goblin" > 0`) are a **compile-time type error**, which produces a `ResultEcho` diagnostic and executes **neither** branch.
4. **Error Policy**: Expression errors (syntax, unsupported operators, type mismatch) produce an internal `ResultEcho` message to the main buffer. They do **not** silently fall through to the else branch.
5. **Forbidden constructs**: function calls, array indexing, ternary operators (`?`/`:`), and the `in` operator are explicitly rejected in the pre-validator.
6. **Trigger Command Expansion**:
   - **Send effects**: the raw command (with `%1` tokens) is stored in the `Effect`; expansion happens inside `ProcessInputWithCaptures`, allowing `#if {%1 == "..."}` to work correctly.
   - **Button effects**: the command is pre-expanded at `MatchTriggers` time so the clickable button has a fully resolved command.
   - **Echo routing**: expanded at `MatchTriggers` time.
7. **Local Scoping**: Captures are passed via stack (function arguments), ensuring that nested alias/action calls do not bleed captures.

### 3. Verification and Testing
- **Added `go/internal/vm/if_regression_test.go`** covering:
  - `ден` alias: positive arg, zero, and no arg.
  - Trigger `#if {%1 == "goblin"}` string comparison.
  - `%1 != ""` guard for missing/empty/string/numeric captures.
  - Type error diagnostic for non-numeric capture in numeric comparison.
  - Modulo forms: `$hp % 2`, `$hp%2`, `$hp %2`.
  - Trigger send command expanded through `ApplyEffects`.
  - Button trigger command expanded at `MatchTriggers` time.
- **Modified `go/internal/vm/if_test.go`**: added error-case tests for unsupported syntax and confirmed expression errors return diagnostics.

## Impact
Users can now use basic boolean and comparison logic in their `#if` commands. Capture groups from aliases and triggers are correctly scoped. Expression errors are visible and never silently misdirect execution.

## Known Issues & Limitations

### Nested Capture Shadowing
When defining an `#alias` or `#action` *inside* another alias or trigger body, the `%N` variables are expanded immediately by the outer command. 
For example:
```ruby
#alias {attack_mode} {#alias {a} {kill %1}}
```
Executing `attack_mode goblin` will immediately expand the inner `%1`, resulting in the alias being defined as `#alias {a} {kill goblin}` rather than keeping the dynamic `%1`. 
This is standard early-expansion behavior in many MUD clients (like TinTin++), but unlike TinTin++, RubyMUD does not currently support escaping captures (e.g. `%%1`) to defer evaluation. If dynamic inner captures are needed, this will require a separate feature to implement escape sequences.
