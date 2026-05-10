package vm

import (
	"fmt"
	"rubymud/go/internal/storage"
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
	pattern = v.substituteVars(pattern)
	command, afterCommand := splitBraceArg(afterPattern)
	group, afterGroup := splitBraceArg(afterCommand)
	group = v.substituteVars(group)

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

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#action: save error: no primary profile found"})
		}
		if err := v.store.SaveTrigger(pid, pattern, command, isButton, group); err != nil {
			return echoResults([]string{fmt.Sprintf("#action: save error: %v", err)})
		}
		v.ensureFresh()
	} else {
		v.triggers = append(v.triggers, storage.TriggerRule{
			Pattern:   pattern,
			Command:   command,
			IsButton:  isButton,
			GroupName: group,
			Enabled:   true,
		})
	}

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
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteTrigger(pid, pattern); err != nil {
				return echoResults([]string{fmt.Sprintf("#unaction: error: %v", err)})
			}
			v.ensureFresh()
		}
	}
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
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#highlight: save error: no primary profile found"})
		}
		if err := v.store.SaveHighlight(pid, h); err != nil {
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
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteHighlight(pid, pattern); err != nil {
				return echoResults([]string{fmt.Sprintf("#unhighlight: error: %v", err)})
			}
			v.ensureFresh()
		}
	}
	return echoResults([]string{fmt.Sprintf("#unhighlight: %s removed", pattern)})
}
