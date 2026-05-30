package session

import "rubymud/go/internal/vm"

func (s *Session) runLocalResult(res vm.Result) []string {
	switch res.Kind {
	case vm.ResultExec:
		if !s.allowExecCommand() {
			return []string{"#exec is disabled in Settings. Enable Allow #exec in Settings -> App Settings to use it."}
		}
		return s.runLocalExec(res.Text, res.Args)
	case vm.ResultWebFetch:
		if !s.allowWebFetchCommand() {
			return []string{"#webfetch is disabled in Settings. Enable Allow #webfetch in Settings -> App Settings to use it."}
		}
		queryKey, queryValue := webFetchArgs(res.Args)
		return s.runLocalWebFetch(res.Text, queryKey, queryValue)
	default:
		return nil
	}
}

func isLocalResultKind(kind vm.ResultKind) bool {
	return kind == vm.ResultExec || kind == vm.ResultWebFetch
}
