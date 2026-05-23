# Version 0.0.8.9.8.b — $var in Trigger Patterns (Done)

## Current State

- **`substitutePatternVars()`**: `pattern_matcher.go:22-29` — `$var` substitution with `regexp.QuoteMeta` in trigger/highlight/sub pattern templates.
- **Usage**: Called from `compileMatcherTemplate()` (line 33), used by triggers, highlights, and substitutes.
- **Trigger commands**: `ExpandCaptures` in `triggers.go:31` handles `%N` in trigger command bodies.
