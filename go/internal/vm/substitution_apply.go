package vm

import (
	"encoding/json"
	"log"
	"regexp"
	"strconv"

	"rubymud/go/internal/storage"
)

var captureRefPattern = regexp.MustCompile(`%(\d+)`)

type substitutionPayload struct {
	ReplacementRaw   string `json:"replacement_raw"`
	ReplacementPlain string `json:"replacement_plain"`
	RuleID           int64  `json:"rule_id"`
	PatternTemplate  string `json:"pattern_template"`
	EffectivePattern string `json:"effective_pattern"`
}

type gagPayload struct {
	RuleID           int64  `json:"rule_id"`
	PatternTemplate  string `json:"pattern_template"`
	EffectivePattern string `json:"effective_pattern"`
}

func (v *VM) compileEffectivePattern(template, effective string) *regexp.Regexp {
	if re, ok := v.effectivePatternCache[effective]; ok {
		return re
	}
	re, err := regexp.Compile(effective)
	if err != nil {
		log.Printf("pattern compile error template=%q effective=%q: %v", template, effective, err)
		v.effectivePatternCache[effective] = nil
		return nil
	}
	v.effectivePatternCache[effective] = re
	return re
}

func (v *VM) CheckGag(plainText string) (storage.LogOverlay, bool) {
	v.ensureFresh()
	for i := range v.substitutes {
		rule := &v.substitutes[i]
		if !rule.Enabled || !rule.IsGag {
			continue
		}
		effectivePattern := v.substitutePatternVars(rule.Pattern)
		re := v.compileEffectivePattern(rule.Pattern, effectivePattern)
		if re == nil {
			continue
		}
		matches := re.FindAllStringIndex(plainText, -1)
		for _, loc := range matches {
			if loc[0] == loc[1] {
				continue
			}
			payload, _ := json.Marshal(gagPayload{
				RuleID:           rule.ID,
				PatternTemplate:  rule.Pattern,
				EffectivePattern: effectivePattern,
			})
			return storage.LogOverlay{
				OverlayType: "gag",
				Layer:       0,
				PayloadJSON: string(payload),
				SourceType:  "substitute_rule",
				SourceID:    strconv.FormatInt(rule.ID, 10),
			}, true
		}
	}
	return storage.LogOverlay{}, false
}

func (v *VM) ApplySubsAndCollectOverlays(rawText, plainText string) (string, string, []storage.LogOverlay) {
	v.ensureFresh()
	displayRaw := rawText
	displayPlain := plainText
	var overlays []storage.LogOverlay
	layer := 1

	for i := range v.substitutes {
		rule := &v.substitutes[i]
		if !rule.Enabled || rule.IsGag {
			continue
		}
		effectivePattern := v.substitutePatternVars(rule.Pattern)
		re := v.compileEffectivePattern(rule.Pattern, effectivePattern)
		if re == nil {
			continue
		}
		matches := re.FindAllStringSubmatchIndex(displayPlain, -1)
		if len(matches) == 0 {
			continue
		}

		for j := len(matches) - 1; j >= 0; j-- {
			loc := matches[j]
			start, end := loc[0], loc[1]
			if start == end {
				continue
			}
			rawStart, rawEnd, ok := plainRangeToRawRange(displayRaw, start, end)
			if !ok || rawStart < 0 || rawEnd > len(displayRaw) || rawStart >= rawEnd {
				continue
			}

			replacementTemplate := v.substituteVars(rule.Replacement)
			replacementRaw := expandSubstitutionCaptures(replacementTemplate, displayPlain, loc)
			replacementPlain := stripANSIFromVM(replacementRaw)
			payload, _ := json.Marshal(substitutionPayload{
				ReplacementRaw:   replacementRaw,
				ReplacementPlain: replacementPlain,
				RuleID:           rule.ID,
				PatternTemplate:  rule.Pattern,
				EffectivePattern: effectivePattern,
			})
			startOffset := start
			endOffset := end
			overlays = append(overlays, storage.LogOverlay{
				OverlayType: "substitution",
				Layer:       layer,
				StartOffset: &startOffset,
				EndOffset:   &endOffset,
				PayloadJSON: string(payload),
				SourceType:  "substitute_rule",
				SourceID:    strconv.FormatInt(rule.ID, 10),
			})
			layer++

			displayRaw = displayRaw[:rawStart] + replacementRaw + displayRaw[rawEnd:]
			displayPlain = displayPlain[:start] + replacementPlain + displayPlain[end:]
		}
	}

	return displayRaw, displayPlain, overlays
}

func (v *VM) substitutePatternVars(template string) string {
	return v.varPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := match[1:]
		if value, ok := v.variables[name]; ok {
			return regexp.QuoteMeta(value)
		}
		return regexp.QuoteMeta(match)
	})
}

func expandSubstitutionCaptures(template, plainText string, indices []int) string {
	return captureRefPattern.ReplaceAllStringFunc(template, func(match string) string {
		idx, err := strconv.Atoi(match[1:])
		if err != nil {
			return ""
		}
		startIndex := idx * 2
		endIndex := startIndex + 1
		if endIndex >= len(indices) || indices[startIndex] < 0 || indices[endIndex] < 0 {
			return ""
		}
		start, end := indices[startIndex], indices[endIndex]
		if start > end || start < 0 || end > len(plainText) {
			return ""
		}
		return plainText[start:end]
	})
}
