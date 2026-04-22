# 0.0.6.3 - Variables Management

## Goal

Make variables usable as a real gameplay tool instead of a hidden settings-only feature.

This patch adds:

- profile-level declared variables
- optional default values for declared variables
- session-level actual values that override defaults
- fast editing of variables directly inside the game client

The main use case is equipment and party setup variables such as:

- `прайм`
- `оффхенд`
- `щит`
- `двуруч`
- `сумка`
- `t1`..`t5`
- `r1`..`r4`

---

## Product Model

Variables are split into two layers:

1. Profile declarations
2. Session values

### Profile declaration

A profile may declare that a variable exists.

Each declared variable may have:

- `name`
- `default_value` optional
- `description` optional, not required in first pass
- `position` for stable UI ordering

This means a profile can say:

- variable `прайм` exists
- default value is `коп.молн`

or:

- variable `щит` exists
- no default, user must fill it manually per session

### Session value

Actual variable values remain session-scoped.

That means:

- one shared profile can be reused across multiple sessions
- each session may have different concrete values
- runtime variables still live in SQLite as session data

Resolution rule:

1. If session value exists, use it
2. Else if declared variable has default, use default
3. Else variable is declared but unset

---

## Why This Shape

This keeps profile portability while preserving per-character runtime data.

Profiles should describe expected inputs.
Sessions should hold the actual values used by a concrete character.

This avoids the bad tradeoff where one imported profile hardcodes equipment for every session.

---

## Scope

### Included

- new profile-declared variables table
- CRUD for declared variables in Settings UI
- bottom in-game variables panel shows declared variables, not only stored session values
- inline edit in game client
- optional `Fill Missing From Defaults`
- export/import `.tt` support for declared variables and defaults

### Not Included

- variable types
- validation rules
- secret masking
- per-variable permissions
- bulk preset switching
- formula/computed variables

---

## Data Model

### New table

Add a new table in `go/internal/storage/types.go` and AutoMigrate it in `open.go`:

```go
type ProfileVariable struct {
    ID           int64      `gorm:"primaryKey" json:"id"`
    ProfileID     int64      `json:"profile_id"`
    Position      int        `json:"position"`
    Name          string     `json:"name"`
    DefaultValue  string     `json:"default_value"`
    Description   string     `json:"description"`
    UpdatedAt     SQLiteTime `json:"updated_at"`
}
```

Constraints:

- unique on `profile_id + name`
- ordered by `position ASC`

### Existing table kept as-is

Session variables stay in current `variables` table.

No destructive migration needed.

---

## Storage Layer

New file:

`go/internal/storage/profile_variable.go`

Functions:

```go
ListProfileVariables(profileID int64) ([]ProfileVariable, error)
CreateProfileVariable(profileID int64, name, defaultValue, description string) (ProfileVariable, error)
UpdateProfileVariable(v ProfileVariable) error
DeleteProfileVariable(id, profileID int64) error
LoadProfileVariablesForProfiles(profileIDs []int64) ([]ProfileVariable, error)
```

Behavior:

- new declarations get `Position = MAX(position) + 1`
- list order is always `position ASC`
- load across profiles respects active profile priority order, same as aliases/triggers/hotkeys

Merge semantics across profiles:

- first declaration by name wins, using profile priority order
- this matches alias-style override behavior

---

## Runtime Resolution

Add VM/store helper logic so the client can receive resolved variables metadata.

We need two concepts:

1. session variable values
2. declared variables for active profiles

Suggested resolved payload shape:

```go
type ResolvedVariable struct {
    Name         string `json:"name"`
    Value        string `json:"value"`
    DefaultValue string `json:"default_value"`
    Declared     bool   `json:"declared"`
    HasValue     bool   `json:"has_value"`
    UsesDefault  bool   `json:"uses_default"`
}
```

Resolution:

- if session value exists -> `value = session value`, `has_value = true`, `uses_default = false`
- else if declared default exists -> `value = default value`, `has_value = false`, `uses_default = true`
- else declared but empty -> `value = ""`, `has_value = false`, `uses_default = false`

---

## API

### Settings / profile declarations

Add routes:

