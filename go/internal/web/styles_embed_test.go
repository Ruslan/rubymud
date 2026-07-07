package web

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// embeddedStyleFiles lists every CSS file the export embeds, paired with its
// source-of-truth path under ui/src/styles.
var embeddedStyleFiles = map[string]string{
	"styles/ansi-classes.css":            "ansi-classes.css",
	"styles/export-base.css":             "export-base.css",
	"styles/ansi-themes/classic.css":     "ansi-themes/classic.css",
	"styles/ansi-themes/high-contrast.css": "ansi-themes/high-contrast.css",
	"styles/ansi-themes/tango-dark.css":  "ansi-themes/tango-dark.css",
	"styles/ansi-themes/dracula.css":     "ansi-themes/dracula.css",
	"styles/ansi-themes/gruvbox-dark.css": "ansi-themes/gruvbox-dark.css",
}

// TestEmbeddedStylesResolve is a hermetic smoke test: the committed CSS mirror
// is embedded and every export theme resolves. It needs NO prior UI build.
func TestEmbeddedStylesResolve(t *testing.T) {
	for embedPath := range embeddedStyleFiles {
		data, err := styleFiles.ReadFile(embedPath)
		if err != nil {
			t.Fatalf("embedded style %q missing: %v", embedPath, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded style %q is empty", embedPath)
		}
	}

	// Every declared export theme must have an embedded stylesheet, and building
	// its <style> must succeed.
	for theme := range exportThemes {
		if _, err := styleFiles.ReadFile("styles/ansi-themes/" + theme + ".css"); err != nil {
			t.Fatalf("theme %q has no embedded stylesheet: %v", theme, err)
		}
		if _, err := buildExportStyle(theme); err != nil {
			t.Fatalf("buildExportStyle(%q): %v", theme, err)
		}
	}
}

// TestBuildExportStyleDefinesAllReferencedVars verifies that for EVERY theme the
// assembled <style> actually defines every --ansi-* custom property that
// ansi-classes.css references. Non-classic themes only override a subset of the
// variables (e.g. high-contrast defines only the 8 bright fg vars), so the
// assembled style MUST include classic.css as the base layer (mirroring the live
// app's `:root` default + `[data-ansi-theme]` override cascade). Without that,
// colored text/backgrounds fall back to defaults and the export is broken.
func TestBuildExportStyleDefinesAllReferencedVars(t *testing.T) {
	classesCSS, err := styleFiles.ReadFile("styles/ansi-classes.css")
	if err != nil {
		t.Fatalf("read ansi-classes.css: %v", err)
	}
	// Collect every --ansi-* variable referenced via var(...).
	refRe := regexp.MustCompile(`var\((--ansi-[a-z0-9-]+)\)`)
	refSet := map[string]bool{}
	for _, m := range refRe.FindAllStringSubmatch(string(classesCSS), -1) {
		refSet[m[1]] = true
	}
	if len(refSet) == 0 {
		t.Fatal("no --ansi-* var references found in ansi-classes.css")
	}

	for theme := range exportThemes {
		style, err := buildExportStyle(theme)
		if err != nil {
			t.Fatalf("buildExportStyle(%q): %v", theme, err)
		}
		for name := range refSet {
			// A definition looks like `--ansi-red-fg:` (value follows).
			defRe := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*:`)
			if !defRe.MatchString(style) {
				t.Fatalf("theme %q: assembled <style> references %s but never defines it", theme, name)
			}
		}
	}
}

// TestEmbeddedStylesMatchSource guards against the committed Go mirror drifting
// from the ui/src/styles source of truth. It reads the source via a relative
// path; if the UI source tree is not present (e.g. a Go-only checkout) it skips,
// so `go test` stays hermetic.
func TestEmbeddedStylesMatchSource(t *testing.T) {
	srcRoot := filepath.Join("..", "..", "..", "ui", "src", "styles")
	if _, err := os.Stat(srcRoot); err != nil {
		t.Skipf("ui/src/styles not present (%v); skipping drift check", err)
	}

	for embedPath, srcRel := range embeddedStyleFiles {
		embedded, err := styleFiles.ReadFile(embedPath)
		if err != nil {
			t.Fatalf("embedded %q: %v", embedPath, err)
		}
		source, err := os.ReadFile(filepath.Join(srcRoot, srcRel))
		if err != nil {
			t.Fatalf("source %q: %v", srcRel, err)
		}
		if string(embedded) != string(source) {
			t.Fatalf("embedded %q is out of sync with ui/src/styles/%s — run `make sync-styles`", embedPath, srcRel)
		}
	}
}
