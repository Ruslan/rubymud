# Go Migration Plan

## Purpose

This document fixes the migration target for the current Ruby MUD client and protects existing behavior during the rewrite.

The goals are:

1. Describe what the current project already does, so important behavior is not lost.
2. Define what will move into the Go version.
3. Define a path to a Windows-friendly `.exe` build that friends can run locally while still allowing plugins in Ruby, JavaScript, and later other languages.

## Product Direction

Target product shape:

1. A local-first MUD client runtime written in Go.
2. The runtime exposes a browser UI over localhost.
3. The runtime can keep one live MUD session and allow multiple browser clients to attach to it.
4. User customization lives in plugins, not in compiled Go code.
5. The first plugin to ship with the Go runtime should be a Ruby compatibility plugin, so the project can launch with existing scripting power instead of regressing.

Non-goals for the first Go release:

1. Full cloud SaaS orchestration.
2. Full multi-tenant hosting.
3. Full event-sourced system where all state is rebuilt only from the event log.
4. Perfect plugin sandboxing.

## Iteration 1

Iteration 1 is the smallest possible Go runtime that proves the new architecture without waiting for plugins, storage, or scripting parity.

Iteration 1 goal:

1. One Go binary.
2. One live TCP connection directly to the MUD.
3. One HTTP server on localhost.
4. One WebSocket endpoint for browser clients.
5. More than one browser tab/device attached to the same live session.
6. Output broadcast from the MUD to all attached clients.
7. Input sent from the browser to the same MUD session.

Iteration 1 intentionally does not include:

1. plugins
2. SQLite
3. aliases
4. triggers
5. buttons
6. windows/buffers
7. history restore
8. reconnect logic
9. multi-session support
10. external mux

Why no mux in Iteration 1:

1. The Go runtime itself becomes the session host.
2. It already owns one TCP connection and many browser attachments.
3. Adding a separate mux would duplicate responsibility before it is needed.

Iteration 1 success criteria:

1. `go run ./go/cmd/mudhost --mud HOST:PORT --listen :8080` starts successfully.
2. Opening `http://localhost:8080` shows a live browser client.
3. MUD output appears in the browser.
4. A command entered in the browser is sent to the MUD.
5. A second browser tab sees the same live output stream.
6. Commands from both tabs go to the same live character session.

## What We Have Today

Current stack:

1. Ruby + Sinatra server.
2. Browser UI over WebSocket.
3. A single shared runtime process.
4. A TCP MUD client connected through a local mux.
5. SQLite storage for logs and history.
6. Configurable aliases, variables, keys, triggers, buttons, and window routing.

### Current Architectural Shape

The current app behaves like this:

1. One server process owns one MUD connection.
2. Multiple browser tabs/devices can attach to that same live session.
3. All clients see the same output stream and can send commands into the same character.
4. Logs and command history survive reconnects.

This is important: the project is already closer to a session host than to a traditional local-only MUD client.

### Features That Must Not Be Lost

These are the current product behaviors that the Go version must preserve or replace deliberately.

#### Session Model

1. One persistent live MUD session in the backend.
2. Browser clients attach and detach without killing the MUD session.
3. Multiple devices can view and control the same character.
4. Reconnect restores recent log output and input history.

#### UI Model

1. Browser-based terminal UI.
2. Multiple output windows/buffers.
3. Buttons generated from server-side trigger logic.
4. Hotkeys configured by profile.
5. Command history in browser and backend.

#### Scripting Model

1. Variables with persistence.
2. String aliases.
3. Block/function aliases.
4. Regex-based server triggers.
5. Trigger actions that can:
   - send commands,
   - emit local output,
   - emit output to another window,
   - create clickable buttons,
   - transform regex captures,
   - read and mutate variables.
6. Config reload during development.

#### State and Persistence

1. SQLite-backed log history.
2. SQLite-backed command history.
3. Variable persistence.
4. Enough persisted state to restore the user context after browser reconnect.

### Current Weak Points We Intentionally Want To Change

The Go rewrite should preserve behavior, not preserve these implementation problems.

1. Runtime logic depends on mutable global Ruby config.
2. Reload is unsafe and can duplicate rules.
3. Arbitrary Ruby execution is tightly coupled to the core runtime.
4. Session, UI, script engine, and transport are too mixed together.
5. The current setup is awkward to distribute to Windows users.

## Target Go Version

The Go version should split the system into a stable core and restartable plugins.

### Core Principles

