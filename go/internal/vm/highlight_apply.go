package vm

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func (v *VM) ApplyHighlights(text string) string {
	v.ensureFresh()

	plainText := stripANSIFromVM(text)
	for i := range v.highlights {
		rule := &v.highlights[i]
		if !rule.Enabled {
			continue
		}
		if i >= len(v.compiledHighlights) {
			continue
		}
		ansi := v.compiledHighlights[i].ansi
		if ansi == "" {
			continue
		}

		effectivePattern := v.substitutePatternVars(rule.Pattern)
		re := v.compileEffectivePattern(rule.Pattern, effectivePattern)
		if re == nil {
			continue
		}

		allLocs := re.FindAllStringIndex(plainText, -1)
		if len(allLocs) == 0 {
			continue
		}

		// Apply highlights backwards so that indices in allLocs remain valid
		// even as we inject ANSI codes that change the string length.
		for j := len(allLocs) - 1; j >= 0; j-- {
			loc := allLocs[j]
			if loc[0] == loc[1] {
				continue
			}
			rawStart, rawEnd, ok := plainRangeToRawRange(text, loc[0], loc[1])
			if !ok || rawStart < 0 || rawEnd > len(text) || rawStart >= rawEnd {
				continue
			}
			matched := text[rawStart:rawEnd]
			restore := activeANSIAt(text, rawEnd)
			text = text[:rawStart] + ansi + matched + resetANSI() + restore + text[rawEnd:]
		}
	}
	return text
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
	var state ansiState
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
