package web

import (
	"embed"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rubymud/go/internal/ansihtml"
	"rubymud/go/internal/storage"
)

// styleFiles holds the theme/ANSI CSS mirrored from ui/src/styles (single source
// of truth). The mirror under styles/ is COMMITTED to git (kept in sync by
// `make sync-styles`, guarded by TestEmbeddedStylesMatchSource) so `go build`
// and `go test` work from a clean checkout with no prior UI build, and the
// resulting binary is self-contained.
//
//go:embed styles/ansi-classes.css styles/export-base.css styles/ansi-themes/*.css
var styleFiles embed.FS

// exportThemes are the valid ansi_theme names (must match ui/src/ansi.ts).
var exportThemes = map[string]bool{
	"classic":       true,
	"high-contrast": true,
	"tango-dark":    true,
	"dracula":       true,
	"gruvbox-dark":  true,
}

func normalizeExportTheme(theme string) string {
	if exportThemes[theme] {
		return theme
	}
	return "classic"
}

// exportLogHTML streams a self-contained, colored HTML export of a session's
// logs for a time range. Generation is fully SERVER-SIDE: the merged stream
// (server "mud" output + optional canonical commands) is read as a single
// ordered cursor (never buffering the whole range) and each line is converted to
// ansi_up-compatible HTML by the ansihtml package, so it renders identically to
// the live pane. The embedded theme CSS makes the file self-contained.
func (s *Server) exportLogHTML(w http.ResponseWriter, r *http.Request) {
	_, id, err := s.getSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	q := r.URL.Query()

	var from, to storage.SQLiteTime
	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			http.Error(w, "invalid 'from' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		from.Time = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			http.Error(w, "invalid 'to' date: must be RFC3339 format", http.StatusBadRequest)
			return
		}
		to.Time = t
	}

	theme := normalizeExportTheme(q.Get("theme"))
	title := q.Get("title")

	style, err := buildExportStyle(theme)
	if err != nil {
		http.Error(w, "export styles unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}

	opts := storage.ExportStreamOptions{
		SessionID:       id,
		Buffer:          q.Get("buffer"),
		From:            from,
		To:              to,
		IncludeCommands: q.Get("commands") != "0", // default on
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+exportHTMLFilename(id, fromStr, toStr))

	// Head + opening body. Written before streaming rows so the client sees a
	// valid document even for empty ranges.
	fmt.Fprint(w, exportHTMLHead(theme, style, title, id, opts.Buffer, fromStr, toStr))

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush() // send the head immediately (good time-to-first-byte)
	}

	// One persistent converter carries server-ANSI state across OUTPUT rows.
	conv := ansihtml.NewConverter(theme)
	var firstMS int64
	haveFirst := false
	rowCount := 0
	streamErr := s.store.StreamExportLog(opts, func(item storage.ExportStreamItem) error {
		ms := int64(0)
		if !haveFirst {
			firstMS = item.CreatedAt.Time.UnixMilli()
			haveFirst = true
		} else {
			ms = item.CreatedAt.Time.UnixMilli() - firstMS
			if ms < 0 {
				ms = 0
			}
		}
		writeExportLine(w, conv, item, ms, theme)
		rowCount++
		if flusher != nil && rowCount%256 == 0 {
			flusher.Flush() // keep memory low + progressive delivery on huge ranges
		}
		return nil
	})

	// The stream can only fail mid-document (headers already sent), so we can't
	// change the status code; log and close the document as best-effort.
	fmt.Fprint(w, "</pre>\n</div>\n")
	// Phase 2 replay script at the very end of <body> so the whole log DOM is
	// already parsed. Purely additive: the file above is a complete static log.
	// Inject the first row's absolute epoch (ms) so replay can show a wall-clock
	// time per line (data-t is relative to it). 0 when the range is empty.
	fmt.Fprintf(w, "<script>window.__REPLAY_T0=%d;</script>\n", firstMS)
	fmt.Fprint(w, replayScript)
	fmt.Fprint(w, "</body>\n</html>\n")
	if streamErr != nil {
		fmt.Fprintf(w, "<!-- export error: %s -->\n", ansihtml.EscapeHTML(streamErr.Error()))
	}
}

