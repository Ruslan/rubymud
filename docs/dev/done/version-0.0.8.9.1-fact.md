# Version 0.0.8.9.1 — Hide Local Command Echo Noise (Done)

## Current State

- **`IsInternal`** field on `vm.Result` in `types.go:43` — marks local echo results from `#if` errors, `#showme`, `#woutput`, `#tts` diagnostics.
- **Filtering**: `session.go:925` and `commands.go:57` filter out `IsInternal` results from normal log output. Internal results are not persisted to DB.
- **Tests**: `echo_hiding_test.go` (46-136) — nested aliases with `#var` suppressed, `#showme` visible.
