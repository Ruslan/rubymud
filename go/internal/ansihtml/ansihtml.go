// Package ansihtml converts MUD ANSI (SGR) text to HTML that matches the
// browser's ansi_up library (v6.0.6, use_classes=true) BYTE-FOR-BYTE, so the
// server-streamed colored HTML log export renders identically to the live pane
// (which uses ansi_up). The embedded theme CSS (ui/src/styles/ansi-classes.css
// + a theme file) maps the emitted classes to colors.
//
// Parity is against the ACTUAL ansi_up 6.0.6 shipped in ui/node_modules/ansi_up
// (cross-checked with a node fixture generator). Notable real-ansi_up behavior
// this mirrors (which some docs describe differently):
//   - bold/faint/italic/underline are emitted as INLINE styles
//     (font-weight:bold, opacity:0.7, font-style:italic, text-decoration:underline),
//     NOT as classes.
//   - Only the 16 standard/bright colors (SGR 30-37/40-47/90-97/100-107 and
//     38;5;n / 48;5;n with n 0-15) become classes (ansi-<name>-fg/-bg).
//   - 256-color cube/grayscale and truecolor become inline color:rgb(...) /
//     background-color:rgb(...).
//   - blink (5), reverse (7), and strikethrough (9) are IGNORED (ansi_up 6.0.6
//     does not implement them), so they never appear in the export either.
//
// Two deliberate divergences from ansi_up, for a self-contained export:
//   - OSC-8 hyperlinks (\x1b]8;;URL...text...) are STRIPPED entirely (no <a>, no
//     leaked URL, no control bytes) rather than rendered as <a href>.
//   - ToHTML resets state per call; use a Converter to carry unterminated SGR
//     across consecutive OUTPUT lines, matching the live pane's single
//     persistent ansi_up instance.
package ansihtml

import (
	"strconv"
	"strings"
)

// HighContrastTheme is the theme name that triggers the bold->bright foreground
// promotion (mirrors ui/src/ansi.ts promoteBoldAnsiForeground).
const HighContrastTheme = "high-contrast"

type colorVal struct {
	r, g, b   int
	className string // "" => truecolor (inline rgb); else e.g. "ansi-red"
}

var ansiColors = [2][8]colorVal{
	{
		{0, 0, 0, "ansi-black"},
		{187, 0, 0, "ansi-red"},
		{0, 187, 0, "ansi-green"},
		{187, 187, 0, "ansi-yellow"},
		{0, 0, 187, "ansi-blue"},
		{187, 0, 187, "ansi-magenta"},
		{0, 187, 187, "ansi-cyan"},
		{255, 255, 255, "ansi-white"},
	},
	{
		{85, 85, 85, "ansi-bright-black"},
		{255, 85, 85, "ansi-bright-red"},
		{0, 255, 0, "ansi-bright-green"},
		{255, 255, 85, "ansi-bright-yellow"},
		{85, 85, 255, "ansi-bright-blue"},
		{255, 85, 255, "ansi-bright-magenta"},
		{85, 255, 255, "ansi-bright-cyan"},
		{255, 255, 255, "ansi-bright-white"},
	},
}

// palette256 is built exactly like ansi_up.setup_palettes: 16 named colors, then
// the 6x6x6 cube (levels [0,95,135,175,215,255]), then 24 grayscale (8+10*i).
var palette256 = buildPalette256()

func buildPalette256() [256]colorVal {
	var p [256]colorVal
	i := 0
	for row := 0; row < 2; row++ {
		for col := 0; col < 8; col++ {
			p[i] = ansiColors[row][col]
			i++
		}
	}
	levels := [6]int{0, 95, 135, 175, 215, 255}
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				p[i] = colorVal{levels[r], levels[g], levels[b], ""}
				i++
			}
		}
	}
	grey := 8
	for k := 0; k < 24; k++ {
		p[i] = colorVal{grey, grey, grey, ""}
		grey += 10
		i++
	}
	return p
}

// boldPromote maps a normal foreground class (with -fg suffix) to its bright
// variant for the high-contrast theme (mirrors ui/src/ansi.ts:5-14).
var boldPromote = map[string]string{
	"ansi-black-fg":   "ansi-bright-black-fg",
	"ansi-red-fg":     "ansi-bright-red-fg",
	"ansi-green-fg":   "ansi-bright-green-fg",
	"ansi-yellow-fg":  "ansi-bright-yellow-fg",
	"ansi-blue-fg":    "ansi-bright-blue-fg",
	"ansi-magenta-fg": "ansi-bright-magenta-fg",
	"ansi-cyan-fg":    "ansi-bright-cyan-fg",
	"ansi-white-fg":   "ansi-bright-white-fg",
}

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#x27;",
)

