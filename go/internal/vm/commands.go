package vm

import (
	"fmt"
	"strings"
)

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
	return v.evalLine(input, 0, nil)
}

func (v *VM) ProcessInputWithCaptures(input string, captures []string) []Result {
	v.ensureFresh()
	return v.evalLine(input, 0, captures)
}

func (v *VM) evalLine(input string, depth int, captures []string) []Result {
	if depth >= maxExpandDepth {
		return []Result{{Text: input, Kind: ResultCommand}}
	}

	statements := splitStatements(input)
	var results []Result
	for _, stmt := range statements {
		results = append(results, v.evalStatement(stmt, depth, captures)...)
	}
	return results
}

func (v *VM) evalStatement(stmt string, depth int, captures []string) []Result {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return nil
	}

	if isCodeBearingCommand(stmt) {
		if stmt == "#if" || strings.HasPrefix(stmt, "#if ") || strings.HasPrefix(stmt, "#if	") || strings.HasPrefix(stmt, "#if{") {
			return v.dispatchIf(stmt, depth, captures)
		}
		if len(captures) > 0 {
			stmt = ExpandCaptures(stmt, captures)
		}
		return v.dispatchCommand(stmt, depth, nil)
	}

	if len(captures) > 0 {
		stmt = ExpandCaptures(stmt, captures)
		stmt = strings.TrimSpace(stmt)
	}

	// 1. Variable substitution
	stmt = v.substituteVars(stmt)

	// 2. System command dispatch
	if strings.HasPrefix(stmt, "#") {
		return v.dispatchCommand(stmt, depth, nil)
	}

	// 3. Alias expansion
	parsed := parseArgs(stmt)
	if len(parsed) > 0 {
		cmd := parsed[0]
		args := parsed[1:]
		for _, a := range v.aliases {
			if a.Name == cmd && a.Enabled {
				aliasCaptures := make([]string, 0, len(args)+1)
				aliasCaptures = append(aliasCaptures, strings.Join(args, " "))
				aliasCaptures = append(aliasCaptures, args...)
				return v.evalLine(a.Template, depth+1, aliasCaptures)
			}
		}
	}

	// 4. Speedwalk expansion
	if expanded, ok := expandSpeedwalk(stmt); ok {
		return commandResults(expanded, depth)
	}

	// 5. Default: send to MUD
	return []Result{{Text: stmt, Kind: ResultCommand, TargetBuffer: "main", IsInternal: false, Depth: depth}}
}

func (v *VM) dispatchCommand(input string, depth int, captures []string) []Result {
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
			result = append(result, v.evalLine(rest, depth+1, captures)...)
		}
		return result
	}

	var keyword, rest string
	if idx := strings.IndexAny(cmd, " \t{"); idx != -1 {
		keyword = cmd[:idx]
		rest = strings.TrimSpace(cmd[idx:])
	} else {
		keyword = cmd
		rest = ""
	}

	switch keyword {
	case "alias", "ali":
		return v.cmdAlias(rest, depth)
	case "unalias":
		return v.cmdUnalias(rest, depth)
	case "variable", "var":
		return v.cmdVariable(rest, depth)
	case "unvariable", "unvar":
		return v.cmdUnvariable(rest, depth)
	case "action", "act":
		return v.cmdAction(rest, depth)
	case "unaction", "unact":
		return v.cmdUnaction(rest, depth)
	case "highlight", "high":
		return v.cmdHighlight(rest, depth)
	case "unhighlight", "unhigh":
		return v.cmdUnhighlight(rest, depth)
	case "sub", "substitute":
		return v.cmdSubstitute(rest, depth)
	case "gag":
		return v.cmdGag(rest, depth)
	case "unsub":
		return v.cmdUnsub(rest, depth)
	case "hotkey", "hot":
		return v.cmdHotkey(rest, depth)
	case "tickon":
		return v.cmdTickOn(rest, depth)
	case "tickoff":
		return v.cmdTickOff(rest, depth)
	case "tickset":
		return v.cmdTickSet(rest, depth)
	case "ticksize":
		return v.cmdTickSize(rest, depth)
	case "tickicon":
		return v.cmdTickIcon(rest, depth)
	case "tickmode":
		return v.cmdTickMode(rest, depth)
	case "ticker":
		return v.cmdTicker(rest, depth)
	case "tickat":
		return v.cmdTickAt(rest, depth)
	case "untickat":
		return v.cmdUntickat(rest, depth)
	case "delay":
		return v.cmdDelay(rest, depth, captures)
	case "undelay":
		return v.cmdUndelay(rest, depth)
	case "tts", "ts":
		return v.cmdTTS(rest, depth)
	case "showme", "show":
		text, _ := splitBraceArg(rest)
		if text == "" {
			text = rest // Fallback if no braces
		}
		text = renderLocalMarkup(text)
		return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: "main", IsInternal: false, Depth: depth}}
	case "woutput":
		buffer, afterBuffer := splitBraceArg(rest)
		text, _ := splitBraceArg(afterBuffer)
		if text == "" {
			text = strings.TrimSpace(afterBuffer) // Fallback
		}
		text = renderLocalMarkup(text)
		return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: buffer, IsInternal: false, Depth: depth}}
	}

	return []Result{{Text: input, Kind: ResultCommand, IsInternal: false, Depth: depth}}
}

func (v *VM) dispatchIf(input string, depth int, captures []string) []Result {
	rest := strings.TrimPrefix(input, "#if")
	rest = strings.TrimSpace(rest)

	if rest == "" {
		return []Result{{Text: "#if error: missing expression", Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	exprStr, rest := splitBraceArg(rest)
	if exprStr == "" {
		return []Result{{Text: "#if error: missing expression", Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	thenBranch, rest := splitBraceArg(rest)
	if thenBranch == "" {
		return []Result{{Text: "#if error: missing then-branch", Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	elseBranch, rest := splitBraceArg(rest)
	if strings.TrimSpace(rest) != "" {
		return []Result{{Text: "#if error: too many arguments", Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	// Evaluate expression
	res, err := EvalExpression(exprStr, v.variables, captures)
	if err != nil {
		return []Result{{Text: fmt.Sprintf("#if expression error: %v", err), Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	boolRes, ok := res.(bool)
	if !ok {
		return []Result{{Text: fmt.Sprintf("#if error: expression must return boolean, got %T", res), Kind: ResultEcho, TargetBuffer: "main", IsInternal: true, Depth: depth}}
	}

	if boolRes {
		return v.evalLine(thenBranch, depth+1, captures)
	} else if elseBranch != "" {
		return v.evalLine(elseBranch, depth+1, captures)
	}

	return nil
}

func commandResults(lines []string, depth int) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultCommand, IsInternal: false, Depth: depth})
	}
	return results
}

func echoResults(lines []string, depth int) []Result {
	results := make([]Result, 0, len(lines))
	for _, line := range lines {
		results = append(results, Result{Text: line, Kind: ResultEcho, IsInternal: true, Depth: depth})
	}
	return results
}
func isCodeBearingCommand(stmt string) bool {
	cmds := []string{"#if", "#alias", "#ali", "#action", "#act", "#hotkey", "#hot", "#tickat", "#delay", "#ticker", "#sub", "#substitute", "#gag", "#unsub"}
	for _, c := range cmds {
		if stmt == c || strings.HasPrefix(stmt, c+" ") || strings.HasPrefix(stmt, c+"\t") || strings.HasPrefix(stmt, c+"{") {
			return true
		}
	}
	return false
}
