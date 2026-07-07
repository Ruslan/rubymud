package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"rubymud/go/internal/storage"
)

// seedLog inserts a log_entries row with an explicit created_at (source_type
// "mud") and returns its id.
func seedLog(t *testing.T, s *Server, sessionID int64, buffer, raw, plain string, at time.Time) int64 {
	t.Helper()
	rec := storage.LogRecord{
		SessionID:  sessionID,
		WindowName: buffer,
		Stream:     "mud",
		RawText:    raw,
		PlainText:  plain,
		SourceType: "mud",
		CreatedAt:  storage.SQLiteTime{Time: at.UTC()},
		ReceivedAt: storage.SQLiteTime{Time: at.UTC()},
	}
	if err := s.store.DB().Create(&rec).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	return rec.ID
}

// seedOverlay inserts a log_overlays row with an explicit created_at.
func seedOverlay(t *testing.T, s *Server, logEntryID int64, overlayType, payloadJSON, sourceType string, at time.Time) {
	t.Helper()
	ov := storage.LogOverlay{
		LogEntryID:  logEntryID,
		OverlayType: overlayType,
		PayloadJSON: payloadJSON,
		SourceType:  sourceType,
		CreatedAt:   storage.SQLiteTime{Time: at.UTC()},
	}
	if err := s.store.DB().Create(&ov).Error; err != nil {
		t.Fatalf("seed overlay: %v", err)
	}
}

func getExportHTML(t *testing.T, ts *httptest.Server, token, url string) string {
	t.Helper()
	req, err := newAuthenticatedRequest(http.MethodGet, url, nil, token)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET export-html: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestExportHTMLSelfContainedAndColored verifies the streamed document is a
// valid, self-contained HTML file with embedded theme CSS (no external refs) and
// native ANSI baked to ansi_up-compatible spans.
func TestExportHTMLSelfContainedAndColored(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	seedLog(t, s, sid, "main", "\x1b[31mred alert\x1b[0m", "red alert", base)

	url := fmt.Sprintf("%s/api/sessions/%d/logs/export-html?title=My+Raid", ts.URL, sid)
	html := getExportHTML(t, ts, s.apiToken, url)

	for _, want := range []string{
		"<!DOCTYPE html>", "<style>", "</html>",
		`<pre class="log-export__body">`,
		"--ansi-red-fg",   // theme palette variable embedded
		".ansi-red-fg",    // ansi-classes mapping embedded
		".log-export__body", // export-base embedded
		`class="ansi-red-fg"`, // native ANSI colored via ansihtml
		"red alert",
		"My Raid", // title
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("export missing %q\n---\n%s", want, html)
		}
	}

	// No external assets.
	if regexp.MustCompile(`<link\b`).MatchString(html) {
		t.Fatalf("export has <link> (external asset)")
	}
	if strings.Contains(html, "src=") {
		t.Fatalf("export has src= (external asset)")
	}
	if regexp.MustCompile(`https?://`).MatchString(html) {
		t.Fatalf("export references an http(s) URL (external asset)")
	}

	// Content-Disposition filename.
	// (Checked indirectly: a follow-up assert on the header would need the resp;
	// filename format is covered by TestExportHTMLFilename.)
}

// TestExportHTMLExcludesEchoAndTogglesCommands verifies only source_type='mud'
// output is exported (local echo excluded) and that commands are toggled.
func TestExportHTMLExcludesEchoAndTogglesCommands(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	id := seedLog(t, s, sid, "main", "SERVER-LINE", "SERVER-LINE", base)
	seedOverlay(t, s, id, "command_hint", `{"command":"kill dragon"}`, "client", base.Add(time.Second))
	// Local echo (source_type="echo") must be excluded.
	if _, err := s.store.AppendLogEntry(sid, "main", "LOCAL-ECHO-LINE", "LOCAL-ECHO-LINE"); err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}

	withCmds := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html", ts.URL, sid))
	if !strings.Contains(withCmds, "SERVER-LINE") {
		t.Fatalf("server output missing")
	}
	if strings.Contains(withCmds, "LOCAL-ECHO-LINE") {
		t.Fatalf("local echo leaked into export")
	}
	if !strings.Contains(withCmds, "kill dragon") || !strings.Contains(withCmds, `class="log-line log-input"`) {
		t.Fatalf("command missing/undistinguished:\n%s", withCmds)
	}
	if !strings.Contains(withCmds, "&gt; kill dragon") {
		t.Fatalf("command not prefixed with '> '")
	}

	noCmds := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html?commands=0", ts.URL, sid))
	if strings.Contains(noCmds, "kill dragon") {
		t.Fatalf("commands=0 should omit commands")
	}
	if !strings.Contains(noCmds, "SERVER-LINE") {
		t.Fatalf("commands=0 dropped server output")
	}
}

