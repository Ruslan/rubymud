package vm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"rubymud/go/internal/colorreg"
	"rubymud/go/internal/storage"
)

type TextStyle struct {
	FG            string
	BG            string
	Bold          bool
	Faint         bool
	Italic        bool
	Underline     bool
	Blink         bool
	Reverse       bool
	Strikethrough bool
}

func (s TextStyle) ToANSI() string {
	codes := s.ANSICodes()
	if len(codes) == 0 {
		return "\x1b[0m"
	}
	return fmt.Sprintf("\x1b[0;%sm", strings.Join(codes, ";"))
}

func (s TextStyle) ANSICodes() []string {
	codes := []string{}

	if s.Bold {
		codes = append(codes, "1")
	}
	if s.Faint {
		codes = append(codes, "2")
	}
	if s.Italic {
		codes = append(codes, "3")
	}
	if s.Underline {
		codes = append(codes, "4")
	}
	if s.Blink {
		codes = append(codes, "5")
	}
	if s.Reverse {
		codes = append(codes, "7")
	}
	if s.Strikethrough {
		codes = append(codes, "9")
	}

	if s.FG != "" {
		if code, ok := colorreg.NameToAnsiFg(s.FG); ok {
			codes = append(codes, code)
		} else if hexCode, ok := HexColorANSI(s.FG, "38"); ok {
			codes = append(codes, hexCode)
		} else if strings.HasPrefix(strings.ToLower(s.FG), "rgb") {
			parts := strings.Split(strings.TrimPrefix(strings.ToLower(s.FG), "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("38;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(strings.ToLower(s.FG), "256:") {
			codes = append(codes, fmt.Sprintf("38;5;%s", s.FG[4:]))
		}
	}

	if s.BG != "" {
		if code, ok := colorreg.NameToAnsiBg(s.BG); ok {
			codes = append(codes, code)
		} else if hexCode, ok := HexColorANSI(s.BG, "48"); ok {
			codes = append(codes, hexCode)
		} else if strings.HasPrefix(strings.ToLower(s.BG), "rgb") {
			parts := strings.Split(strings.TrimPrefix(strings.ToLower(s.BG), "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("48;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(strings.ToLower(s.BG), "256:") {
			codes = append(codes, fmt.Sprintf("48;5;%s", s.BG[4:]))
		}
	}

	return codes
}

func HexColorANSI(value string, prefix string) (string, bool) {
	if !strings.HasPrefix(value, "#") {
		return "", false
	}
	hex := strings.TrimPrefix(value, "#")
	if len(hex) == 3 {
		hex = strings.Repeat(string(hex[0]), 2) + strings.Repeat(string(hex[1]), 2) + strings.Repeat(string(hex[2]), 2)
	}
	if len(hex) != 6 {
		return "", false
	}

	var r, g, b uint64
	_, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	if err != nil {
		return "", false
	}

	return fmt.Sprintf("%s;2;%d;%d;%d", prefix, r, g, b), true
}

type stackEntry struct {
	style    TextStyle
	closeKey string
}

var tagRegex = regexp.MustCompile(`(?i)<(/?[a-z0-9#-][a-z0-9#\s,:-]*)>`)

func renderLocalMarkup(input string) string {
	if !strings.Contains(input, "<") {
		return input
	}

	stack := []stackEntry{{style: TextStyle{}, closeKey: ""}}
	current := func() TextStyle { return stack[len(stack)-1].style }

	var result strings.Builder
	lastPos := 0

	matches := tagRegex.FindAllStringSubmatchIndex(input, -1)
	for _, m := range matches {
		// Append text before tag
		result.WriteString(input[lastPos:m[0]])

		tagFull := input[m[2]:m[3]]
		lastPos = m[1]

		if strings.HasPrefix(tagFull, "/") {
			// Closing tag
			tagName := strings.ToLower(tagFull[1:])
			if len(stack) > 1 && stack[len(stack)-1].closeKey == tagName {
				stack = stack[:len(stack)-1]
				result.WriteString(current().ToANSI())
			} else {
				// Broken or unknown closing tag, or mismatch - keep as text
				result.WriteString(input[m[0]:m[1]])
			}
			continue
		}

		// Opening tag
		newStyle, closeKey, ok := parseTag(current(), tagFull)
		if ok {
			if tagFull == "reset" {
				stack = []stackEntry{{style: TextStyle{}, closeKey: ""}}
			} else {
				stack = append(stack, stackEntry{style: newStyle, closeKey: closeKey})
			}
			result.WriteString(current().ToANSI())
		} else {
			// Unknown tag, keep as text
			result.WriteString(input[m[0]:m[1]])
		}
	}

	result.WriteString(input[lastPos:])

	// If we ended with some styles, we should probably reset to be safe,
	// but the requirement says "Unbalanced opening tags at end of string must not panic or break output".
	// ANSI 0m is usually expected at the end if we started it.
	if len(stack) > 1 {
		result.WriteString("\x1b[0m")
	}

	return result.String()
}

func isValidColor(color string) bool {
	if _, ok := colorreg.NameToAnsiFg(color); ok {
		return true
	}
	if _, ok := HexColorANSI(color, "38"); ok {
		return true
	}
	colorLower := strings.ToLower(color)
	if strings.HasPrefix(colorLower, "rgb") {
		parts := strings.Split(strings.TrimPrefix(colorLower, "rgb"), ",")
		if len(parts) != 3 {
			return false
		}
		for _, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil || v < 0 || v > 255 {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(colorLower, "256:") {
		v, err := strconv.Atoi(colorLower[4:])
		return err == nil && v >= 0 && v <= 255
	}
	return false
}

func parseTag(base TextStyle, tag string) (TextStyle, string, bool) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	style := base

	// Simple attribute tags
	switch tag {
	case "b", "bold":
		style.Bold = true
		return style, tag, true
	case "faint":
		style.Faint = true
		return style, tag, true
	case "i", "italic":
		style.Italic = true
		return style, tag, true
	case "u", "underline":
		style.Underline = true
		return style, tag, true
	case "s", "strike", "strikethrough":
		style.Strikethrough = true
		return style, tag, true
	case "blink":
		style.Blink = true
		return style, tag, true
	case "reverse":
		style.Reverse = true
		return style, tag, true
	case "reset":
		return TextStyle{}, "reset", true
	}

	// Explicit fg/bg tags
	if strings.HasPrefix(tag, "fg ") {
		color := strings.TrimSpace(tag[3:])
		if isValidColor(color) {
			style.FG = color
			return style, "fg", true
		}
		return base, "", false
	}
	if strings.HasPrefix(tag, "bg ") {
		color := strings.TrimSpace(tag[4:]) // "bg " is 3 chars, but wait "bg " is index 0,1,2. tag[3:] is the color.
		// Wait, strings.HasPrefix(tag, "bg ") -> "bg " is 3 chars. tag[3:] is the color.
		color = strings.TrimSpace(tag[3:])
		if isValidColor(color) {
			style.BG = color
			return style, "bg", true
		}
		return base, "", false
	}

	// Named colors (allow hyphenated names like light-red)
	colorName := strings.ReplaceAll(tag, "-", " ")
	if _, ok := colorreg.CanonicalName(colorName); ok {
		style.FG = colorName
		return style, tag, true
	}
	if strings.HasPrefix(colorName, "bg ") {
		color := colorName[3:]
		if isValidColor(color) {
			style.BG = color
			return style, tag, true
		}
	}

	return base, "", false
}

func highlightRuleToTextStyle(h *storage.HighlightRule) TextStyle {
	return TextStyle{
		FG:            h.FG,
		BG:            h.BG,
		Bold:          h.Bold,
		Faint:         h.Faint,
		Italic:        h.Italic,
		Underline:     h.Underline,
		Blink:         h.Blink,
		Reverse:       h.Reverse,
		Strikethrough: h.Strikethrough,
	}
}
