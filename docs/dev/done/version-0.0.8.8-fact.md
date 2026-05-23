# Version 0.0.8.8 — #if Expression Layer (Done)

Covered by 0.0.5.2 fact. The `#if` command uses `expr-lang/expr` for full expression evaluation.

## Current State

- **`EvalExpression`**: `expr.go:96` — full expression evaluator with `$var` and `%N` capture support.
- **Supported operators**: `==`, `!=`, `>`, `<`, `>=`, `<=`, `+`, `-`, `*`, `/`, `%`, `&&`, `||`, `!`, `()`, `not`, `and`, `or`.
- **Lazy evaluation**: Non-selected `#if` branches are never executed.
- **Depth protection**: `maxExpandDepth=10` prevents infinite recursion.
- **Error diagnostics**: Type mismatch, unsupported operators, missing branches.
