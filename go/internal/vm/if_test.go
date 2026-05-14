package vm

import (
	"rubymud/go/internal/storage"
	"strings"
	"testing"
)

func TestCmdIf(t *testing.T) {
	v := New(nil, 0)
	v.variables["hp"] = "40"
	v.variables["target"] = "orc"

	tests := []struct {
		input string
		want  []Result
	}{
		{
			"#if {$hp == 40} {heal}",
			[]Result{{Text: "heal", Kind: ResultCommand}},
		},
		{
			"#if {$hp == 50} {heal}",
			nil,
		},
		{
			"#if {$hp == 50} {heal} {drink}",
			[]Result{{Text: "drink", Kind: ResultCommand}},
		},
		{
			"#if {$target == \"orc\"} {kill $target}",
			[]Result{{Text: "kill orc", Kind: ResultCommand}},
		},
		{
			"#if {$missing == \"\"} {echo yes}",
			[]Result{{Text: "echo yes", Kind: ResultCommand}},
		},
		{
			"#if {1 + 1 == 2} {#var {a} {1}; #showme {ok}}",
			[]Result{
				{Text: "#variable {a} = {1}", Kind: ResultEcho},
				{Text: "ok", Kind: ResultEcho, TargetBuffer: "main"},
			},
		},
		{
			"#if {$hp == 40} {#if {1 == 1} {nested}}",
			[]Result{{Text: "nested", Kind: ResultCommand}},
		},
		{
			"#if {$hp > 10} {then}",
			[]Result{{Text: "then", Kind: ResultCommand}},
		},
	}

	for _, tt := range tests {
		got := v.ProcessInputDetailed(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ProcessInputDetailed(%q) returned %d results, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i].Text != tt.want[i].Text || got[i].Kind != tt.want[i].Kind {
				t.Errorf("ProcessInputDetailed(%q) result[%d] = %v, want %v", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIfLazy(t *testing.T) {
	v := New(nil, 0)
	v.variables["hp"] = "40"

	v.ProcessInputDetailed("#if {$hp == 50} {#var {executed} {true}}")
	if _, ok := v.variables["executed"]; ok {
		t.Error("Non-selected branch was executed (variable set)")
	}

	v.ProcessInputDetailed("#if {$hp == 40} {#nop} {#var {executed} {true}}")
	if _, ok := v.variables["executed"]; ok {
		t.Error("Non-selected else branch was executed (variable set)")
	}

	// Test alias inside selected branch
	v.aliases = append(v.aliases, storage.AliasRule{Name: "greet", Template: "say hello", Enabled: true})
	got := v.ProcessInputDetailed("#if {1 == 1} {greet}")
	if len(got) != 1 || got[0].Text != "say hello" {
		t.Errorf("Alias inside #if failed: %v", got)
	}
}

func TestIfErrors(t *testing.T) {
	v := New(nil, 0)
	v.variables["hp"] = "40"

	tests := []struct {
		input        string
		wantContains string
	}{
		{"#if {1 + 1} {then}", "must return boolean"},
		{"#if {} {then}", "missing expression"},
		{"#if", "missing expression"},
		{"#if {true} {}", "missing then-branch"},
		{"#if {true} {then} {else} {extra}", "too many arguments"},
		{"#if {invalid++} {then} {else}", "unsupported word operator"},
		{"#if {len(\"abc\") == 3} {then} {else}", "unsupported word operator"},
		{"#if {a ? b : c} {then} {else}", "unsupported word operator"},
		{"#if {a[0] == 1} {then} {else}", "unsupported word operator"},
	}

	for _, tt := range tests {
		got := v.ProcessInputDetailed(tt.input)
		found := false
		for _, r := range got {
			if r.Kind == ResultEcho && strings.Contains(r.Text, tt.wantContains) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ProcessInputDetailed(%q) did not return expected error %q. Got: %v", tt.input, tt.wantContains, got)
		}
	}
}

func TestIfDepth(t *testing.T) {
	v := New(nil, 0)
	// maxExpandDepth is 10.
	input := "#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {#if {1==1} {too deep}}}}}}}}}}}"
	got := v.ProcessInputDetailed(input)

	found := false
	for _, r := range got {
		if r.Kind == ResultCommand && strings.Contains(r.Text, "too deep") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Depth protection failed or too deep command not found: %v", got)
	}
}
