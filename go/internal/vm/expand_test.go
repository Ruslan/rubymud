package vm

import (
	"testing"
	"time"
)

// TestBuiltinVarAtFormatsInInstantZone verifies the time builtins render for the
// exact instant/zone they are given. Deterministic: no wall-clock reads.
func TestBuiltinVarAtFormatsInInstantZone(t *testing.T) {
	// 2026-07-06 23:45:12 UTC rendered in +05:30 rolls to the next calendar day
	// (2026-07-07 05:15:12), which makes DATE/HOUR sensitive to the zone.
	kolkata := time.FixedZone("+0530", 5*3600+30*60)
	instant := time.Date(2026, 7, 6, 23, 45, 12, 0, time.UTC).In(kolkata)

	cases := map[string]string{
		"DATE":      "07-07-2026",
		"TIME":      "05:15:12",
		"HOUR":      "05",
		"MINUTE":    "15",
		"SECOND":    "12",
		"TIMESTAMP": "1783381512", // epoch is zone-independent
	}
	for key, want := range cases {
		got, ok := builtinVarAt(instant, key)
		if !ok {
			t.Fatalf("builtinVarAt(%q) missing", key)
		}
		if got != want {
			t.Fatalf("builtinVarAt(%q) = %q, want %q", key, got, want)
		}
	}

	if _, ok := builtinVarAt(instant, "NONEXISTENT"); ok {
		t.Fatalf("builtinVarAt(NONEXISTENT) should not exist")
	}
}

// TestVMLocationWiring verifies SetLocation threads the chosen zone into
// expansion, and that a fresh VM defaults to UTC (no client attached case).
func TestVMLocationWiring(t *testing.T) {
	v := New(nil, 1)
	if v.location() != time.UTC {
		t.Fatalf("fresh VM location = %v, want UTC", v.location())
	}

	east := time.FixedZone("east", 5*3600)
	v.SetLocation(east)
	if v.location() != east {
		t.Fatalf("after SetLocation, location = %v, want east", v.location())
	}

	// Same instant, two zones 10h apart => hour strings must differ, proving the
	// VM location (not the host zone) drives expansion.
	west := time.FixedZone("west", -5*3600)
	instant := time.Now()
	eastHour, _ := builtinVarAt(instant.In(east), "HOUR")
	westHour, _ := builtinVarAt(instant.In(west), "HOUR")
	if eastHour == westHour {
		t.Fatalf("HOUR did not follow zone: east=%s west=%s", eastHour, westHour)
	}

	// SetLocation(nil) resets to UTC.
	v.SetLocation(nil)
	if v.location() != time.UTC {
		t.Fatalf("SetLocation(nil) location = %v, want UTC", v.location())
	}
}
