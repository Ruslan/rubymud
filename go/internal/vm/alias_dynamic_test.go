package vm

import (
	"testing"
)

func TestAliasDynamicResolution(t *testing.T) {
	v := New(nil, 0)
	v.variables["bag"] = "sack"
	v.variables["vra"] = "2"

	// Define alias with variable
	v.ProcessInputDetailed("#alias {gt} {get %0 $bag}")

	// Invoke and check
	got := v.ProcessInputDetailed("gt sword")
	if len(got) != 1 || got[0].Text != "get sword sack" {
		t.Errorf("Expected 'get sword sack', got %v", got)
	}

	// Change variable and invoke again
	v.variables["bag"] = "backpack"
	got = v.ProcessInputDetailed("gt sword")
	if len(got) != 1 || got[0].Text != "get sword backpack" {
		t.Errorf("Expected 'get sword backpack' after variable change, got %v", got)
	}

	// Define alias with #if and variable
	v.ProcessInputDetailed("#alias {test1} {#if {$vra == 1} {one} {two}}")

	// Invoke with vra=2
	got = v.ProcessInputDetailed("test1")
	if len(got) != 1 || got[0].Text != "two" {
		t.Errorf("Expected 'two' for vra=2, got %v", got)
	}

	// Change vra to 1 and invoke
	v.variables["vra"] = "1"
	got = v.ProcessInputDetailed("test1")
	if len(got) != 1 || got[0].Text != "one" {
		t.Errorf("Expected 'one' for vra=1, got %v", got)
	}

	// Verify #var still uses eager substitution (as per constraints)
	v.variables["y"] = "value_y"
	v.ProcessInputDetailed("#var {x} {$y}")
	if v.variables["x"] != "value_y" {
		t.Errorf("Expected #var to substitute $y at definition time, got %v", v.variables["x"])
	}
}
