package vm

import (
	"fmt"
	"strings"
)

func (v *VM) cmdAction(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, t := range v.triggers {
			s := fmt.Sprintf("#action {%s} {%s}", t.Pattern, t.Command)
			if t.IsButton {
				s += " {button}"
			}
			s += fmt.Sprintf(" {%s}", t.GroupName)
			lines = append(lines, s)
		}
		if len(lines) == 0 {
			lines = append(lines, "#action: no actions defined")
		}
		return echoResults(lines)
	}

	pattern, afterPattern := splitBraceArg(rest)
	command, afterCommand := splitBraceArg(afterPattern)
	group, afterGroup := splitBraceArg(afterCommand)

	isButton := false
	remaining := strings.TrimSpace(afterGroup)
	if remaining == "button" || strings.HasPrefix(remaining, "{button}") {
		isButton = true
	}

	if pattern == "" || command == "" {
		return echoResults([]string{"#action: usage: #action {pattern} {command} [group] [button]"})
	}

	if group == "" {
		group = "default"
	}

	if err := v.store.SaveTrigger(v.sessionID, pattern, command, isButton, group); err != nil {
		return echoResults([]string{fmt.Sprintf("#action: save error: %v", err)})
	}
	v.ensureFresh()

	label := ""
	if isButton {
		label = " {button}"
	}
	return echoResults([]string{fmt.Sprintf("#action {%s} {%s} {%s}%s", pattern, command, group, label)})
}

func (v *VM) cmdUnaction(rest string) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unaction: usage: #unaction {pattern}"})
	}
	if err := v.store.DeleteTrigger(v.sessionID, pattern); err != nil {
		return echoResults([]string{fmt.Sprintf("#unaction: error: %v", err)})
	}
	v.ensureFresh()
	return echoResults([]string{fmt.Sprintf("#unaction: %s removed", pattern)})
}

func (v *VM) cmdHighlight(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, h := range v.highlights {
			lines = append(lines, formatHighlight(h))
		}
		if len(lines) == 0 {
			lines = append(lines, "#highlight: no highlights defined")
		}
		return echoResults(lines)
	}

	colorSpec, afterColor := splitBraceArg(rest)
	pattern, afterPattern := splitBraceArg(strings.TrimSpace(afterColor))
	group, _ := splitBraceArg(strings.TrimSpace(afterPattern))

	if pattern == "" {
		return echoResults([]string{"#highlight: usage: #highlight {color} {pattern} [group]"})
	}
	if group == "" {
		group = "default"
	}

	h := parseColorSpec(colorSpec)
	h.Pattern = pattern
	h.GroupName = group

	if v.store != nil {
		if err := v.store.SaveHighlight(v.sessionID, h); err != nil {
			return echoResults([]string{fmt.Sprintf("#highlight: save error: %v", err)})
		}
		v.ensureFresh()
	} else {
		h.Enabled = true
		v.highlights = append(v.highlights, h)
	}

	return echoResults([]string{formatHighlight(h)})
}

func (v *VM) cmdUnhighlight(rest string) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unhighlight: usage: #unhighlight {pattern}"})
	}
	if v.store != nil {
		if err := v.store.DeleteHighlight(v.sessionID, pattern); err != nil {
			return echoResults([]string{fmt.Sprintf("#unhighlight: error: %v", err)})
		}
		v.ensureFresh()
	}
	return echoResults([]string{fmt.Sprintf("#unhighlight: %s removed", pattern)})
}
