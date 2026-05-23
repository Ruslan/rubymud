# Variable Command Behavior

## Priority

Small behavior hotfix.

This plan tracks two `#var` UX issues:

1. `#var` output is not sorted by variable name
2. `#var {name} {}` from an alias/function-like command should show the current value instead of overwriting it with an empty value

## Current Problems

### Unsorted Output

When `#var` lists variables, the output order is not stable or not alphabetical. This makes it harder to scan variables during play.

### Empty Alias Argument

Aliases can be used as small variable setter/getter commands.

Example:

```text
#alias {каст1} {#var {kast1} {%0}}
```

Expected behavior:

```text
каст1 Тартис
```

sets:

```text
kast1 = Тартис
```

Then:

```text
каст1
```

should behave like:

```text
#var {kast1}
```

and output the current value instead of setting `kast1` to an empty string.

## Desired Semantics

### `#var` Listing

`#var` with no arguments should list variables sorted by name ascending.

Sorting should be deterministic and case-sensitive/insensitive according to the existing storage/UI convention. If no convention exists, use simple lexical ascending order.

### `#var {name}` Getter

`#var {name}` should show the current variable value.

This is already the intended getter form and should remain unchanged.

### `#var {name} {value}` Setter

`#var {name} {value}` should set the variable only when `value` is explicitly present and non-empty after command/alias expansion.

If the second argument expands to empty because an alias had no arguments, treat it as the getter form.

Recommended rule:

1. source has one argument: getter
2. source has two arguments and the second argument is non-empty after expansion: setter
3. source has two arguments and the second argument is empty after expansion: getter

This supports alias getter/setter patterns without requiring separate aliases.

## Examples

```text
#alias {каст1} {#var {kast1} {%0}}
```

```text
каст1 Тартис
```

Result:

```text
kast1 = Тартис
```

```text
каст1
```

Output:

```text
kast1 = Тартис
```

or the current project-standard `#var {name}` display format.

## Open Question

This changes the ability to set a variable to an intentionally empty string via `#var {name} {}`.

Recommended answer for this hotfix: accept that tradeoff because empty-string assignment is less useful than function-like alias getter/setter behavior.

If explicit empty assignment is needed later, add a dedicated syntax such as:

```text
#var {name} {""}
```

or a separate unset/clear command.

## Repro Test Requirement

Follow repro-first TDD.

Add tests for:

1. `#var` lists variables sorted by name
2. `#var {kast1}` shows the current value
3. alias `#alias {каст1} {#var {kast1} {%0}}` with `каст1 Тартис` sets `kast1`
4. the same alias with `каст1` shows `kast1` instead of clearing it
5. no regression for normal non-empty `#var {name} {value}` assignment

## Acceptance Criteria

1. `#var` output is sorted by variable name.
2. `#var {name}` continues to show the current value.
3. `#var {name} {%0}` inside an alias acts as a getter when `%0` expands to empty.
4. Non-empty alias argument still sets the variable.
5. Behavior is covered by regression tests.
