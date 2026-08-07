package web

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	mcpToolInventoryBegin = "<!-- MCP-TOOL-INVENTORY: begin -->"
	mcpToolInventoryEnd   = "<!-- MCP-TOOL-INVENTORY: end -->"
)

var mcpToolInventoryLine = regexp.MustCompile("(?m)^- `([a-z0-9_]+)`$")

func TestMCPDocumentationToolInventoryMatchesShippedTools(t *testing.T) {
	tools := (&Server{}).mcpListTools().(map[string]any)["tools"].([]any)
	shipped := make(map[string]struct{}, len(tools))
	for _, item := range tools {
		name, ok := item.(map[string]any)["name"].(string)
		if !ok || name == "" {
			t.Fatalf("tools/list item has no name: %#v", item)
		}
		if _, duplicate := shipped[name]; duplicate {
			t.Fatalf("tools/list has duplicate tool name %q", name)
		}
		shipped[name] = struct{}{}
	}

	docPath := findMCPDocumentation(t)
	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	docText := string(doc)
	if begins, ends := strings.Count(docText, mcpToolInventoryBegin), strings.Count(docText, mcpToolInventoryEnd); begins != 1 || ends != 1 {
		t.Fatalf("%s must contain exactly one bounded MCP tool inventory block; begin markers=%d end markers=%d", docPath, begins, ends)
	}
	start := strings.Index(docText, mcpToolInventoryBegin)
	end := strings.Index(docText, mcpToolInventoryEnd)
	if end <= start {
		t.Fatalf("%s must contain one bounded MCP tool inventory block", docPath)
	}

	documented := make(map[string]struct{}, len(tools))
	block := docText[start+len(mcpToolInventoryBegin) : end]
	for _, line := range strings.Split(block, "\n") {
		if line == "" {
			continue
		}
		match := mcpToolInventoryLine.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("documentation inventory contains an invalid line %q", line)
		}
		name := match[1]
		if _, duplicate := documented[name]; duplicate {
			t.Fatalf("documentation inventory has duplicate tool name %q", name)
		}
		documented[name] = struct{}{}
	}
	for name := range shipped {
		if _, ok := documented[name]; !ok {
			t.Errorf("shipped tool %q is missing from the documentation inventory", name)
		}
	}
	for name := range documented {
		if _, ok := shipped[name]; !ok {
			t.Errorf("documentation inventory lists non-shipped tool %q", name)
		}
	}
}

func findMCPDocumentation(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate MCP documentation test source")
	}
	candidate := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "docs", "mcp.md"))
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "docs", "mcp.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find docs/mcp.md from %s", dir)
		}
		dir = parent
	}
}
