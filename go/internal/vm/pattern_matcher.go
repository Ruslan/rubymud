package vm

import (
	"log"
	"regexp"
)

type MatcherKind string

const (
	MatcherRegex MatcherKind = "regex"
	MatcherMud   MatcherKind = "mud"
)

type CompiledMatcher struct {
	Kind             MatcherKind
	Template         string
	EffectivePattern string
	Regex            *regexp.Regexp
}

func (v *VM) substitutePatternVars(template string) string {
	return v.varPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := match[1:]
		if value, ok := v.variables[name]; ok {
			return regexp.QuoteMeta(value)
		}
		return "" // Undefined $var in pattern templates should expand to empty string
	})
}

func (v *VM) compileMatcherTemplate(template string, cache map[string]*regexp.Regexp) CompiledMatcher {
	effectivePattern := v.substitutePatternVars(template)

	var re *regexp.Regexp
	var err error
	if cached, ok := cache[effectivePattern]; ok {
		re = cached
	} else {
		re, err = regexp.Compile(effectivePattern)
		if err != nil {
			log.Printf("pattern compile error template=%q effective=%q: %v", template, effectivePattern, err)
		}
		cache[effectivePattern] = re
	}

	return CompiledMatcher{
		Kind:             MatcherRegex,
		Template:         template,
		EffectivePattern: effectivePattern,
		Regex:            re,
	}
}
