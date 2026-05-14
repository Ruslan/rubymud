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

func TestDynamicHighlightCacheCorrectness(t *testing.T) {
	v := New(nil, 1)
	v.variables["enemy"] = "Крыса"
	v.highlights = []storage.HighlightRule{
		{Pattern: `$enemy`, FG: "red", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	v.ApplyHighlights("Атакует Крыса!")
	cacheSize1 := len(v.effectivePatternCache)

	v.ApplyHighlights("Еще Крыса!")
	cacheSize2 := len(v.effectivePatternCache)

	if cacheSize1 != cacheSize2 {
		t.Errorf("expected effectivePatternCache size to remain %d, got %d", cacheSize1, cacheSize2)
	}
	if cacheSize1 != 1 {
		t.Errorf("expected cache size to be 1, got %d", cacheSize1)
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
	if !strings.Contains(got, "\x1b[31m$unknown\x1b[0m") {
		t.Errorf("expected literal '$unknown' to be highlighted, got %q", got)
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
	if !strings.Contains(got2, "\x1b[31m$enemy\x1b[0m") {
		t.Errorf("expected literal '$enemy' to be highlighted after unvariable, got %q", got2)
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
