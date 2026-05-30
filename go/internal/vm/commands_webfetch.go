package vm

import "strings"

func (v *VM) cmdWebFetch(rest string, depth int, captures []string) []Result {
	args := parseArgs(rest)
	if len(args) != 3 {
		return echoResults([]string{"#webfetch: usage: #webfetch {url} {queryKey} {queryValue}"}, depth)
	}
	for i := range args {
		args[i] = ExpandCaptures(args[i], captures)
		args[i] = v.substituteVars(args[i])
	}
	url := strings.TrimSpace(args[0])
	queryKey := strings.TrimSpace(args[1])
	if url == "" || queryKey == "" {
		return echoResults([]string{"#webfetch: usage: #webfetch {url} {queryKey} {queryValue}"}, depth)
	}
	return []Result{{Text: url, Args: []string{queryKey, args[2]}, Kind: ResultWebFetch, TargetBuffer: "main", IsInternal: false, Depth: depth}}
}
