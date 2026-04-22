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
