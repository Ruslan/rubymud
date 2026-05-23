# Version 0.0.5.2 — Minimal #if (Done)

## Current State

- **`#if`** — VM command with lazy evaluation (unselected branch is not executed).
- **Syntax**: `#if {expression} {then}` / `#if {expression} {then} {else}`.
- **Expression engine**: `expr-lang/expr` (not a custom parser).
- **Supported operators**: `==`, `!=`, `>`, `<`, `>=`, `<=`, `+`, `-`, `*`, `/`, `%`, `&&`, `||`, `!`, `()`, `$var`, `%N` captures.
- **Files**: `go/internal/vm/commands.go:50-53` (dispatch), `commands.go:196-237` (dispatchIf), `expr.go` (EvalExpression + preprocessAndValidate).
- **Tests**: `if_test.go` (8 cases), `if_regression_test.go` (206 lines), `expr_test.go`.
- **Docs**: `docs/engine-commands.md:447-484`.
