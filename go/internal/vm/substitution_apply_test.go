package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

func TestSubstituteCommandsPersistTemplates(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)

	v.ProcessInputDetailed("#sub {$target1} {[F1] $target1}")
	v.ProcessInputDetailed("#substitute {foo} {[%0]}")
	v.ProcessInputDetailed("#gag {spam} {noise}")

	rules, err := store.ListSubstitutes(1)
	if err != nil {
		t.Fatalf("ListSubstitutes: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(rules))
	}
	if rules[0].Pattern != "$target1" || rules[0].Replacement != "[F1] $target1" || rules[0].IsGag {
		t.Fatalf("#sub did not preserve templates: %+v", rules[0])
	}
	if rules[2].Pattern != "spam" || !rules[2].IsGag || rules[2].Replacement != "" || rules[2].GroupName != "noise" {
		t.Fatalf("#gag persisted wrong rule: %+v", rules[2])
	}

	v.ProcessInputDetailed("#unsub {spam}")
	rules, err = store.ListSubstitutes(1)
	if err != nil {
		t.Fatalf("ListSubstitutes after #unsub: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules after #unsub = %d, want 2", len(rules))
	}
}

func TestApplySubsCapturesAndCanonicalReplay(t *testing.T) {
	v := New(nil, 0)
	v.substitutes = []storage.SubstituteRule{{
		ID:          7,
		Pattern:     "Вы сбили (.+) своим",
		Replacement: "%1 ->BASH %9 [%0]",
		Enabled:     true,
	}}

	raw := "Вы сбили орка своим"
	displayRaw, displayPlain, overlays := v.ApplySubsAndCollectOverlays(raw, raw)
	want := "орка ->BASH  [Вы сбили орка своим]"
	if displayRaw != want || displayPlain != want {
		t.Fatalf("display = %q/%q, want %q", displayRaw, displayPlain, want)
	}
	if len(overlays) != 1 {
		t.Fatalf("overlays = %d, want 1", len(overlays))
	}

	replayRaw, replayPlain, hidden := storage.ReplayAppliedOverlays(raw, raw, overlays)
	if hidden {
		t.Fatal("replay unexpectedly hidden")
	}
	if replayRaw != want || replayPlain != want {
		t.Fatalf("replay = %q/%q, want %q", replayRaw, replayPlain, want)
	}
}

func TestApplySubsVariablesAreApplyTimeLiteralRegex(t *testing.T) {
	v := New(nil, 0)
	v.variables["target1"] = `King\dark`
	v.substitutes = []storage.SubstituteRule{{
		ID:          1,
		Pattern:     "$target1",
		Replacement: "[F1] $target1",
		Enabled:     true,
	}}

	displayRaw, displayPlain, overlays := v.ApplySubsAndCollectOverlays(`King\dark and Kingxdark`, `King\dark and Kingxdark`)
	want := `[F1] King\dark and Kingxdark`
	if displayRaw != want || displayPlain != want {
		t.Fatalf("display = %q/%q, want %q", displayRaw, displayPlain, want)
	}
	if len(overlays) != 1 {
		t.Fatalf("overlays = %d, want 1", len(overlays))
	}

	v.variables["target1"] = "Different"
	replayRaw, replayPlain, hidden := storage.ReplayAppliedOverlays(`King\dark and Kingxdark`, `King\dark and Kingxdark`, overlays)
	if hidden || replayRaw != want || replayPlain != want {
		t.Fatalf("replay after variable change = %q/%q hidden=%v, want %q", replayRaw, replayPlain, hidden, want)
	}
}

func TestApplySubsIgnoresZeroWidthMatches(t *testing.T) {
	v := New(nil, 0)
	v.substitutes = []storage.SubstituteRule{{ID: 1, Pattern: "^", Replacement: "x", Enabled: true}}

	displayRaw, displayPlain, overlays := v.ApplySubsAndCollectOverlays("foo", "foo")
	if displayRaw != "foo" || displayPlain != "foo" {
		t.Fatalf("display = %q/%q, want foo", displayRaw, displayPlain)
	}
	if len(overlays) != 0 {
		t.Fatalf("overlays = %d, want 0", len(overlays))
	}
}

func TestCheckGagUsesLiteralVariablePattern(t *testing.T) {
	v := New(nil, 0)
	v.variables["spam"] = `a.b`
	v.substitutes = []storage.SubstituteRule{{ID: 2, Pattern: "$spam", IsGag: true, Enabled: true}}

	if _, gagged := v.CheckGag("axb"); gagged {
		t.Fatal("gag matched regex interpretation of variable value")
	}
	if overlay, gagged := v.CheckGag("a.b"); !gagged || overlay.OverlayType != "gag" {
		t.Fatalf("gag literal match = %+v, %v", overlay, gagged)
	}
}
