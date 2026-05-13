package vm

import (
	"fmt"
	"strings"

	"rubymud/go/internal/storage"
)

func (v *VM) cmdSubstitute(rest string, depth int) []Result {
	if rest == "" {
		var lines []string
		for _, rule := range v.substitutes {
			if rule.IsGag {
				continue
			}
			lines = append(lines, fmt.Sprintf("#sub {%s} {%s} {%s}", rule.Pattern, rule.Replacement, rule.GroupName))
		}
		if len(lines) == 0 {
			lines = append(lines, "#sub: no substitutions defined")
		}
		return echoResults(lines, depth)
	}

	pattern, afterPattern := splitBraceArg(rest)
	replacement, afterReplacement := splitBraceArg(afterPattern)
	group, _ := splitBraceArg(afterReplacement)
	group = v.substituteVars(group)
	if group == "" {
		group = "default"
	}
	if pattern == "" {
		return echoResults([]string{"#sub: usage: #sub {pattern} {replacement} [group]"}, depth)
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#sub: save error: no primary profile found"}, depth)
		}
		if err := v.store.SaveSubstitute(pid, pattern, replacement, false, group); err != nil {
			return echoResults([]string{fmt.Sprintf("#sub: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	} else {
		v.substitutes = append(v.substitutes, storage.SubstituteRule{
			Pattern:     pattern,
			Replacement: replacement,
			Enabled:     true,
			GroupName:   group,
		})
		v.rulesVersion++
	}

	msg := fmt.Sprintf("#sub {%s} {%s}", pattern, replacement)
	if group != "default" {
		msg = fmt.Sprintf("#sub {%s} {%s} {%s}", pattern, replacement, group)
	}
	return echoResults([]string{msg}, depth)
}

func (v *VM) cmdGag(rest string, depth int) []Result {
	pattern, afterPattern := splitBraceArg(rest)
	group, _ := splitBraceArg(afterPattern)
	group = v.substituteVars(group)
	if group == "" {
		group = "default"
	}
	if pattern == "" {
		return echoResults([]string{"#gag: usage: #gag {pattern} [group]"}, depth)
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid == 0 {
			return echoResults([]string{"#gag: save error: no primary profile found"}, depth)
		}
		if err := v.store.SaveSubstitute(pid, pattern, "", true, group); err != nil {
			return echoResults([]string{fmt.Sprintf("#gag: save error: %v", err)}, depth)
		}
		v.rulesVersion++
		v.ensureFresh()
	} else {
		v.substitutes = append(v.substitutes, storage.SubstituteRule{
			Pattern:   pattern,
			IsGag:     true,
			Enabled:   true,
			GroupName: group,
		})
		v.rulesVersion++
	}

	msg := fmt.Sprintf("#gag {%s}", pattern)
	if group != "default" {
		msg = fmt.Sprintf("#gag {%s} {%s}", pattern, group)
	}
	return echoResults([]string{msg}, depth)
}

func (v *VM) cmdUnsub(rest string, depth int) []Result {
	pattern, _ := splitBraceArg(rest)
	if pattern == "" {
		pattern = strings.TrimSpace(strings.Trim(rest, "{}'\""))
	}
	if pattern == "" {
		return echoResults([]string{"#unsub: usage: #unsub {pattern}"}, depth)
	}

	if v.store != nil {
		pid := v.primaryProfileID()
		if pid != 0 {
			if err := v.store.DeleteSubstitute(pid, pattern); err != nil {
				return echoResults([]string{fmt.Sprintf("#unsub: error: %v", err)}, depth)
			}
			v.rulesVersion++
			v.ensureFresh()
		}
	} else {
		kept := v.substitutes[:0]
		for _, rule := range v.substitutes {
			if rule.Pattern != pattern {
				kept = append(kept, rule)
			}
		}
		v.substitutes = kept
		v.rulesVersion++
	}
	return echoResults([]string{fmt.Sprintf("#unsub: %s removed", pattern)}, depth)
}
