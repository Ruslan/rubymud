# Password / Telnet Echo-Off Logging Guard

## Context

Player input is logged in cleartext, so an in-band **password prompt** can leak
into stored history and into shareable exports. This became relevant when the
colored HTML export (`html-log-export.md`) started surfacing sent commands in a
file users share.

Two distinct paths, discovered during the HTML-export work:

1. **Session-init / auto-login commands** (`SessionRecord.InitialCommands`,
   sent with `source="connect"`): **already excluded** from the HTML export —
   `commands.go` skips `AppendCommandHintToLatestLogEntry` when
   `source == "connect"` (`if source != "connect"`), and the export reads
   `command_hint` overlays. **But** these commands are still written to
   `history_entries` with `kind="connect"` in cleartext (admin History view /
   DB). So auto-login passwords are safe in *export* but not in *storage*.
2. **Live-typed password at an in-band echo-off prompt** (`source="input"`):
   NOT guarded at all. The MUD sends `Password:` and negotiates telnet
   `WILL ECHO` (server takes over echo so the client hides input). Our telnet
   layer currently **refuses** it — `handleTelnetEvent` answers `DONT` to
   `WILL ECHO` — so we never enter echo-off mode, the input is locally echoed,
   AND it is logged (`history_entries kind="expanded"` + a `command_hint`
   overlay), so it CAN appear in the HTML export.

Why this was easy to miss: MUDs with external/out-of-band auth never send an
in-band password prompt, so the leak never triggers there.

## Goal

Detect when the server has requested echo-off (a password prompt) and **do not
log the user's input** entered while echo is off — neither to `history_entries`
nor as a `command_hint` overlay — so passwords never hit storage or export.

## Approach (proposed)

1. **Track telnet ECHO state instead of blanket-refusing it.** In
   `handleTelnetEvent` (`session.go`), handle `IAC WILL ECHO` by entering an
   "server-echo / local echo-off" mode (respond appropriately per RFC 857 so the
   client stops local echo), and `IAC WONT ECHO` to exit it. Keep MCCP handling
   as-is. This also fixes the UX bug that passwords are currently locally
   echoed.
2. **Suppress input logging while echo is off.** In `SendCommandWithTrace`
   (`commands.go`), when the session is in echo-off mode and `source=="input"`,
   send the bytes to the MUD but **skip** `AppendHistoryEntry` and
   `AppendCommandHintToLatestLogEntry`. Optionally record a redacted marker
   (`kind="input"`, text `"********"`) so history shows *that* a password was
   sent without the value.
3. **Front-end**: while echo-off, the input box should mask input (password
   field behavior) and the optimistic sent-command hint should be redacted, not
   show the typed secret.
4. **Auto-login storage redaction (optional, separate sub-task).** Consider not
   persisting `source="connect"` commands to `history_entries` verbatim, or
   redacting lines that follow a `Password:`-like prompt in the init script.
   `InitialCommands` itself is user-entered config; document that it stores
   credentials in cleartext in `sessions` and consider at-rest handling.

## Non-Goals

- Encrypting the whole DB / credential vaulting (bigger effort; out of scope).
- Guessing password prompts by text heuristics — rely on the telnet ECHO signal,
  which is the standard mechanism.

## Acceptance Criteria

1. When the server negotiates `WILL ECHO`, the session enters echo-off mode and
   the client masks input.
2. Input typed while echo is off is sent to the MUD but is **not** written to
   `history_entries` or `command_hint` overlays (optionally a redacted marker
   is stored instead of the value).
3. `WONT ECHO` restores normal echo + normal logging.
4. A live-typed password at an in-band prompt never appears in the HTML export
   (`html-log-export.md`) nor in the admin History view.
5. Regression: the existing `source="connect"` exclusion from `command_hint`
   overlays is preserved (asserted by test), so auto-login stays out of export.
6. Repro-first tests cover: echo-off entered on `WILL ECHO`; input during
   echo-off not logged; echo restored on `WONT ECHO`; normal input still logged.

## References

- `html-log-export.md` — Security Note (this is the "separate small security
  fix" referenced there).
- `go/internal/session/session.go` `handleTelnetEvent`, telnet option handling.
- `go/internal/session/commands.go` `SendCommandWithTrace` — the log-write point.
- `go/internal/storage/log_store.go` `AppendCommandHintToLatestLogEntry`.
