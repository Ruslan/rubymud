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
		store:      store,
		sessionID:  sessionID,
		variables:  make(map[string]string),
		varPattern: regexp.MustCompile(`\$([\p{L}\p{N}_]+)`),
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

	return nil
}

func (v *VM) ensureFresh() {
	if v.store == nil {
		return
	}
	if err := v.Reload(); err != nil {
		// Runtime must continue operating even if refresh fails.
		log.Printf("vm reload error: %v", err)
	}
}

func (v *VM) Aliases() []storage.AliasRule        { return v.aliases }
func (v *VM) Variables() map[string]string        { return v.variables }
func (v *VM) Triggers() []storage.TriggerRule     { return v.triggers }
func (v *VM) Highlights() []storage.HighlightRule { return v.highlights }
