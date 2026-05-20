# Architecture And Duplication Todo

This document tracks technical debt around duplicated logic and architectural boundaries that increase maintenance cost and bug risk.

## 1. Unify ANSI/SGR state parsing

Current state:

- ANSI state logic exists in more than one place:
  - `go/internal/vm/highlight_apply.go` (`ansiState`, `activeANSIAtWithBase`, `applyANSISequence`)
  - `go/internal/session/io.go` (`sgrState`, `activeSGRAtEndWithBase`, `applySGRSequence`)
- Implementations are very similar but not shared.
- Any future ANSI bugfix requires touching multiple files and keeping behavior aligned manually.

What to do:

- Extract a shared package, for example `go/internal/ansi`.
- Move state model + parser + helpers there:
  - apply sequence into state,
  - compute active style at offset,
  - compute effective tail style with base state,
  - serialize state back to ANSI sequence.
- Keep VM/session code as orchestration layers only.

Expected result:

- Single source of truth for ANSI behavior.
- Lower chance of drift between VM and session paths.

## 2. Clarify transform ownership in the line pipeline

Current state:

- `processLine` handles many responsibilities: parse, gag, triggers, substitutions, highlights, effects, DB writes, websocket broadcasting, latency metrics.
- Visual transform ownership is split across VM and session.

What to do:

- Define explicit stages and contracts for the output pipeline.
- Introduce a small pipeline struct or helper methods for:
  - input normalization,
  - rule application,
  - visual rendering (with carry context),
  - persistence,
  - fan-out to target/copy buffers.
- Keep each stage side-effect scope clear.

Expected result:

- Easier reasoning about where style/state mutations happen.
- Lower regression risk when adding new transforms.

## 3. Consolidate per-buffer rendering logic

Current state:

- Rendering now uses `renderHighlightedForBuffer(...)` and buffer-local carry.
- The same conceptual operation is called from target, copy, and echo branches.

What to do:

- Move all buffer-render calls through one internal path with a small request struct:
  - `buffer`,
  - `displayRaw`,
  - `originalRaw`.
- Ensure the helper is the only place that reads/writes ANSI carry.

Expected result:

- Fewer accidental bypasses of carry logic.
- Simpler future changes for per-buffer visual behavior.

## 4. Strengthen coverage of architectural invariants

Current state:

- We have good regressions for multi-line carry, end-of-line highlight, and copy-buffer isolation.
- Invariants are still implicit and spread across tests.

What to do:

- Add explicit invariant-style tests for:
  - carry independence across buffers,
  - carry reset on explicit `\x1b[0m`,
  - no visual transform should alter effective tail state unless intended,
  - mixed substitutions + highlights + echoes in one batch.
- Group these tests under a dedicated section in `session_test.go`.

Expected result:

- Faster detection of architectural regressions.
- Better onboarding context for future contributors.

## 5. VM/session boundary documentation

Current state:

- Some behavior is obvious only from code reading (what VM transforms return vs what session persists/broadcasts).

What to do:

- Add a short architecture note in `docs/dev/` describing:
  - what VM owns (rule evaluation and text transforms),
  - what session owns (stateful stream context, persistence, fan-out, transport),
  - how layering/profile resolution affects runtime.

Expected result:

- Clearer ownership model for feature work and bugfixes.
