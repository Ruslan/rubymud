package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

func TestEnsureFreshRebuildsCachesOnFirstCall(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^test`, Command: "ok", Enabled: true},
	}
	if len(v.compiledTriggers) != 0 {
		t.Fatal("expected compiledTriggers to be empty before ensureFresh")
	}
	v.ensureFresh()
	if v.loadedRulesVersion != v.rulesVersion {
		t.Fatalf("expected loadedRulesVersion to be updated")
	}
	if len(v.compiledTriggers) != 1 {
		t.Fatalf("expected 1 compiled trigger, got %d", len(v.compiledTriggers))
	}
}

func TestEnsureFreshSkipsWhenFresh(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^test`, Command: "ok", Enabled: true},
	}
	v.ensureFresh()
	if v.loadedRulesVersion != v.rulesVersion {
		t.Fatal("expected fresh after first ensureFresh")
	}
	// Second call should be a no-op
	v.ensureFresh()
	if v.loadedRulesVersion != v.rulesVersion {
		t.Fatal("version changed unexpectedly on second ensureFresh")
	}
}

func TestCacheInvalidationAfterAddingTrigger(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `^foo`, Command: "bar", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	effects, _ := v.MatchTriggers("foo")
	if len(effects) != 1 {
		t.Fatalf("expected 1 effect, got %d", len(effects))
	}

	v.triggers = append(v.triggers, storage.TriggerRule{Pattern: `^baz`, Command: "qux", Enabled: true})
	v.rulesVersion++

	effects, _ = v.MatchTriggers("baz")
	if len(effects) != 1 || effects[0].Command != "qux" {
		t.Fatalf("expected new trigger to match, got %v", effects)
	}
}

func TestInvalidTriggerPatternSkipped(t *testing.T) {
	v := New(nil, 1)
	v.triggers = []storage.TriggerRule{
		{Pattern: `[`, Command: "bad", Enabled: true},
		{Pattern: `^valid`, Command: "ok", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	effects, _ := v.MatchTriggers("valid")
	if len(effects) != 1 || effects[0].Command != "ok" {
		t.Fatalf("expected valid trigger only, got %v", effects)
	}
}

func TestHighlightCachePrecomputesANSI(t *testing.T) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `test`, FG: "red", Enabled: true}}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	got := v.ApplyHighlights("test")
	if got == "test" {
		t.Fatal("expected highlight applied")
	}

	if len(v.compiledHighlights) != 1 {
		t.Fatalf("expected 1 compiled highlight, got %d", len(v.compiledHighlights))
	}
	if v.compiledHighlights[0].ansi == "" {
		t.Fatal("expected precomputed ansi string")
	}
	if v.compiledHighlights[0].matcher.Regex == nil {
		t.Fatal("expected highlight matcher to be compiled")
	}

	got2 := v.ApplyHighlights("test")
	if got2 != got {
		t.Fatalf("second apply differed: %q vs %q", got2, got)
	}
}

func TestCompiledSubstituteRebuildsForVariableChange(t *testing.T) {
	v := New(nil, 1)
	v.variables["target"] = "orc"
	v.substitutes = []storage.SubstituteRule{
		{ID: 1, Pattern: "$target", Replacement: "monster", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	raw, plain, _ := v.ApplySubsAndCollectOverlays("orc", "orc")
	if raw != "monster" || plain != "monster" {
		t.Fatalf("first apply = %q/%q, want monster", raw, plain)
	}
	if len(v.compiledSubstitutes) != 1 || v.compiledSubstitutes[0].matcher.Regex == nil {
		t.Fatal("expected substitute matcher to be compiled")
	}

	v.variables["target"] = "goblin"
	v.rulesVersion++

	raw, plain, _ = v.ApplySubsAndCollectOverlays("goblin", "goblin")
	if raw != "monster" || plain != "monster" {
		t.Fatalf("second apply = %q/%q, want monster", raw, plain)
	}
}

func TestVariableChangeRebuildsCompiledSubstitutes(t *testing.T) {
	v := New(nil, 1)
	v.variables["target"] = "orc"
	v.substitutes = []storage.SubstituteRule{
		{ID: 1, Pattern: "$target", Replacement: "monster", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	v.ApplySubsAndCollectOverlays("orc", "orc")
	first := v.compiledSubstitutes[0].matcher.EffectivePattern

	v.ProcessInputDetailed("#variable {target} {goblin}")
	raw, plain, _ := v.ApplySubsAndCollectOverlays("goblin", "goblin")
	if raw != "monster" || plain != "monster" {
		t.Fatalf("apply after variable change = %q/%q, want monster", raw, plain)
	}
	second := v.compiledSubstitutes[0].matcher.EffectivePattern
	if first == second {
		t.Fatalf("expected compiled matcher to rebuild with new effective pattern, still %q", second)
	}
}

func TestRebuildCachesCompilesSubstitutes(t *testing.T) {
	v := New(nil, 1)
	v.substitutes = []storage.SubstituteRule{
		{ID: 1, Pattern: "foo", Replacement: "bar", Enabled: true},
	}
	v.rulesVersion = 2
	v.loadedRulesVersion = 1

	v.ApplySubsAndCollectOverlays("foo", "foo")
	if len(v.compiledSubstitutes) != 1 || v.compiledSubstitutes[0].matcher.Regex == nil {
		t.Fatal("expected substitute matcher compiled")
	}

	v.rulesVersion = 3
	v.ensureFresh()
	if len(v.compiledSubstitutes) != 1 || v.compiledSubstitutes[0].matcher.Regex == nil {
		t.Fatalf("expected substitute matcher after rebuild, got %+v", v.compiledSubstitutes)
	}
}

func TestReloadFromStoreRebuildsCompiledCaches(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)

	// Seed an initial trigger via store
	if err := store.SaveTrigger(1, `^old`, "oldcmd", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}
	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore: %v", err)
	}

	effects, _ := v.MatchTriggers("old")
	if len(effects) != 1 || effects[0].Command != "oldcmd" {
		t.Fatalf("expected old trigger, got %v", effects)
	}

	// Simulate external/UI edit: swap trigger directly in DB
	if err := store.DeleteTrigger(1, `^old`); err != nil {
		t.Fatalf("DeleteTrigger: %v", err)
	}
	if err := store.SaveTrigger(1, `^new`, "newcmd", false, "default"); err != nil {
		t.Fatalf("SaveTrigger: %v", err)
	}

	// Before reload, compiled cache is stale and still has old trigger
	effects, _ = v.MatchTriggers("new")
	if len(effects) != 0 {
		t.Fatalf("expected stale cache to miss new trigger, got %v", effects)
	}

	// ReloadFromStore should rebuild caches
	if err := v.ReloadFromStore(); err != nil {
		t.Fatalf("ReloadFromStore: %v", err)
	}

	effects, _ = v.MatchTriggers("new")
	if len(effects) != 1 || effects[0].Command != "newcmd" {
		t.Fatalf("expected new trigger after ReloadFromStore, got %v", effects)
	}
}
