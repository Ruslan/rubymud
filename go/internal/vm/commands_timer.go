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

func (v *VM) cmdTickAt(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	secondStr, afterSecond := splitBraceArg(rest)
	command, remaining := splitBraceArg(strings.TrimSpace(afterSecond))

	if secondStr == "" || command == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickat: usage: #tickat {second} {command}"})
	}

	second, err := strconv.Atoi(secondStr)
	if err != nil || second < 0 {
		return echoResults([]string{fmt.Sprintf("#tickat: invalid second %q", secondStr)})
	}

	maxSec := v.timerCtrl.GetTimerCycleSeconds("ticker")
	if second > maxSec {
		return echoResults([]string{fmt.Sprintf("#tickat: second %d is out of range (max %d)", second, maxSec)})
	}

	v.timerCtrl.SubscribeTimer("ticker", second, command)
	return nil
}

func (v *VM) cmdUntickat(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	secondStr, remaining := splitBraceArg(rest)
	if secondStr == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#untickat: usage: #untickat {second}"})
	}

	second, err := strconv.Atoi(secondStr)
	if err != nil || second < 0 {
		return echoResults([]string{fmt.Sprintf("#untickat: invalid second %q", secondStr)})
	}

	v.timerCtrl.UnsubscribeTimer("ticker", second)
	return nil
}

func (v *VM) cmdDelay(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, after2 := splitBraceArg(strings.TrimSpace(after1))
	arg3, remaining := splitBraceArg(strings.TrimSpace(after2))

	var id string
	var secondsStr string
	var command string

	if arg3 != "" {
		// #delay {id} {seconds} {command}
		id = arg1
		secondsStr = arg2
		command = arg3
		if strings.TrimSpace(remaining) != "" {
			return echoResults([]string{"#delay: too many arguments, usage: #delay [{id}] {seconds} {command}"})
		}
	} else if arg2 != "" {
		// #delay {seconds} {command}
		id = ""
		secondsStr = arg1
		command = arg2
	} else {
		return echoResults([]string{"#delay: usage: #delay [{id}] {seconds} {command}"})
	}

	seconds, err := strconv.ParseFloat(secondsStr, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#delay: invalid seconds %q", secondsStr)})
	}

	if err := v.timerCtrl.ScheduleDelay(id, seconds, command); err != nil {
		return echoResults([]string{fmt.Sprintf("#delay: %v", err)})
	}
	return nil
}

func (v *VM) cmdUndelay(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	id, remaining := splitBraceArg(rest)
	if id == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#undelay: usage: #undelay {id}"})
	}

	v.timerCtrl.CancelDelay(id)
	return nil
}
