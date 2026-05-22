package vm

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func TestDynamicHighlightVariable(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got := v.ApplyHighlights("Атакует Крыса!")
	if !strings.Contains(got, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' to be highlighted red, got %q", got)
	}
}

func TestDynamicHighlightLiteralEscaping(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "a.b"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got := v.ApplyHighlights("axb a.b")
	if strings.Contains(got, "\x1b[31maxb\x1b[0m") {
		t.Errorf("regex meta characters should be escaped, matched 'axb' incorrectly in %q", got)
	}
	if !strings.Contains(got, "\x1b[31ma.b\x1b[0m") {
		t.Errorf("expected literal 'a.b' to be highlighted, got %q", got)
	}
}

func TestDynamicHighlightVariableChange(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got1 := v.ApplyHighlights("Атакует Крыса!")
	if !strings.Contains(got1, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' to be highlighted, got %q", got1)
	}

	v.ProcessInputDetailed("#variable {enemy} {Волк}")

	got2 := v.ApplyHighlights("Атакует Волк, а Крыса убегает!")
	if !strings.Contains(got2, "\x1b[31mВолк\x1b[0m") {
		t.Errorf("expected 'Волк' to be highlighted after variable change, got %q", got2)
	}
	if strings.Contains(got2, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' NOT to be highlighted after variable change, got %q", got2)
	}
}

func TestDynamicHighlightCompiledMatcherReusedWhenFresh(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	v.ApplyHighlights("Атакует Крыса!")
	if len(v.compiledHighlights) != 1 || v.compiledHighlights[0].matcher.Regex == nil {
		t.Fatal("expected highlight matcher to be compiled")
	}
	first := v.compiledHighlights[0].matcher.Regex

	v.ApplyHighlights("Еще Крыса!")
	second := v.compiledHighlights[0].matcher.Regex

	if first != second {
		t.Errorf("expected compiled highlight matcher to be reused while VM is fresh")
	}
}

func TestDynamicHighlightMultipleVariables(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.variables["weapon"] = "Меч"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
		{Pattern: `$weapon`, FG: "blue", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got := v.ApplyHighlights("Крыса бьет об Меч")
	if !strings.Contains(got, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' to be highlighted red, got %q", got)
	}
	if !strings.Contains(got, "\x1b[34mМеч\x1b[0m") {
		t.Errorf("expected 'Меч' to be highlighted blue, got %q", got)
	}
}

func TestDynamicHighlightUnknownVariable(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{
		{Pattern: `$unknown`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got := v.ApplyHighlights("literal $unknown text")
	if strings.Contains(got, "\x1b[31m") {
		t.Errorf("expected undefined variable $unknown to expand to empty string and NOT match, got %q", got)
	}
}

func TestDynamicHighlightUnvariable(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got1 := v.ApplyHighlights("Атакует Крыса!")
	if !strings.Contains(got1, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' to be highlighted, got %q", got1)
	}

	v.ProcessInputDetailed("#unvariable {enemy}")

	got2 := v.ApplyHighlights("Атакует Крыса, но $enemy это literal")
	if strings.Contains(got2, "\x1b[31mКрыса\x1b[0m") {
		t.Errorf("expected 'Крыса' NOT to be highlighted after unvariable, got %q", got2)
	}
	if strings.Contains(got2, "\x1b[31m") {
		t.Errorf("expected undefined variable after unvariable to expand to empty string and NOT match, got %q", got2)
	}
}

func TestDynamicHighlightStoreVariableChange(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)

	if err := store.SaveHighlight(1, storage.HighlightRule{Pattern: `$enemy`, FG: "red", Enabled: true, GroupName: "default"}); err != nil {
		t.Fatalf("SaveHighlight: %v", err)
	}
	if err := store.SetVariable(1, "enemy", "Goblin"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}

	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore: %v", err)
	}

	got1 := v.ApplyHighlights("A wild Goblin appears!")
	if !strings.Contains(got1, "\x1b[31mGoblin\x1b[0m") {
		t.Fatalf("expected 'Goblin' to be highlighted, got %q", got1)
	}

	v.ProcessInputDetailed("#variable {enemy} {Troll}")

	got2 := v.ApplyHighlights("A wild Troll and Goblin appear!")
	if !strings.Contains(got2, "\x1b[31mTroll\x1b[0m") {
		t.Errorf("expected 'Troll' to be highlighted after variable change, got %q", got2)
	}
	if strings.Contains(got2, "\x1b[31mGoblin\x1b[0m") {
		t.Errorf("expected 'Goblin' NOT to be highlighted after variable change, got %q", got2)
	}
}
