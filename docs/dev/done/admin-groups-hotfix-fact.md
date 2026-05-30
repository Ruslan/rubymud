# Admin Groups Hotfix Fact

Status: done.

## Implemented behavior

- Settings `Groups` tab uses the unified backend group response.
- Group rows no longer expect a per-domain `domain` field.
- Group counts use backend summary fields: `total_count`, `enabled_count`, and `disabled_count`.
- Bulk enable/disable sends `{ group_name, enabled }`, matching the unified API.
- The tab opens without the previous `undefined.slice` crash.

## Implementation references

- `df48cdb fix: align settings groups with unified API`

## Verification

- Added `ui/src/settings/api.test.ts` coverage for group toggle payload shape.
- UI tests and build passed during implementation.
