package mapref

import "testing"

// TestCanonicalRoundTrip is the property the WRITE path depends on: whatever
// lenient spelling arrives, Canonical(ParseFormatRef(x)) is the one form the
// storage layer's exact-string queries will match.
//
// This is the bug it prevents: "db:007" parses fine, so the endpoint used to
// store it verbatim. FormatRefForDB renders "db:7", and both
// DeleteMapperProfile and SessionIDsUsingMapperProfile match on that. The session
// was therefore found by neither — editing the profile never reached the live
// session, and deleting it left a dangling reference, silently.
func TestCanonicalRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"db:7", "db:7"},
		{"db:007", "db:7"},
		{" db:7 ", "db:7"},
		{"db:0000000042", "db:42"},
		{"builtin:rmud", "builtin:rmud"},
		{"builtin: rmud ", "builtin:rmud"},
		{" builtin:diku-en", "builtin:diku-en"},
	}
	for _, c := range cases {
		kind, key, id, ok := ParseFormatRef(c.in)
		if !ok {
			t.Errorf("ParseFormatRef(%q) rejected it", c.in)
			continue
		}
		got := Canonical(kind, key, id)
		if got != c.want {
			t.Errorf("Canonical(ParseFormatRef(%q)) = %q, want %q", c.in, got, c.want)
		}
		// Canonical output must be a fixed point: re-parsing it changes nothing.
		if got != "" {
			k2, key2, id2, ok2 := ParseFormatRef(got)
			if !ok2 || Canonical(k2, key2, id2) != got {
				t.Errorf("canonical form %q is not a fixed point", got)
			}
		}
	}
}

func TestParseFormatRefRejectsMalformed(t *testing.T) {
	for _, in := range []string{"db:0", "db:-1", "db:abc", "db:", "builtin:", "7", "rmud", "nonsense"} {
		if _, _, _, ok := ParseFormatRef(in); ok {
			t.Errorf("ParseFormatRef(%q) accepted a malformed reference", in)
		}
	}
}

// TestDefaultKeyIsRMUD — a NULL sessions.mapper_format resolves through this, so
// changing it silently re-parses every existing installation with different rules.
func TestDefaultKeyIsRMUD(t *testing.T) {
	if DefaultDetectorKey != "rmud" {
		t.Fatalf("DefaultDetectorKey = %q, want \"rmud\"", DefaultDetectorKey)
	}
	kind, key, _, ok := ParseFormatRef("")
	if !ok || kind != RefDefault || key != DefaultDetectorKey {
		t.Errorf("the empty reference must resolve to the default detector, got kind=%v key=%q ok=%v", kind, key, ok)
	}
}

// TestRenderersAgreeWithPrefixes pins the two renderers against the prefixes the
// SQL and the parser share.
func TestRenderersAgreeWithPrefixes(t *testing.T) {
	if got := FormatRefForDB(7); got != RefDBPrefix+"7" {
		t.Errorf("FormatRefForDB(7) = %q", got)
	}
	if got := BuiltinRef("rmud"); got != RefBuiltinPrefix+"rmud" {
		t.Errorf("BuiltinRef(rmud) = %q", got)
	}
}
