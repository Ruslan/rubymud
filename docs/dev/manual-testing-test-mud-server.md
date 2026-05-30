# Manual Testing With Test MUD Server

`ruby/test_mud_server.rb` is a small local-only TCP server for browser/client manual QA.

It is intentionally not part of the Go runtime. Use it when a feature needs controlled MUD output that is awkward to reproduce on a real server.

## Start Server

Default host and port:

```bash
ruby ruby/test_mud_server.rb
```

Custom port:

```bash
PORT=4001 ruby ruby/test_mud_server.rb
```

Then start RubyMUD against it:

```bash
make run MUD=127.0.0.1:4000
```

Or configure a session in Settings with:

```text
Host: 127.0.0.1
Port: 4000
```

## Commands

`BELL`

Sends an actual ASCII BEL control character (`0x07`) before a system message:

```text
\x07[*** СИСТЕМА: Перезагрузка через 30 минут. ***]
```

Expected client behavior:

1. output shows safe `[BEL]` marker
2. output does not contain a raw control character
3. active pane flashes once via visual bell CSS
4. refresh/restore keeps the marker and metadata but does not replay the flash

`HELP`

Lists available commands.

`QUIT`

Closes the test session.

Any other input is echoed back as plain text.

## Notes

- Keep this server deterministic and small.
- Add commands here only for manual QA cases that need controlled protocol output.
- Do not depend on this server in automated tests.
