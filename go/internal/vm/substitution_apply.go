package vm

import (
	"encoding/json"
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

func (v *VM) CheckGag(plainText string) (storage.LogOverlay, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ensureFresh()
	for i := range v.compiledSubstitutes {
		cs := &v.compiledSubstitutes[i]
		if !cs.rule.Enabled || !cs.rule.IsGag {
			continue
		}
		if cs.matcher.Regex == nil {
			continue
		}
		matches := cs.matcher.Regex.FindAllStringIndex(plainText, -1)
		for _, loc := range matches {
			if loc[0] == loc[1] {
				continue
			}
			payload, _ := json.Marshal(gagPayload{
				RuleID:           cs.rule.ID,
				PatternTemplate:  cs.rule.Pattern,
				EffectivePattern: cs.matcher.EffectivePattern,
			})
			return storage.LogOverlay{
				OverlayType: "gag",
				Layer:       0,
				PayloadJSON: string(payload),
				SourceType:  "substitute_rule",
				SourceID:    strconv.FormatInt(cs.rule.ID, 10),
			}, true
		}
	}
	return storage.LogOverlay{}, false
}

func (v *VM) ApplySubsAndCollectOverlays(rawText, plainText string) (string, string, []storage.LogOverlay) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ensureFresh()
	displayRaw := rawText
	displayPlain := plainText
	var overlays []storage.LogOverlay
	layer := 1

	for i := range v.compiledSubstitutes {
		cs := &v.compiledSubstitutes[i]
		if !cs.rule.Enabled || cs.rule.IsGag {
			continue
		}
		if cs.matcher.Regex == nil {
			continue
		}
		matches := cs.matcher.Regex.FindAllStringSubmatchIndex(displayPlain, -1)
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

			replacementTemplate := v.substituteVars(cs.rule.Replacement)
			replacementRaw := expandSubstitutionCaptures(replacementTemplate, displayPlain, loc)
			replacementPlain := stripANSIFromVM(replacementRaw)
			payload, _ := json.Marshal(substitutionPayload{
				ReplacementRaw:   replacementRaw,
				ReplacementPlain: replacementPlain,
				RuleID:           cs.rule.ID,
				PatternTemplate:  cs.rule.Pattern,
				EffectivePattern: cs.matcher.EffectivePattern,
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
				SourceID:    strconv.FormatInt(cs.rule.ID, 10),
			})
			layer++

			displayRaw = displayRaw[:rawStart] + replacementRaw + displayRaw[rawEnd:]
			displayPlain = displayPlain[:start] + replacementPlain + displayPlain[end:]
		}
	}

	return displayRaw, displayPlain, overlays
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
