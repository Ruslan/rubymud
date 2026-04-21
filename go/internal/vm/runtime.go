package vm

import (
	"log"
	"regexp"

	"rubymud/go/internal/storage"
)

func New(store *storage.Store, sessionID int64) *VM {
	return &VM{
		store:      store,
		sessionID:  sessionID,
		variables:  make(map[string]string),
		varPattern: regexp.MustCompile(`\$([\p{L}\p{N}_]+)`),
	}
}

func (v *VM) Reload() error {
	if v.store == nil {
		return nil
	}

	aliases, err := v.store.LoadAliases(v.sessionID)
	if err != nil {
		return err
	}
	v.aliases = aliases

	variables, err := v.store.LoadVariables(v.sessionID)
	if err != nil {
		return err
	}
	v.variables = variables

	triggers, err := v.store.LoadTriggers(v.sessionID)
	if err != nil {
		return err
	}
	v.triggers = triggers

	highlights, err := v.store.LoadHighlights(v.sessionID)
	if err != nil {
		return err
	}
	v.highlights = highlights

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
