package storage

import (
	"strings"
	"testing"
)

func TestProfileScript_ExportAndParse(t *testing.T) {
	s := newProfileTestStore(t)

	// Create profile
	p, err := s.CreateProfile("FullFeature", "A very complex profile")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Add Alias
	s.SaveAlias(p.ID, "atk", "kill $1", true, "combat")

	// Add Triggers
	// 1. Normal
	s.SaveTrigger(p.ID, "You are hungry", "eat bread", false, "survival")
	// 2. Complex with all flags
	s.db.Create(&TriggerRule{
		ProfileID:      p.ID,
		Position:       2,
		Name:           "StunnedTrigger",
		Pattern:        "You are stunned",
		Command:        "flee",
		IsButton:       true,
		Enabled:        true,
		StopAfterMatch: true,
		GroupName:      "combat",
	})

	// Add Highlights
	// 1. Normal
	s.SaveHighlight(p.ID, HighlightRule{
		Pattern:   "danger",
		FG:        "256:196",
		Enabled:   true,
		GroupName: "alerts",
	})
	// 2. Complex with all styles
	s.SaveHighlight(p.ID, HighlightRule{
		Pattern:       "magic",
		FG:            "#ff00ff",
		BG:            "#000000",
		Bold:          true,
		Faint:         true,
		Italic:        true,
		Underline:     true,
		Strikethrough: true,
		Blink:         true,
		Reverse:       true,
		Enabled:       true,
		GroupName:     "spells",
	})

	// Add Hotkey
	s.CreateHotkey(p.ID, "f1", "north")

	// Export
	exported, err := s.ExportProfileScript(p.ID)
	if err != nil {
		t.Fatalf("ExportProfileScript: %v", err)
	}

	if !strings.Contains(exported, "#nop Profile: FullFeature") {
		t.Errorf("Export missing profile name: %s", exported)
	}
	if !strings.Contains(exported, "A very complex profile") {
		t.Errorf("Export missing description: %s", exported)
	}

	// Parse back
	ps, err := ParseProfileScript(exported)
	if err != nil {
		t.Fatalf("ParseProfileScript: %v", err)
	}

	if ps.Name != "FullFeature" {
		t.Errorf("Parsed name = %q, want FullFeature", ps.Name)
	}
	if ps.Description != "A very complex profile" {
		t.Errorf("Parsed desc = %q, want A very complex profile", ps.Description)
	}

	// Check Aliases
	if len(ps.Aliases) != 1 {
		t.Fatalf("Parsed %d aliases, want 1", len(ps.Aliases))
	}
	if a := ps.Aliases[0]; a.Name != "atk" || a.Template != "kill $1" {
		t.Errorf("Parsed alias = %+v, want atk -> kill $1", a)
	}

	// Check Triggers
	if len(ps.Triggers) != 2 {
		t.Fatalf("Parsed %d triggers, want 2", len(ps.Triggers))
	}
	t2 := ps.Triggers[1]
	if t2.Pattern != "You are stunned" || t2.Command != "flee" {
		t.Errorf("Parsed trigger 2 pattern/cmd = %q / %q", t2.Pattern, t2.Command)
	}
	if !t2.IsButton || !t2.StopAfterMatch || t2.Name != "StunnedTrigger" {
		t.Errorf("Parsed trigger 2 flags = button:%v, stop:%v, name:%q", t2.IsButton, t2.StopAfterMatch, t2.Name)
	}

	// Check Highlights
	if len(ps.Highlights) != 2 {
		t.Fatalf("Parsed %d highlights, want 2", len(ps.Highlights))
	}
	h2 := ps.Highlights[1]
	if h2.Pattern != "magic" || h2.FG != "#ff00ff" || h2.BG != "#000000" {
		t.Errorf("Parsed highlight 2 = %+v", h2)
	}
	if !h2.Bold || !h2.Italic || !h2.Blink || !h2.Reverse || !h2.Underline || !h2.Strikethrough || !h2.Faint {
		t.Errorf("Parsed highlight 2 missing style flags: %+v", h2)
	}

	// Check Hotkeys
	if len(ps.Hotkeys) != 1 {
		t.Fatalf("Parsed %d hotkeys, want 1", len(ps.Hotkeys))
	}
	if hk := ps.Hotkeys[0]; hk.Shortcut != "f1" || hk.Command != "north" {
		t.Errorf("Parsed hotkey = %+v", hk)
	}

	// Import it back into a new profile
	ps.Name = "ImportedFull"
	importedProfile, err := s.ImportProfileScript(ps)
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}

	// Verify imported rules in DB
	importedAliases, _ := s.ListAliases(importedProfile.ID)
	if len(importedAliases) != 1 || importedAliases[0].Name != "atk" {
		t.Errorf("Imported aliases mismatch: %+v", importedAliases)
	}
}

