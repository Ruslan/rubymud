# Settings Inline Editing

## Priority

High priority Settings UX improvement.

Editing large profiles is currently annoying because the user must click `Edit` on a row near the bottom, then scroll back up to the form at the top of the page.

This is especially painful for large rule lists:

1. aliases
2. triggers
3. substitutions
4. highlights
5. hotkeys
6. variables

## Problem

The current edit flow breaks context:

1. user finds a rule near the bottom of a long table
2. user clicks `Edit`
3. editor form is elsewhere, usually above the list
4. user loses scroll position/context
5. after saving, user must find the row again

For large profiles this makes small edits slow and frustrating.

## Goal

Allow editing a rule in place, directly where the user clicked it.

The user should be able to make a small change without leaving the row they are inspecting.

## Recommended UX

Use inline row editing.

Behavior:

1. clicking `Edit` turns that row into an editable row
2. alternatively, clicking editable cells may enter edit mode if it does not cause accidental edits
3. only one row should be in edit mode at a time for the first implementation
4. edited row shows `Save` and `Cancel`
5. `Cancel` restores the original row without API calls
6. `Save` updates the item and keeps the user near the same scroll position
7. after save, the row exits edit mode and shows updated values

Keyboard behavior:

1. `Esc` cancels inline edit
2. `Ctrl+Enter` or `Cmd+Enter` saves multiline fields if practical
3. tab order should stay inside the row in a predictable way

## Scope By Rule Type

Start with the highest-volume rule lists:

1. aliases
2. triggers
3. substitutions
4. highlights

Then extend to:

1. hotkeys
2. variables
3. timers, if needed later

Profiles and sessions can keep their current form-based editing unless they become painful.

## Field Layout

Inline edit does not need to duplicate every advanced affordance on day one, but it must cover the common fields.

Aliases:

1. pattern/name
2. command
3. group
4. enabled
5. position if already exposed

Triggers:

1. pattern
2. command/action
3. group
4. enabled
5. priority/position if already exposed

Substitutions:

1. pattern
2. replacement
3. gag flag
4. group
5. enabled

Highlights:

1. pattern
2. color/style fields
3. group
4. enabled

If a row has complex editing needs, it may expose an `Advanced` action that opens the existing full editor, but common edits should stay inline.

## Implementation Direction

Keep the first implementation pragmatic:

1. store `editingRowKey` and `editingDraft` in Settings state
2. clone the row data into `editingDraft` when edit starts
3. render row cells as inputs/selects when `editingRowKey` matches
4. call existing update endpoints on save
5. update local row data or refetch while preserving scroll position
6. do not introduce a heavy table/grid dependency unless plain Svelte becomes too awkward

Avoid modal editing for this feature. A modal still breaks row context and does not solve the main annoyance as well as inline editing.

## Interaction With Existing Top Forms

The current top forms can remain for adding new rules.

Recommended split:

1. top form: add new rule
2. inline row edit: edit existing rule

Do not force existing-row edits through the add form.

## Safety

Inline editing should avoid accidental destructive changes:

1. do not save on blur in the first version
2. require explicit `Save`
3. keep `Cancel` visible
4. preserve validation errors near the row being edited
5. prevent starting a second inline edit while another row has unsaved changes, or ask for confirmation

## Repro Test Requirement

Follow repro-first TDD where practical.

Add tests for at least one representative rule type first, then reuse the same pattern for others:

1. clicking `Edit` changes the row into inputs
2. saving sends the same API update payload as the old editor
3. cancel restores the original displayed row
4. validation errors show near the edited row
5. scroll/context is not reset by merely entering edit mode

## Acceptance Criteria

1. A user can edit aliases inline without scrolling to a top form.
2. A user can edit triggers inline without scrolling to a top form.
3. Substitutions and highlights support inline editing or have a tracked follow-up if split by slice.
4. Save and Cancel are explicit and visible in the edited row.
5. Editing a row near the bottom keeps the user near that row after save/cancel.
6. Existing add-new-rule forms still work.
7. Existing APIs and validation behavior are reused where possible.
