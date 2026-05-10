package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/colorreg"
	"rubymud/go/internal/storage"
)

func formatHighlight(h storage.HighlightRule) string {
	color := formatColorSpec(h)
	return fmt.Sprintf("#highlight {%s} {%s} {%s}", color, h.Pattern, h.GroupName)
}

func formatColorSpec(h storage.HighlightRule) string {
	parts := []string{}
	if h.FG != "" {
		parts = append(parts, colorreg.NormalizeExportColor(h.FG))
	}
	if h.BG != "" {
		parts = append(parts, "b "+colorreg.NormalizeExportColor(h.BG))
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
		t := tokens[i]
		switch t {
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
				// Try multi-word BG
				if i+1 < len(tokens) {
					combined := tokens[i] + " " + tokens[i+1]
					if _, ok := colorreg.CanonicalName(combined); ok {
						h.BG = colorreg.NormalizeStoredColor(combined)
						i++
						continue
					}
				}
				h.BG = colorreg.NormalizeStoredColor(tokens[i])
			}
		default:
			if h.FG == "" {
				// Try multi-word FG
				if i+1 < len(tokens) {
					combined := t + " " + tokens[i+1]
					if _, ok := colorreg.CanonicalName(combined); ok {
						h.FG = colorreg.NormalizeStoredColor(combined)
						i++
						continue
					}
				}
				h.FG = colorreg.NormalizeStoredColor(t)
			}
		}
	}
	return h
}
