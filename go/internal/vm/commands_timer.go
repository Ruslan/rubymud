package vm

import (
	"fmt"
	"strconv"
	"strings"
)

func isValidTimerName(name string) bool {
	if name == "" {
		return false
	}
	// Name must not be numeric
	if _, err := strconv.ParseFloat(name, 64); err == nil {
		return false
	}
	// Name must not start with + or -
	if name[0] == '+' || name[0] == '-' {
		return false
	}
	return true
}

func (v *VM) cmdTickOn(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}
	name, remaining := splitBraceArg(rest)
	if strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickon: too many arguments, usage: #tickon [{name}]"})
	}
	if name == "" {
		name = "ticker"
	} else if !isValidTimerName(name) {
		return echoResults([]string{fmt.Sprintf("#tickon: invalid timer name %q", name)})
	}
	v.timerCtrl.TickOn(name)
	return nil
}

func (v *VM) cmdTickOff(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}
	name, remaining := splitBraceArg(rest)
	if strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickoff: too many arguments, usage: #tickoff [{name}]"})
	}
	if name == "" {
		name = "ticker"
	} else if !isValidTimerName(name) {
		return echoResults([]string{fmt.Sprintf("#tickoff: invalid timer name %q", name)})
	}
	v.timerCtrl.TickOff(name)
	return nil
}

func (v *VM) cmdTickSet(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, remaining := splitBraceArg(strings.TrimSpace(after1))

	if strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickset: too many arguments, usage: #tickset [{name}] [{seconds}]"})
	}

	var name string
	var secondsStr string

	if arg1 == "" {
		// #tickset
		name = "ticker"
		v.timerCtrl.TickReset(name)
		return nil
	}

	// Determine if arg1 is name or seconds
	if _, err := strconv.ParseFloat(arg1, 64); err == nil {
		// arg1 is numeric, so it's #tickset {seconds} for default ticker
		name = "ticker"
		secondsStr = arg1
		if arg2 != "" {
			return echoResults([]string{"#tickset: usage: #tickset [{name}] [{seconds}]"})
		}
	} else {
		// arg1 is not numeric, treat as name: #tickset {name} [{seconds}]
		if !isValidTimerName(arg1) {
			return echoResults([]string{fmt.Sprintf("#tickset: invalid timer name %q", arg1)})
		}
		name = arg1
		secondsStr = arg2
	}

	if secondsStr == "" {
		v.timerCtrl.TickReset(name)
		return nil
	}

	seconds, err := strconv.ParseFloat(secondsStr, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#tickset: invalid non-negative seconds %q", secondsStr)})
	}

	v.timerCtrl.TickSet(name, seconds)
	return nil
}

func (v *VM) cmdTickSize(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, remaining := splitBraceArg(strings.TrimSpace(after1))

	if arg1 == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#ticksize: usage: #ticksize [{name}] {seconds}"})
	}

	var name string
	var secondsStr string

	if _, err := strconv.ParseFloat(arg1, 64); err == nil {
		// arg1 is numeric, treat as #ticksize {seconds} for default ticker
		name = "ticker"
		secondsStr = arg1
		if arg2 != "" {
			return echoResults([]string{"#ticksize: usage: #ticksize [{name}] {seconds}"})
		}
	} else {
		// arg1 is name, arg2 must be seconds
		if !isValidTimerName(arg1) {
			return echoResults([]string{fmt.Sprintf("#ticksize: invalid timer name %q", arg1)})
		}
		if arg2 == "" {
			return echoResults([]string{"#ticksize: usage: #ticksize [{name}] {seconds}"})
		}
		name = arg1
		secondsStr = arg2
	}

	seconds, err := strconv.ParseFloat(secondsStr, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#ticksize: invalid non-negative seconds %q", secondsStr)})
	}

	v.timerCtrl.TickSize(name, seconds)
	return nil
}

