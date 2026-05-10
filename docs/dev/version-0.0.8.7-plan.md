# Version 0.0.8.7 Plan: MCCP2 Support and Initial Commands

## Summary
This version adds two session-level features needed for better compatibility with servers such as `rmud.org`:

1. MCCP2 receive-side compression support.
2. Per-session startup commands sent automatically after connect.

The important implementation detail is that this is not just a small zlib patch. The current input path is built around `IAC GA` packet splitting, so proper MCCP2 support requires replacing that assumption with a stateful Telnet decoder.

## Current State
The current code path has a few hard limits that this feature must address:

1. `go/internal/session/io.go` reads raw bytes directly from `net.Conn` and waits for `packetEnd = []byte{0xff, 0xf9}` before processing data.
2. `0xff 0xf9` is Telnet `IAC GA`, not a general packet boundary. It is only one possible prompt marker.
3. Telnet negotiation bytes are not parsed as a protocol. They are only tolerated indirectly by the current `packetEnd` split.
4. MCCP2 starts in the middle of the Telnet stream, immediately after `IAC SB MCCP2 IAC SE`, so the current model cannot safely switch to decompression.
5. Session settings UI currently edits only `name`, `mud_host`, and `mud_port`.

Because of this, the feature should be implemented as a transport/input-pipeline refactor, not as a narrow "detect one byte sequence and call zlib" patch.

## Goals
1. Negotiate MCCP2 when the server offers it and the session allows it.
2. Strip Telnet control traffic from user-visible logs, triggers, and highlights.
3. Preserve prompt flushing behavior for servers that use `GA`, and improve compatibility with `EOR` if present.
4. Let each session define startup commands, one command per line, sent automatically on connect.
5. Keep manual command sending, triggers, highlights, timers, history, and websocket behavior unchanged unless directly needed for this feature.

## Non-Goals
This version does not attempt to implement:

1. GMCP, MSDP, TTYPE, NAWS, CHARSET, or other Telnet options beyond correctly refusing or ignoring them.
2. Outbound compression.
3. A full reconnect system.
4. A general-purpose connect script language beyond newline-separated startup commands.
5. Perfect prompt detection for servers that send neither newline nor `GA`/`EOR`.

## Proposed Data Model
Add two columns to `sessions`:

1. `initial_commands TEXT NOT NULL DEFAULT ''`
2. `mccp_enabled INTEGER NOT NULL DEFAULT 1 CHECK (mccp_enabled IN (0, 1))`

### Notes
1. `mccp_enabled` is effectively a boolean stored as integer because the codebase already uses SQLite integer flags in several places.
2. Even though negotiation is "automatic when enabled", the stored value is still just `0/1`, not a tri-state mode.
3. `initial_commands` should default to the empty string, not `NULL`, so the runtime does not need null handling.

## Storage and API Changes
Files involved:

1. `go/internal/storage/migrations/006_mccp_settings.sql`
2. `go/internal/storage/types.go`
3. `go/internal/storage/session_store.go`
4. `go/internal/web/server.go`

### Required changes
1. Add the migration file.
2. Extend `storage.SessionRecord` with:
   - `InitialCommands string`
   - `MCCPEnabled int`
3. Add `gorm:"default:1"` on `MCCPEnabled` so tests that rely on `AutoMigrate` do not silently diverge from production schema defaults.
4. Set `MCCPEnabled: 1` explicitly in `CreateSession` and `EnsureDefaultSession` so defaults are correct even in tests or manual struct construction.

### Update semantics nuance
The current session update path decodes JSON into `storage.SessionRecord` and calls `db.Save(&record)`.

That means missing fields are not "left unchanged"; they become Go zero values and may be written back to the database. This matters for `mccp_enabled` because the zero value is `0`.

There are two acceptable ways to handle this:

1. Minimal approach: ensure the settings UI always sends `initial_commands` and `mccp_enabled` on every update.
2. Safer approach: switch `updateSession` to an explicit request DTO and update only known writable fields.

Recommended choice for this version: do the minimal approach, but keep the DTO hardening in mind if session settings continue to grow.

## Session Runtime Changes
Files involved:

1. `go/internal/session/manager.go`
2. `go/internal/session/session.go`
3. `go/internal/session/io.go`
4. `go/internal/session/commands.go`

### Configuration plumbing
`Manager.Connect` already loads the full `SessionRecord`, but `session.New(...)` currently receives only `sessionID`, address, store, and VM.

For this feature, the session runtime needs startup configuration at construction time. Recommended change:

