package vm

import (
	"strings"
	"testing"

	"rubymud/go/internal/storage"
)

func BenchmarkApplyHighlightsSparse4K(b *testing.B) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `needle`, FG: "red", Enabled: true}}
	text := strings.Repeat("x", 2045) + "needle" + strings.Repeat("x", 2045)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ApplyHighlights(text)
	}
}

func BenchmarkApplyHighlightsNoMatch4K(b *testing.B) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `needle`, FG: "red", Enabled: true}}
	text := strings.Repeat("x", 4096)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ApplyHighlights(text)
	}
}

func BenchmarkApplyHighlightsANSI4K(b *testing.B) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `needle`, FG: "red", Enabled: true}}
	text := "\x1b[1;34m" + strings.Repeat("x", 2040) + "needle" + strings.Repeat("x", 2040) + "\x1b[0m"
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ApplyHighlights(text)
	}
}

func BenchmarkApplyHighlights64K16Matches(b *testing.B) {
	v := New(nil, 1)
	v.highlights = []storage.HighlightRule{{Pattern: `needle`, FG: "red", Enabled: true}}
	text := strings.Repeat(strings.Repeat("x", 4090)+"needle", 16)
	text += strings.Repeat("x", 65536-len(text))
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.ApplyHighlights(text)
	}
}
