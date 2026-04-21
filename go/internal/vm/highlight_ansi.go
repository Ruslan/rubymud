package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

func highlightToANSI(h *storage.HighlightRule) string {
	codes := []string{}
	fgMap := map[string]string{
		"black": "30", "red": "31", "green": "32", "brown": "33", "yellow": "33",
		"blue": "34", "magenta": "35", "cyan": "36", "white": "37", "gray": "37",
		"coal": "30", "light red": "91", "light green": "92", "light yellow": "93",
		"light blue": "94", "light magenta": "95", "light cyan": "96", "light white": "97",
		"grey": "37", "charcoal": "30", "light brown": "33", "purple": "35",
	}
	bgMap := map[string]string{
		"black": "40", "red": "41", "green": "42", "brown": "43", "yellow": "43",
		"blue": "44", "magenta": "45", "cyan": "46", "white": "47", "gray": "47",
		"coal": "40", "light red": "101", "light green": "102", "light yellow": "103",
		"light blue": "104", "light magenta": "105", "light cyan": "106", "light white": "107",
		"grey": "47", "charcoal": "40", "light brown": "43", "purple": "45",
	}

	if h.Bold {
		codes = append(codes, "1")
	}
	if h.Faint {
		codes = append(codes, "2")
	}
	if h.Italic {
		codes = append(codes, "3")
	}
	if h.Underline {
		codes = append(codes, "4")
	}
	if h.Blink {
		codes = append(codes, "5")
	}
	if h.Reverse {
		codes = append(codes, "7")
	}
	if h.Strikethrough {
		codes = append(codes, "9")
	}
	if h.FG != "" {
		if code, ok := fgMap[h.FG]; ok {
			codes = append(codes, code)
		} else if strings.HasPrefix(h.FG, "rgb") {
			parts := strings.Split(strings.TrimPrefix(h.FG, "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("38;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(h.FG, "256:") {
			codes = append(codes, fmt.Sprintf("38;5;%s", strings.TrimPrefix(h.FG, "256:")))
		}
	}
	if h.BG != "" {
		if code, ok := bgMap[h.BG]; ok {
			codes = append(codes, code)
		} else if strings.HasPrefix(h.BG, "rgb") {
			parts := strings.Split(strings.TrimPrefix(h.BG, "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("48;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(h.BG, "256:") {
			codes = append(codes, fmt.Sprintf("48;5;%s", strings.TrimPrefix(h.BG, "256:")))
		}
	}

	if len(codes) == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}