// EscapeHTML escapes text exactly like ansi_up's escape_txt_for_html.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }

type state struct {
	bold, faint, italic, underline bool
	fg, bg                         *colorVal
}

func (st *state) reset() {
	st.bold, st.faint, st.italic, st.underline = false, false, false, false
	st.fg, st.bg = nil, nil
}

// Converter carries SGR state across successive Convert calls, mirroring the
// live pane's SINGLE persistent ansi_up instance (ui/src/render.ts): an
// unterminated color/bold on one line carries into subsequent lines. Use one
// Converter for the consecutive OUTPUT rows of an export; render commands (which
// are not server ANSI) with ToHTML so they don't perturb the carried state.
type Converter struct {
	st    state
	theme string
}

// NewConverter returns a Converter for the given ansi_theme with empty state.
func NewConverter(theme string) *Converter { return &Converter{theme: theme} }

// Convert renders input and UPDATES the carried state (so a trailing
// unterminated SGR affects later calls, like the persistent live-pane ansi_up).
func (c *Converter) Convert(input string) string {
	return convert(&c.st, input, c.theme)
}

// ToHTML converts an ANSI/SGR string into ansi_up-compatible HTML spans with a
// FRESH state (each call self-contained, like one ansi_up.ansi_to_html on a new
// instance). `theme` is the session ansi_theme; only "high-contrast" changes
// output (bold->bright foreground promotion).
func ToHTML(input, theme string) string {
	var st state
	return convert(&st, input, theme)
}

func convert(st *state, input, theme string) string {
	var b strings.Builder
	buf := input
	for len(buf) > 0 {
		esc := strings.IndexByte(buf, 0x1b)
		if esc == -1 {
			b.WriteString(renderText(st, buf, theme))
			break
		}
		if esc > 0 {
			b.WriteString(renderText(st, buf[:esc], theme))
			buf = buf[esc:]
			continue
		}
		// buf starts with ESC.
		if len(buf) < 3 {
			// Incomplete: ansi_up stops (drops the trailing partial escape).
			break
		}
		next := buf[1]
		if next == ']' {
			// OSC (e.g. OSC-8 hyperlinks): STRIP the whole sequence, emitting
			// neither the control bytes nor any URL. A self-contained export must
			// not leak external URLs; we intentionally diverge from ansi_up here
			// (which would render an <a href>). Consume ESC ] ... up to BEL (0x07)
			// or ST (ESC \). If unterminated, drop the remainder.
			buf = buf[stripOSC(buf):]
			continue
		}
		if next != '[' {
			// ESC / charset / non-CSI: ansi_up emits nothing. '(' consumes 3
			// bytes (charset selection); everything else drops 1 byte.
			if next == '(' && len(buf) >= 3 {
				buf = buf[3:]
			} else {
				buf = buf[1:]
			}
			continue
		}
		consumed, params, kind := parseCSI(buf)
		if kind == csiIncomplete {
			break
		}
		if kind == csiSGR {
			applySGR(st, params)
		}
		if consumed <= 0 {
			buf = buf[1:]
		} else {
			buf = buf[consumed:]
		}
	}
	return b.String()
}

// stripOSC returns the number of bytes to consume for an OSC sequence starting
// at buf[0:2] == "\x1b]". It consumes through the terminator (BEL 0x07 or ST
// "\x1b\\"); if none is found, it consumes the entire remaining buffer.
func stripOSC(buf string) int {
	for i := 2; i < len(buf); i++ {
		if buf[i] == 0x07 { // BEL terminator
			return i + 1
		}
		if buf[i] == 0x1b && i+1 < len(buf) && buf[i+1] == '\\' { // ST terminator
			return i + 2
		}
	}
	return len(buf)
}

type csiKind int

const (
	csiSGR csiKind = iota
	csiUnknown
	csiEsc
	csiIncomplete
)

// parseCSI mirrors ansi_up's _csi_regex: after "\x1b[", an optional private-mode
// char (0x3c-0x3f), digits/semicolons, an optional intermediate (0x20-0x2f), and
// a final byte (0x40-0x7e). SGR requires no private char and final == 'm'.
func parseCSI(buf string) (consumed int, params string, kind csiKind) {
	i := 2 // past "\x1b["
	var priv byte
	if i < len(buf) && buf[i] >= 0x3c && buf[i] <= 0x3f {
		priv = buf[i]
		i++
	}
	pstart := i
	for i < len(buf) {
		c := buf[i]
		if (c >= '0' && c <= '9') || c == ';' {
			i++
			continue
		}
		break
	}
	params = buf[pstart:i]
	if i >= len(buf) {
		return 0, "", csiIncomplete
	}
	c := buf[i]
	if c <= 0x1f || c == ':' {
		return 1, "", csiEsc // illegal byte -> ESC (drop 1)
	}
	if c >= 0x20 && c <= 0x2f {
		i++ // intermediate modifier
		if i >= len(buf) {
			return 0, "", csiIncomplete
		}
		c = buf[i]
	}
	if c >= 0x40 && c <= 0x7e {
		final := c
		i++
		if priv == 0 && final == 'm' {
			return i, params, csiSGR
		}
		return i, "", csiUnknown
	}
	return 1, "", csiEsc
}

