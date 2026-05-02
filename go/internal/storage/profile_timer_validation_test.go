package storage

import (
	"testing"
)

func TestProfileTimer_ValidationHardening(t *testing.T) {
	s := newProfileTestStore(t)

	// 1. Validate #tickat range during import - FAIL explicitly
	script1 := `
#nop Profile: ValidationTest
#ticksize {short} {10}
#tickat {short} {5} {valid}
#tickat {short} {11} {invalid_out_of_range}
`
	_, err := ParseProfileScript(script1)
	if err == nil {
		t.Error("Expected error for out-of-range #tickat, got nil")
	}

	// 2. Handle invalid #tickmode - FAIL explicitly
	script2 := `
#nop Profile: ModeTest
#tickmode {m1} {oneshot}
`
	_, err = ParseProfileScript(script2)
	if err == nil {
		t.Error("Expected error for invalid #tickmode, got nil")
	}

	// 3. Define and implement behavior for #ticksize {name} {0} - FAIL explicitly
	script3 := `
#nop Profile: ZeroSizeTest
#ticksize {z} {0}
`
	_, err = ParseProfileScript(script3)
	if err == nil {
		t.Error("Expected error for #ticksize 0, got nil")
	}

	// 4. Valid timer scripts still import correctly (sanity check)
	script4 := `
#nop Profile: Sanity
#tickicon {s} {🛡️}
#ticksize {s} {60}
#tickmode {s} {repeating}
#tickat {s} {0} {ping}
`
	ps4, err := ParseProfileScript(script4)
	if err != nil {
		t.Fatalf("ParseProfileScript failed for valid input: %v", err)
	}
	p4, err := s.ImportProfileScript(ps4)
	if err != nil {
		t.Fatalf("ImportProfileScript: %v", err)
	}
	timers4, _ := s.GetProfileTimers(p4.ID)
	if len(timers4) != 1 || timers4[0].Name != "s" || timers4[0].Icon != "🛡️" {
		t.Errorf("Sanity import failed: %v", timers4)
	}
}
