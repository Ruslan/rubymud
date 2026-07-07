package ansihtml

import (
	"strings"
	"testing"
)

// TestToHTMLParityWithAnsiUp asserts byte-for-byte parity against the ACTUAL
// output of ansi_up 6.0.6 (use_classes=true). Expected strings were captured by
// running ui/node_modules/ansi_up over each input (see the fixture generator in
// the task notes). If ansi_up is upgraded, regenerate these.
func TestToHTMLParityWithAnsiUp(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello world", "hello world"},
		{"bold_red", "\x1b[1;31mhello\x1b[0m", `<span style="font-weight:bold" class="ansi-red-fg">hello</span>`},
		{"cube_196", "\x1b[38;5;196mX", `<span style="color:rgb(255,0,0)">X</span>`},
		{"gray_240", "\x1b[38;5;240mX", `<span style="color:rgb(88,88,88)">X</span>`},
		{"truecolor_fg", "\x1b[38;2;10;20;30mX", `<span style="color:rgb(10,20,30)">X</span>`},
		{"bg_green", "\x1b[42mX", `<span class="ansi-green-bg">X</span>`},
		{"bright_red_fg", "\x1b[91mX", `<span class="ansi-bright-red-fg">X</span>`},
		{"italic_underline", "\x1b[3;4mX", `<span style="font-style:italic;text-decoration:underline">X</span>`},
		// Reverse (7) is IGNORED by ansi_up 6.0.6 (no fg/bg swap).
		{"reverse_then_red", "\x1b[7;31mX", `<span class="ansi-red-fg">X</span>`},
		{"bg_truecolor", "\x1b[48;2;1;2;3mX", `<span style="background-color:rgb(1,2,3)">X</span>`},
		{"escaping", "\x1b[31m<b>&\"'y", `<span class="ansi-red-fg">&lt;b&gt;&amp;&quot;&#x27;y</span>`},
		{"text_then_sgr", "a\x1b[31mb", `a<span class="ansi-red-fg">b</span>`},
		{"fg_bg_palette", "\x1b[31;42mX", `<span class="ansi-red-fg ansi-green-bg">X</span>`},
		{"bold_truecolor", "\x1b[1;38;2;5;6;7mX", `<span style="font-weight:bold;color:rgb(5,6,7)">X</span>`},
		// Malformed / partial sequences.
		{"incomplete_38_5", "\x1b[38;5mX", "X"},
		{"private_mode", "\x1b[?25hX", "X"},
		{"empty_sgr_reset", "\x1b[31mA\x1b[mB", `<span class="ansi-red-fg">A</span>B`},
		// blink (5) and strikethrough (9) are IGNORED by ansi_up 6.0.6.
		{"blink_strike_ignored", "\x1b[5;9;32mX", `<span class="ansi-green-fg">X</span>`},
		{"faint", "\x1b[2;34mX", `<span style="opacity:0.7" class="ansi-blue-fg">X</span>`},
		{"bg256_cube", "\x1b[48;5;21mX", `<span style="background-color:rgb(0,0,255)">X</span>`},
		{"color256_index15", "\x1b[38;5;15mX", `<span class="ansi-bright-white-fg">X</span>`},
		{"reset_39", "\x1b[31mA\x1b[39mB", `<span class="ansi-red-fg">A</span>B`},
		{"multi_reset", "\x1b[1;4;31mA\x1b[0mB", `<span style="font-weight:bold;text-decoration:underline" class="ansi-red-fg">A</span>B`},
		{"trailing_partial_escape", "hi\x1b[", "hi"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToHTML(tc.input, "classic")
			if got != tc.want {
				t.Fatalf("ToHTML(%q)\n got: %q\nwant: %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestToHTMLStripsOSCHyperlinks verifies OSC-8 hyperlink sequences are stripped
// entirely (no control bytes, no leaked URL) — a deliberate divergence from
// ansi_up for a self-contained, no-external-URL export.
func TestToHTMLStripsOSCHyperlinks(t *testing.T) {
	cases := []string{
		"\x1b]8;;http://evil.example.com\x07link text\x1b]8;;\x07",     // BEL-terminated
		"\x1b]8;;https://evil.example.com\x1b\\link text\x1b]8;;\x1b\\", // ST-terminated
	}
	for _, in := range cases {
		got := ToHTML(in, "classic")
		if strings.Contains(got, "\x07") {
			t.Fatalf("output retains BEL: %q", got)
		}
		if strings.Contains(got, "]8;;") {
			t.Fatalf("output leaks literal OSC: %q", got)
		}
		if strings.Contains(got, "http") {
			t.Fatalf("output leaks URL: %q", got)
		}
		if !strings.Contains(got, "link text") {
			t.Fatalf("output dropped the visible link text: %q", got)
		}
	}

	// Unterminated OSC: consume the remainder, emit nothing (no leak).
	if got := ToHTML("before\x1b]8;;http://x", "classic"); got != "before" {
		t.Fatalf("unterminated OSC: got %q, want %q", got, "before")
	}
}

// TestConverterCarriesStateAcrossLines verifies the persistent Converter carries
// an unterminated SGR into the next line (mirroring the live pane's single
// ansi_up), while a ToHTML render in between (a command row) does NOT perturb it.
func TestConverterCarriesStateAcrossLines(t *testing.T) {
	c := NewConverter("classic")

	if got, want := c.Convert("\x1b[31mred line"), `<span class="ansi-red-fg">red line</span>`; got != want {
		t.Fatalf("line1:\n got: %q\nwant: %q", got, want)
	}
	// Line 2 is plain text but should carry the red state.
	if got, want := c.Convert("still red"), `<span class="ansi-red-fg">still red</span>`; got != want {
		t.Fatalf("line2 (carried):\n got: %q\nwant: %q", got, want)
	}
	// A command row rendered via ToHTML must not change the Converter's state.
	_ = ToHTML("\x1b[32mgreen command", "classic")
	if got, want := c.Convert("still red 2"), `<span class="ansi-red-fg">still red 2</span>`; got != want {
		t.Fatalf("line3 (carried after command):\n got: %q\nwant: %q", got, want)
	}
	// A reset clears the carried state.
	c.Convert("\x1b[0m")
	if got, want := c.Convert("plain now"), "plain now"; got != want {
		t.Fatalf("after reset:\n got: %q\nwant: %q", got, want)
	}

	// ToHTML always starts fresh (no cross-call carry).
	if got, want := ToHTML("plain", "classic"), "plain"; got != want {
		t.Fatalf("ToHTML fresh: got %q want %q", got, want)
	}
}

// TestToHTMLHighContrastPromotesBoldForeground verifies the high-contrast quirk:
// bold + a normal fg class becomes the bright fg class (mirrors ui/src/ansi.ts
// promoteBoldAnsiForeground). Non-bold, and non-high-contrast themes, are
// unchanged.
func TestToHTMLHighContrastPromotesBoldForeground(t *testing.T) {
	// Bold red under high-contrast -> bright red fg.
	if got, want := ToHTML("\x1b[1;31mhello", "high-contrast"),
		`<span style="font-weight:bold" class="ansi-bright-red-fg">hello</span>`; got != want {
		t.Fatalf("high-contrast bold promote:\n got: %q\nwant: %q", got, want)
	}
	// Non-bold red under high-contrast is unchanged.
	if got, want := ToHTML("\x1b[31mhello", "high-contrast"),
		`<span class="ansi-red-fg">hello</span>`; got != want {
		t.Fatalf("high-contrast non-bold:\n got: %q\nwant: %q", got, want)
	}
	// Bold red under classic is NOT promoted.
	if got, want := ToHTML("\x1b[1;31mhello", "classic"),
		`<span style="font-weight:bold" class="ansi-red-fg">hello</span>`; got != want {
		t.Fatalf("classic bold not promoted:\n got: %q\nwant: %q", got, want)
	}
	// Bright bg is not promoted (fg-only mapping); bold+bright green fg stays.
	if got, want := ToHTML("\x1b[1;92mhi", "high-contrast"),
		`<span style="font-weight:bold" class="ansi-bright-green-fg">hi</span>`; got != want {
		t.Fatalf("high-contrast bright fg unchanged:\n got: %q\nwant: %q", got, want)
	}
}