// TestExportHTMLDataAttributesMonotonic verifies every line carries data-source
// and a monotonic non-decreasing data-t (ms from the first row).
func TestExportHTMLDataAttributesMonotonic(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 59, 0, time.UTC)
	seedLog(t, s, sid, "main", "a", "a", base)
	seedLog(t, s, sid, "main", "b", "b", base.Add(100*time.Millisecond))
	seedLog(t, s, sid, "main", "c", "c", base.Add(time.Second))

	html := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html", ts.URL, sid))

	re := regexp.MustCompile(`data-source="(output|command)" data-t="(\d+)"`)
	matches := re.FindAllStringSubmatch(html, -1)
	if len(matches) != 3 {
		t.Fatalf("expected 3 log lines, got %d\n%s", len(matches), html)
	}
	var prev int64 = -1
	for i, m := range matches {
		dt, _ := strconv.ParseInt(m[2], 10, 64)
		if i == 0 && dt != 0 {
			t.Fatalf("first data-t = %d, want 0", dt)
		}
		if dt < prev {
			t.Fatalf("data-t not monotonic at %d: %d < %d", i, dt, prev)
		}
		prev = dt
	}
	// b is 100ms after a, c is 1000ms after a.
	if matches[1][2] != "100" || matches[2][2] != "1000" {
		t.Fatalf("data-t values = %s,%s want 100,1000", matches[1][2], matches[2][2])
	}
}

// TestExportHTMLBufferFilter verifies the buffer filter scopes output.
func TestExportHTMLBufferFilter(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	seedLog(t, s, sid, "main", "MAIN-ONLY", "MAIN-ONLY", base)
	seedLog(t, s, sid, "chat", "CHAT-ONLY", "CHAT-ONLY", base.Add(time.Second))

	html := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html?buffer=chat", ts.URL, sid))
	if !strings.Contains(html, "CHAT-ONLY") || strings.Contains(html, "MAIN-ONLY") {
		t.Fatalf("buffer filter failed:\n%s", html)
	}
}

// TestExportHTMLCarriesAnsiStateAcrossOutputLines verifies the handler renders
// consecutive OUTPUT rows through one persistent converter (an unterminated color
// carries to later lines, matching the live pane) while a COMMAND row in between
// does not perturb the carried state.
func TestExportHTMLCarriesAnsiStateAcrossOutputLines(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	// Line 1 opens red, no reset.
	id := seedLog(t, s, sid, "main", "\x1b[31mALPHA", "ALPHA", base)
	// A command in between (should not clear the carried red state).
	seedOverlay(t, s, id, "command_hint", `{"command":"look"}`, "client", base.Add(time.Second))
	// Line 2 is plain text -> should still be red.
	seedLog(t, s, sid, "main", "BRAVO", "BRAVO", base.Add(2*time.Second))

	html := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html", ts.URL, sid))

	reAlpha := regexp.MustCompile(`data-source="output"[^>]*><span class="ansi-red-fg">ALPHA`)
	reBravo := regexp.MustCompile(`data-source="output"[^>]*><span class="ansi-red-fg">BRAVO`)
	if !reAlpha.MatchString(html) {
		t.Fatalf("line 1 not red:\n%s", html)
	}
	if !reBravo.MatchString(html) {
		t.Fatalf("line 2 did not carry red state across the command row:\n%s", html)
	}
	if regexp.MustCompile(`data-source="command"[^>]*><span class="ansi-red-fg">`).MatchString(html) {
		t.Fatalf("command row wrongly rendered with carried server ANSI state")
	}
}