1. Introduce a small session options struct or pass the full `SessionRecord` into `New`.
2. The session should know at minimum:
   - `initialCommands string`
   - `mccpEnabled bool`
3. Keep the raw `net.Conn` for writes even after compression starts.
4. Add an optional `io.Closer` field for the active zlib reader so `Session.Close()` can close it cleanly.

## Telnet Decoder Design
### Why this is necessary
MCCP2 is negotiated through Telnet, but the compressed payload starts immediately after a Telnet subnegotiation terminator. The runtime therefore needs a real Telnet parser that survives across multiple `Read()` calls and can trigger a mid-stream transport switch.

### Parser states
The parser should be stateful across reads and should use more than the original three coarse states. Recommended states:

1. `normal`
2. `iac`
3. `iacOption` for `WILL/WONT/DO/DONT`
4. `sbOption` for the first byte after `SB`
5. `sbData`
6. `sbDataIAC` for handling `IAC` inside subnegotiation data

### Commands to handle explicitly
1. `IAC IAC`: emit one literal `0xff` byte into the text stream.
2. `IAC WILL <opt>`
3. `IAC WONT <opt>`
4. `IAC DO <opt>`
5. `IAC DONT <opt>`
6. `IAC SB <opt> ... IAC SE`
7. `IAC GA`
8. `IAC EOR`

Other Telnet commands can be ignored unless they require an immediate flush boundary.

### Reply policy
Recommended response rules:

1. On `WILL MCCP2` and `mccp_enabled == true`, send `DO MCCP2`.
2. On `WILL MCCP2` and `mccp_enabled == false`, send `DONT MCCP2`.
3. On unknown `WILL <opt>`, send `DONT <opt>`.
4. On unknown `DO <opt>`, send `WONT <opt>`.
5. On `WONT` or `DONT`, update local state if needed and continue.
6. If MCCP is already active and the server repeats MCCP negotiation, ignore the duplicate and log it once at debug level.

This keeps negotiation deterministic and prevents endless option chatter.

### Fragmentation rules
The parser must survive all of the following cases:

1. `IAC` is the last byte of a `Read()`, and the command byte arrives in the next `Read()`.
2. `WILL MCCP2` is split across multiple reads.
3. `SB MCCP2 IAC SE` is split across multiple reads.
4. `IAC SE` itself is split across reads.
5. `IAC IAC` appears in normal text.
6. `IAC IAC` appears inside subnegotiation data.

The parser state and subnegotiation buffer must therefore live outside the local loop scope and persist for the entire session.

## Text Assembly and Flush Rules
The current code treats `IAC GA` as a packet terminator. The new design should separate transport decoding from text/log framing.

### Recommended pipeline
1. Read bytes from the current transport source.
2. Feed them into the Telnet decoder.
3. The decoder emits clean text bytes and flush events.
4. Accumulate clean text bytes in a session buffer.
5. Flush buffered text when one of these conditions occurs:
   - newline is present
   - `IAC GA` is received
   - `IAC EOR` is received
6. After flush, run the existing log/trigger/highlight pipeline on the resulting text chunk.

### Important details
1. Do not use raw socket read boundaries as log boundaries.
2. Keep partial UTF-8 sequences in the byte buffer until a flush boundary is reached.
3. Keep ANSI escape fragments in the byte buffer until a flush boundary is reached.
4. Continue using the current invalid UTF-8 escaping behavior only after transport decoding, not on raw pre-decompression bytes.
5. Add a reasonable maximum size for the pending text buffer so a malformed server cannot grow it forever.

### Prompt behavior
`GA` and `EOR` should flush the current partial line even if no newline has arrived. This preserves prompt visibility and keeps current behavior for servers that rely on prompt markers.

Servers that send prompts without newline and without `GA`/`EOR` will still be imperfect. That limitation is acceptable for this version and should be documented as a residual risk.

## MCCP2 Activation Design
### Negotiation flow
1. Start in raw socket mode.
2. Parse raw bytes as Telnet.
3. When the server sends `IAC WILL MCCP2`, decide whether to accept based on session settings.
4. When the server later sends `IAC SB MCCP2 IAC SE`, switch the read source to zlib immediately after `SE`.

### Transport switch detail
This is the most important nuance in the entire feature.

The compressed stream starts on the very next byte after `SE`. If the current raw buffer contains additional bytes after `SE`, those bytes are already part of the compressed stream and must not be discarded.

Recommended implementation:

1. Detect the exact index where `SB MCCP2` terminates.
2. Slice the remainder of the current raw buffer into `leftoverCompressed`.
3. Build `io.MultiReader(bytes.NewReader(leftoverCompressed), s.conn)`.
4. Create `zlib.NewReader(...)` on top of that composite reader.
5. Continue the outer read loop from the zlib reader instead of raw `s.conn.Read(...)`.

