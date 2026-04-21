package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

func formatHighlight(h storage.HighlightRule) string {
	color := formatColorSpec(h)
	return fmt.Sprintf("#highlight {%s} {%s} {%s}", color, h.Pattern, h.GroupName)
}

func formatColorSpec(h storage.HighlightRule) string {
	parts := []string{}
	if h.FG != "" {
		parts = append(parts, h.FG)
	}
	if h.BG != "" {
		parts = append(parts, "b "+h.BG)
	}
	if h.Bold {
		parts = append(parts, "bold")
	}
	if h.Faint {
		parts = append(parts, "faint")
	}
	if h.Italic {
		parts = append(parts, "italic")
	}
	if h.Underline {
		parts = append(parts, "underline")
	}
	if h.Strikethrough {
		parts = append(parts, "strikethrough")
	}
	if h.Blink {
		parts = append(parts, "blink")
	}
	if h.Reverse {
		parts = append(parts, "reverse")
	}
	return strings.Join(parts, " ")
}

func parseColorSpec(spec string) storage.HighlightRule {
	var h storage.HighlightRule
	tokens := strings.Fields(spec)
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "bold":
			h.Bold = true
		case "faint":
			h.Faint = true
		case "italic":
			h.Italic = true
		case "underline":
			h.Underline = true
		case "strikethrough":
			h.Strikethrough = true
		case "blink":
			h.Blink = true
		case "reverse":
			h.Reverse = true
		case "b":
			i++
			if i < len(tokens) {
				h.BG = tokens[i]
			}
		default:
			if h.FG == "" {
				h.FG = tokens[i]
			}
		}
	}
	return h
}
