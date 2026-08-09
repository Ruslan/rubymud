# Web Access Bootstrap

## Goal

Make the first run convenient for a solo player without exposing an
unauthenticated client to the local network. Database-managed Web Access remains
the normal user-facing configuration; CLI and environment values are explicit
operator overrides.

## Contract

1. With no saved listener and no explicit `--listen`, `mudhost` listens on
   `127.0.0.1:8080`. Web Access may remain disabled on an effective loopback
   listener.
2. App Settings exposes `Local only` and `Network access` listener modes next to
   the Web Access credentials. Network mode accepts a validated TCP listen
   address such as `0.0.0.0:8080`.
3. Saving a listener changes the next-start configuration. It does not rebind a
   running HTTP server. Settings shows the current listener, configured
   next-start listener, and `Restart required` when they differ.
4. Network access can be selected only when effective Web Access credentials
   exist. Setting a password never opens the listener automatically.
5. Startup is fail-closed: a non-loopback effective listener without enabled
   Web Access credentials is a fatal configuration error. `:8080` and wildcard
   IPv4/IPv6 binds are network listeners.
6. Web Access cannot be disabled unless both the current listener and the saved
   next-start listener are loopback. To remove a password from a network
   deployment, save `Local only`, restart, then disable protection.
7. An explicit `--listen` owns the effective listener while the process runs.
   Settings still permits repairing the persisted next-start listener, so an
   operator can recover from an unsafe saved address through a temporary
   loopback override. The persisted listener is left untouched until saved.
   Existing paired `MUDHOST_BASIC_AUTH_USER` / `MUDHOST_BASIC_AUTH_PASS`
   credentials remain a read-only security override.
8. Exact `/mcp` remains outside browser Basic Auth and continues to use its own
   Bearer credential. Listener safety for the browser and MCP tokenless-listener
   safety are separate checks.

## Persistence And API

- Store the desired listener as application configuration in SQLite. Do not add
  a schema migration when the existing `app_settings` storage can represent the
  value atomically.
- Extend `GET /api/web-auth/settings` with safe listener status only: current
  address/exposure, configured address/exposure, external-management state, and
  restart-required state.
- Extend the authenticated same-origin PATCH contract without returning a
  password, password hash, API token, or environment credential.
- Validate addresses with structured host/port parsing. A loopback decision must
  be based on the bound/configured host, never on `Host`, `Origin`, or forwarded
  request headers.

## UI

- Keep the control in `Settings -> App -> Web Access` beside MCP.
- Use a mode control for `Local only` / `Network access`, an address input for
  network mode, current status, and a concise restart-required state.
- Disable network mode until credentials are active. With `--listen`, show that
  the current address is an override while keeping the saved next-start value
  repairable.
- Do not claim that a saved listener is active before restart.

## Deployment

- Caddy should normally proxy to the loopback listener and provide TLS only;
  built-in Web Access protects browser HTML, API, and WebSocket routes.
- Basic Auth over raw network HTTP exposes the password to the network. Use
  HTTPS through Caddy or another TLS reverse proxy for every network listener.
- A container which needs a published wildcard listener must bootstrap with
  paired Basic Auth environment credentials or an already configured database.
  Otherwise startup fails instead of exposing an unprotected port.
- Document the breaking default change from `:8080` to `127.0.0.1:8080` and the
  safe Caddy/Docker migration paths.

## Verification

- Repro-first tests for default resolution, explicit flag precedence, persisted
  listener restart behavior, IPv4/IPv6 loopback, wildcard rejection, and partial
  environment credentials.
- HTTP tests for listener status shape, same-origin/strict JSON writes, read-only
  overrides, restart-required transitions, network-without-credential rejection,
  and password-disable ordering.
- Ensure two Server instances backed by one Store observe committed settings
  consistently without reading SQLite on each request.
- UI API/component tests for both listener modes, locked states, and responsive
  layout.
- Run full Go/UI tests, focused race tests, `go vet ./...`, production UI build,
  and `git diff --check`.