### Post-activation parsing
After MCCP starts:

1. The raw network stream is compressed binary.
2. Decompressed output may still contain Telnet commands.
3. Therefore, the Telnet parser must continue running on decompressed bytes.
4. Do not assume compression removes the need for Telnet parsing.

### Failure behavior
If MCCP has been accepted and zlib reader creation or decompression fails, the safest behavior is to log the error and disconnect the session.

There is no reliable plaintext fallback after the server has switched to compressed output.

## Initial Commands Design
### Storage format
`initial_commands` is a multiline text field, one command per line.

### Normalization rules
1. Normalize `\r\n` to `\n`.
2. Trim a trailing `\r` from each line if needed.
3. Ignore fully empty lines.
4. Preserve interior whitespace inside commands.
5. Do not call `strings.TrimSpace` on the full command, because that would silently change intentional leading or trailing spaces.

### Send path
Recommended flow:

1. `Manager.Connect` creates the session.
2. Start the read loop.
3. Register the session in the manager map.
4. Queue startup commands through the existing command dispatcher rather than calling `conn.Write(...)` in a tight loop.

This reuses the existing 50 ms pacing in `runCommandDispatcher()` and keeps connect-time command sending consistent with other automated commands.

### Timing choice
Preferred default: queue startup commands immediately after the session is fully constructed and the read loop is running.

If manual verification against `rmud.org` shows that the first line is sometimes lost when sent immediately, add one small one-time delay before the first queued startup command, such as 75 ms. Do not add extra per-line sleeps beyond the existing dispatcher pacing.

### History and overlays
Startup commands should be visible in history, but they should not attach command hints to the "latest log entry".

Reason:

1. On a fresh connect there may be no current log entry yet.
2. The latest log entry in storage may belong to a previous session run.
3. Reusing the generic `SendCommand` path unchanged could annotate stale history/log context.

Recommended behavior:

1. Add a dedicated source kind such as `connect` or `startup`.
2. Reuse the normal outbound write path and history append.
3. Skip `AppendCommandHintToLatestLogEntry(...)` for connect-time commands.

### Reconnect semantics
`initial_commands` and `mccp_enabled` are connect-time settings. Changes to them should take effect on the next connect, not mid-session.

## UI Changes
Files involved:

1. `ui/src/SettingsApp.svelte`

### Required changes
1. Extend the `Session` interface with:
   - `initial_commands: string`
   - `mccp_enabled: number`
2. Extend `defaultSession()` so new sessions default to:
   - `initial_commands: ''`
   - `mccp_enabled: 1`
3. Add a multiline textarea to the session editor for startup commands.
4. Add a checkbox or select for MCCP2.
5. Add helper text explaining:
   - one command per line
   - sent on each connect
   - changes apply on next reconnect

### UI wording recommendation
1. Label: `Initial Commands`
2. Help text: `One command per line. Sent automatically after connect.`
3. Label: `Enable MCCP2 compression`
4. Help text: `If disabled, the client will refuse MCCP2 even if the server offers it.`

## Logging and Observability
Add targeted server-side logs for:

1. `WILL MCCP2` seen
2. `DO MCCP2` or `DONT MCCP2` sent
3. MCCP stream activation
4. duplicate MCCP negotiation ignored
5. zlib activation/decompression failure
6. startup commands queued

These should go to application logs, not user-visible MUD logs.

## Test Plan
### Unit tests for Telnet parser
Create focused tests for parser behavior independent of the full websocket/log pipeline.

Cover at least:

1. plain text without Telnet commands
2. `IAC IAC` becomes one literal `0xff`
3. `IAC GA` flushes pending prompt text
4. `IAC EOR` flushes pending prompt text
5. fragmented `WILL MCCP2`
6. fragmented `SB MCCP2 IAC SE`
7. unknown `WILL` yields `DONT`
8. unknown `DO` yields `WONT`
9. subnegotiation with escaped `IAC IAC`

### Session-level tests
Add tests around real session behavior using either `net.Pipe()` or a scripted fake connection.

Cover at least:

1. startup commands are split by line and queued in order
2. blank startup lines are ignored
3. startup commands are stored in history with source `connect` or `startup`
4. startup commands do not add stale command overlays to old log entries
5. MCCP disabled causes `DONT MCCP2`
6. MCCP activation uses leftover compressed bytes from the same raw read
7. Telnet commands still parse correctly after decompression begins
8. closing a session closes any active zlib reader