// buildExportStyle assembles the single embedded <style>: the theme palette
// variables, the shared ansi_up class mappings, and the base document styling.
//
// classic.css is ALWAYS embedded first as the base layer (its selector is the
// unconditional `:root, :root[data-ansi-theme="classic"]`), then the selected
// theme file overrides a subset via its `:root[data-ansi-theme="X"]` selector —
// mirroring the live app's cascade. Non-classic themes define only the vars they
// change (e.g. high-contrast defines only the 8 bright fg vars), so without the
// classic base the remaining --ansi-* vars would be undefined and colors/
// backgrounds would break.
func buildExportStyle(theme string) (string, error) {
	classicCSS, err := styleFiles.ReadFile("styles/ansi-themes/classic.css")
	if err != nil {
		return "", err
	}
	classesCSS, err := styleFiles.ReadFile("styles/ansi-classes.css")
	if err != nil {
		return "", err
	}
	baseCSS, err := styleFiles.ReadFile("styles/export-base.css")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Write(classicCSS)
	b.WriteString("\n")
	if theme != "classic" {
		themeCSS, err := styleFiles.ReadFile("styles/ansi-themes/" + theme + ".css")
		if err != nil {
			return "", err
		}
		b.Write(themeCSS)
		b.WriteString("\n")
	}
	b.Write(classesCSS)
	b.WriteString("\n")
	b.Write(baseCSS)
	return b.String(), nil
}

// Phase 2 (embedded replay) — a self-contained vanilla-JS layer emitted INTO the
// exported file. It is purely additive: with JS disabled (or the Replay toggle
// off) the document is a complete static log with EVERY line visible. Lines are
// only hidden while actively replaying, gated behind the `.replaying` class the
// script ADDS to <body> — never a default CSS rule.

// replayCSS styles the replay control bar and gates line-hiding behind the
// JS-added `.replaying` class. Kept self-contained in the generator (a dedicated
// <style>) so replay styling can't drift from the shared theme CSS.
const replayCSS = `/* Phase 2 replay: lines are visible by default; hidden ONLY while replaying. */
.replay-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  /* Pinned to the top of the viewport so controls stay reachable in a long log. */
  position: sticky;
  top: 0;
  z-index: 20;
  margin: 0 0 1rem;
  padding: 0.4rem 0.6rem;
  background: #1a1d20;
  border: 1px solid #2b2f33;
  border-radius: 4px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.45);
  font-size: 0.8rem;
  color: #c7d0da;
}
.replay-hint { color: #6f7b8a; }
.replay-bar[hidden] { display: none; }
.replay-label {
  font-weight: 600;
  color: #e6ecf3;
  user-select: none;
}
.replay-bar button {
  font: inherit;
  color: inherit;
  background: #24282c;
  border: 1px solid #333940;
  border-radius: 3px;
  padding: 0.15rem 0.5rem;
  cursor: pointer;
}
.replay-bar button[hidden] { display: none; }
.replay-speed-btn.is-active {
  background: var(--ansi-bright-blue-fg, #6cf);
  border-color: var(--ansi-bright-blue-fg, #6cf);
  color: #0b0e10;
}
.replay-progress {
  margin-left: auto;
  width: 96px;
  height: 6px;
  background: #2b3138;
  border-radius: 3px;
  overflow: hidden;
}
.replay-progress > i {
  display: block;
  height: 100%;
  width: 0%;
  background: var(--ansi-bright-blue-fg, #6cf);
}
.replay-clock {
  color: #8b98a7;
  font-variant-numeric: tabular-nums;
  min-width: 5.5em;
  text-align: right;
}
/* Hiding is gated behind the JS-added .replay-pending class — never a default
   rule, so the file is a valid static log with JS disabled. During replay only a
   SLIDING WINDOW of not-yet-due lines around the viewport carries this class;
   lines far off-screen stay in the DOM unhidden (cheap — no O(n) hide of 100k+
   lines). Any USER scroll stops replay and restores the full static log, so
   those speculatively-unhidden off-screen lines are never actually seen.
   visibility (not display) keeps full layout so the scroll never jumps. */
.log-line.replay-pending { visibility: hidden; }
/* Keep the newest revealed line ~2 lines off the very bottom edge: auto-scroll
   leaves that reserve of background below it (scroll-padding), and while
   replaying there is trailing space so even the final lines can sit above the
   bottom. (rem fallback first, then exact 2 line-heights via the lh unit.) */
html { scroll-padding-bottom: 2.6rem; scroll-padding-bottom: 2lh; }
body.replaying { padding-bottom: 2.6rem; padding-bottom: 2lh; }
`

