# Version 0.0.8.9.4 — HTML-like Markup for Local Output (Done)

## Current State

- **`renderLocalMarkup()`**: `markup.go:119` — parses `<red>`, `<b>`, `<fg #ff0000>`, `<bg blue>`, `<reset>`, `<u>`, `<i>`, `<blink>`, `<reverse>` and compiles to ANSI escape codes.
- **Usage**: Called for `#showme`/`#show` at `commands.go:181` and `#woutput` at `commands.go:189`.
- **Tests**: `markup_test.go` (131 lines, extensive tag coverage).
