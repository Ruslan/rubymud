# Settings Inline Editing — Done

## Status

Done.

## Product Decision

Editing rules in large profiles used to break context: the editor form lived at
the top of the page, so editing a row near the bottom meant scrolling away and
losing your place. RubyMUD now edits every rule in place, directly in its row,
with an explicit Save/Cancel panel — no modal, no scroll jump.

## Implemented Behavior

- Inline row editing is available for every high-volume list:
  aliases, triggers, substitutions, highlights, hotkeys (incl. shortcut capture),
  declared variables, runtime variables, profiles, and sessions.
- Clicking `Edit` expands the row into an editable panel; the row shows an
  `Editing…` indicator. Only one row edits at a time.
- `Save` reuses the existing update APIs and validation; errors render next to the
  edited row. `Cancel` discards the draft with no API call. Neither resets scroll.
- Top forms remain for *adding* new rules; inline panels handle *editing* existing ones.
- Keyboard behavior is provided by a shared Svelte action
  `ui/src/settings/inline-edit-keys.ts`, wired into all nine inline panels:
  - `Esc` cancels.
  - `Ctrl+Enter` / `Cmd+Enter` saves (works inside multiline textareas).
  - Plain `Enter` in a single-line input saves; `Enter` in a textarea keeps the newline.

## Deferred (not painful yet)

- Timers/tickers keep their own always-editable inline inputs (different UX model);
  converting them to the Edit/Save/Cancel panel is optional future work.
- Profile/session forms can get further polish only if they become painful.

## Verification

- `ui: npm test` (115 tests pass, incl. `src/settings/inline-edit-keys.test.ts`)
- `ui: npm run build`
