package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

func (v *VM) cmdAlias(rest string) []Result {
	if rest == "" {
		var lines []string
		for _, a := range v.aliases {
			lines = append(lines, fmt.Sprintf("#alias {%s} {%s}", a.Name, a.Template))
		}
		if len(lines) == 0 {
			lines = append(lines, "#alias: no aliases defined")
		}
		return echoResults(lines)
	}

	name, afterName := splitBraceArg(rest)
	template, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#alias: usage: #alias {name} {template}"})
	}

	if template == "" {
		for _, a := range v.aliases {
			if a.Name == name {
				return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, a.Template)})
			}
		}
		return echoResults([]string{fmt.Sprintf("#alias: %s not found", name)})
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#alias: save error: no primary profile found"})
		}
		if err := v.store.SaveAlias(pid, name, template, true, "default"); err != nil {
			return echoResults([]string{fmt.Sprintf("#alias: save error: %v", err)})
		}
		v.ensureFresh()
	} else {
		v.aliases = append(v.aliases, storage.AliasRule{Name: name, Template: template, Enabled: true, GroupName: "default"})
	}

	return echoResults([]string{fmt.Sprintf("#alias {%s} = {%s}", name, template)})
}

func (v *VM) cmdUnalias(rest string) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unalias: usage: #unalias {name}"})
	}
	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteAlias(pid, name); err != nil {
				return echoResults([]string{fmt.Sprintf("#unalias: error: %v", err)})
			}
			v.ensureFresh()
		}
	}
	return echoResults([]string{fmt.Sprintf("#unalias: %s removed", name)})
}

func (v *VM) cmdVariable(rest string) []Result {
	if rest == "" {
		var lines []string
		for k, val := range v.variables {
			lines = append(lines, fmt.Sprintf("#variable {%s} = {%s}", k, val))
		}
		if len(lines) == 0 {
			lines = append(lines, "#variable: no variables defined")
		}
		return echoResults(lines)
	}

	name, afterName := splitBraceArg(rest)
	value, _ := splitBraceArg(strings.TrimSpace(afterName))
	if name == "" {
		return echoResults([]string{"#variable: usage: #variable {name} [value]"})
	}

	if value == "" {
		if val, ok := v.variables[name]; ok {
			return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, val)})
		}
		return echoResults([]string{fmt.Sprintf("#variable: %s not found", name)})
	}

	if v.store != nil {
		if err := v.store.SetVariable(v.sessionID, name, value); err != nil {
			return echoResults([]string{fmt.Sprintf("#variable: save error: %v", err)})
		}
		v.ensureFresh()
	} else {
		v.variables[name] = value
	}

	return echoResults([]string{fmt.Sprintf("#variable {%s} = {%s}", name, value)})
}

func (v *VM) cmdUnvariable(rest string) []Result {
	name := strings.TrimSpace(strings.Trim(rest, "{}'\""))
	if name == "" {
		return echoResults([]string{"#unvariable: usage: #unvariable {name}"})
	}
	if err := v.store.DeleteVariable(v.sessionID, name); err != nil {
		return echoResults([]string{fmt.Sprintf("#unvariable: error: %v", err)})
	}
	delete(v.variables, name)
	v.ensureFresh()
	return echoResults([]string{fmt.Sprintf("#unvariable: %s removed", name)})
}
