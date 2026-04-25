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
	return v.evalLine(input, 0)
}

func (v *VM) evalLine(input string, depth int) []Result {
	if depth >= maxExpandDepth {
		return []Result{{Text: input, Kind: ResultCommand}}
	}

	statements := splitStatements(input)
	var results []Result
	for _, stmt := range statements {
		results = append(results, v.evalStatement(stmt, depth)...)
	}
	return results
}

func (v *VM) evalStatement(stmt string, depth int) []Result {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil
	}

	// 1. Variable substitution
	stmt = v.substituteVars(stmt)

	// 2. System command dispatch
	if strings.HasPrefix(stmt, "#") {
		return v.dispatchCommand(stmt, depth)
	}

	// 3. Alias expansion
	parsed := parseArgs(stmt)
	if len(parsed) > 0 {
		cmd := parsed[0]
		args := parsed[1:]
		for _, a := range v.aliases {
			if a.Name == cmd {
				expanded := substituteTemplate(a.Template, args)
				return v.evalLine(expanded, depth+1)
			}
		}
	}

	// 4. Speedwalk expansion
	if expanded, ok := expandSpeedwalk(stmt); ok {
		return commandResults(expanded)
	}

	// 5. Default: send to MUD
	return []Result{{Text: stmt, Kind: ResultCommand}}
}

func (v *VM) dispatchCommand(input string, depth int) []Result {
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
		var result []Result
		for i := 0; i < n; i++ {
			result = append(result, v.evalLine(rest, depth+1)...)
		}
		return result
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
	case "tickon":
		return v.cmdTickOn(rest)
	case "tickoff":
		return v.cmdTickOff(rest)
	case "tickset":
		return v.cmdTickSet(rest)
	case "ticksize":
		return v.cmdTickSize(rest)
	case "tts", "ts":
		return v.cmdTTS(rest)
	case "showme", "show":
		text, _ := splitBraceArg(rest)
		if text == "" {
			text = rest // Fallback if no braces
		}
		return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: "main"}}
	case "woutput":
		buffer, afterBuffer := splitBraceArg(rest)
		text, _ := splitBraceArg(afterBuffer)
		if text == "" {
			text = strings.TrimSpace(afterBuffer) // Fallback
		}
		return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: buffer}}
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
