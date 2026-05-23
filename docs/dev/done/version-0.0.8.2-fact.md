# Version 0.0.8.2 — #tickat, #delay, Scheduler (Done)

## Current State

- **Commands**: `#tickat {second} {command}`, `#untickat {second}`, `#delay [{id}] {seconds} {command}`, `#undelay {id}`.
- **Subscriptions**: Fire once per cycle-second boundary, re-armed each cycle. `#untickat` removes specific second slot.
- **Delays**: One-shot. `#delay {id}` form supports cancellation. Guardrails: 100ms min delay, 50 max pending.
- **Scheduler**: Central `runTimerLoop()` (100ms tick), `runCommandDispatcher()` (50ms command spacing). No per-timer goroutines.
- **Low-time UI**: `<5s styling on ticker pill.
