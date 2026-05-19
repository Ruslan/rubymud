# Performance And Reliability Todo

This document tracks issues that were not fully resolved by the current server-side fix for WebSocket broadcast hangs.

## 1. Per-client WebSocket write queue

Current state:

- `broadcastMsg` sends messages to clients sequentially.
- `SetWriteDeadline(10s)` limits how long one client can block, but other clients may still wait up to 10 seconds.
- This is acceptable with a small number of clients, but architecturally one slow client can still impact others.

What to do:

- Add a buffered message channel for each client.
- Start a dedicated writer goroutine for each WebSocket client.
- `broadcastMsg` should only enqueue messages into each client queue in a non-blocking way.
- If a client queue is full, close that client connection and detach the client.
- Preserve message ordering within each individual client.

Expected result:

- One stalled or slow client does not delay the MUD read loop or other clients.
- Backpressure is isolated to the specific client.

## 2. VM reload synchronization

Current state:

- `NotifySettingsChanged` calls `s.vm.ReloadFromStore()`.
- The read loop concurrently calls `CheckGag`, `MatchTriggers`, `ApplySubsAndCollectOverlays`, `ApplyHighlights`, and `ApplyEffects`.
- VM currently has no internal mutex, so data races are possible when reload and MUD-line processing run at the same time.

What to do:

- Add synchronization inside VM: use `sync.RWMutex` around rules, variables, and compiled caches.
- Reload should acquire the write lock.
- VM runtime methods should acquire the read lock.
- After that, run `go test -race ./...`.

Expected result:

- UI setting changes can be applied without race risk against live processing of incoming MUD traffic.

## 3. Panic recovery while locks are held

Current state:

- Background goroutines are protected via `recoverGoroutine`.
- On panic, `s.Close()` is called.
- If panic happens in code that already holds `s.mu`, `Close()` can block on the same mutex.

What to do:

- Review critical sections for potential `panic` while holding `s.mu`.
- Minimize the amount of code under `s.mu` and avoid calling external operations while locked.
- Consider a separate safe-close path for panic recovery that does not require immediately acquiring `s.mu`, or closes `done/conn` via an idempotent atomic mechanism.
- Add a test/stress test for panic in the read loop and timer loop.

Expected result:

- Panic recovery reliably transitions the session into a closed state and does not hang on a lock.

## 4. Additional diagnostics

What to do:

- Add metrics or structured logs for:
  - WS write duration per client;
  - detach count by write error;
  - size of future per-client queues;
  - DB write duration in the read loop;
  - VM reload duration.
- Add a health/debug endpoint for active sessions:
  - read loop status;
  - number of clients;
  - timestamp of the last MUD packet;
  - timestamp of the last successful WS broadcast.

Expected result:

- If the server looks "alive" again but stops sending data, it should be possible to quickly identify exactly where the pipeline is stuck.
