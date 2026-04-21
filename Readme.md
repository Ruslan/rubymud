# rubymud

Local-first MUD host and browser client.

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

Build only the frontend assets:

```bash
make ui
```

## Testing

Run Go tests:

```bash
make test
```

## Notes

- The generated Vite assets in `go/internal/web/static/` are derived artifacts, not the frontend source of truth
- If the browser UI looks stale after frontend changes, rebuild with `make ui` or `make run` and hard-refresh the page
