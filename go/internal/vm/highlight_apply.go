package vm

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type highlightMatch struct {
	start     int
	end       int
	ruleOrder int
	style     TextStyle
}

type highlightBoundary struct {
	offset int
	starts []int
	ends   []int
}

func (v *VM) ApplyHighlights(text string) string {
	return v.ApplyHighlightsWithBase(text, "")
}

func (v *VM) ApplyHighlightsWithBase(text, baseANSI string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ensureFresh()

	plainText := stripANSIFromVM(text)
	matches := make([]highlightMatch, 0)
	for i := range v.compiledHighlights {
		ch := &v.compiledHighlights[i]
		if !ch.rule.Enabled {
			continue
		}
		if ch.ansi == "" {
			continue
		}
		if ch.matcher.Regex == nil {
			continue
		}

		for _, loc := range ch.matcher.Regex.FindAllStringIndex(plainText, -1) {
			if loc[0] == loc[1] {
				continue
			}
			matches = append(matches, highlightMatch{
				start:     loc[0],
				end:       loc[1],
				ruleOrder: i,
				style:     ch.style,
			})
		}
	}
	if len(matches) == 0 {
		return text
	}

	// The rules are intervals over plainText. Sweep the original stream so
	// existing ANSI escapes remain in place while overlay boundaries can
	// recompose the full effective style. This avoids nesting/reset-order bugs
	// for crossing intervals and returns the original string on the no-match path.
	boundaryByOffset := make(map[int]int, len(matches)*2)
	boundaries := make([]highlightBoundary, 0, len(matches)*2)
	boundary := func(offset int) *highlightBoundary {
		if i, ok := boundaryByOffset[offset]; ok {
			return &boundaries[i]
		}
		boundaryByOffset[offset] = len(boundaries)
		boundaries = append(boundaries, highlightBoundary{offset: offset})
		return &boundaries[len(boundaries)-1]
	}
	addEvent := func(offset int, match int, start bool) {
		event := boundary(offset)
		if start {
			event.starts = append(event.starts, match)
		} else {
			event.ends = append(event.ends, match)
		}
	}
	for i, match := range matches {
		addEvent(match.start, i, true)
		addEvent(match.end, i, false)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].offset < boundaries[j].offset })
	if baseANSI == "" && !strings.Contains(text, "\x1b") && utf8.ValidString(text) {
		active := make([]bool, len(matches))
		var out strings.Builder
		out.Grow(len(text) + len(matches)*8)
		var overlay TextStyle
		applyEvents := func(event highlightBoundary) {
			for _, i := range event.ends {
				active[i] = false
			}
			for _, i := range event.starts {
				active[i] = true
			}
			next := composedHighlightStyle(matches, active)
			if next == overlay {
				return
			}
			if overlay != (TextStyle{}) {
				out.WriteString(resetANSI())
			}
			out.WriteString(highlightANSI(next))
			overlay = next
		}
		plainOffset := 0
		for _, event := range boundaries {
			out.WriteString(text[plainOffset:event.offset])
			applyEvents(event)
			plainOffset = event.offset
		}
		out.WriteString(text[plainOffset:])
		return out.String()
	}
	active := make([]bool, len(matches))
	var out strings.Builder
	out.Grow(len(text) + len(matches)*8)
	var source ansiState
	applyANSISequence(&source, baseANSI)
	var overlay TextStyle

	applyEvents := func(event highlightBoundary) {
		for _, i := range event.ends {
			active[i] = false
		}
		for _, i := range event.starts {
			active[i] = true
		}
		next := composedHighlightStyle(matches, active)
		if next == overlay {
			return
		}
		if overlay != (TextStyle{}) {
			out.WriteString(resetANSI())
			out.WriteString(source.ansi())
		}
		out.WriteString(highlightANSI(next))
		overlay = next
	}

	plainOffset := 0
	nextBoundary := 0
	for rawOffset := 0; rawOffset < len(text); {
		if text[rawOffset] == '\x1b' {
			end := ansiSequenceEnd(text, rawOffset)
			if end > rawOffset {
				sequence := text[rawOffset:end]
				out.WriteString(sequence)
				applyANSISequence(&source, sequence)
				if overlay != (TextStyle{}) {
					out.WriteString(highlightANSI(overlay))
				}
				rawOffset = end
				continue
			}
		}

		if nextBoundary < len(boundaries) && boundaries[nextBoundary].offset == plainOffset {
			applyEvents(boundaries[nextBoundary])
			nextBoundary++
		}
		r, size := utf8.DecodeRuneInString(text[rawOffset:])
		if size <= 0 {
			break
		}
		out.WriteString(text[rawOffset : rawOffset+size])
		rawOffset += size
		plainOffset += normalizedRuneSize(r, size)
	}
	for nextBoundary < len(boundaries) && boundaries[nextBoundary].offset == plainOffset {
		applyEvents(boundaries[nextBoundary])
		nextBoundary++
	}
	return out.String()
}