func (v *VM) cmdTickAt(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, after2 := splitBraceArg(strings.TrimSpace(after1))
	arg3, remaining := splitBraceArg(strings.TrimSpace(after2))

	if arg1 == "" || arg2 == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickat: usage: #tickat [{name}] {second} {command}"})
	}

	var name string
	var secondStr string
	var command string

	if _, err := strconv.Atoi(arg1); err == nil {
		// arg1 is numeric, treat as #tickat {second} {command} for default ticker
		name = "ticker"
		secondStr = arg1
		command = arg2
		if arg3 != "" {
			return echoResults([]string{"#tickat: usage: #tickat [{name}] {second} {command}"})
		}
	} else {
		// arg1 is name, arg2 is second, arg3 is command
		if !isValidTimerName(arg1) {
			return echoResults([]string{fmt.Sprintf("#tickat: invalid timer name %q", arg1)})
		}
		if arg3 == "" {
			return echoResults([]string{"#tickat: usage: #tickat [{name}] {second} {command}"})
		}
		name = arg1
		secondStr = arg2
		command = arg3
	}

	second, err := strconv.Atoi(secondStr)
	if err != nil || second < 0 {
		return echoResults([]string{fmt.Sprintf("#tickat: invalid second %q", secondStr)})
	}

	maxSec := v.timerCtrl.GetTimerCycleSeconds(name)
	if second > maxSec {
		return echoResults([]string{fmt.Sprintf("#tickat: second %d is out of range (max %d for timer %q)", second, maxSec, name)})
	}

	v.timerCtrl.SubscribeTimer(name, second, command)
	return nil
}

func (v *VM) cmdUntickat(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, remaining := splitBraceArg(strings.TrimSpace(after1))

	if arg1 == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#untickat: usage: #untickat [{name}] {second}"})
	}

	var name string
	var secondStr string

	if _, err := strconv.Atoi(arg1); err == nil {
		// arg1 is numeric, treat as #untickat {second} for default ticker
		name = "ticker"
		secondStr = arg1
		if arg2 != "" {
			return echoResults([]string{"#untickat: usage: #untickat [{name}] {second}"})
		}
	} else {
		// arg1 is name, arg2 is second
		if !isValidTimerName(arg1) {
			return echoResults([]string{fmt.Sprintf("#untickat: invalid timer name %q", arg1)})
		}
		if arg2 == "" {
			return echoResults([]string{"#untickat: usage: #untickat [{name}] {second}"})
		}
		name = arg1
		secondStr = arg2
	}

	second, err := strconv.Atoi(secondStr)
	if err != nil || second < 0 {
		return echoResults([]string{fmt.Sprintf("#untickat: invalid second %q", secondStr)})
	}

	v.timerCtrl.UnsubscribeTimer(name, second)
	return nil
}

func (v *VM) cmdTickIcon(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, remaining := splitBraceArg(strings.TrimSpace(after1))

	if arg1 == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#tickicon: usage: #tickicon [{name}] {icon}"})
	}

	var name string
	var icon string

	// If after1 is not empty, it means we had at least two potentially braced arguments,
	// or one arg and trailing text.
	if strings.TrimSpace(after1) != "" {
		// Named form: #tickicon {name} {icon}
		if !isValidTimerName(arg1) {
			return echoResults([]string{fmt.Sprintf("#tickicon: invalid timer name %q", arg1)})
		}
		name = arg1
		icon = arg2
	} else {
		// Single argument form: #tickicon {icon} (for default ticker)
		name = "ticker"
		icon = arg1
	}

	v.timerCtrl.TickIcon(name, icon)
	return nil
}

func (v *VM) cmdTicker(rest string) []Result {
	if v.timerCtrl == nil {
		return nil
	}

	arg1, after1 := splitBraceArg(rest)
	arg2, after2 := splitBraceArg(strings.TrimSpace(after1))
	arg3, remaining := splitBraceArg(strings.TrimSpace(after2))

	if arg1 == "" || arg2 == "" || arg3 == "" || strings.TrimSpace(remaining) != "" {
		return echoResults([]string{"#ticker: usage: #ticker {name} {seconds} {command}"})
	}

	name := arg1
	if !isValidTimerName(name) {
		return echoResults([]string{fmt.Sprintf("#ticker: invalid timer name %q", name)})
	}

	seconds, err := strconv.ParseFloat(arg2, 64)
	if err != nil || seconds < 0 {
		return echoResults([]string{fmt.Sprintf("#ticker: invalid non-negative seconds %q", arg2)})
	}

	command := arg3

	v.timerCtrl.TickSet(name, seconds)
	v.timerCtrl.SubscribeTimer(name, 0, command)
	v.timerCtrl.TickOn(name)

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
