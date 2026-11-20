# rubymud

Local-first MUD host and browser client.

If you downloaded a ready-made binary (`mudhost.exe` on Windows), the most important folders are:

- `data/mudhost.db` - your local database
- `data/config/` - import/export folder for `.tt` profile files

If you already have your own `.tt` file, put it into `data/config/`, start `mudhost`, open `http://localhost:8080/settings#profiles`, and import it from the `Files in config/` section.

The current runtime is Go-based. The browser UI source of truth lives in `ui/` and is built with Vite into Go-embedded static assets under `go/internal/web/static/`.

## Source Of Truth

- Frontend source of truth: `ui/`
- Generated frontend assets: `go/internal/web/static/`
- Generated assets are not committed on purpose
- Anyone who clones the repo is expected to build the UI locally

`make run` and `make build` already rebuild the frontend before starting or compiling the Go server.

## Requirements

- Go
- Node.js + npm
- `sqlite3`

## First-Time Setup

## Using A Downloaded Binary

If you are not building from source and just want to run the app:

1. Put `mudhost.exe` in any folder you like.
2. Create a `data/` folder next to it if it does not exist yet.
3. Put your `.tt` profile files into `data/config/`.
4. Start `mudhost.exe`.
5. Open `http://localhost:8080/settings#profiles`.
6. In `Files in config/`, click `Import` for your `.tt` file.
7. Open the `Sessions` tab and make sure the imported profile is attached to your session.

Expected layout:

```text
mudhost.exe
data/
  mudhost.db
  config/
    my-profile.tt
    healer.tt
```

Notes:

- `data/mudhost.db` is created automatically on first start
- `data/config/` is created automatically on first start
- exported profiles are also written back into `data/config/`

Install frontend dependencies once after cloning:

```bash
make ui-install
```

Or manually:

```bash
cd ui && npm install
```

## Running

Start the app against a MUD server:

```bash
make run MUD=127.0.0.1:4000
```

This will:

1. initialize the SQLite database
2. build the UI from `ui/`
3. start the Go server

Open `http://localhost:8080` in your browser.

## Running As A systemd Service

On a Linux host you can keep mudhost running as a systemd **user** service, so it starts at boot and restarts after a crash:

```bash
loginctl enable-linger "$USER"   # once, so user services run without a login session
make service-install             # write ~/.config/systemd/user/rubymud.service and enable it
make deploy                      # build, install the binary, (re)start the service
make service-logs                # follow the journal
```

- The service runs a deployed copy of the binary (`DEPLOY_BIN`, default `~/.local/lib/rubymud/mudhost`), not `bin/mudhost`. `make build`, tests and E2E runs rebuild `bin/mudhost` without affecting the running server; only `make deploy` ships a new version.
- It uses `data/mudhost.db` in the checkout and the listener saved in Settings → Web Access (first-run default is loopback-only).
- Optional `MUDHOST_*` variables (MCP, basic auth) go in `~/.config/rubymud/env`, one `KEY=value` per line; run `systemctl --user restart rubymud` after editing it.
- Like `make run`, startup connects the `default` session, so every `make deploy` reconnects the game session.
- Stop the service (`systemctl --user stop rubymud`) before using `make run`: both want the same listener and database.
- `SERVICE` and `DEPLOY_BIN` can be overridden on the `make` command line.

## Docker

You can also run the application using Docker. A multi-stage `Dockerfile` is provided which builds the Go backend and Node.js frontend into a lightweight Alpine image.

To build the Docker image:

```bash
make docker-build
```

To run the container with the default settings (exposing port 8080 and mounting the local `./data` folder to persist your database and profiles):

```bash
make docker-run
```

Alternatively, you can run the container manually and pass environment variables to configure it:

```bash
docker run -it --rm \
  -p 8080:8080 \
  -v "$(pwd)/data:/data" \
  -e MUD_HOST="rmud.org:4000" \
  rubymud
```

### Docker Volumes

It is important to map the `/data` volume to a local folder (as shown with `-v "$(pwd)/data:/data"`) so that your local SQLite database (`mudhost.db`) and `.tt` profiles in `data/config/` are not lost when the container stops.

#### Using an Existing Database

If you already have an existing database file (for example, you downloaded a ready `mudhost.db`), you should place it inside your mapped local directory before starting the container:

```bash
# Example layout on your host machine:
data/
  mudhost.db
```

When the container starts, it will automatically find and use the `./data/mudhost.db` file instead of creating a new empty one.

## Building

Build the frontend assets and the Go binary:

```bash
make build
```

This writes the compiled binary to `bin/mudhost`.

Build only the frontend assets:

```bash
make ui
```

Run the Go server directly without `make`:

```bash
cd go && go run ./cmd/mudhost --mud "rmud.org:4000" --listen ":8080" --db "../data/mudhost.db" --config-dir "../data/config"
```

## Testing

Run Go tests:

```bash
make test
```

Tests and E2E runs never touch a running service: the mapper E2E (`make e2e-mapper-reconciliation`) uses its own loopback fake MUD, a temporary database and a random port.

## Notes

- The generated Vite assets in `go/internal/web/static/` are derived artifacts, not the frontend source of truth
- If the browser UI looks stale after frontend changes, rebuild with `make ui` or `make run` and hard-refresh the page

## Troubleshooting

### `vite: command not found`

The frontend dependencies have not been installed yet.

```bash
make ui-install
```

Then rerun `make ui`, `make build`, or `make run`.

### `go run ./cmd/mudhost`: directory not found

The Go entrypoint lives at `go/cmd/mudhost/main.go` and should be run as a package from the `go/` module root:

```bash
cd go && go run ./cmd/mudhost
```

Compiled binaries are written to `bin/`, not next to the source package.

## License

RubyMUD is licensed under the [Apache License 2.0](LICENSE).
