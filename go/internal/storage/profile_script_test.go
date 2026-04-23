package storage

import (
	"strings"
	"testing"
)

func TestProfileScript_LegacyCompatibility(t *testing.T) {
	script := `
#nop Profile: Legacy
#alias 'quoted' 'say hello'
#alias "double" "say world"
#highlight {red} {danger} {legacy_group}
#action {legacy act} {say hi} {survival}
`
	ps, err := ParseProfileScript(script)
	if err != nil {
		t.Fatalf("ParseProfileScript: %v", err)
	}

	// Verify Aliases (quoted)
	if len(ps.Aliases) != 2 {
		t.Fatalf("Expected 2 aliases, got %d", len(ps.Aliases))
	}
	if ps.Aliases[0].Name != "quoted" || ps.Aliases[0].Template != "say hello" {
		t.Errorf("Quoted alias 1 mismatch: %+v", ps.Aliases[0])
	}
	if ps.Aliases[1].Name != "double" || ps.Aliases[1].Template != "say world" {
		t.Errorf("Quoted alias 2 mismatch: %+v", ps.Aliases[1])
	}

	// Verify Highlight (legacy group)
	if len(ps.Highlights) != 1 {
		t.Fatalf("Expected 1 highlight, got %d", len(ps.Highlights))
	}
	if ps.Highlights[0].GroupName != "legacy_group" {
		t.Errorf("Legacy highlight group mismatch: %q", ps.Highlights[0].GroupName)
	}

	// Verify Action (legacy group)
	if len(ps.Triggers) != 1 {
		t.Fatalf("Expected 1 trigger, got %d", len(ps.Triggers))
	}
	if ps.Triggers[0].GroupName != "survival" {
		t.Errorf("Legacy trigger group mismatch: %q", ps.Triggers[0].GroupName)
	}
}

func TestImportProfileScript_DeepIntegration(t *testing.T) {
	s := newProfileTestStore(t)

	// 1. Prepare a script with all complex features
	script := `
#nop Profile: DeepIntegration
#nop Description: Testing all fields against real SQL schema

#nop rubymud:rule {"group_name":"Combat group"}
#alias {test_template} {#showme {Template content with $variables}}

#nop rubymud:rule {"bold":true,"italic":true,"underline":true}
#highlight {256:196} {bold_red}

#nop rubymud:rule {"bg":"#0000ff","blink":true,"faint":true,"strikethrough":true,"reverse":true}
#highlight {default} {blink_faint}

#nop rubymud:rule {"is_button":true,"stop_after_match":true,"target_buffer":"chat","buffer_action":"copy"}
#action {^You see (.*)$} {#woutput {chat} {%1}}
`

	// 2. Parse it
	ps, err := ParseProfileScript(script)
	if err != nil {
		t.Fatalf("ParseProfileScript: %v", err)
	}

	// 3. Import into DB
	p, err := s.ImportProfileScript(ps)
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}

	// 4. Verify DB content via Store methods (reading back from SQL)

	// Check Alias Template
	aliases, _ := s.ListAliases(p.ID)
	if len(aliases) != 1 || aliases[0].Template != "#showme {Template content with $variables}" {
		t.Errorf("Alias template not preserved in DB: %q", aliases[0].Template)
	}
	if aliases[0].GroupName != "Combat group" {
		t.Errorf("Alias group not preserved: %q", aliases[0].GroupName)
	}

	// Check Highlight Styles
	highlights, _ := s.ListHighlights(p.ID)
	if len(highlights) != 2 {
		t.Fatalf("Expected 2 highlights in DB, got %d", len(highlights))
	}

	h1 := highlights[0] // bold_red
	if h1.Pattern != "bold_red" || h1.FG != "256:196" || !h1.Bold || !h1.Italic || !h1.Underline {
		t.Errorf("Highlight 1 (bold_red) mismatch: pattern=%q, fg=%q, bold:%v", h1.Pattern, h1.FG, h1.Bold)
	}

	h2 := highlights[1] // blink_faint
	if h2.Pattern != "blink_faint" || h2.FG != "" || h2.BG != "#0000ff" || !h2.Blink || !h2.Faint || !h2.Strikethrough || !h2.Reverse {
		t.Errorf("Highlight 2 (blink_faint) mismatch: pattern=%q, bg=%q, blink:%v", h2.Pattern, h2.BG, h2.Blink)
	}

	// Check Trigger Flags
	triggers, _ := s.ListTriggers(p.ID)
	if len(triggers) != 1 {
		t.Fatalf("Expected 1 trigger in DB, got %d", len(triggers))
	}
	t1 := triggers[0]
	if !t1.IsButton || !t1.StopAfterMatch || t1.TargetBuffer != "chat" || t1.BufferAction != "copy" {
		t.Errorf("Trigger flags mismatch: %+v", t1)
	}

	// 5. Export back and verify round-trip
	exported, err := s.ExportProfileScript(p.ID)
	if err != nil {
		t.Fatalf("ExportProfileScript: %v", err)
	}

	if !strings.Contains(exported, "test_template") || !strings.Contains(exported, "bold_red") {
		t.Errorf("Exported script missing data: %s", exported)
	}

	// Final parse of exported script
	ps2, _ := ParseProfileScript(exported)
	if len(ps2.Highlights) != 2 || ps2.Highlights[0].Bold != true {
		t.Errorf("Exported highlight styles mismatch")
	}
}

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
