package vm

import (
	"regexp"

	"rubymud/go/internal/storage"
)

const maxExpandDepth = 10

type Effect struct {
	Type       string
	Command    string
	Label      string
	LogEntryID int64
}

type ResultKind string

const (
	ResultCommand ResultKind = "command"
	ResultEcho    ResultKind = "echo"
)

type Result struct {
	Text string
	Kind ResultKind
}

type VM struct {
	store      *storage.Store
	sessionID  int64
	aliases    []storage.AliasRule
	triggers   []storage.TriggerRule
	highlights []storage.HighlightRule
	variables  map[string]string
	varPattern *regexp.Regexp
}
