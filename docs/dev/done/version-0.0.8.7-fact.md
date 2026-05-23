# Version 0.0.8.7 — MCCP2 Support & Initial Commands (Done)

## Current State

- **MCCP2 (zlib)**: `telnet.go` — telopt 86 negotiation (WILL/DO MCCP2, `SB MCCP2 IAC SE` detection). `session.go:1047-1070` (`activateMCCP2`) wraps connection in `zlib.NewReader`. Tracks compressed/decompressed byte counts.
- **Telnet decoder**: `telnet.go:62-148` (`Feed()`) — full state machine: IAC, WILL, DO, DONT, WONT, SB, SE, GA, EOR. Tested in `telnet_test.go`.
- **Initial commands**: `SessionRecord.InitialCommands` field (`types.go:29`). `session.go:1095-1110` (`QueueStartupCommands`) splits by newlines, queues at connect. Passed from `manager.go:82`.
- **MCCP setting**: `mccp_enabled` column in sessions table (`types.go:30`, default 1). Migration `006_mccp_settings.sql`.
- **Per-session config**: `mccpOn` bool accepted by `session.New()`, stored as `Session.mccpOn`.
