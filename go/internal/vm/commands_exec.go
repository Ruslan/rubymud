package vm

import "strings"

func (v *VM) cmdExec(rest string, depth int, captures []string) []Result {
	args := parseArgs(rest)
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return echoResults([]string{"#exec: usage: #exec {path} [arg ...]"}, depth)
	}

	for i := range args {
		args[i] = ExpandCaptures(args[i], captures)
		args[i] = v.substituteVars(args[i])
	}

	path := strings.TrimSpace(args[0])
	if path == "" {
		return echoResults([]string{"#exec: usage: #exec {path} [arg ...]"}, depth)
	}

	return []Result{{Text: path, Args: args[1:], Kind: ResultExec, TargetBuffer: "main", IsInternal: false, Depth: depth}}
}
