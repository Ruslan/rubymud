# Admin Groups Hotfix

## Priority

High priority hotfix.

The Settings `Groups` section is currently broken and can crash the admin UI.

## User-Visible Failure

Opening the `Groups` tab in Settings can throw:

```text
settings-BMbv3Mhf.js:1 Uncaught TypeError: Cannot read properties of undefined (reading 'slice')
    at gt (settings-BMbv3Mhf.js:2:55192)
    at settings-BMbv3Mhf.js:10:7351
    at K (settings-BMbv3Mhf.js:1:26380)
    at settings-BMbv3Mhf.js:10:7345
    at ir (settings-BMbv3Mhf.js:1:24169)
    at Dt (settings-BMbv3Mhf.js:1:14382)
    at Ot (settings-BMbv3Mhf.js:1:14432)
    at G (settings-BMbv3Mhf.js:1:25998)
    at Array.map (<anonymous>)
    at settings-BMbv3Mhf.js:1:21371
```

## Likely Root Cause

The frontend and backend disagree about the `Groups` API shape.

Current Settings UI expects group rows with a domain:

```svelte
titleCase(group.domain)
toggleGroup(group.domain, group.group_name, true)
ruleCountForGroup(group.domain, group.group_name)
```

But the current profile groups backend returns unified group summaries from `ListUnifiedGroups`:

```go
type UnifiedGroupSummary struct {
    GroupName     string `json:"group_name"`
    TotalCount    int64  `json:"total_count"`
    EnabledCount  int64  `json:"enabled_count"`
    DisabledCount int64  `json:"disabled_count"`
}
```

There is no `domain` field. `titleCase(group.domain)` receives `undefined` and calls `.slice()` on it.

## Fix Direction

Prefer the smallest safe fix.

Recommended hotfix:

1. update the Settings `Groups` tab to match the unified backend response
2. remove the `Domain` column or replace it with a fixed label such as `All rule types`
3. show `group.total_count` instead of recomputing counts by domain
4. call `toggleGroup(group.group_name, enabled)` without a domain argument
5. update `toggleGroup` frontend payload to match backend: `{ group_name, enabled }`
6. harden `titleCase` or avoid calling it on untrusted/optional fields

Alternative if domain-level groups are still desired:

1. change backend to return per-domain group summaries
2. change `toggleProfileGroup` to accept and enforce `domain`
3. preserve unified toggle separately

Do not choose the alternative for the hotfix unless there is a concrete product reason. The backend already has unified group semantics, and the UI copy says "Bulk enable/disable rule groups", so frontend alignment is likely the correct minimal fix.

## Repro Test Requirement

Follow repro-first TDD.

Add a test or browser-level regression that covers:

1. `/api/profiles/:profileID/groups` returning a unified group row without `domain`
2. opening/rendering the Settings `Groups` tab does not throw
3. the row displays group name, total count, enabled count, and disabled count
4. clicking Enable/Disable sends `{ group_name, enabled }`

If a full Svelte component test is too heavy, add the smallest practical UI test around the rendering/helper logic and a backend/API test for response shape.

## Acceptance Criteria

1. Settings `Groups` tab opens without console exceptions.
2. Existing groups render correctly using unified group summaries.
3. Enable/Disable works for a group and updates aliases, triggers, highlights, and substitutions together.
4. Empty groups state still renders cleanly.
5. Missing or malformed optional fields cannot crash the tab via `.slice()`.
6. The fix is covered by a regression test.

## Related Work

This is separate from the broader planned UX work:

1. `live-buffer-navigation.md` tracks PageUp scrollback and buffer-local search.
2. `log-browsing.md` tracks admin log search/view/export.
3. GitHub-reported usability bugs should be triaged alongside this hotfix, but this plan is specifically for the broken Settings `Groups` section.
