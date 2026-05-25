# ANSI Color Themes

RubyMUD supports per-session ANSI color themes for the live game output.

## Changing the theme

Open **Settings → Sessions**, edit the session, and choose **ANSI color theme**.

Available themes include:

- **Classic** — the default. This is the previous RubyMUD live output palette, preserved for compatibility. It is not copied from a named terminal theme; it is a softer/pastel dark palette, similar in spirit to modern editor themes. It often feels calmer for long reading because it avoids very saturated terminal colors, but normal/bright pairs can be less distinguishable.
- **High Contrast** — brighter light/bright ANSI colors for easier distinction.
- **Tango Dark**, **Dracula**, and **Gruvbox Dark** — terminal-style palettes based on iTerm2 color schemes.

If distinguishing normal vs bright ANSI colors matters more than preserving the old look, try **High Contrast** or one of the terminal palettes.

## Refresh after changing

After changing a session's ANSI color theme, refresh the already-open game client tab for that session.

Theme selection is applied when the game client loads. This keeps live output rendering fast and simple during play, at the cost of requiring a page refresh after changing the setting.
