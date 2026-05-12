package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

func highlightToANSI(h *storage.HighlightRule) string {
	ts := highlightRuleToTextStyle(h)
	codes := ts.ANSICodes()
	if len(codes) == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}
