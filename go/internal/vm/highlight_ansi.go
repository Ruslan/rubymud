package vm

import (
	"fmt"
	"strconv"
	"strings"

	"rubymud/go/internal/colorreg"
	"rubymud/go/internal/storage"
)

func highlightToANSI(h *storage.HighlightRule) string {
	codes := []string{}
	fgValue := strings.TrimSpace(h.FG)
	bgValue := strings.TrimSpace(h.BG)

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
	if fgValue != "" {
		if code, ok := colorreg.NameToAnsiFg(fgValue); ok {
			codes = append(codes, code)
		} else if hexCode, ok := hexColorANSI(fgValue, "38"); ok {
			codes = append(codes, hexCode)
		} else if strings.HasPrefix(strings.ToLower(fgValue), "rgb") {
			parts := strings.Split(strings.TrimPrefix(strings.ToLower(fgValue), "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("38;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(strings.ToLower(fgValue), "256:") {
			codes = append(codes, fmt.Sprintf("38;5;%s", fgValue[4:]))
		}
	}
	if bgValue != "" {
		if code, ok := colorreg.NameToAnsiBg(bgValue); ok {
			codes = append(codes, code)
		} else if hexCode, ok := hexColorANSI(bgValue, "48"); ok {
			codes = append(codes, hexCode)
		} else if strings.HasPrefix(strings.ToLower(bgValue), "rgb") {
			parts := strings.Split(strings.TrimPrefix(strings.ToLower(bgValue), "rgb"), ",")
			if len(parts) == 3 {
				codes = append(codes, fmt.Sprintf("48;2;%s", strings.Join(parts, ";")))
			}
		} else if strings.HasPrefix(strings.ToLower(bgValue), "256:") {
			codes = append(codes, fmt.Sprintf("48;5;%s", bgValue[4:]))
		}
	}

	if len(codes) == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}

func hexColorANSI(value string, prefix string) (string, bool) {
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

	r, err := strconv.ParseUint(hex[0:2], 16, 8)
	if err != nil {
		return "", false
	}
	g, err := strconv.ParseUint(hex[2:4], 16, 8)
	if err != nil {
		return "", false
	}
	b, err := strconv.ParseUint(hex[4:6], 16, 8)
	if err != nil {
		return "", false
	}

	return fmt.Sprintf("%s;2;%d;%d;%d", prefix, r, g, b), true
}
