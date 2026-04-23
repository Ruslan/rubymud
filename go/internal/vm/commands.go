package vm

import "strings"

func (v *VM) ProcessInput(input string) []string {
	results := v.ProcessInputDetailed(input)
	output := make([]string, 0, len(results))
	for _, result := range results {
		output = append(output, result.Text)
	}
	return output
}

func (v *VM) ProcessInputDetailed(input string) []Result {
	v.ensureFresh()

	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	if strings.HasPrefix(input, "#") {
		return v.dispatchCommand(input)
	}

	if expanded, ok := expandSpeedwalk(input); ok {
		return commandResults(expanded)
	}

	return commandResults(v.ExpandInput(input))
}

func (v *VM) dispatchCommand(input string) []Result {
	cmd := strings.TrimPrefix(input, "#")
	cmd = strings.TrimSpace(cmd)

	if cmd == "" {
		return nil
	}

	if cmd == "nop" || strings.HasPrefix(cmd, "nop ") {
		return nil
	}

	if numRepeat(cmd) > 0 {
		n, rest := parseRepeat(cmd)
		expanded := v.ExpandInput(rest)
		var result []string
		for i := 0; i < n; i++ {
			result = append(result, expanded...)
		}
		return commandResults(result)
	}

	keyword, args := splitFirstWord(cmd)
	rest := strings.Join(args, " ")

	switch keyword {
	case "alias", "ali":
		return v.cmdAlias(rest)
	case "unalias":
		return v.cmdUnalias(rest)
	case "variable", "var":
		return v.cmdVariable(rest)
	case "unvariable", "unvar":
		return v.cmdUnvariable(rest)
	case "action", "act":
		return v.cmdAction(rest)
	case "unaction", "unact":
		return v.cmdUnaction(rest)
	case "highlight", "high":
		return v.cmdHighlight(rest)
	case "unhighlight", "unhigh":
		return v.cmdUnhighlight(rest)
	case "hotkey", "hot":
		return v.cmdHotkey(rest)
	case "showme", "show":
		return []Result{{Text: rest, Kind: ResultEcho, TargetBuffer: "main"}}
	case "woutput":
		buffer, textArgs := splitFirstWord(rest)
		return []Result{{Text: strings.Join(textArgs, " "), Kind: ResultEcho, TargetBuffer: buffer}}
	}

	return []Result{{Text: input, Kind: ResultCommand}}
}

func commandResults(lines []string) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultCommand})
	}
	return results
}

func echoResults(lines []string) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultEcho})
	}
	return results
}
