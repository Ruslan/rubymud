package vm

import (
	"fmt"
	"regexp"
	"strings"

	"rubymud/go/internal/storage"
)

func (v *VM) cmdAlias(rest string, depth int) []Result {
	if rest == "" {
		var lines []string
		for _, a := range v.aliases {
			lines = append(lines, fmt.Sprintf("#alias {%s} {%s}", a.Name, a.Template))
		}
		if len(lines) == 0 {
			lines = append(lines, "#alias: no aliases defined")
		}
		return echoResults(lines, depth)
	}

	name, afterName := splitBraceArg(rest)
	name = v.substituteVars(name)
	template, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#alias: usage: #alias {name} {template}"}, depth)
	}

	if template == "" {
		for _, a := range v.aliases {
			if a.Name == name {
				return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, a.Template)}, depth)
			}
		}
		return echoResults([]string{fmt.Sprintf("#alias: %s not found", name)}, depth)
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#alias: save error: no primary profile found"}, depth)
		}
		if err := v.store.SaveAlias(pid, name, template, true, "default"); err != nil {
			return echoResults([]string{fmt.Sprintf("#alias: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	} else {
		v.aliases = append(v.aliases, storage.AliasRule{Name: name, Template: template, Enabled: true, GroupName: "default"})
		v.rulesVersion++
	}

	return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, template)}, depth)
}

func (v *VM) cmdUnalias(rest string, depth int) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unalias: usage: #unalias {name}"}, depth)
	}
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteAlias(pid, name); err != nil {
				return echoResults([]string{fmt.Sprintf("#unalias: error: %v", err)}, depth)
			}
			v.rulesVersion++
			v.ensureFresh()
		}
	}
	return echoResults([]string{fmt.Sprintf("#unalias: %s removed", name)}, depth)
}

func (v *VM) cmdVariable(rest string, depth int) []Result {
	if rest == "" {
		var lines []string
		for k, val := range v.variables {
			lines = append(lines, fmt.Sprintf("#variable {%s} = {%s}", k, val))
		}
		if len(lines) == 0 {
			lines = append(lines, "#variable: no variables defined")
		}
		return echoResults(lines, depth)
	}

	name, afterName := splitBraceArg(rest)
	value, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#variable: usage: #variable {name} [value]"}, depth)
	}

	if value == "" {
		if val, ok := v.variables[name]; ok {
			return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, val)}, depth)
		}
		return echoResults([]string{fmt.Sprintf("#variable: %s not found", name)}, depth)
	}

	if v.store != nil {
		if err := v.store.SetVariable(v.sessionID, name, value); err != nil {
			return echoResults([]string{fmt.Sprintf("#variable: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.effectivePatternCache = make(map[string]*regexp.Regexp)
		v.ensureFresh()
	} else {
		v.variables[name] = value
		v.effectivePatternCache = make(map[string]*regexp.Regexp)
	}

	return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, value)}, depth)
}

func (v *VM) cmdUnvariable(rest string, depth int) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unvariable: usage: #unvariable {name}"}, depth)
	}
	if v.store != nil {
		if err := v.store.DeleteVariable(v.sessionID, name); err != nil {
			return echoResults([]string{fmt.Sprintf("#unvariable: error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.effectivePatternCache = make(map[string]*regexp.Regexp)
		v.ensureFresh()
	}
	delete(v.variables, name)
	v.effectivePatternCache = make(map[string]*regexp.Regexp)
	return echoResults([]string{fmt.Sprintf("#unvariable: %s removed", name)}, depth)
}

func (v *VM) cmdHotkey(rest string, depth int) []Result {
	shortcut, afterShortcut := splitBraceArg(strings.TrimSpace(rest))
	shortcut = v.substituteVars(shortcut)
	command, _ := splitBraceArg(strings.TrimSpace(afterShortcut))
	if shortcut == "" || command == "" {
		return echoResults([]string{"#hotkey: usage: #hotkey {shortcut} {command}"}, depth)
	}
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#hotkey: error: no primary profile"}, depth)
		}
		if _, err := v.store.CreateHotkey(pid, shortcut, command, 0, 0); err != nil {
			return echoResults([]string{fmt.Sprintf("#hotkey: error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	}
	return echoResults([]string{fmt.Sprintf("#hotkey {%s} {%s}", shortcut, command)}, depth)
}
