# Version 0.0.8.9.7 — Operators in #if (Done)

Covered by 0.0.5.2 and 0.0.8.8 facts. All operators (`>`, `<`, `%`, `%N` captures, etc.) supported via `expr-lang/expr`.

## Current State

- Comparison: `==`, `!=`, `>`, `<`, `>=`, `<=`
- Arithmetic: `+`, `-`, `*`, `/`, `%`
- Boolean: `&&`, `||`, `!`, `not`, `and`, `or`
- Captures: `%N` with numeric coercion in numeric contexts
- Tests: `if_regression_test.go:65-83` (modulo), `:85-128` (capture guards), `expr_test.go` (20 cases).
