# Transport Layer Guide

Use this when you need to understand how `rubymud` communicates with MUD servers and browser clients, including protocols, compression, and session lifecycle.

## 1. MUD Transport (Backend to MUD)

- **Protocol**: Standard TCP with a Telnet state machine implementation.
- **Location**: `go/internal/session/telnet.go` and `io.go`.

### Telnet & MCCP
- **Telnet Implementation**: A custom state machine handles IAC (Interpret As Command) sequences, SB (Subnegotiation), and GA (Go Ahead).
- **MCCP2 (MUD Client Compression Protocol)**: Supports zlib-based compression. When the MUD server sends the MCCP2 subnegotiation, the engine transparently switches to decompressing the stream using `compress/zlib`.
- **Charset**: Normalization of incoming text is handled at the transport edge.

### Connection Lifecycle
- **Keep-alive**: Dialers use a 30-second TCP Keep-alive.
- **Persistence**: Connection parameters (host, port) are stored in the `sessions` table.

## 2. Web Transport (Backend to Browser)

- **Protocol**: WebSockets (via Gorilla WebSocket).
- **Location**: `go/internal/web/server.go`.

### Authentication
- **Token Protection**: Every WebSocket and API request must include a valid session token.
- **Token Delivery**: The token is either sent in the `X-Session-Token` header or as a `token` query parameter.
- **Storage**: The token is generated on the first run and stored in the `settings` table of the database.

### Message Format
- **Outgoing (Server -> Client)**: JSON objects with a `type` field (e.g., `log`, `status`, `tick`, `variables`).
- **Incoming (Client -> Server)**: JSON objects with a `method` field (e.g., `command`, `history`, `toggle_group`).

## 3. Session & Client Management

- **Manager**: `go/internal/session/manager.go` tracks all active MUD sessions.
- **Multi-client Support**: Multiple browser clients can attach to the same MUD session. The backend broadcasts logs to all attached clients.
- **Restore Logic**: When a browser client connects, the server performs a "restore" process, sending the most recent N lines of history from SQLite to the client so the UI can rebuild its buffer.
- **Batching**: To improve performance during high-frequency output (e.g., combat), the server batches log entries and sends them as a single WebSocket message.
