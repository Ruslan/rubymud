package vm

import (
	"regexp"

	"rubymud/go/internal/storage"
)

const maxExpandDepth = 10

type Effect struct {
	Type         string
	Command      string
	Label        string
	LogEntryID   int64
	TargetBuffer string
}

type RoutingInfo struct {
	TargetBuffer string
	CopyBuffers  []string
	Echoes       []EchoAction
}

type EchoAction struct {
	TargetBuffer string
	Text         string
}

type ResultKind string

const (
	ResultCommand ResultKind = "command"
	ResultEcho    ResultKind = "echo"
)

type Result struct {
	Text         string
	Kind         ResultKind
	TargetBuffer string
}

type VM struct {
	store      *storage.Store
	sessionID  int64
	aliases    []storage.AliasRule
	triggers   []storage.TriggerRule
	highlights []storage.HighlightRule
	variables  map[string]string
	varPattern *regexp.Regexp
	ttsFn      func(string)
}