// TestExportHTMLEmbedsReplayLayer verifies the Phase 2 replay layer is emitted
// into the file: the control bar (Replay label, play/pause + stop, speed,
// status), the vanilla-JS <script>, and the named silence-compression cap — all
// self-contained (no external assets), while Phase 1 guarantees still hold.
func TestExportHTMLEmbedsReplayLayer(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	seedLog(t, s, sid, "main", "FIRST-LINE", "FIRST-LINE", base)
	// A big idle gap to exercise silence compression at runtime (not asserted at
	// runtime here, but ensures the data-t deltas are real).
	seedLog(t, s, sid, "main", "SECOND-LINE", "SECOND-LINE", base.Add(30*time.Second))

	html := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html", ts.URL, sid))

	for _, want := range []string{
		"<script>",                          // replay engine emitted
		`class="replay-bar"`,                // control bar markup
		`class="replay-label">Replay`,       // the control-bar label
		`class="replay-playpause"`,          // play/pause control
		`class="replay-stop"`,               // stop control
		`data-speed="0.5"`,                  // slow-motion speed option
		`class="replay-speed-btn"`,          // speed control
		`class="replay-progress"`,           // next-line wait bar
		`class="replay-clock"`,              // wall-clock time readout
		"window.__REPLAY_T0=",               // first-line epoch injected for the clock
		"SILENCE_CAP_MS",                    // named silence-compression cap
		"2000",                              // cap value
		`data-speed="2"`,                    // speed multiplier options
		"startFrom",                         // replay-from-here entry point
		"replaying",                         // the JS-added gating class
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("replay layer missing %q\n---\n%s", want, html)
		}
	}

	// Still fully self-contained: no external assets pulled in by the replay layer.
	if regexp.MustCompile(`<link\b`).MatchString(html) {
		t.Fatalf("export has <link> (external asset)")
	}
	if strings.Contains(html, "src=") {
		t.Fatalf("export has src= (external asset)")
	}
	if regexp.MustCompile(`https?://`).MatchString(html) {
		t.Fatalf("export references an http(s) URL (external asset)")
	}

	// Phase 1 contract intact: lines still carry data-source + data-t and the
	// full line text is present in the static document.
	if !regexp.MustCompile(`data-source="output" data-t="\d+"`).MatchString(html) {
		t.Fatalf("data-source/data-t missing on lines:\n%s", html)
	}
	if !strings.Contains(html, "FIRST-LINE") || !strings.Contains(html, "SECOND-LINE") {
		t.Fatalf("static log lost line text with replay layer present:\n%s", html)
	}
}

// TestExportHTMLLinesVisibleByDefault verifies lines are NOT hidden by default:
// there is no CSS rule that hides `.log-line` unconditionally — hiding is gated
// behind the JS-added `.replaying` class, so the file is a valid static log with
// JS disabled.
func TestExportHTMLLinesVisibleByDefault(t *testing.T) {
	s, sess := setupTestServer(t)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()
	sid := sess.SessionID()

	base := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	seedLog(t, s, sid, "main", "VISIBLE-LINE", "VISIBLE-LINE", base)

	html := getExportHTML(t, ts, s.apiToken, fmt.Sprintf("%s/api/sessions/%d/logs/export-html", ts.URL, sid))

	// Every rule that hides a .log-line must be gated by a JS-added class
	// (.replay-pending, the sliding hide-window, or .replaying) — never a default
	// rule. Hiding uses `visibility: hidden` (not display) so the doc keeps full
	// layout while replaying (no scroll jumps). Scan for any `... .log-line ...
	// visibility: hidden` / `display: none` and require such a gate on the
	// selector.
	hideRe := regexp.MustCompile(`([^{}]*\.log-line[^{}]*)\{[^{}]*(?:visibility:\s*hidden|display:\s*none)`)
	for _, m := range hideRe.FindAllStringSubmatch(html, -1) {
		selector := m[1]
		if !strings.Contains(selector, ".replay-pending") && !strings.Contains(selector, ".replaying") {
			t.Fatalf("found unconditional .log-line hide rule (no JS-added gate): %q", strings.TrimSpace(selector))
		}
	}

	// The gated hide rule must exist (so hiding is possible while replaying).
	if !regexp.MustCompile(`\.log-line\.replay-pending\s*\{[^}]*visibility:\s*hidden`).MatchString(html) {
		t.Fatalf("expected gated `.log-line.replay-pending { visibility:hidden }` rule:\n%s", html)
	}
}

func TestExportHTMLFilename(t *testing.T) {
	if got := exportHTMLFilename(7, "2026-07-01T00:00:00Z", "2026-07-07T23:59:59Z"); got != "session-7-2026-07-01-2026-07-07.html" {
		t.Fatalf("filename = %q", got)
	}
	if got := exportHTMLFilename(3, "", ""); got != "session-3-all-all.html" {
		t.Fatalf("open-range filename = %q", got)
	}
}
