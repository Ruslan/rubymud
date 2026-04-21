package vm

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

func (v *VM) ExpandInput(input string) []string {
	return v.expand(input, 0)
}

func (v *VM) expand(input string, depth int) []string {
	if depth >= maxExpandDepth {
		return []string{input}
	}

	segments := splitSemicolons(input)
	var result []string
	for _, segment := range segments {
		segment = v.substituteVars(segment)
		result = append(result, v.expandAlias(segment, depth)...)
	}
	return result
}

func (v *VM) substituteVars(s string) string {
	return v.varPattern.ReplaceAllStringFunc(s, func(match string) string {
		key := match[1:]
		if val, ok := v.variables[key]; ok {
			return val
		}
		if val, ok := builtinVar(key); ok {
			return val
		}
		return match
	})
}

func builtinVar(key string) (string, bool) {
	now := time.Now()
	switch key {
	case "DATE":
		return now.Format("02-01-2006"), true
	case "TIME":
		return now.Format("15:04:05"), true
	case "HOUR":
		return fmt.Sprintf("%02d", now.Hour()), true
	case "MINUTE":
		return fmt.Sprintf("%02d", now.Minute()), true
	case "SECOND":
		return fmt.Sprintf("%02d", now.Second()), true
	case "TIMESTAMP":
		return fmt.Sprintf("%d", now.Unix()), true
	default:
		return "", false
	}
}

func (v *VM) expandAlias(input string, depth int) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}

	cmd, args := splitFirstWord(trimmed)
	for i := range v.aliases {
		if v.aliases[i].Name == cmd {
			expanded := substituteTemplate(v.aliases[i].Template, args)
			return v.expand(expanded, depth+1)
		}
	}

	return []string{input}
}

func substituteTemplate(template string, args []string) string {
	allArgs := strings.Join(args, " ")
	r := regexp.MustCompile(`%(\d+)`)
	return r.ReplaceAllStringFunc(template, func(match string) string {
		idx := 0
		for _, c := range match[1:] {
			idx = idx*10 + int(c-'0')
		}
		if idx == 0 {
			return allArgs
		}
		if idx >= 1 && idx <= len(args) {
			return args[idx-1]
		}
		return match
	})
}

func expandSpeedwalk(input string) ([]string, bool) {
	dirSet := map[rune]bool{'n': true, 's': true, 'e': true, 'w': true, 'u': true, 'd': true}

	allDirs := true
	for _, c := range input {
		if !dirSet[c] && (c < '0' || c > '9') {
			allDirs = false
			break
		}
	}
	if !allDirs || len(input) == 0 {
		return nil, false
	}

	var commands []string
	count := 0
	for _, c := range input {
		if c >= '0' && c <= '9' {
			count = count*10 + int(c-'0')
			continue
		}
		n := count
		if n == 0 {
			n = 1
		}
		if n > 100 {
			n = 100
		}
		for i := 0; i < n; i++ {
			commands = append(commands, string(c))
		}
		count = 0
	}

	if len(commands) == 0 {
		return nil, false
	}
	return commands, true
}
