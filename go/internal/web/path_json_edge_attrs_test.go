package web

import (
	"reflect"
	"strings"
	"testing"

	"rubymud/go/internal/mapper"
	"rubymud/go/internal/storage"
)

// pathResultJSON is what the map pane sends to the MUD: the UI joins `directions`
// with ';' and submits it verbatim. So the ONE thing that must hold is that the
// list is a complete, walkable script — including the door-opening commands, which
// is exactly what the route used to omit (it truncated at the first door and told
// the player to open it by hand).
//
// These are unit tests on the renderer rather than end-to-end route tests because
// mapper's own tests already pin what a PathStep contains; what is unowned here is
// the flattening of steps into commands.

func dirsOf(t *testing.T, res mapper.PathResult) []string {
	t.Helper()
	out := pathResultJSON(res)
	dirs, ok := out["directions"].([]string)
	if !ok {
		t.Fatalf("directions missing or wrong type: %#v", out)
	}
	return dirs
}

// TestPathJSONPutsTheOpeningCommandBeforeTheDoorHop — the fix. Two hops, a door on
// the second: three commands, with the opening action between them.
func TestPathJSONPutsTheOpeningCommandBeforeTheDoorHop(t *testing.T) {
	res := mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "e", Dir: "e"},
		{Command: "n", Dir: "n", Door: true, DoorKind: mapper.DoorConfirmed, OpenAction: "откр две север"},
	}}
	got := dirsOf(t, res)
	want := []string{"e", "откр две север", "n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("directions = %q, want %q — the walk must not stop at the door", got, want)
	}
	out := pathResultJSON(res)
	if out["doors"] != 1 {
		t.Errorf("doors = %v, want 1", out["doors"])
	}
}

func TestPathJSONIncludesTotalWeight(t *testing.T) {
	out := pathResultJSON(mapper.PathResult{Reachable: true, TotalWeight: 1.5})
	if out["total_weight"] != 1.5 {
		t.Errorf("total_weight = %#v, want 1.5", out["total_weight"])
	}
}

// TestPathJSONEmitsThePhraseInsteadOfTheDirection — п.2.1 on the wire. A letter
// does not take this passage, so a letter must not be what the pane sends.
func TestPathJSONEmitsThePhraseInsteadOfTheDirection(t *testing.T) {
	res := mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "двигать ящик", Dir: "d", Phrase: "двигать ящик"},
		{Command: "w", Dir: "w"},
	}}
	got := dirsOf(t, res)
	want := []string{"двигать ящик", "w"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("directions = %q, want %q", got, want)
	}
	if pathResultJSON(res)["phrases"] != 1 {
		t.Errorf("phrases not counted: %#v", pathResultJSON(res))
	}
}

// A phrase behind a door needs BOTH, in order: open, then the phrase.
func TestPathJSONCombinesAnOpeningCommandWithAPhrase(t *testing.T) {
	res := mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "лезть ковёр", Dir: "u", Phrase: "лезть ковёр",
			Door: true, DoorKind: mapper.DoorConfirmed, OpenAction: "дёрнуть рычаг", Locked: true},
	}}
	got := dirsOf(t, res)
	want := []string{"дёрнуть рычаг", "лезть ковёр"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("directions = %q, want %q", got, want)
	}
	if pathResultJSON(res)["locked"] != 1 {
		t.Errorf("a locked hop was not reported: %#v", pathResultJSON(res))
	}
	if got := pathResultJSON(res)["summary"]; got != "1 step, doors: 1 (opening included in route), ⚠ locked by default: 1, phrase passages: 1" {
		t.Errorf("singular summary = %q", got)
	}
}

// TestPathJSONFlagsAVetoOnlyRefusal — п.2.3 on the wire. "No route" and "no route
// you allow me to take" are different answers and must not look the same, or the
// player hunts for a hole in the map that is not there.
func TestPathJSONFlagsAVetoOnlyRefusal(t *testing.T) {
	out := pathResultJSON(mapper.PathResult{
		Reachable: false, VetoedOnly: true, Reason: "a route exists only through edges marked \"avoid in routes\"",
	})
	if out["vetoed_only"] != true {
		t.Errorf("vetoed_only not surfaced: %#v", out)
	}
	if out["reason"] == "" {
		t.Errorf("reason dropped: %#v", out)
	}
	// An ordinary unreachable must NOT carry the flag.
	plain := pathResultJSON(mapper.PathResult{Reachable: false, Reason: "no route"})
	if _, present := plain["vetoed_only"]; present {
		t.Errorf("an ordinary refusal claimed a veto: %#v", plain)
	}
}

