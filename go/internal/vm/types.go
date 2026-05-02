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

type TimerControl interface {
	TickOn(name string)
	TickOff(name string)
	TickReset(name string)
	TickSet(name string, seconds float64)
	TickSize(name string, seconds float64)
	TickIcon(name string, icon string)
	TickAdjust(name string, deltaSeconds float64)
	TickMode(name string, mode string)
	SubscribeTimer(name string, second int, command string)
	UnsubscribeTimer(name string, second int)
	ScheduleDelay(id string, seconds float64, command string) error
	CancelDelay(id string)
	GetTimerCycleSeconds(name string) int
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
	ttsCustom  bool
	timerCtrl  TimerControl
}
