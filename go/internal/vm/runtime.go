package vm

import (
	"log"
	"os/exec"
	"regexp"
	"runtime"

	"rubymud/go/internal/storage"
)

func New(store *storage.Store, sessionID int64) *VM {
	v := &VM{
		store:                 store,
		sessionID:             sessionID,
		variables:             make(map[string]string),
		varPattern:            regexp.MustCompile(`\$([\p{L}\p{N}_]+)`),
		rulesVersion:          1,
		loadedRulesVersion:    0,
		effectivePatternCache: make(map[string]*regexp.Regexp),
	}

	if runtime.GOOS == "darwin" {
		v.ttsFn = func(text string) {
			go func() { _ = exec.Command("say", text).Run() }()
		}
	} else {
		v.ttsFn = func(string) {}
	}

	return v
}

func (v *VM) SetTimerControl(tc TimerControl) {
	v.timerCtrl = tc
}

func (v *VM) SetTTS(fn func(string)) {
	v.ttsFn = fn
	v.ttsCustom = true
}

func (v *VM) primaryProfileID() int64 {
	if v.store == nil {
		return 0
	}
	id, _ := v.store.GetPrimaryProfileID(v.sessionID)
	return id
}

func (v *VM) Reload() error {
	if v.store == nil {
		return nil
	}

	profileIDs, err := v.store.GetOrderedProfileIDs(v.sessionID)
	if err != nil {
		return err
	}

	allAliases, err := v.store.LoadAliasesForProfiles(profileIDs)
	if err != nil {
		return err
	}
	aliasesByProfile := make(map[int64][]storage.AliasRule)
	for _, a := range allAliases {
		aliasesByProfile[a.ProfileID] = append(aliasesByProfile[a.ProfileID], a)
	}
	var mergedAliases []storage.AliasRule
	seenAliasNames := make(map[string]bool)
	for _, pid := range profileIDs {
		for _, a := range aliasesByProfile[pid] {
			if !seenAliasNames[a.Name] {
				mergedAliases = append(mergedAliases, a)
				seenAliasNames[a.Name] = true
			}
		}
	}
	v.aliases = mergedAliases

	variables, err := v.store.LoadVariables(v.sessionID)
	if err != nil {
		return err
	}
	v.variables = variables

	allTriggers, err := v.store.LoadTriggersForProfiles(profileIDs)
	if err != nil {
		return err
	}
	triggersByProfile := make(map[int64][]storage.TriggerRule)
	for _, t := range allTriggers {
		triggersByProfile[t.ProfileID] = append(triggersByProfile[t.ProfileID], t)
	}
	var mergedTriggers []storage.TriggerRule
	for _, pid := range profileIDs {
		mergedTriggers = append(mergedTriggers, triggersByProfile[pid]...)
	}
	v.triggers = mergedTriggers

	allHighlights, err := v.store.LoadHighlightsForProfiles(profileIDs)
	if err != nil {
		return err
	}
	highlightsByProfile := make(map[int64][]storage.HighlightRule)
	for _, h := range allHighlights {
		highlightsByProfile[h.ProfileID] = append(highlightsByProfile[h.ProfileID], h)
	}
	var mergedHighlights []storage.HighlightRule
	for _, pid := range profileIDs {
		mergedHighlights = append(mergedHighlights, highlightsByProfile[pid]...)
	}
	v.highlights = mergedHighlights

	allSubstitutes, err := v.store.LoadSubstitutesForProfiles(profileIDs)
	if err != nil {
		return err
	}
	substitutesByProfile := make(map[int64][]storage.SubstituteRule)
	for _, sub := range allSubstitutes {
		substitutesByProfile[sub.ProfileID] = append(substitutesByProfile[sub.ProfileID], sub)
	}
	var mergedSubstitutes []storage.SubstituteRule
	for _, pid := range profileIDs {
		mergedSubstitutes = append(mergedSubstitutes, substitutesByProfile[pid]...)
	}
	v.substitutes = mergedSubstitutes

	return nil
}

// ReloadFromStore reloads raw rules from the backing store and rebuilds all
// compiled caches so that external/UI edits are visible immediately.
func (v *VM) ReloadFromStore() error {
	if v.store == nil {
		return nil
	}
	if err := v.Reload(); err != nil {
		return err
	}
	v.loadedRulesVersion = v.rulesVersion
	v.rebuildCaches()
	return nil
}

func (v *VM) ensureFresh() {
	if v.rulesVersion == v.loadedRulesVersion {
		return
	}
	if v.store != nil {
		if err := v.Reload(); err != nil {
			// Runtime must continue operating even if refresh fails.
			log.Printf("vm reload error: %v", err)
			return
		}
	}
	v.loadedRulesVersion = v.rulesVersion
	v.rebuildCaches()
}

func (v *VM) rebuildCaches() {
	v.compiledTriggers = v.compiledTriggers[:0]
	for i := range v.triggers {
		t := &v.triggers[i]
		ct := compiledTrigger{rule: *t}
		if re, err := regexp.Compile(t.Pattern); err == nil {
			ct.re = re
		} else {
			log.Printf("trigger pattern compile error %q: %v", t.Pattern, err)
		}
		v.compiledTriggers = append(v.compiledTriggers, ct)
	}

	v.compiledHighlights = v.compiledHighlights[:0]
	for i := range v.highlights {
		h := &v.highlights[i]
		ch := compiledHighlight{rule: *h, ansi: highlightToANSI(h)}
		if re, err := regexp.Compile(h.Pattern); err == nil {
			ch.re = re
		}
		v.compiledHighlights = append(v.compiledHighlights, ch)
	}

	v.effectivePatternCache = make(map[string]*regexp.Regexp)
}

func (v *VM) Aliases() []storage.AliasRule          { return v.aliases }
func (v *VM) Variables() map[string]string          { return v.variables }
func (v *VM) Triggers() []storage.TriggerRule       { return v.triggers }
func (v *VM) Highlights() []storage.HighlightRule   { return v.highlights }
func (v *VM) Substitutes() []storage.SubstituteRule { return v.substitutes }
