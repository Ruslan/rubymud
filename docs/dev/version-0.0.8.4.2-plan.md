# Version 0.0.8.4.2 Plan

## Goal

Make mobile hotkeys easier to use as a primary control surface by introducing a simple mobile-only row-based layout that stays predictable, degrades gracefully on narrow screens, and avoids brittle grid math.

The core product intent for `0.0.8.4.2` is:

1. directional hotkeys such as north/south/west/east should be placeable in a visually meaningful arrangement
2. players should be able to tune that arrangement manually without fighting strict coordinate systems
3. unplaced hotkeys should still remain usable and should automatically fill available space where practical
4. desktop hotkey rendering should remain unchanged and continue to act mostly as a reference surface

Compatibility rule for this release:

1. existing `#hotkey {shortcut} {command}` command syntax must remain unchanged
2. mobile layout metadata is administrative/configuration metadata, not new end-user command syntax
3. mobile layout metadata should be edited through the settings/admin UI or preserved via `#nop rubymud:rule` metadata in exported/imported profile scripts

---

## User Value

After `0.0.8.4.2`:

1. a mobile player can place movement and other muscle-memory commands into stable visual rows
2. north can stay above south, west can stay left of east, and other nearby actions can be arranged around them without requiring exact grid coordinates
3. long or irregular labels do not break the model, because layout is based on row ordering plus measured fit rather than rigid cell occupancy
4. players can still rely on a simple fallback: if auto-fill is not ideal, they can manually tune row and order in settings

Example target outcome on narrow mobile widths:

```text
scan north down bash sleep
west south east longcommand
```

The exact visual width may vary by label length, but the semantic arrangement should remain understandable and user-directed.

---

## Scope

### 1. Mobile-Only Hotkey Layout Metadata

Add mobile layout metadata to hotkeys:

1. `mobile_row`
2. `mobile_order`

Behavior rules:

1. metadata affects mobile rendering only
2. desktop rendering continues to use the current straightforward list/chip layout
3. hotkeys without mobile metadata remain valid and should still render on mobile via fallback placement
4. this release intentionally does NOT require `mobile_x/mobile_y` or strict geometric coordinates
5. metadata is not part of the user-facing `#hotkey` command syntax
6. command-driven hotkey creation should continue to create ordinary hotkeys with default mobile metadata (`0` / unset)

### 2. Row-Based Mobile Layout

On mobile widths, hotkeys should be rendered as ordered rows rather than as one flat wrapped sequence.

Algorithm direction:

1. split hotkeys into two groups:
   1. positioned hotkeys with `mobile_row`
   2. free hotkeys without `mobile_row`
2. group positioned hotkeys by `mobile_row`
3. sort each row by `mobile_order`, then by stable fallback order for ties
4. render each positioned row first as a horizontal sequence of buttons
5. attempt to append free hotkeys to the end of existing rows when measured space allows
6. overflow free hotkeys into additional trailing rows as needed

This keeps the mental model simple:

1. `mobile_row` expresses vertical intent
2. `mobile_order` expresses left-to-right intent
3. unplaced hotkeys are opportunistic filler, not hard-positioned elements

### 3. Measured Auto-Fill for Free Hotkeys

`0.0.8.4.2` should allow JS to opportunistically place free hotkeys into the remaining visible space of an existing row.

Expected behavior:

1. after rendering or measuring row contents, determine whether another free hotkey can fit at the end of the row
2. if it fits, append it there
3. if it does not fit, try the next row
4. if no existing row fits, create a new trailing row

Design notes:

1. this is a best-effort enhancement, not a strict packing solver
2. perfect density is not required
3. stable and understandable behavior is more important than maximizing occupancy
4. manual row/order tuning remains the primary escape hatch when auto-fill is imperfect

### 4. Conflict and Irregular Width Behavior

This release should prefer resilience over overengineering.

Rules:

1. multiple hotkeys with the same `mobile_row` and `mobile_order` are allowed
2. when such conflicts occur, preserve both buttons in that row using stable ordering rather than rejecting or hiding either one
3. button label width is allowed to influence final visual alignment
4. semantic placement matters more than exact grid symmetry

This means the system should tolerate layouts like:

1. `scan` above `west`
2. `north` centered only approximately by eye
3. wider labels pushing adjacent filler commands naturally to later rows

### 5. Mobile Presentation Rules

To preserve space and clarity on phones:

1. mobile hotkeys should show action labels only
2. shortcut hints such as `Ctrl`, `Alt`, or key chords should remain hidden on mobile widths
3. desktop keeps existing shortcut visibility

This release continues the product direction that mobile hotkeys are an active control surface, not merely a keyboard-reference legend.

### 6. Settings Support

The settings UI should expose mobile row ordering in the simplest practical form.

Minimum viable editing support:

1. allow editing `mobile_row`
2. allow editing `mobile_order`
3. preserve existing hotkey editing flows as much as possible

Configuration ownership rules:

1. settings/admin UI is the primary editing surface for `mobile_row` and `mobile_order`
2. exported profile scripts should preserve this metadata in adjacent `#nop rubymud:rule` JSON comments
3. imported profile scripts should restore this metadata from those `#nop` comments
4. plain `#hotkey` commands without metadata comments remain valid and should import as ordinary unpositioned hotkeys

Non-goal for this release:

1. no advanced drag-and-drop mobile layout editor
2. no visual WYSIWYG coordinate planner

---

## Non-Goals

1. do not introduce strict `mobile_x/mobile_y` grid coordinates in this release
2. do not implement colspan/rowspan math for mobile hotkeys in this release
3. do not redesign desktop hotkey behavior or treat desktop as the primary layout target
4. do not attempt optimal bin-packing or a complex layout engine
5. do not require exact visual centering of directional controls under variable-width labels
6. do not extend `#hotkey` with extra positional arguments or mobile-specific inline syntax in this release

---

## Acceptance Criteria

1. a hotkey with `mobile_row=1` renders above a hotkey with `mobile_row=2` on mobile widths
2. within the same mobile row, lower `mobile_order` renders to the left of higher `mobile_order`
3. hotkeys without `mobile_row` still appear on mobile and are not dropped
4. free hotkeys can be appended to the end of an existing mobile row when space permits
5. when space does not permit, remaining free hotkeys continue in additional rows
6. duplicate `mobile_row + mobile_order` values do not crash rendering and do not cause hotkeys to disappear
7. desktop hotkey rendering remains functionally unchanged
8. mobile hotkey rendering hides shortcut hints and shows only the action label
9. the settings UI allows a player to tune mobile row placement manually without direct file editing
10. resizing the mobile viewport or changing orientation recomputes the mobile row layout without requiring a full page reload
11. `#hotkey {shortcut} {command}` remains backward compatible and does not require or parse mobile layout arguments
12. profile export/import preserves mobile layout metadata through `#nop rubymud:rule` comments without changing plain `#hotkey` syntax
