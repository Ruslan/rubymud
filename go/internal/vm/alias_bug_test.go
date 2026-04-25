package vm

import (
	"regexp"
	"testing"
	"rubymud/go/internal/storage"
)

func TestAliasSemicolonBug(t *testing.T) {
	v := New(nil, 1)
	v.aliases = []storage.AliasRule{
		{Name: "testalias", Template: "say %1;say %2", Enabled: true},
		{Name: "ггбаф", Template: "гг %1;#woutput {buffs} {[$TIME] %2}", Enabled: true},
	}

	// Test 1: Simple commands
	// Verifies that a semicolon in an alias template correctly splits results
	results1 := v.ProcessInputDetailed("testalias {hello} {world}")
	if len(results1) != 2 {
		t.Fatalf("testalias: expected 2 results, got %d: %+v", len(results1), results1)
	}
	
	cases1 := []struct {
		text string
		kind ResultKind
	}{
		{"say hello", ResultCommand},
		{"say world", ResultCommand},
	}
	for i, c := range cases1 {
		if results1[i].Text != c.text {
			t.Errorf("testalias[%d] text: expected %q, got %q", i, c.text, results1[i].Text)
		}
		if results1[i].Kind != c.kind {
			t.Errorf("testalias[%d] kind: expected %v, got %v", i, c.kind, results1[i].Kind)
		}
	}

	// Test 2: Complex with #woutput and nested braces
	// This specifically protects against the bug where space-based splitting broke multi-word braced arguments
	// causing semicolons to be mis-parsed as part of the argument.
	results2 := v.ProcessInputDetailed("ггбаф {<<< -ЗОЗ>>>} {-ЗОЗ}")
	if len(results2) != 2 {
		t.Fatalf("ггбаф: expected 2 results, got %d: %+v", len(results2), results2)
	}

	// 1. First part is a MUD command (ResultCommand)
	if results2[0].Text != "гг <<< -ЗОЗ>>>" {
		t.Errorf("ггбаф[0] text: expected 'гг <<< -ЗОЗ>>>', got %q", results2[0].Text)
	}
	if results2[0].Kind != ResultCommand {
		t.Errorf("ггбаф[0] kind: expected ResultCommand, got %v", results2[0].Kind)
	}

	// 2. Second part is a local echo routed to the 'buffs' pane (ResultEcho)
	if results2[1].Kind != ResultEcho {
		t.Errorf("ггбаф[1] kind: expected ResultEcho, got %v", results2[1].Kind)
	}
	if results2[1].TargetBuffer != "buffs" {
		t.Errorf("ггбаф[1] buffer: expected 'buffs', got %q", results2[1].TargetBuffer)
	}

	// Check output format: [HH:MM:SS] -ЗОЗ
	timePattern := regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] -ЗОЗ$`)
	if !timePattern.MatchString(results2[1].Text) {
		t.Errorf("ггбаф[1] text: output %q does not match expected format [HH:MM:SS] -ЗОЗ", results2[1].Text)
	}
}