```text
/api/profiles/{profileID}/variables
  GET    /           -> listProfileVariables
  POST   /           -> createProfileVariable

/api/profiles/{profileID}/variables/{id}
  PUT    /           -> updateProfileVariable
  DELETE /           -> deleteProfileVariable
```

### Game / resolved variables

Add route:

```text
/api/sessions/{sessionID}/variables/resolved
  GET / -> listResolvedVariablesForSession
```

This route should combine:

- active profile variable declarations
- actual session variable values

### Existing session variable routes remain

Keep:

```text
/api/sessions/{sessionID}/variables
```

for direct set/delete of actual session values.

---

## VM Commands

Keep current commands working:

- `#variable`
- `#unvariable`

These still operate on session values.

Optional small addition in this patch:

- if `#variable {name}` references a declared variable with default and no session override, echo both resolved/default state clearly

This is nice-to-have, not required for first pass.

---

## Settings UI

### New tab or sub-tab

Add a new profile-scoped tab:

`Declared Variables`

This tab behaves like aliases/hotkeys:

- profile selector at top
- table of declared variables for that profile
- create/edit/delete UI

Columns:

- order
- name
- default value
- description
- actions

Minimal first pass may omit description editing if needed.

### Session Variables tab stays

The existing `Variables` tab remains session-scoped.

But it should become clearer that it edits actual session values.

Suggested text:

- `Declared by profile, filled for this session`

---

## In-Game Client

### Bottom variables panel

Current bottom panel only shows existing session variables.

Change it to show resolved variables from:

`GET /api/sessions/{sessionID}/variables/resolved`

Render all declared variables even if unset.

### Display states

Each row should communicate one of these states:

1. session value set
2. using default
3. declared but empty

Minimal first-pass rendering:

- variable name on left
- text input on right
- if using default, show default as placeholder or muted text
- if declared but empty, show empty editable field

### Inline editing

Editing should work directly in the game client.

Suggested behavior:

- click variable row or input
- type value
- `Enter` saves via session variable API
- empty string may either clear value or require explicit clear button

Recommended first pass:

- save on `Enter`
- add small `Clear` button to remove session override

### Quick actions

Add one small button in the variables panel:

- `Fill Missing From Defaults`

Behavior:

- for declared variables with default value
- if session value is missing
- set session value = default

This is especially useful for new sessions and template characters.

---

## Import / Export `.tt`

Extend `0.0.6.2` profile script format.

### New line type

```text
#var {name} {default_value}
```

If no default exists:

```text
#var {name} {}
```

If description is later needed, it can go into metadata:

```text
#nop rubymud:rule {"description":"main weapon"}
#var {прайм} {коп.молн}
```

For first pass, description can be skipped from export/import.

### Export order

Profile export should include declared variables section before aliases.

### Import behavior

Import should replace declared variables together with the rest of the profile content.

---

## UX Notes

This feature should reduce context switching.

The intended workflow is:

1. import or create profile
2. see expected variables immediately in game client
3. fill `прайм`, `оффхенд`, `щит`, `сумка` without opening Settings
4. keep using the same profile across sessions with different session values

---

## Verification

1. Create a profile with declared variables `прайм`, `оффхенд`, `щит`, `сумка`
2. Assign defaults to some of them
3. Attach profile to a session
4. Open game UI
5. Confirm all declared variables appear in bottom variables panel
6. Confirm unset variables are visible, not hidden
7. Enter session-specific values directly in game UI
8. Refresh page and confirm values persist
9. Confirm `#variable {прайм}` reflects the session value
10. Export profile to `.tt`, import it into a fresh DB, and confirm declarations/defaults survive

---

## Critical Files

| File | Change |
|------|--------|
| `go/internal/storage/types.go` | add `ProfileVariable` |
| `go/internal/storage/open.go` | AutoMigrate new table |
| `go/internal/storage/profile_variable.go` | new storage file |
| `go/internal/web/server.go` | profile variable routes + resolved session variables route |
| `go/internal/vm/runtime.go` | optional resolved variables helper wiring |
| `ui/src/SettingsApp.svelte` | declared variables editor |
| `ui/src/main.ts` | fetch/render/edit variables directly in game client |
| `ui/src/render.ts` | richer variables panel rendering |
| `go/internal/storage/profile_script.go` | export/import `#var` declarations |
