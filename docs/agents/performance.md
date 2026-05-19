# Performance & Batching Guide

Use this when you need to understand how the system maintains low latency and high throughput during intense game activity.

## Low Latency Philosophy
MUDs are real-time games. Every millisecond counts, especially in combat. The system is designed to minimize the "time-to-glass" for every character received from the server.

## Backend Batching
To avoid overwhelming the WebSocket and the browser's DOM during high-frequency output, the Go backend implements **batching**:
- **Logic**: Located in `go/internal/session/session.go`.
- **Thresholds**: Small groups of lines are collected over a very short window (milliseconds) and sent as a single `batch` message.
- **Latency Tracking**: The system tracks "batch latency" to ensure that the batching itself doesn't introduce perceptible delay.

## UI Performance
- **DOM Pruning**: The UI maintains a maximum number of rendered lines (e.g., 5000). When this limit is reached, it prunes the oldest lines to keep the DOM light and responsive.
- **Direct DOM Updates**: The main log renderer uses Vanilla TS and direct DOM manipulation instead of a reactive framework to ensure maximum speed.

## Network
- **Keep-alive**: 30s TCP keep-alives to prevent silent connection drops.
- **WebSocket Auth**: Minimal overhead token-based authentication.
