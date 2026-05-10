package colorreg

import (
	"fmt"
	"strings"
)

type NamedColor struct {
	Name    string `json:"name"`    // "red", "light cyan", ...
	Hex     string `json:"hex"`     // "#aa0000"
	AnsiFg  int    `json:"ansi_fg"` // SGR код 31
	AnsiBg  int    `json:"ansi_bg"` // SGR код 41
	Ansi256 int    `json:"ansi256"` // ближайший/предпочтительный 256-индекс
}

var canonicalColors = []NamedColor{
	{"black", "#000000", 30, 40, 16},
	{"red", "#aa0000", 31, 41, 124},
	{"green", "#00aa00", 32, 42, 28},
	{"brown", "#aaaa00", 33, 43, 142},
	{"blue", "#0000aa", 34, 44, 20},
	{"magenta", "#aa00aa", 35, 45, 127},
	{"cyan", "#00aaaa", 36, 46, 37},
	{"white", "#aaaaaa", 37, 47, 248},
	{"light red", "#ff5555", 91, 101, 203},
	{"light green", "#55ff55", 92, 102, 83},
	{"light yellow", "#ffff55", 93, 103, 227},
	{"light blue", "#5555ff", 94, 104, 63},
	{"light magenta", "#ff55ff", 95, 105, 207},
	{"light cyan", "#55ffff", 96, 106, 87},
	{"light white", "#ffffff", 97, 107, 231},
}

var aliases = map[string]string{
	"coal":        "black",
	"charcoal":    "black",
	"yellow":      "brown",
	"gray":        "white",
	"grey":        "white",
	"purple":      "magenta",
	"light brown": "brown",
}

// All returns canonical colors for API/UI.
func All() []NamedColor {
	return canonicalColors
}

// CanonicalName returns canonical name for input (trim, lower, alias resolution).
func CanonicalName(input string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return "", false
	}
	if target, ok := aliases[s]; ok {
		return target, true
	}
	for _, c := range canonicalColors {
		if c.Name == s {
			return s, true
		}
	}
	return s, false
}

// LookupName returns color info by canonical or alias name.
func LookupName(input string) (NamedColor, bool) {
	name, _ := CanonicalName(input)
	for _, c := range canonicalColors {
		if c.Name == name {
			return c, true
		}
	}
	return NamedColor{}, false
}

func NameToHex(input string) (string, bool) {
	if c, ok := LookupName(input); ok {
		return c.Hex, true
	}
	return "", false
}

func NameToAnsiFg(input string) (string, bool) {
	if c, ok := LookupName(input); ok {
		return fmt.Sprintf("%d", c.AnsiFg), true
	}
	return "", false
}

func NameToAnsiBg(input string) (string, bool) {
	if c, ok := LookupName(input); ok {
		return fmt.Sprintf("%d", c.AnsiBg), true
	}
	return "", false
}

// NameFor256 returns readable name for export if it's a confident match.
func NameFor256(index int) (string, bool) {
	// Exact match in canonical
	for _, c := range canonicalColors {
		if c.Ansi256 == index {
			return c.Name, true
		}
	}
	// Popular UI color picker matches
	switch index {
	case 196:
		return "red", true
	case 46:
		return "green", true
	case 21:
		return "blue", true
	case 201:
		return "magenta", true
	case 51:
		return "cyan", true
	case 226:
		return "brown", true
	}
	return "", false
}

// NameForHex returns readable name for export if it's a confident match.
func NameForHex(hex string) (string, bool) {
	hex = strings.ToLower(strings.TrimSpace(hex))
	for _, c := range canonicalColors {
		if c.Hex == hex {
			return c.Name, true
		}
	}
	switch hex {
	case "#ff0000":
		return "red", true
	case "#00ff00":
		return "green", true
	case "#0000ff":
		return "blue", true
	}
	return "", false
}

// NormalizeExportColor converts "256:196" -> "red", "#ff0000" -> "red", etc.
func NormalizeExportColor(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" || v == "default" {
		return ""
	}

	// Try if it's already a name/alias
	if name, ok := CanonicalName(v); ok {
		return name
	}

	// Try 256:N
	if strings.HasPrefix(v, "256:") {
		var idx int
		if _, err := fmt.Sscanf(v, "256:%d", &idx); err == nil {
			if name, ok := NameFor256(idx); ok {
				return name
			}
		}
	}

	// Try hex
	if strings.HasPrefix(v, "#") {
		if name, ok := NameForHex(v); ok {
			return name
		}
	}

	return v
}

// NormalizeStoredColor trims, lowercases, handles "default" -> "", aliases -> canonical.
func NormalizeStoredColor(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	if s == "" || s == "default" {
		return ""
	}
	if target, ok := aliases[s]; ok {
		return target
	}
	// Check if it's a known canonical name
	for _, c := range canonicalColors {
		if c.Name == s {
			return s
		}
	}
	// Otherwise return as is (could be 256:N, #hex, unknown name)
	return s
}
