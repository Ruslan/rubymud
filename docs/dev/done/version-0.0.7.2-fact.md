# Version 0.0.7.2 — Alias-to-Local Commands & #tts (Done)

## Current State

- **Alias re-parse**: `evalStatement` in `commands.go:83` calls `evalLine()` on expanded alias template, re-checking `#`-prefix for local dispatch. `#var`, `#showme`, `#woutput`, `#tts` inside aliases all execute locally.
- **`#tts`**: `commands_tts.go` — macOS `say` command, graceful failure on other OS. Registered at `commands.go:174` as `#tts`/`#ts`.
- **`.tt` routing round-trip**: `profile_script.go:136-141` exports `target_buffer`/`buffer_action` in `#nop rubymud:rule` metadata; import restores them at `profile_script.go:378-401`. Tested in `profile_script_test.go:120`.
- **Tests**: `TestAliasRecursiveLocalCommands` (`command_test.go:99`), `TestTTSCommand` (`command_test.go:11`), `TestEchoHiding` (`echo_hiding_test.go`).