func TestParseProfileScript_Malformed(t *testing.T) {
	script := `
#nop Profile: Garbage
#nop Some random comment

#alias {incomplete
#alias {ok} {fine}
#alias   {extra}  {spaces}

#nop rubymud:rule {"broken_json": true
#action {broken json} {should ignore meta}

#nop rubymud:rule {"is_button": true}
#action {missing command}

#highlight {red}
#highlight {blue} {pattern} {grp} {extra arg}

#hotkey {f1}
#hotkey {f2} {south}

garbage line without hash
`

	ps, err := ParseProfileScript(script)
	if err != nil {
		t.Fatalf("ParseProfileScript on malformed: %v", err)
	}

	if ps.Name != "Garbage" {
		t.Errorf("Name = %q", ps.Name)
	}

	// Should parse the valid alias, ignoring the incomplete one
	if len(ps.Aliases) != 2 {
		t.Errorf("Expected 2 aliases parsed from malformed input, got %d", len(ps.Aliases))
	}

	// Should parse the trigger with broken json (ignoring json)
	if len(ps.Triggers) != 1 {
		t.Errorf("Expected 1 trigger, got %d", len(ps.Triggers))
	} else if ps.Triggers[0].Pattern != "broken json" {
		t.Errorf("Trigger pattern = %q", ps.Triggers[0].Pattern)
	}

	// Highlight: one missing pattern, one with extra args
	if len(ps.Highlights) != 1 {
		t.Errorf("Expected 1 highlight, got %d", len(ps.Highlights))
	} else if ps.Highlights[0].Pattern != "pattern" {
		t.Errorf("Highlight pattern = %q", ps.Highlights[0].Pattern)
	}

	// Hotkey: one missing command
	if len(ps.Hotkeys) != 1 {
		t.Errorf("Expected 1 hotkey, got %d", len(ps.Hotkeys))
		} else if ps.Hotkeys[0].Shortcut != "f2" {
			t.Errorf("Hotkey shortcut = %q", ps.Hotkeys[0].Shortcut)
		}
}

func TestImportProfileScript_ReplacesExistingProfileByName(t *testing.T) {
	s := newProfileTestStore(t)

	p, err := s.CreateProfile("ReplaceMe", "old")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := s.SaveAlias(p.ID, "old", "old command", true, "default"); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}
	if err := s.AddProfileToSession(1, p.ID, 1); err != nil {
		t.Fatalf("AddProfileToSession: %v", err)
	}

	imported, err := s.ImportProfileScript(&ProfileScript{
		Name:        "ReplaceMe",
		Description: "new",
		Aliases: []AliasRule{{Position: 1, Name: "new", Template: "new command", Enabled: true, GroupName: "default"}},
	})
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}
	if imported.ID != p.ID {
		t.Fatalf("expected import to reuse profile id %d, got %d", p.ID, imported.ID)
	}

	aliases, err := s.ListAliases(p.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 || aliases[0].Name != "new" {
		t.Fatalf("expected replaced aliases, got %+v", aliases)
	}

	entries, err := s.GetSessionProfiles(1)
	if err != nil {
		t.Fatalf("GetSessionProfiles: %v", err)
	}
	if len(entries) != 1 || entries[0].ProfileID != p.ID {
		t.Fatalf("expected existing session attachment to remain, got %+v", entries)
	}
}
