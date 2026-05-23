# Version 0.0.8.9.5 — Regex Precompilation (Done)

## Current State

- **`CompiledMatcher`**: `pattern_matcher.go:15-20` — caches `*regexp.Regexp` per pattern string.
- **Cache**: `pattern_matcher.go:37-44` — checks `cache[effectivePattern]` before compiling. Used by triggers, highlights, substitutes.
- **`compiledSubstitutes[]`**: Precompiled at VM init in `runtime.go:197-204`.
- **Tests**: `runtime_cache_test.go`.
