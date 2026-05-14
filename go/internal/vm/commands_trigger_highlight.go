package vm

import (
	"fmt"
	"rubymud/go/internal/storage"
	"strings"
)

func (v *VM) cmdAction(rest string, depth int) []Result {
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
		return echoResults(lines, depth)
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
		return echoResults([]string{"#action: usage: #action {pattern} {command} [group] [button]"}, depth)
	}

	if group == "" {
		group = "default"
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#action: save error: no primary profile found"}, depth)
		}
		if err := v.store.SaveTrigger(pid, pattern, command, isButton, group); err != nil {
			return echoResults([]string{fmt.Sprintf("#action: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	} else {
		v.triggers = append(v.triggers, storage.TriggerRule{
			Pattern:   pattern,
			Command:   command,
			IsButton:  isButton,
			GroupName: group,
			Enabled:   true,
		})
		v.rulesVersion++
	}

	label := ""
	if isButton {
		label = " {button}"
	}
	return echoResults([]string{fmt.Sprintf("#action {%s} {%s} {%s}%s", pattern, command, group, label)}, depth)
}

func (v *VM) cmdUnaction(rest string, depth int) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unaction: usage: #unaction {pattern}"}, depth)
	}
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteTrigger(pid, pattern); err != nil {
				return echoResults([]string{fmt.Sprintf("#unaction: error: %v", err)}, depth)
			}
			v.rulesVersion++
			v.ensureFresh()
		}
	}
	return echoResults([]string{fmt.Sprintf("#unaction: %s removed", pattern)}, depth)
}

func (v *VM) cmdHighlight(rest string, depth int) []Result {
	if rest == "" {
		var lines []string
		for _, h := range v.highlights {
			lines = append(lines, formatHighlight(h))
		}
		if len(lines) == 0 {
			lines = append(lines, "#highlight: no highlights defined")
		}
		return echoResults(lines, depth)
	}

	colorSpec, afterColor := splitBraceArg(rest)
	pattern, afterPattern := splitBraceArg(strings.TrimSpace(afterColor))
	group, _ := splitBraceArg(strings.TrimSpace(afterPattern))

	if pattern == "" {
		return echoResults([]string{"#highlight: usage: #highlight {color} {pattern} [group]"}, depth)
	}
	if group == "" {
		group = "default"
	}

	h := parseColorSpec(colorSpec)
	h.Pattern = pattern
	h.GroupName = group
	h.Enabled = true

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#highlight: save error: no primary profile found"}, depth)
		}
		if err := v.store.SaveHighlight(pid, h); err != nil {
			return echoResults([]string{fmt.Sprintf("#highlight: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	} else {
		v.highlights = append(v.highlights, h)
		v.rulesVersion++
	}

	return echoResults([]string{formatHighlight(h)}, depth)
}

func (v *VM) cmdUnhighlight(rest string, depth int) []Result {
	pattern := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if pattern == "" {
		return echoResults([]string{"#unhighlight: usage: #unhighlight {pattern}"}, depth)
	}
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteHighlight(pid, pattern); err != nil {
				return echoResults([]string{fmt.Sprintf("#unhighlight: error: %v", err)}, depth)
			}
			v.rulesVersion++
			v.ensureFresh()
		}
	}
	return echoResults([]string{fmt.Sprintf("#unhighlight: %s removed", pattern)}, depth)
}
