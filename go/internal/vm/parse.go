package vm

import "strings"

func splitFirstWord(input string) (string, []string) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

func splitSemicolons(input string) []string {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitBraceArg(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}

	delim := s[0]
	closer := byte('}')
	switch delim {
	case '{':
		closer = '}'
	case '\'':
		closer = '\''
	case '"':
		closer = '"'
	default:
		parts := strings.SplitN(s, " ", 2)
		if len(parts) == 1 {
			return parts[0], ""
		}
		return parts[0], parts[1]
	}

	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == byte(delim) && i == 0 {
			depth = 1
			continue
		}
		if s[i] == delim && delim != closer {
			depth++
		}
		if s[i] == closer {
			depth--
			if depth == 0 {
				return s[1:i], strings.TrimSpace(s[i+1:])
			}
		}
	}

	return strings.Trim(s, string(delim)), ""
}

func numRepeat(cmd string) int {
	for i, c := range cmd {
		if c < '0' || c > '9' {
			if i == 0 {
				return 0
			}
			n := 0
			for _, d := range cmd[:i] {
				n = n*10 + int(d-'0')
			}
			if n >= 1 && n <= 100 {
				return n
			}
			return 0
		}
	}
	return 0
}

func parseRepeat(cmd string) (int, string) {
	for i, c := range cmd {
		if c < '0' || c > '9' {
			n := 0
			for _, d := range cmd[:i] {
				n = n*10 + int(d-'0')
			}
			if c == ' ' || c == '{' {
				return n, strings.TrimSpace(cmd[i:])
			}
			return 0, cmd
		}
	}
	return 0, cmd
}
