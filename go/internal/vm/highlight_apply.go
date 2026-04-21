package vm

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

func (v *VM) ApplyHighlights(text string) string {
	v.ensureFresh()

	plainText := stripANSIFromVM(text)
	for i := range v.highlights {
		h := &v.highlights[i]
		if !h.Enabled {
			continue
		}
		re, err := regexp.Compile(h.Pattern)
		if err != nil {
			continue
		}
		loc := re.FindStringIndex(plainText)
		if loc == nil {
			continue
		}
		rawStart, rawEnd, ok := plainRangeToRawRange(text, loc[0], loc[1])
		if !ok || rawStart < 0 || rawEnd > len(text) || rawStart >= rawEnd {
			continue
		}
		matched := text[rawStart:rawEnd]
		ansi := highlightToANSI(h)
		text = text[:rawStart] + ansi + matched + resetANSI() + text[rawEnd:]
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
