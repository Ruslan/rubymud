package vm

import (
	"testing"

	"rubymud/go/internal/storage"
)

func TestApplySubsMultipleMatches(t *testing.T) {
	v := New(nil, 0)
	v.substitutes = []storage.SubstituteRule{{
		ID:          1,
		Pattern:     "foo",
		Replacement: "bar",
		Enabled:     true,
	}}

	raw := "foo middle foo"
	displayRaw, _, overlays := v.ApplySubsAndCollectOverlays(raw, raw)
	want := "bar middle bar"
	if displayRaw != want {
		t.Fatalf("display = %q, want %q", displayRaw, want)
	}
	if len(overlays) != 2 {
		t.Fatalf("overlays = %d, want 2", len(overlays))
	}
}

func TestApplySubsChaining(t *testing.T) {
	v := New(nil, 0)
	// Rules are applied in order. If A -> B and then B -> C exists, it should chain if the system allows it.
	// Current implementation applies rules sequentially on the result of the previous rule.
	v.substitutes = []storage.SubstituteRule{
		{ID: 1, Pattern: "A", Replacement: "B", Enabled: true},
		{ID: 2, Pattern: "B", Replacement: "C", Enabled: true},
	}

	raw := "A"
	displayRaw, _, _ := v.ApplySubsAndCollectOverlays(raw, raw)
	want := "C"
	if displayRaw != want {
		t.Fatalf("chaining display = %q, want %q", displayRaw, want)
	}
}

func TestApplySubsEmptyReplacement(t *testing.T) {
	v := New(nil, 0)
	v.substitutes = []storage.SubstituteRule{{
		ID:          1,
		Pattern:     "foo",
		Replacement: "",
		Enabled:     true,
	}}

	raw := "start foo end"
	displayRaw, _, overlays := v.ApplySubsAndCollectOverlays(raw, raw)
	want := "start  end"
	if displayRaw != want {
		t.Fatalf("display = %q, want %q", displayRaw, want)
	}
	if len(overlays) != 1 || overlays[0].OverlayType != "substitution" {
		t.Fatalf("overlay missing or wrong type: %+v", overlays)
	}
}

func TestApplySubsANSIPreservation(t *testing.T) {
	v := New(nil, 0)
	v.substitutes = []storage.SubstituteRule{{
		ID:          1,
		Pattern:     "target",
		Replacement: "REPLACED",
		Enabled:     true,
	}}

	raw := "\x1b[31mtarget\x1b[0m"
	plain := "target"
	displayRaw, _, _ := v.ApplySubsAndCollectOverlays(raw, plain)
	want := "\x1b[31mREPLACED\x1b[0m"
	if displayRaw != want {
		t.Fatalf("ANSI preservation failed: %q, want %q", displayRaw, want)
	}
}

func TestApplySubsUnknownVariableStaysLiteral(t *testing.T) {
	v := New(nil, 0)
	// $unknown is not in v.variables
	v.substitutes = []storage.SubstituteRule{{
		ID:          1,
		Pattern:     "$unknown",
		Replacement: "found $unknown",
		Enabled:     true,
	}}

	raw := "$unknown text"
	displayRaw, _, _ := v.ApplySubsAndCollectOverlays(raw, raw)
	want := "found $unknown text"
	if displayRaw != want {
		t.Fatalf("unknown variable display = %q, want %q", displayRaw, want)
	}
}

func TestUnsubDeletesGags(t *testing.T) {
	store := newRuntimeTestStore(t)
	v := New(store, 1)

	v.ProcessInputDetailed("#gag {spam}")
	v.ProcessInputDetailed("#unsub {spam}")

	rules, _ := store.ListSubstitutes(1)
	for _, r := range rules {
		if r.Pattern == "spam" {
			t.Fatal("#unsub did not remove gag rule")
		}
	}
}