// replayControlsHTML is the control bar. It carries the `hidden` attribute so the
// non-functional controls never appear when JS is disabled; the script un-hides
// it once it runs.
const replayControlsHTML = `<div class="replay-bar" hidden aria-label="Replay controls">
<span class="replay-label">Replay</span>
<button type="button" class="replay-playpause">Play</button>
<button type="button" class="replay-stop" hidden>Stop</button>
<span class="replay-speed" role="group" aria-label="Replay speed">
<button type="button" class="replay-speed-btn" data-speed="0.25">0.25×</button>
<button type="button" class="replay-speed-btn" data-speed="0.5">0.5×</button>
<button type="button" class="replay-speed-btn is-active" data-speed="1">1×</button>
<button type="button" class="replay-speed-btn" data-speed="2">2×</button>
<button type="button" class="replay-speed-btn" data-speed="4">4×</button>
</span>
<span class="replay-hint">Play replays from the top visible line</span>
<span class="replay-progress" aria-label="Time until next line"><i></i></span>
<span class="replay-clock"></span>
</div>
`

// replayScript is the vanilla-JS replay engine, emitted at the end of <body> so
// the whole log DOM is already parsed. No external assets, no network.
const replayScript = `<script>
(function () {
  "use strict";
  // SILENCE_CAP_MS: the maximum REAL wait between two consecutive lines. Any
  // idle/AFK gap in data-t longer than this is compressed down to the cap so
  // long silences never stall the replay (silence compression). The speed
  // multiplier divides the already-compressed delay.
  var SILENCE_CAP_MS = 2000;

  var body = document.body;
  var bar = document.querySelector(".replay-bar");
  if (!bar) return;
  var lines = Array.prototype.slice.call(document.querySelectorAll(".log-line"));
  var n = lines.length;

  var playPause = bar.querySelector(".replay-playpause");
  var stopBtn = bar.querySelector(".replay-stop");
  var clockEl = bar.querySelector(".replay-clock");
  var progressFill = bar.querySelector(".replay-progress > i");
  var speedButtons = Array.prototype.slice.call(bar.querySelectorAll(".replay-speed-btn"));

  // Absolute epoch (ms) of the first line, injected by the server. data-t on each
  // line is relative to it, so line i's wall-clock time = t0 + rawT[i].
  var t0 = window.__REPLAY_T0 || 0;

  // Reveal the controls now that JS is active. With JS disabled the bar stays
  // hidden and the document is a complete static log (all lines visible).
  bar.hidden = false;

  // Precompute a COMPRESSED, monotonic virtual timeline once: cum[i] is the
  // virtual ms at which line i should appear, where every inter-line gap is
  // clamped to [0, SILENCE_CAP_MS] so AFK silences never stall replay (silence
  // compression). Also index elements for O(1) click-to-line lookup. One pass —
  // this is what lets replay scale to 100k+ line logs without per-line timers.
  var cum = new Array(n);   // compressed virtual timeline (drives replay pacing)
  var rawT = new Array(n);  // real ms-from-first (drives the wall-clock readout)
  var indexOf = new Map();
  var prevT = 0, acc = 0;
  for (var i = 0; i < n; i++) {
    indexOf.set(lines[i], i);
    var t = parseInt(lines[i].getAttribute("data-t"), 10);
    if (!isFinite(t)) t = prevT;
    if (i > 0) {
      var gap = t - prevT;
      if (gap < 0) gap = 0;
      if (gap > SILENCE_CAP_MS) gap = SILENCE_CAP_MS;
      acc += gap;
    }
    cum[i] = acc;
    rawT[i] = t;
    prevT = t;
  }

  var revealed = 0;     // number of lines shown so far (next-to-reveal index)
  var playing = false;
  var speed = 1;
  var raf = null;
  var clockStart = 0;   // performance.now() when the current play segment began
  var virtualBase = 0;  // virtual ms already elapsed at clockStart

  // Sliding hide-window: ONLY lines in [revealed, hideEnd) carry .replay-pending
  // (hidden). AHEAD ≈ two viewport-heights of lines (a deliberately small line-
  // height estimate so we OVER-cover and never leave a visible spoiler at the
  // fold). Lines beyond the window stay unhidden — that is the cheap part — and
  // are never seen because any user scroll stops replay.
  var AHEAD = Math.max(150, Math.ceil(window.innerHeight / 14) * 2);
  var hideEnd = 0;      // exclusive upper bound of the currently-hidden window
  var following = true; // auto-scroll to follow the cursor (detached when the user scrolls up)

  // updateClock shows the wall-clock time of the current (last revealed) line —
  // more meaningful than a message counter. Blank in the static state.
  function updateClock() {
    if (!clockEl) return;
    var idx = revealed - 1;
    clockEl.textContent = (body.classList.contains("replaying") && idx >= 0 && idx < n)
      ? new Date(t0 + rawT[idx]).toLocaleTimeString()
      : "";
  }

  // updateProgress fills the little bar as we approach the next reveal, using the
  // COMPRESSED timeline (what you actually wait): near-empty = next line is a
  // while off, near-full = it is about to appear.
  function updateProgress(virtualNow) {
    if (!progressFill) return;
    var frac = 1;
    if (revealed < n) {
      var a = revealed > 0 ? cum[revealed - 1] : 0;
      var b = cum[revealed];
      frac = b > a ? (virtualNow - a) / (b - a) : 1;
      if (frac < 0) frac = 0; else if (frac > 1) frac = 1;
    }
    progressFill.style.width = (frac * 100) + "%";
  }

  function stopRAF() { if (raf !== null) { cancelAnimationFrame(raf); raf = null; } }

  // clearWindow: un-hide the current window (the only pending lines are
  // [revealed, hideEnd)).
  function clearWindow() {
    for (var i = revealed; i < hideEnd; i++) lines[i].classList.remove("replay-pending");
    hideEnd = revealed;
  }

  // Restore the full static log and stop replaying.
  function showStatic() {
    stopRAF();
    playing = false;
    clearWindow();
    body.classList.remove("replaying");
    revealed = 0;
    hideEnd = 0;
    if (playPause) playPause.textContent = "Play";
    if (stopBtn) stopBtn.hidden = true; // nothing to stop in the static state
    if (progressFill) progressFill.style.width = "0%";
    updateClock();
  }

  // frame: advance the virtual clock and reveal everything due so far. For each
  // revealed line we un-hide it and slide the window's far edge down by one — so
  // exactly ~AHEAD lines stay hidden ahead of the cursor. O(1) amortized/line.
  function frame(now) {
    raf = null;
    if (!playing) return;
    var virtualNow = virtualBase + (now - clockStart) * speed;
    var advanced = false;
    while (revealed < n && cum[revealed] <= virtualNow) {
      lines[revealed].classList.remove("replay-pending");           // reveal current
      if (hideEnd < n) { lines[hideEnd].classList.add("replay-pending"); hideEnd++; } // hide new far edge
      revealed++;
      advanced = true;
    }
    if (advanced) {
      if (following) lines[revealed - 1].scrollIntoView({ block: "nearest" }); // follow only if not scrolled away
      updateClock();
    }
    updateProgress(virtualNow);                                     // smooth fill every frame
    if (revealed >= n) { pause(); return; }
    raf = requestAnimationFrame(frame);
  }

  function play() {
    if (revealed >= n) return;
    playing = true;
    if (playPause) playPause.textContent = "Pause";
    clockStart = performance.now();
    virtualBase = revealed > 0 ? cum[revealed - 1] : 0; // resume from last revealed line
    stopRAF();
    raf = requestAnimationFrame(frame);
  }

  function pause() {
    playing = false;
    stopRAF();
    if (playPause) playPause.textContent = "Play";
  }

  // topVisibleIndex: the index of the topmost log line under the (sticky) bar —
  // O(1) via elementFromPoint, so it scales to 100k-line logs. Falls back to 0.
  function topVisibleIndex() {
    var y = bar.getBoundingClientRect().bottom + 2;
    var x = Math.max(2, Math.floor(window.innerWidth / 2));
    var el = document.elementFromPoint(x, y);
    el = el && el.closest ? el.closest(".log-line") : null;
    var idx = el ? indexOf.get(el) : 0;
    return idx === undefined ? 0 : idx;
  }

  // playFromTop: (re)start replay anchored at the topmost visible line — the line
  // you're looking at becomes the current moment and replay runs forward from it.
  function playFromTop() {
    startFrom(topVisibleIndex());
  }

  // startFrom: begin replaying at fromIndex. Lines 0..fromIndex are the shown
  // "past" (left untouched — already visible), and only the sliding window of
  // future lines around the viewport is hidden. fromIndex=-1 starts from the top.
  // This is the core of "replay from here".
  function startFrom(fromIndex) {
    stopRAF();
    clearWindow();                       // drop any previous window
    body.classList.add("replaying");
    if (stopBtn) stopBtn.hidden = false; // replay is running — Stop is now available
    revealed = fromIndex + 1;
    if (revealed < 0) revealed = 0;
    hideEnd = Math.min(n, revealed + AHEAD);
    for (var i = revealed; i < hideEnd; i++) lines[i].classList.add("replay-pending");
    following = true; // a fresh anchor follows the cursor again
    if (revealed > 0) lines[revealed - 1].scrollIntoView({ block: "nearest" });
    updateClock();
    play();
  }

  // Play/Pause is the primary control (media-player style): from the static log
  // Play starts replay anchored at the top visible line; while playing it
  // pauses; while paused it resumes. Stop exits replay and restores the full
  // static log.
  if (playPause) {
    playPause.addEventListener("click", function () {
      if (playing) { pause(); return; }
      if (body.classList.contains("replaying")) play();  // resume from pause
      else playFromTop();                                // fresh start from the top visible line
    });
  }

  if (stopBtn) {
    stopBtn.addEventListener("click", function () { showStatic(); });
  }

  speedButtons.forEach(function (btn) {
    btn.addEventListener("click", function () {
      var s = parseFloat(btn.getAttribute("data-speed"));
      if (!isFinite(s) || s <= 0) return;
      if (playing) {
        // Rebase the virtual clock so the speed change is seamless (no jump).
        var now = performance.now();
        virtualBase = virtualBase + (now - clockStart) * speed;
        clockStart = now;
      }
      speed = s;
      speedButtons.forEach(function (b) { b.classList.toggle("is-active", b === btn); });
    });
  });

  // Scroll handling while replaying — driven by USER input intents (wheel /
  // touch / scroll keys), NOT the scroll event, so our own programmatic
  // scrollIntoView is never mistaken for the user (keying off the scroll event
  // is what broke auto-follow).
  //  - Scrolling UP just detaches auto-follow (keep playing, don't yank back).
  //  - Scrolling DOWN past the pre-hidden window — an unhidden FUTURE line
  //    (index >= hideEnd) shows at the bottom — stops replay and restores the
  //    full static log. Scrolling back down to the cursor re-attaches follow.
  function lineIndexAt(y) {
    var x = Math.max(2, Math.floor(window.innerWidth / 2));
    var el = document.elementFromPoint(x, y);
    el = el && el.closest ? el.closest(".log-line") : null;
    var idx = el ? indexOf.get(el) : undefined;
    return idx === undefined ? -1 : idx;
  }
  function cursorInView() {
    if (revealed <= 0) return true;
    var r = lines[revealed - 1].getBoundingClientRect();
    return r.top < window.innerHeight && r.bottom > bar.getBoundingClientRect().bottom;
  }
  function onUserScroll(dir) {
    if (!dir || !body.classList.contains("replaying")) return;
    if (dir < 0) { following = false; return; } // up: detach follow, keep playing
    // down: after the scroll applies, stop if a spoiler is now visible.
    requestAnimationFrame(function () {
      if (!body.classList.contains("replaying")) return;
      if (hideEnd < n && lineIndexAt(window.innerHeight - 2) >= hideEnd) { showStatic(); return; }
      following = cursorInView(); // re-attach if we scrolled back to the cursor
    });
  }
  window.addEventListener("wheel", function (ev) { onUserScroll(ev.deltaY); }, { passive: true });
  var touchY = 0;
  window.addEventListener("touchstart", function (ev) { touchY = ev.touches[0].clientY; }, { passive: true });
  window.addEventListener("touchmove", function (ev) {
    var y = ev.touches[0].clientY;
    onUserScroll(touchY - y); // finger up (y decreases) = scrolling down
    touchY = y;
  }, { passive: true });
  var upKeys = { ArrowUp: 1, PageUp: 1, Home: 1 };
  var downKeys = { ArrowDown: 1, PageDown: 1, End: 1, " ": 1, Spacebar: 1 };
  window.addEventListener("keydown", function (ev) {
    if (bar.contains(ev.target)) return;
    if (upKeys[ev.key]) onUserScroll(-1);
    else if (downKeys[ev.key]) onUserScroll(1);
  });

  updateClock();
})();
</script>
`

