package vm

import (
	"testing"
)

func TestRenderLocalMarkup(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "simple red",
			input:    "<red>hello</red>",
			expected: "\x1b[0;31mhello\x1b[0m",
		},
		{
			name:     "bold red",
			input:    "<red><b>bold red</b></red>",
			expected: "\x1b[0;31m\x1b[0;1;31mbold red\x1b[0;31m\x1b[0m",
		},
		{
			name:     "restore after nested",
			input:    "<red>a <u>b</u> c</red>",
			expected: "\x1b[0;31ma \x1b[0;4;31mb\x1b[0;31m c\x1b[0m",
		},
		{
			name:     "reset tag",
			input:    "<red>red <reset>plain",
			expected: "\x1b[0;31mred \x1b[0mplain",
		},
		{
			name:     "unbalanced opening",
			input:    "<red>hello",
			expected: "\x1b[0;31mhello\x1b[0m",
		},
		{
			name:     "unbalanced closing",
			input:    "hello</red>",
			expected: "hello</red>",
		},
		{
			name:     "unknown tag",
			input:    "<foo>bar</foo>",
			expected: "<foo>bar</foo>",
		},
		{
			name:     "broken tag",
			input:    "<red hello",
			expected: "<red hello",
		},
		{
			name:     "fg and bg tags",
			input:    "<fg #ff0000><bg blue>text</bg></fg>",
			expected: "\x1b[0;38;2;255;0;0m\x1b[0;38;2;255;0;0;44mtext\x1b[0;38;2;255;0;0m\x1b[0m",
		},
		{
			name:     "named colors with hyphens",
			input:    "<light-red>text</light-red>",
			expected: "\x1b[0;91mtext\x1b[0m",
		},
		{
			name:     "256 color",
			input:    "<fg 256:196>text</fg>",
			expected: "\x1b[0;38;5;196mtext\x1b[0m",
		},
		{
			name:     "rgb color",
			input:    "<fg rgb255,128,0>text</fg>",
			expected: "\x1b[0;38;2;255;128;0mtext\x1b[0m",
		},
		{
			name:     "mismatched closing tag",
			input:    "<red>a<b>b</red>c",
			expected: "\x1b[0;31ma\x1b[0;1;31mb</red>c\x1b[0m",
		},
		{
			name:     "task: mismatched closing tag",
			input:    "<red>a</b>b</red>",
			expected: "\x1b[0;31ma</b>b\x1b[0m",
		},
		{
			name:     "task: background tag-name form closing",
			input:    "<bg-blue>x</bg-blue>",
			expected: "\x1b[0;44mx\x1b[0m",
		},
		{
			name:     "task: invalid explicit color",
			input:    "<fg nope>x</fg>",
			expected: "<fg nope>x</fg>",
		},
		{
			name:     "task: malformed rgb tag",
			input:    "<fg rgb1,2>x</fg>",
			expected: "<fg rgb1,2>x</fg>",
		},
		{
			name:     "task: malformed 256 tag",
			input:    "<fg 256:>x</fg>",
			expected: "<fg 256:>x</fg>",
		},
		{
			name:     "task: malformed non-numeric rgb",
			input:    "<fg rgb1,two,3>x</fg>",
			expected: "<fg rgb1,two,3>x</fg>",
		},
		{
			name:     "task: malformed empty-component rgb",
			input:    "<fg rgb1,2,>x</fg>",
			expected: "<fg rgb1,2,>x</fg>",
		},
		{
			name:     "task: out-of-range rgb",
			input:    "<fg rgb256,0,0>x</fg>",
			expected: "<fg rgb256,0,0>x</fg>",
		},
		{
			name:     "task: malformed non-numeric 256",
			input:    "<fg 256:abc>x</fg>",
			expected: "<fg 256:abc>x</fg>",
		},
		{
			name:     "task: out-of-range 256",
			input:    "<fg 256:999>x</fg>",
			expected: "<fg 256:999>x</fg>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := renderLocalMarkup(tt.input)
			if actual != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, actual)
			}
		})
	}
}