// applySGR mirrors ansi_up.process_ansi.
func applySGR(st *state, params string) {
	cmds := strings.Split(params, ";")
	i := 0
	for i < len(cmds) {
		tok := cmds[i]
		i++
		num, err := strconv.Atoi(tok)
		if err != nil || num == 0 { // NaN or 0 -> reset
			st.reset()
			continue
		}
		switch {
		case num == 1:
			st.bold = true
		case num == 2:
			st.faint = true
		case num == 3:
			st.italic = true
		case num == 4:
			st.underline = true
		case num == 21:
			st.bold = false
		case num == 22:
			st.faint = false
			st.bold = false
		case num == 23:
			st.italic = false
		case num == 24:
			st.underline = false
		case num == 39:
			st.fg = nil
		case num == 49:
			st.bg = nil
		case num >= 30 && num < 38:
			c := ansiColors[0][num-30]
			st.fg = &c
		case num >= 40 && num < 48:
			c := ansiColors[0][num-40]
			st.bg = &c
		case num >= 90 && num < 98:
			c := ansiColors[1][num-90]
			st.fg = &c
		case num >= 100 && num < 108:
			c := ansiColors[1][num-100]
			st.bg = &c
		case num == 38 || num == 48:
			isFg := num == 38
			if i >= len(cmds) {
				continue
			}
			mode := cmds[i]
			i++
			switch mode {
			case "5":
				if i >= len(cmds) {
					continue
				}
				idx, e := strconv.Atoi(cmds[i])
				i++
				if e == nil && idx >= 0 && idx <= 255 {
					c := palette256[idx]
					if isFg {
						st.fg = &c
					} else {
						st.bg = &c
					}
				}
			case "2":
				if i+2 >= len(cmds) {
					continue
				}
				r, e1 := strconv.Atoi(cmds[i])
				g, e2 := strconv.Atoi(cmds[i+1])
				bl, e3 := strconv.Atoi(cmds[i+2])
				i += 3
				if e1 == nil && e2 == nil && e3 == nil &&
					r >= 0 && r <= 255 && g >= 0 && g <= 255 && bl >= 0 && bl <= 255 {
					c := colorVal{r, g, bl, ""}
					if isFg {
						st.fg = &c
					} else {
						st.bg = &c
					}
				}
			}
		}
	}
}

// renderText mirrors ansi_up.transform_to_html (use_classes=true).
func renderText(st *state, text, theme string) string {
	if len(text) == 0 {
		return ""
	}
	esc := EscapeHTML(text)
	if !st.bold && !st.italic && !st.faint && !st.underline && st.fg == nil && st.bg == nil {
		return esc
	}
	var styles, classes []string
	if st.bold {
		styles = append(styles, "font-weight:bold")
	}
	if st.faint {
		styles = append(styles, "opacity:0.7")
	}
	if st.italic {
		styles = append(styles, "font-style:italic")
	}
	if st.underline {
		styles = append(styles, "text-decoration:underline")
	}
	if fg := st.fg; fg != nil {
		if fg.className != "" {
			classes = append(classes, fg.className+"-fg")
		} else {
			styles = append(styles, "color:rgb("+rgb(fg)+")")
		}
	}
	if bg := st.bg; bg != nil {
		if bg.className != "" {
			classes = append(classes, bg.className+"-bg")
		} else {
			styles = append(styles, "background-color:rgb("+rgb(bg)+")")
		}
	}
	// High-contrast quirk: bold + normal fg class -> bright fg class.
	if theme == HighContrastTheme && st.bold {
		for idx, c := range classes {
			if bright, ok := boldPromote[c]; ok {
				classes[idx] = bright
				break
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("<span")
	if len(styles) > 0 {
		sb.WriteString(` style="`)
		sb.WriteString(strings.Join(styles, ";"))
		sb.WriteString(`"`)
	}
	if len(classes) > 0 {
		sb.WriteString(` class="`)
		sb.WriteString(strings.Join(classes, " "))
		sb.WriteString(`"`)
	}
	sb.WriteString(">")
	sb.WriteString(esc)
	sb.WriteString("</span>")
	return sb.String()
}

func rgb(c *colorVal) string {
	return strconv.Itoa(c.r) + "," + strconv.Itoa(c.g) + "," + strconv.Itoa(c.b)
}