### Storage and API tests
Cover at least:

1. migration applies cleanly on an existing database
2. session create path defaults `mccp_enabled` to `1`
3. session JSON round-trips `initial_commands` and `mccp_enabled`
4. session update persists both fields

### Manual verification
Use `rmud.org` as the primary real-server verification target.

Manual checklist:

1. connect with MCCP enabled and confirm no binary garbage appears in logs
2. confirm server offers MCCP2 and the client activates zlib successfully
3. set startup commands to navigate the initial menu and verify they run automatically
4. disable MCCP and confirm the session still works without accepting compression
5. reconnect after changing settings and confirm new values apply only on the next connect

## Implementation Order
1. Add migration and storage model fields.
2. Plumb session connect-time configuration from `Manager.Connect` into `Session`.
3. Add parser-focused unit tests before changing the full read loop.
4. Replace the `packetEnd`-based read logic with Telnet decoding plus clean text assembly.
5. Add MCCP activation and compressed-reader handoff.
6. Add startup command queuing and connect-time history behavior.
7. Add session settings UI fields.
8. Run targeted tests and manual verification against `rmud.org`.

## Acceptance Criteria
This work is complete when all of the following are true:

1. Telnet negotiation bytes no longer appear in logs, triggers, or highlights.
2. MCCP2 sessions render readable text after compression starts.
3. Prompt flushing still works for `GA`, and also works for `EOR` if received.
4. Startup commands run once per connect, in the configured order.
5. Startup commands are recorded in history without attaching misleading log overlays.
6. Disabling MCCP2 causes the client to refuse the server offer.
7. The settings UI can view and edit both new session fields.

## Residual Risks
1. Some servers may send prompt fragments without newline and without `GA`/`EOR`; those prompts may remain buffered until later text arrives.
2. Once MCCP is accepted, decompression failure is effectively fatal for that session.
3. The session update API still uses a full-record save unless separately hardened.

## Appendix: Expected MCCP2 Sequence
Typical successful flow:

1. client connects over plain TCP
2. server sends banner and Telnet negotiation bytes
3. server sends `IAC WILL MCCP2`
4. client replies `IAC DO MCCP2`
5. server sends `IAC SB MCCP2 IAC SE`
6. every following raw socket byte belongs to the zlib stream
7. client reads through zlib, then continues Telnet parsing on decompressed bytes

This ordering is why the implementation must switch transports exactly at the `SE` boundary and must preserve any leftover bytes already read from the socket.

## MCCP Monitoring and Runtime Stats
To aid in manual verification and diagnostics, the client provides real-time monitoring of MCCP compression performance.

### Implementation Details
1. **Runtime-Only State**:
   - MCCP stats (active status, byte counters, and ratio) are stored in-memory only in the `Session` struct.
   - They are **reset** automatically on every new connection or reconnect.
   - There are **no SQLite writes** per packet or per read for statistics tracking to avoid overhead.

2. **Byte Counting**:
   - **Compressed Bytes**: Counts the raw zlib input bytes consumed from the network after MCCP activation. This includes any leftover compressed bytes already read into the buffer during the activation sequence.
   - **Decompressed Bytes**: Counts the bytes produced by the zlib stream and processed by the Telnet read loop after activation.

3. **Compression Ratio**:
   - The "bandwidth saved" percentage is computed as `100 * (1 - compressed / decompressed)`.
   - If decompressed bytes are less than or equal to compressed bytes (which can happen during small initial reads or if zlib header overhead is significant), the UI displays `0%`.

### UI and Observability
1. **Sessions Tab**:
   - The Settings/Sessions UI table includes a "Network" column for connected sessions.
   - If MCCP is active, it displays an `MCCP Active` tag along with KB throughput and the savings percentage.
   - If the session is disconnected, stats are omitted or shown as inactive.

2. **Polling Refresh**:
   - The UI uses **client-side polling** (every 1.5 seconds) while the Sessions tab is active to refresh these stats.
   - This avoids the overhead of a dedicated WebSocket push for diagnostic data while still providing a "live" feel for manual testing.

### Manual Verification Checklist
- [ ] **Verify Activation**: Connect to an MCCP-supporting server (e.g., `rmud.org`) and confirm the `MCCP Active` tag appears in the Sessions tab.
- [ ] **Confirm Stats Increase**: Observe the `compressed / decompressed` byte counts increasing as MUD traffic is received.
- [ ] **Verify Reset**: Disconnect and reconnect the session; confirm the counters reset to zero and the ratio returns to `0%` until compression re-activates.