1. Go owns transport, state, persistence, browser sync, and plugin lifecycle.
2. Plugins own most user customization logic.
3. Browser clients are attachable frontends, not owners of the MUD session.
4. The first release must run locally as a single executable on Windows.
5. The first release must still feel powerful to advanced users on day one.

### Core Responsibilities in Go

These parts belong in Go and should not depend on plugins for basic operation:

1. TCP/Telnet connection to the MUD.
2. Optional local mux/proxy support.
3. Browser HTTP server.
4. Browser WebSocket server.
5. Shared session runtime.
6. Attached client management.
7. Command routing to the MUD.
8. Output broadcast to attached clients.
9. SQLite storage.
10. Session history restore.
11. Event bus.
12. Timer scheduler infrastructure.
13. Plugin process lifecycle.
14. Capability checks for plugin effects.

### Plugin Responsibilities

These parts should be implemented as plugins or plugin-provided logic:

1. User aliases.
2. User triggers.
3. User variable logic.
4. Optional timers created by script logic.
5. TTS integrations.
6. LLM advisor features.
7. Custom routing and custom windows beyond the core defaults.
8. Experimental gameplay automation.

### Why The First Release Needs a Ruby Plugin

The current project's strongest differentiator is not Ruby itself, but the scripting power users already have.

If the first Go release ships without a strong scripting story, it will be a regression.

Because of that, the first Go release should include:

1. A stable plugin protocol.
2. A reference Ruby plugin host/client.
3. A compatibility-oriented Ruby plugin that can express the current alias/trigger workflows.

This lets the Go runtime launch without waiting for a brand new declarative DSL.

## Event Model

The Go runtime should not treat the whole system as raw text pipes only.

It should expose typed events internally and to plugins.

### Base Event Types

Minimum event families for v1:

1. `session.started`
2. `session.connected`
3. `session.disconnected`
4. `client.attached`
5. `client.detached`
6. `line.received`
7. `command.input`
8. `command.sent`
9. `button.clicked`
10. `timer.fired`
11. `variable.changed`
12. `plugin.started`
13. `plugin.stopped`
14. `plugin.failed`

### Event Payload Shape

At minimum, each event should contain:

1. `event_id`
2. `session_id`
3. `type`
4. `timestamp`
5. `source`
6. `payload`

### Do We Need Full Event Sourcing?

Not for the first Go version.

Instead, use:

1. Current in-memory state.
2. Persisted typed event log.
3. SQLite projections for fast reads.
4. Optional snapshots for session recovery.

This keeps the event model useful without making the whole runtime depend on full replay semantics.

## Plugin Model

Plugins should run out-of-process.

The plugin process may be written in Ruby, JavaScript, or another language later.

### Plugin Design Rules

1. Plugins communicate with the Go core over a stable protocol.
2. Plugin crashes must not kill the session host.
3. Plugins may be stopped and restarted independently.
4. Plugins do not directly mutate Go memory.
5. Plugins return effects, not direct in-process callbacks.

### Transport

Preferred order:

1. `stdio` subprocess protocol for local plugins.
2. Unix socket or named pipe support later.
3. TCP only when remote plugins are needed.

### Plugin Effects

Minimum effect types for v1:

1. `send_command`
2. `echo`
3. `write_window`
4. `show_buttons`
5. `set_variable`
6. `schedule_timer`
7. `cancel_timer`
8. `notify`
9. `speak`

### Plugin Capabilities

Plugins should declare capabilities so the core can restrict what they may do.

Minimum capability set:

1. `read_lines`
2. `send_commands`
3. `write_windows`
4. `manage_variables`
5. `manage_timers`
6. `speech`
7. `notifications`

## Storage Plan

Keep SQLite.

Reasons:

1. Already used by the project.
2. Good fit for Windows local execution.
3. Single-file deployment.
4. Good enough for history, events, variables, and snapshots.
5. No separate database server required.

### Storage Approach

Use SQLite as the primary storage for structured data.

Suggested tables for v1:

1. `sessions`
2. `clients`
3. `events`
4. `history_entries`
5. `variables`
6. `snapshots`
7. `plugins`

Optional later:

1. `plugin_effects`
2. `attachments`
3. `profiles`

### ORM Choice

Default recommendation: avoid heavy ORM usage.

Use:

1. `database/sql` or `sqlx`
2. explicit SQL queries
3. explicit migrations

Reason:

