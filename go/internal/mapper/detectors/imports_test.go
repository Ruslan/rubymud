package detectors

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The import denylist.
//
// A contributed detector takes lines and returns events. It has no business
// opening a socket, running a program, touching the database or reaching into
// the rest of rubymud — and a reviewer skimming a 200-line diff at 1am is not a
// reliable enforcement mechanism. This test is.
//
// It reads the SOURCE of every non-test file in this package tree and checks the
// import declarations. That is deliberately a check on DIRECT imports, not on
// the transitive closure: a detector legitimately imports `mapper`, which
// imports `storage`, which imports gorm. The transitive set is therefore
// meaningless here; what matters is what the detector itself can call.
//
// The rubymud allowlist is the stronger half. Denylists leak (there is always
// another package that can write a file); "the only rubymud package a detector
// may import is internal/mapper" does not, because everything dangerous in this
// codebase — the store, the session, the web layer, the VM — is behind one of
// the packages it excludes.
//
// _test.go files are exempt. A contribution's own test may legitimately import
// `testing`, `os` (to read a fixture it ships) or the conformance harness; the
// constraint is on what runs inside a live session, and test files do not.
// ---------------------------------------------------------------------------

// deniedImports are import paths (exact) and path prefixes (trailing "/") a
// detector may not import. Each entry says WHY, because a future contributor
// meeting this failure deserves the reason and not just a wall.
var deniedImports = map[string]string{
	"net":           "a detector must not touch the network — it receives lines the session already read",
	"net/http":      "a detector must not make HTTP requests",
	"net/url":       "a detector has no URLs; this usually means networking crept in",
	"os":            "a detector must not touch the filesystem, the environment or the process",
	"os/exec":       "a detector must not run programs",
	"os/signal":     "a detector must not touch process signals",
	"database/sql":  "a detector must not reach the database — it is handed lines, and persistence is the store's job",
	"syscall":       "a detector must not make system calls",
	"unsafe":        "a detector must stay memory-safe; there is no line-parsing reason to need unsafe",
	"plugin":        "a detector must not load code at runtime — registration is compile-time on purpose",
	"runtime":       "runtime.Goexit() inside Feed terminates the session's READ-LOOP goroutine while recover() returns nil — a clean bypass of the panic boundary; nothing a line parser needs lives here",
	"runtime/debug": "a detector must not alter runtime behaviour (SetPanicOnFault, SetGCPercent) for the whole process",
	"log":           "a detector must not write to the server log on the per-line path; return events and let the session report",
	"net/rpc":       "a detector must not expose or call RPC",
	"golang.org/x/": "a detector needs nothing outside the standard library and mapper",
	"github.com/":   "a detector needs nothing outside the standard library and mapper",
	"gorm.io/":      "a detector must not reach the ORM",
	"gopkg.in/":     "a detector needs nothing outside the standard library and mapper",
	"os/user":       "a detector must not read the host's user database",
	"path/filepath": "a detector must not build filesystem paths — it has no filesystem access",
	"io/ioutil":     "a detector must not read or write files",
	"embed":         "a detector must not embed files; ship fixtures as Go literals so a reviewer sees them in the diff",
	"encoding/gob":  "a detector must not deserialize arbitrary data",
	"text/template": "a detector must not evaluate templates on untrusted MUD output",
	"html/template": "a detector must not evaluate templates on untrusted MUD output",
}

// (rubymud/... imports are governed by allowedRubymudImports below, not by this
// map: an allowlist is the right shape there and a denylist would need an entry
// per package in the repo, growing stale the moment someone adds one.)

// allowedRubymudImports is the ONLY rubymud package tree a detector may import.
const allowedRubymudImports = "rubymud/go/internal/mapper"

// TestDetectorImportsAreRestricted is the mechanical guarantee that a
// contributed detector cannot grow capabilities it has no business having.
func TestDetectorImportsAreRestricted(t *testing.T) {
	files := detectorSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("no detector source files found — this test would pass vacuously")
	}
	t.Logf("checking imports of %d detector source file(s)", len(files))

	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import literal %s", path, spec.Path.Value)
			}
			pos := fset.Position(spec.Pos())

			// The rubymud allowlist, checked first: it is the tighter rule.
			if strings.HasPrefix(imp, "rubymud/") {
				if imp != allowedRubymudImports && !strings.HasPrefix(imp, allowedRubymudImports+"/") {
					t.Errorf("%s: forbidden import %q\n\ta detector may import exactly one rubymud package, %q — everything else (the store, the session, the web layer, the VM) is out of a detector's reach by design",
						pos, imp, allowedRubymudImports)
				}
				continue
			}

			for denied, why := range deniedImports {
				hit := imp == denied
				if strings.HasSuffix(denied, "/") && strings.HasPrefix(imp, denied) {
					hit = true
				}
				if hit {
					t.Errorf("%s: forbidden import %q\n\t%s\n\ta detector takes lines and returns events — nothing else. See package docs in doc.go.",
						pos, imp, why)
				}
			}
		}
	}
}

// detectorSourceFiles lists every non-test .go file in this package tree, so a
// contribution that lands in a subdirectory is covered without editing this
// test.
func detectorSourceFiles(t *testing.T) []string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// TestDetectorsDoNotSpawnGoroutines is the statement-level half of the ban.
//
// The import check above parses with parser.ImportsOnly, which stops at the
// import block — it structurally CANNOT see a `go` statement. So doc.go forbade
// goroutines and nothing enforced it: a detector could spawn one from Feed using
// only `strings` and `mapper`.
//
// Why goroutines are banned at all: a detector instance belongs to exactly one
// session and is called from that session's single read loop, so it needs no
// concurrency. One that spawns anyway escapes every boundary this package has —
// the panic wrapper only covers the calling goroutine, so a panic in a spawned
// one takes the whole process down, and nothing stops it outliving the session.
func TestDetectorsDoNotSpawnGoroutines(t *testing.T) {
	files := detectorSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("no detector source files found — this test would pass vacuously")
	}
	fset := token.NewFileSet()
	for _, path := range files {
		// Full parse this time, not ImportsOnly: statements are the point.
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: forbidden `go` statement\n\ta detector runs in the session's single read loop and must not spawn goroutines: the panic boundary only covers the calling goroutine, so a panic in a spawned one kills the whole process. See doc.go.",
					fset.Position(stmt.Pos()))
			case *ast.SelectStmt:
				t.Errorf("%s: forbidden `select` statement\n\ta detector is synchronous: it takes a line and returns an event. A select here means it is waiting on something, which blocks the read loop.",
					fset.Position(stmt.Pos()))
			}
			return true
		})
	}
}
