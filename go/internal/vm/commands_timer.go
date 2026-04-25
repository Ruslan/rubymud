package vm

import (
	"fmt"
	"strconv"
	"strings"
)

func (v *VM) cmdTickOn(rest string) []Result {
	if strings.TrimSpace(rest) != "" {
		return echoResults([]string{"#tickon: usage: #tickon (accepts no arguments)"})
	}
	if v.timerCtrl != nil {
		v.timerCtrl.TickOn("ticker")
	}
	return nil
}

func (v *VM) cmdTickOff(rest string) []Result {
	if strings.TrimSpace(rest) != "" {
		return echoResults([]string{"#tickoff: usage: #tickoff (accepts no arguments)"})
	}
	if v.timerCtrl != nil {
		v.timerCtrl.TickOff("ticker")
	}
	return nil
}

func (v *VM) cmdTickSet(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg, remaining := splitBraceArg(rest)
	if strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickset: too many arguments, usage: #tickset [{seconds}]"})
	}

	if arg == "" {
		v.timerCtrl.TickReset("ticker")
		return nil
	}

	seconds, err := strconv.ParseFloat(arg, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#tickset: invalid non-negative seconds %q", arg)})
	}

	v.timerCtrl.TickSet("ticker", seconds)
	return nil
}

func (v *VM) cmdTickSize(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg, remaining := splitBraceArg(rest)
	if arg == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#ticksize: usage: #ticksize {seconds}"})
	}

	seconds, err := strconv.ParseFloat(arg, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#ticksize: invalid non-negative seconds %q", arg)})
	}

	v.timerCtrl.TickSize("ticker", seconds)
	return nil
}
