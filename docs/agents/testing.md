# Testing & Quality Guide

Use this when you need to write tests, debug issues, or understand the project's quality standards.

## TDD (Test-Driven Development)
We prefer TDD for core logic, especially in the `vm` and `storage` layers. 

## The "Repro-First" Rule
When fixing a bug:
1. **Identify the bug**.
2. **Write a reproduction test case** that fails due to this bug.
3. **Verify the failure**.
4. **Fix the code**.
5. **Verify the test now passes**.

This ensures that the bug is truly understood and will not regress.

## Test Layers

### 1. Go Unit Tests
- Most logic in `go/internal/` has corresponding `_test.go` files.
- Run with: `go test ./...` or `make test`.

### 2. UI Tests (Vitest)
- Located in `ui/src/`.
- Run with: `npm run test` inside the `ui/` directory.
- Focused on ANSI parsing, history management, and rendering logic.

### 3. Smoke Tests
- Located in `scripts/smoke_test`.
- Used to verify that the server starts and basic functions (like DB opening) work in a real environment.

### 4. Live E2E
- `make e2e-mapper-reconciliation`: loopback-only fake MUD, its own temp SQLite and a random port.
- Any throwaway mudhost runs from `bin/mudhost` on a non-live port against a DB **copy**. Never restart or stop the live `rubymud` systemd unit for a test; that is `make deploy`'s job only (see `AGENTS.md`, Live Service).

## Best Practices
- **Mocking**: Use interfaces to mock external dependencies like the database or network.
- **Table-driven tests**: Preferred for complex logic like command parsing or expression evaluation.
- **Coverage**: Aim for high coverage in core engine components.
