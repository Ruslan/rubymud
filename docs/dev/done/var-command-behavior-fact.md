# `#var` Command Behavior Fact

Status: done.

## Implemented behavior

- `#var` with no arguments lists variables sorted by name ascending.
- `#var {name}` shows the current variable value.
- `#var {name} {}` behaves as a getter and does not clear the variable.
- In aliases such as `#alias {каст1} {#var {kast1} {%0}}`:
  - non-empty `%0` sets the variable;
  - empty `%0` behaves as a getter, does not clear the value, and outputs the current value for user input.
- Nested alias setter echoes remain hidden, matching the existing local/meta echo-hiding policy.
- Non-input/background local/meta output remains hidden.

## Implementation references

- `6865f60 fix: preserve variables for empty #var assignments`
- `e32cbfe fix: show alias #var getters for input`

## Verification

- `go test ./internal/vm`
- `go test ./internal/session`
- Manual QA passed.