// stripANSIFromVM replaces malformed UTF-8 bytes with RuneError, so regex
// offsets are measured in the normalized UTF-8 representation. Keep the
// stream sweep in that same coordinate space while retaining original bytes.
func normalizedRuneSize(r rune, decodedSize int) int {
	if decodedSize == 1 && r == utf8.RuneError {
		return utf8.RuneLen(utf8.RuneError)
	}
	return decodedSize
}

func ansiSequenceEnd(text string, start int) int {
	if start < 0 || start >= len(text) || text[start] != '\x1b' {
		return start
	}
	for i := start + 1; i < len(text); i++ {
		if (text[i] >= 'a' && text[i] <= 'z') || (text[i] >= 'A' && text[i] <= 'Z') {
			return i + 1
		}
	}
	return start
}

func composedHighlightStyle(matches []highlightMatch, active []bool) TextStyle {
	var style TextStyle
	fgOrder, bgOrder := -1, -1
	boldOrder, faintOrder := -1, -1
	italicOrder, underlineOrder := -1, -1
	blinkOrder, reverseOrder, strikeOrder := -1, -1, -1
	for i, match := range matches {
		if !active[i] {
			continue
		}
		ruleOrder := match.ruleOrder
		if match.style.FG != "" && ruleOrder > fgOrder {
			style.FG, fgOrder = match.style.FG, ruleOrder
		}
		if match.style.BG != "" && ruleOrder > bgOrder {
			style.BG, bgOrder = match.style.BG, ruleOrder
		}
		if match.style.Bold && ruleOrder > boldOrder {
			style.Bold, boldOrder = true, ruleOrder
		}
		if match.style.Faint && ruleOrder > faintOrder {
			style.Faint, faintOrder = true, ruleOrder
		}
		if match.style.Italic && ruleOrder > italicOrder {
			style.Italic, italicOrder = true, ruleOrder
		}
		if match.style.Underline && ruleOrder > underlineOrder {
			style.Underline, underlineOrder = true, ruleOrder
		}
		if match.style.Blink && ruleOrder > blinkOrder {
			style.Blink, blinkOrder = true, ruleOrder
		}
		if match.style.Reverse && ruleOrder > reverseOrder {
			style.Reverse, reverseOrder = true, ruleOrder
		}
		if match.style.Strikethrough && ruleOrder > strikeOrder {
			style.Strikethrough, strikeOrder = true, ruleOrder
		}
	}
	return style
}