func exportHTMLHead(theme, style, title string, sessionID int64, buffer, from, to string) string {
	docTitle := title
	if docTitle == "" {
		docTitle = "MUD log export"
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="en" data-ansi-theme="` + ansihtml.EscapeHTML(theme) + `">` + "\n")
	b.WriteString("<head>\n")
	b.WriteString(`<meta charset="utf-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString("<title>" + ansihtml.EscapeHTML(docTitle) + "</title>\n")
	b.WriteString("<style>\n" + style + "\n</style>\n")
	// Phase 2 (embedded replay) styling in a dedicated <style>, kept self-contained
	// in the generator so it can't drift from the shared theme CSS. The replay
	// <script> itself mounts at the end of <body> (see exportLogHTML) so the log
	// DOM is parsed first. The file is a correct static log without either.
	b.WriteString("<style>\n" + replayCSS + "</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString(`<div class="log-export">` + "\n")
	if title != "" {
		b.WriteString(`<h1 class="log-export__title">` + ansihtml.EscapeHTML(title) + "</h1>\n")
	}
	b.WriteString(`<p class="log-export__meta">` + ansihtml.EscapeHTML(exportMeta(sessionID, buffer, from, to)) + "</p>\n")
	// Replay control bar (hidden until the script un-hides it). Emitted near the
	// top of the log so it stays in view while replaying.
	b.WriteString(replayControlsHTML)
	// No newline before/after the <pre> content: newlines inside <pre> render as
	// blank lines, and the per-line <div>s already break lines.
	b.WriteString(`<pre class="log-export__body">`)
	return b.String()
}

func exportMeta(sessionID int64, buffer, from, to string) string {
	meta := fmt.Sprintf("Session %d", sessionID)
	if from != "" || to != "" {
		f := from
		if f == "" {
			f = "…"
		}
		t := to
		if t == "" {
			t = "…"
		}
		meta += " · " + f + " — " + t
	}
	if buffer != "" {
		meta += " · buffer: " + buffer
	}
	return meta
}

// writeExportLine writes one <div class="log-line"> row. OUTPUT rows are
// rendered through the persistent Converter so an unterminated color/bold on one
// server line carries to the next (matching the live pane); COMMAND rows are not
// server ANSI, so they use a fresh ToHTML render (get the log-input class + "> "
// prefix) and do NOT perturb the carried output state.
func writeExportLine(w http.ResponseWriter, conv *ansihtml.Converter, item storage.ExportStreamItem, ms int64, theme string) {
	class := "log-line"
	var content string
	if item.Kind == "command" {
		class = "log-line log-input"
		content = "&gt; " + ansihtml.ToHTML(item.Ansi, theme)
	} else {
		content = conv.Convert(item.Ansi)
	}
	fmt.Fprintf(w, `<div class="%s" data-source="%s" data-t="%d">%s</div>`, class, item.Kind, ms, content)
}

// exportHTMLFilename builds session-<id>-<from>-<to>.html using the date parts of
// the from/to instants ("all" when open).
func exportHTMLFilename(sessionID int64, from, to string) string {
	return fmt.Sprintf("session-%d-%s-%s.html", sessionID, dateLabel(from), dateLabel(to))
}

func dateLabel(iso string) string {
	if len(iso) >= 10 {
		iso = iso[:10]
	}
	var b strings.Builder
	for _, r := range iso {
		if (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "all"
	}
	return b.String()
}
