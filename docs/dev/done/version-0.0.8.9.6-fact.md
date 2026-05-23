# Version 0.0.8.9.6 — Dynamic Highlights with Variable Support (Done)

## Current State

- **`substitutePatternVars()`**: `pattern_matcher.go:22-29` — replaces `$var` in highlight patterns with `regexp.QuoteMeta`.
- **Recompilation**: Highlights recompiled on variable changes via `ensureFresh` version checks.
- **Apply**: `highlight_apply.go` applies ANSI formatting for matched lines.
- **Tests**: `highlight_dynamic_test.go` (179 lines) — re-evaluation of `$enemy`, literal escaping, re-application after variable change.
