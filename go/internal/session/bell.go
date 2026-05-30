package session

import (
	"bytes"
	"fmt"

	"rubymud/go/internal/storage"
)

const bellMarker = "[BEL]"

func sanitizeLineControlsAndBEL(text string) (string, []storage.LogOverlay) {
	var out bytes.Buffer
	var overlays []storage.LogOverlay
	plainOffset := 0
	inEscape := false
	data := []byte(text)
	for _, c := range data {
		if c == '\a' {
			start := plainOffset
			out.WriteString(bellMarker)
			plainOffset += len(bellMarker)
			end := plainOffset
			overlays = append(overlays, storage.LogOverlay{
				OverlayType: "bell",
				Layer:       0,
				StartOffset: intPtr(start),
				EndOffset:   intPtr(end),
				PayloadJSON: "{}",
				SourceType:  "system",
			})
			inEscape = false
			continue
		}

		if c < 32 && c != '\t' && c != '\n' && c != '\r' && c != '\x1b' {
			escaped := fmt.Sprintf("[\\x%02x]", c)
			out.WriteString(escaped)
			plainOffset += len(escaped)
			inEscape = false
			continue
		}

		if !inEscape && c == '\x1b' {
			inEscape = true
			out.WriteByte(c)
			continue
		}
		if inEscape {
			out.WriteByte(c)
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEscape = false
			}
			continue
		}

		out.WriteByte(c)
		plainOffset++
	}
	return out.String(), overlays
}

func intPtr(v int) *int {
	return &v
}

func BellPositionsFromOverlays(overlays []storage.LogOverlay) []int {
	var positions []int
	for _, overlay := range overlays {
		if overlay.OverlayType != "bell" || overlay.StartOffset == nil {
			continue
		}
		positions = append(positions, *overlay.StartOffset)
	}
	return positions
}