func highlightANSI(style TextStyle) string {
	codes := style.ANSICodes()
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func effectiveHighlightStyle(style TextStyle) TextStyle {
	if style.FG != "" && !isValidColor(style.FG) {
		style.FG = ""
	}
	if style.BG != "" && !isValidColor(style.BG) {
		style.BG = ""
	}
	return style
}

func resetANSI() string {
	return "\x1b[0m"
}

func stripANSIFromVM(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}

func plainRangeToRawRange(raw string, startPlain, endPlain int) (int, int, bool) {
	plainOffset := 0
	rawStart := -1
	rawEnd := -1
	inEscape := false

	for i := 0; i < len(raw); {
		if !inEscape && raw[i] == 0x1b {
			inEscape = true
			i++
			continue
		}
		if inEscape {
			if (raw[i] >= 'a' && raw[i] <= 'z') || (raw[i] >= 'A' && raw[i] <= 'Z') {
				inEscape = false
			}
			i++
			continue
		}
		if plainOffset == startPlain && rawStart == -1 {
			rawStart = i
		}
		_, size := utf8.DecodeRuneInString(raw[i:])
		if size <= 0 {
			return 0, 0, false
		}
		i += size
		plainOffset += size
		if plainOffset == endPlain {
			rawEnd = i
			break
		}
	}

	if startPlain == endPlain || rawStart == -1 || rawEnd == -1 {
		return 0, 0, false
	}
	return rawStart, rawEnd, true
}

type ansiState struct {
	bold          bool
	faint         bool
	italic        bool
	underline     bool
	blink         bool
	reverse       bool
	strikethrough bool
	fg            []string
	bg            []string
}

func activeANSIAt(raw string, offset int) string {
	return activeANSIAtWithBase(raw, offset, "")
}

func activeANSIAtWithBase(raw string, offset int, baseANSI string) string {
	var state ansiState
	applyANSISequence(&state, baseANSI)
	for i := 0; i < len(raw) && i < offset; {
		if raw[i] != 0x1b || i+1 >= len(raw) || raw[i+1] != '[' {
			_, size := utf8.DecodeRuneInString(raw[i:])
			if size <= 0 {
				break
			}
			i += size
			continue
		}

		end := i + 2
		for end < len(raw) && raw[end] != 'm' {
			end++
		}
		if end >= len(raw) || end >= offset {
			break
		}

		state.apply(raw[i+2 : end])
		i = end + 1
	}

	return state.ansi()
}

func applyANSISequence(state *ansiState, raw string) {
	for i := 0; i < len(raw); {
		if raw[i] != 0x1b || i+1 >= len(raw) || raw[i+1] != '[' {
			_, size := utf8.DecodeRuneInString(raw[i:])
			if size <= 0 {
				break
			}
			i += size
			continue
		}
		end := i + 2
		for end < len(raw) && raw[end] != 'm' {
			end++
		}
		if end >= len(raw) {
			break
		}
		state.apply(raw[i+2 : end])
		i = end + 1
	}
}

func (s *ansiState) apply(params string) {
	if params == "" {
		s.reset()
		return
	}

	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		code, err := strconv.Atoi(parts[i])
		if err != nil {
			continue
		}

		switch code {
		case 0:
			s.reset()
		case 1:
			s.bold = true
		case 2:
			s.faint = true
		case 3:
			s.italic = true
		case 4:
			s.underline = true
		case 5:
			s.blink = true
		case 7:
			s.reverse = true
		case 9:
			s.strikethrough = true
		case 22:
			s.bold = false
			s.faint = false
		case 23:
			s.italic = false
		case 24:
			s.underline = false
		case 25:
			s.blink = false
		case 27:
			s.reverse = false
		case 29:
			s.strikethrough = false
		case 39:
			s.fg = nil
		case 49:
			s.bg = nil
		case 38, 48:
			consumed := s.applyExtendedColor(parts, i, code)
			i += consumed
		case 30, 31, 32, 33, 34, 35, 36, 37, 90, 91, 92, 93, 94, 95, 96, 97:
			s.fg = []string{parts[i]}
		case 40, 41, 42, 43, 44, 45, 46, 47, 100, 101, 102, 103, 104, 105, 106, 107:
			s.bg = []string{parts[i]}
		}
	}
}

func (s *ansiState) applyExtendedColor(parts []string, index int, code int) int {
	if index+1 >= len(parts) {
		return 0
	}
	mode := parts[index+1]
	target := &s.fg
	if code == 48 {
		target = &s.bg
	}

	switch mode {
	case "5":
		if index+2 >= len(parts) {
			return 0
		}
		*target = []string{parts[index], parts[index+1], parts[index+2]}
		return 2
	case "2":
		if index+4 >= len(parts) {
			return 0
		}
		*target = []string{parts[index], parts[index+1], parts[index+2], parts[index+3], parts[index+4]}
		return 4
	default:
		return 0
	}
}

func (s *ansiState) reset() {
	*s = ansiState{}
}

func (s *ansiState) ansi() string {
	var codes []string
	if s.bold {
		codes = append(codes, "1")
	}
	if s.faint {
		codes = append(codes, "2")
	}
	if s.italic {
		codes = append(codes, "3")
	}
	if s.underline {
		codes = append(codes, "4")
	}
	if s.blink {
		codes = append(codes, "5")
	}
	if s.reverse {
		codes = append(codes, "7")
	}
	if s.strikethrough {
		codes = append(codes, "9")
	}
	if len(s.fg) > 0 {
		codes = append(codes, s.fg...)
	}
	if len(s.bg) > 0 {
		codes = append(codes, s.bg...)
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}
