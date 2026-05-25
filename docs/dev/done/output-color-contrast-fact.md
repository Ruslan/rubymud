# Output Color Contrast — Done

## Status

Done.

## Product Decision

The original issue was that some normal and bright ANSI color pairs were hard to distinguish in live output.

Changing the existing palette by default was rejected because it changes the look current users already know. RubyMUD now keeps the previous live-output palette as the default `Classic` theme and exposes higher-contrast alternatives as opt-in per-session themes.

## Implemented Behavior

- `Classic` remains the default and preserves the previous RubyMUD live output colors.
- Sessions now have an `ansi_theme` setting.
- Settings exposes **ANSI color theme** in the session editor.
- Available themes:
  - `Classic`
  - `High Contrast`
  - `Tango Dark`
  - `Dracula`
  - `Gruvbox Dark`
- Theme CSS is split into per-theme source files under `ui/src/styles/ansi-themes/`.
- `High Contrast` keeps MUD-style bold-normal foreground promotion, so `1;3Xm` can render as bright/light while preserving bold.
- `Classic` does not promote `1;3Xm`, preserving prior behavior.
- Game client tabs should be refreshed after changing a session theme. This is a deliberate tradeoff to keep live rendering fast and simple.

## User Documentation

- Added `docs/ansi-themes.md`.
- The doc explains where to change themes, why `Classic` is default, and when to use higher-contrast terminal palettes.

## Verification

- `go test ./internal/storage ./internal/web`
- `npm test`
- `npm run build`
- Reviewed by the broker reviewer with no remaining findings.
