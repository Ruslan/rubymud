# Output Color Contrast

## Priority

Small visual polish plan.

Normal and light colors look almost the same in the actual MUD output, even though they are more distinguishable in the Highlights UI.

## Problem

The color palette or rendering path used by the live output does not provide enough visual contrast between normal ANSI colors and bright/light ANSI colors.

This makes highlight and ANSI output less useful during play because users cannot easily distinguish:

1. normal red vs light red
2. normal green vs light green
3. normal blue vs light blue
4. other normal/light ANSI pairs

The Highlights UI appears to show these colors with better separation, so the issue may be specific to actual output rendering, CSS variables, ANSI conversion, or theme application in the live buffer.

## Goal

Make normal and light colors clearly distinguishable in actual output while preserving the existing visual style.

## Investigation Notes

Check both paths:

1. Highlights UI preview/picker colors
2. live output ANSI/rendered colors

The fix should identify why they differ before changing the palette.

Likely areas to inspect:

1. ANSI-to-HTML color mapping
2. CSS custom properties for normal/bright colors
3. theme-specific output styles
4. opacity or inherited styles in output rows
5. any browser-side normalization applied by the renderer

## Fix Direction

Recommended approach:

1. align live output color tokens with the more distinguishable Highlight UI palette if that palette is already acceptable
2. ensure bright/light ANSI variants use clearly different colors from normal variants
3. preserve dark-theme readability
4. avoid changing stored highlight rules or ANSI parsing semantics

This should be a presentation-layer fix unless investigation proves the ANSI mapping itself is wrong.

## Acceptance Criteria

1. Normal and light ANSI colors are visibly different in the live output.
2. Highlights UI and actual output use consistent or intentionally equivalent color values.
3. Existing highlight rules continue to render without storage migration.
4. The fix does not reduce readability on the default dark background.
5. A small manual color sample or test fixture can show all normal/light pairs in the output area.