// A door-free route is byte-for-byte what it always was.
func TestPathJSONLeavesAnOrdinaryRouteAlone(t *testing.T) {
	res := mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "n", Dir: "n"}, {Command: "e", Dir: "e"},
	}}
	got := dirsOf(t, res)
	if !reflect.DeepEqual(got, []string{"n", "e"}) {
		t.Errorf("directions = %q", got)
	}
	out := pathResultJSON(res)
	if out["summary"] != "2 steps" {
		t.Errorf("plural summary = %q", out["summary"])
	}
	for _, k := range []string{"doors", "door_at", "locked", "phrases", "vetoed_only"} {
		if _, present := out[k]; present {
			t.Errorf("%q leaked into a plain route: %#v", k, out)
		}
	}
}

// The pane prints `summary` verbatim, so every clause must use the mapper's
// English interface language.
func TestPathJSONSummaryIsEnglishThroughout(t *testing.T) {
	out := pathResultJSON(mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "n", Dir: "n", Seam: true},
		{Command: "фраза", Dir: "e", Phrase: "фраза",
			Door: true, DoorKind: mapper.DoorConfirmed, OpenAction: "откр две восток", Locked: true},
	}})
	summary, _ := out["summary"].(string)
	if summary == "" {
		t.Fatalf("no summary: %#v", out)
	}
	for _, want := range []string{"steps", "seams", "doors", "locked", "phrase passages"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q: %q", want, summary)
		}
	}
}

// door_at is gone: it named an index into `directions`, and `directions` now holds
// the opening commands too, so the index addressed something other than the hop it
// promised. Nothing truncates at a door any more, so the count is the whole story.
func TestPathJSONDropsTheStaleDoorIndex(t *testing.T) {
	out := pathResultJSON(mapper.PathResult{Reachable: true, Steps: []mapper.PathStep{
		{Command: "e", Dir: "e"},
		{Command: "n", Dir: "n", Door: true, DoorKind: mapper.DoorConfirmed, OpenAction: "откр две север"},
	}})
	if _, present := out["door_at"]; present {
		t.Errorf("door_at is back, and it no longer indexes what its name says: %#v", out)
	}
	if out["doors"] != 1 {
		t.Errorf("doors = %v, want 1", out["doors"])
	}
}

// A phrase or an opening command that is a bare direction would be pushed onto the
// tracker's pending queue when the route emits it — a phantom step that moves the
// player's recorded position. Both write surfaces refuse it; this pins the shared
// validator they call.
func TestEdgeCommandsRefuseAMovementCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch storage.EdgeAttrPatch
		want  bool
	}{
		{"a russian direction word as a phrase", storage.EdgeAttrPatch{Phrase: strPtrW("вверх")}, true},
		{"an english letter as an opening command", storage.EdgeAttrPatch{OpenCmd: strPtrW("u")}, true},
		{"a direction with stray spacing", storage.EdgeAttrPatch{OpenCmd: strPtrW("  Север ")}, true},
		{"a real phrase", storage.EdgeAttrPatch{Phrase: strPtrW("двигать ящик")}, false},
		{"a real opening command", storage.EdgeAttrPatch{OpenCmd: strPtrW("откр две север")}, false},
		{"a non-direction single word", storage.EdgeAttrPatch{OpenCmd: strPtrW("рычаг")}, false},
		{"clearing a field", storage.EdgeAttrPatch{Phrase: strPtrW("")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := edgeCommandsRefusal(tc.patch)
			if (reason != "") != tc.want {
				t.Errorf("refusal = %q, want refused=%v", reason, tc.want)
			}
		})
	}
}

func strPtrW(s string) *string { return &s }
