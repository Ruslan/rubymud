# Version 0.0.8.2 Plan

## Goal

Add practical tick automation around the default ticker: per-second subscriptions plus one-shot delayed commands.

This turns the visible ticker from passive information into a useful automation primitive.

Timing representation rules remain unchanged:

1. runtime uses `time.Duration`
2. API and storage durations use integer milliseconds
3. scheduler precision target remains within 100ms
4. command examples in this phase may still use integer seconds, but implementation should stay ready for future decimal-second parsing into milliseconds

---

## User Value

After `0.0.8.2`, a player can automate actions around the shared tick:

1. do something 3 seconds before tick
2. do something exactly on tick
3. schedule a short one-shot follow-up after tick
4. cancel a delayed action if circumstances changed

Example workflow:

```text
#ticksize {60}
#tickon
#tickat {3} {stand;wear shield}
#tickat {0} {bash target}
#tickat {0} {#delay {ready} {2} {report ready}}
```

---

## Scope

### Commands

Implement for the default ticker only:

```text
#tickat {second} {command}
#untickat {second}
#delay {seconds} {command}
#delay {id} {seconds} {command}
#undelay {id}
```

Behavior:

1. `#tickat {second} {command}` subscribes a command to fire once per cycle at that remaining second
2. `#untickat {second}` removes subscriptions for that slot
3. `#delay {seconds} {command}` schedules a one-shot delayed command
4. `#delay {id} {seconds} {command}` schedules a cancelable one-shot delayed command
5. `#undelay {id}` cancels a pending delayed command by id
6. subscriptions are re-armed every cycle
7. subscriptions and delays run through the normal VM command pipeline
8. delay ids live in a separate namespace from timer names reserved for later

### Runtime / scheduler

1. introduce one central scheduler loop for due timer events and due delayed commands
2. do not create one sleeper/goroutine per timer or per delay
3. subscriptions must fire once per cycle-second boundary, not repeatedly while UI redraws
4. recursive or too-small delays must not cause unbounded queue growth
5. simultaneous due commands should go through a short paced queue to reduce burst-spam risk
6. scheduler precision target in this phase is within 100ms, not sub-millisecond

### UI

No new major UI is required beyond `0.0.8.1`.

Optional small improvement:

1. low-time styling for the default ticker under 5 seconds

---

## Architecture Reserved For Later

Implementation should still leave room for:

1. named timers with named subscriptions
2. secondary visible timer pills
3. delta sync on `#tickset`

Not yet in scope:

1. named timers
2. named `#tickat` / `#untickat`
3. `#tickon {name}` and related named forms
4. delta sync `#tickset {+N}` / `#tickset {-N}`

---

## Acceptance Criteria

1. `#tickat {3} {stand}` fires once per cycle when 3 seconds remain.
2. `#tickat {0} {bash target}` fires on the cycle boundary.
3. `#tickat {0} {#delay {ready} {2} {report ready}}` schedules a one-shot follow-up command 2 seconds later.
4. `#undelay {ready}` cancels that delayed command before it fires.
5. simultaneous due commands are paced rather than emitted in one same-millisecond spike.
6. recursive/self-rescheduling `#delay` usage does not blow the stack or explode the scheduler queue.
7. no tick/delay command sends raw text to the MUD unless the resulting scheduled command itself is an outgoing MUD command.
8. invalid usage returns a clear local diagnostic.
