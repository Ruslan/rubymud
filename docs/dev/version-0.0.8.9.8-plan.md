# Version 0.0.8.9.8 Plan - Logs Section in Admin UI

## Goal
Add a dedicated "Logs" section to the Settings (Admin) UI to allow browsing historical session logs by date range with pagination and downloading them as plain text.

This is a pre-release for Version 0.0.9, focusing on the core log management requirements.

## Architecture & Implementation

### 1. Backend Changes (Go)

#### Storage Layer (`go/internal/storage/log_store.go`)
- Implement `LogRangeByDate(sessionID int64, from, to time.Time, limit, offset int) ([]LogEntry, int64, error)`:
  - Filter `log_entries` by `session_id` and `created_at` range.
  - Exclude "gagged" entries using `visibleLogEntrySQL`.
  - Return total count for pagination.
  - Load overlays (colors, commands, buttons) for the fetched entries.

#### API Layer (`go/internal/web/server.go`)
- Add `GET /api/sessions/{sessionID}/logs`:
  - Query parameters: `from` (ISO8601), `to` (ISO8601), `page`, `limit`.
  - Returns: `{ "entries": [...], "total": 1234, "page": 1, "limit": 100 }`.
- Add `GET /api/sessions/{sessionID}/logs/download`:
  - Query parameters: `from`, `to`.
  - Response: `text/plain` file download (`Content-Disposition: attachment`).
  - Implementation: Use GORM `Rows()` to stream results and write `PlainText` to the response writer to handle large ranges efficiently.

#### Routing
- Register the new routes in `New()` function in `go/internal/web/server.go`.

### 2. Frontend Changes (Svelte)

#### State Management (`ui/src/SettingsApp.svelte`)
- Add new state variables:
  - `logs: LogEntry[]`
  - `logTotal: number`
  - `logFrom: string` (default to today's date, YYYY-MM-DD)
  - `logTo: string` (default to today's date, YYYY-MM-DD)
  - `logPage: number` (default 1)
  - `logLimit: number` (default 100)

#### UI Components
- **Tabs**: Add `logs` to the `tabs` array.
- **Log Panel**:
  - Date inputs for `from` and `to`.
  - Pagination controls: "Previous", "Next", and "Page X of Y" indicator.
  - Log Display: A scrollable area showing `DisplayPlain` text (or `PlainText` if overlays are not needed for simple view).
  - Download Button: Triggers the `/api/sessions/{sessionID}/logs/download` endpoint.

#### Logic
- Update `fetchData` to handle the `logs` tab.
- Add `fetchLogs` function to be called when dates or page change.
- Ensure the `download` button uses the correct token (via URL query param or header).

## Verification Plan

### Automated Tests
- Add `TestLogRangeByDate` in `go/internal/storage/log_store_test.go`:
  - Verify filtering by date range.
  - Verify pagination (`limit`/`offset`).
  - Verify total count accuracy.

### Manual Verification
- Open Settings UI, navigate to "Logs" tab.
- Select different date ranges and verify logs are displayed correctly.
- Test pagination (Next/Prev).
- Click "Download (.txt)" and verify the downloaded file content and format.
- Verify that "gagged" entries are not shown or exported.

## Risks & Mitigations
- **Large Log Volumes**: Downloading a very large range could be slow. Mitigation: Use streaming on the server side.
- **Timezones**: Inconsistency between browser local time and server UTC. Mitigation: Convert date picker values to full ISO strings (start of day to end of day) and ensure server parses them correctly as UTC.