This runtime is event-heavy and stateful, not a standard CRUD app. Clear SQL will age better than forcing everything through ORM conventions.

## Browser and Multi-Device Requirements

The Go version must keep the current browser-first strength.

### Required v1 Browser Behavior

1. Open in any browser on localhost.
2. Attach more than one browser/device to the same session.
3. Restore recent output on attach.
4. Restore input history on attach.
5. Support trigger-generated action buttons.
6. Support multiple named buffers/windows.

### Required v1 Safety Improvements

These do not all need to exist on day one, but they should be in scope early:

1. `client_id` for each attached browser.
2. Track which client sent each command.
3. Optional read-only attach mode.
4. Optional active-controller lock.

## Windows Distribution Goal

Friends should be able to run the Go version as a local app without installing Go, Ruby, or a database server.

### v1 Distribution Target

1. One Windows `.exe` for the Go runtime.
2. Browser UI served by that `.exe`.
3. SQLite DB file created automatically.
4. Plugins loaded from a local `plugins/` directory.

### Practical Packaging Goal

The first release does not need to bundle every scripting runtime.

Instead:

1. The Go `.exe` must run standalone.
2. A JavaScript plugin path should be possible without extra system dependencies if we later embed or bundle a JS runtime.
3. The Ruby compatibility path may start as an optional extra installation or bundled package, but it must not block the Go runtime from working.

This keeps the base client easy to distribute while preserving the path to power-user scripting.

## Migration Strategy

Do not do a big-bang rewrite.

Build the Go version in stages while using the current Ruby app as a behavior reference.

### Phase 0: Freeze the Behavior Reference

Before serious rewriting:

1. Document current behavior.
2. Capture sample inputs/outputs.
3. Capture representative configs.
4. Capture trigger/button/history workflows.

Success criteria:

1. We can point to concrete examples of current behavior.
2. We know which features are required for launch parity.

### Phase 1: Go Session Host Without Scripting

Build a minimal Go runtime that can:

1. Connect to the MUD.
2. Serve the browser UI.
3. Broadcast output to multiple clients.
4. Accept commands from the browser.
5. Persist and restore history/logs in SQLite.

Success criteria:

1. One live MUD session works through Go.
2. Two browser clients can attach to one session.
3. History survives reconnect.

### Phase 2: Plugin Protocol

Build the out-of-process plugin system.

Add:

1. Event delivery to plugins.
2. Effect return path from plugins.
3. Plugin lifecycle management.
4. Capability model.

Success criteria:

1. A demo plugin can receive `line.received` and emit `echo`.
2. Plugin failure does not break the MUD session.

### Phase 3: Ruby Compatibility Plugin

Build the first real plugin implementation in Ruby.

This plugin should support enough of the current model to avoid a weak first release.

Target support:

1. variables
2. aliases
3. regex triggers
4. buttons
5. window output
6. basic timer scheduling

Success criteria:

1. Existing user workflows can be recreated through the Ruby plugin.
2. The Go runtime is already the real host.

### Phase 4: Friend-Installable Windows Build

Package the Go runtime for Windows.

Success criteria:

1. Friend downloads `.exe`.
2. Friend runs it locally.
3. Browser opens the client.
4. SQLite is created automatically.
5. Plugins can be dropped into a local directory.

### Phase 5: JS Plugin SDK

After the Ruby compatibility path exists, add JavaScript plugin support.

Success criteria:

1. A simple JS plugin can subscribe to events and create buttons.
2. Plugin authors can write custom behavior without touching Go.

### Phase 6: Higher-Level UX

After parity:

1. improve mobile attach UX
2. improve multi-device controls
3. add spectator/controller modes
4. add LLM advisor plugin
5. add cloud/hosted control plane later if needed

## Release Criteria For The First Go Version

The first Go release is acceptable only if all of the following are true:

1. It runs locally on Windows as a `.exe`.
2. It serves the browser UI.
3. It can hold one live MUD session.
4. More than one browser client can attach to the same session.
5. Log and input history persist in SQLite.
6. Plugins run out-of-process.
7. At least one real scripting path exists on launch, starting with Ruby.
8. Plugin authors can later target other languages using the same protocol.

## Immediate Next Steps

1. Lock this plan as the migration baseline.
2. Write a short architecture document for the Go runtime modules.
3. Define the plugin event/effect JSON protocol.
4. Define the SQLite schema for `sessions`, `events`, `history_entries`, `variables`, and `snapshots`.
5. Build the smallest possible Go session host.
