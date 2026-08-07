package mapper

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mapperCorpusArtifactsEnv = "RUBYMUD_MAPPER_ARTIFACT_DIR"

const mapperCorpusFatalProbeEnv = "RUBYMUD_MAPPER_FATAL_PROBE"

// mapperCorpusPath resolves the explicit opt-in location for the private
// reference corpus. Keeping discovery behind one environment variable prevents
// a developer-specific repository-relative path from becoming test behavior.
func mapperCorpusPath() (string, bool) {
	dir := strings.TrimSpace(os.Getenv(mapperCorpusArtifactsEnv))
	if dir == "" {
		return "", false
	}
	return filepath.Join(dir, "rmud_index.json"), true
}

// readMapperCorpus loads the private reference corpus shared by the mapper's
// corpus tests. Ordinary test runs stay independent of private artifacts.
func readMapperCorpus(tb testing.TB) []byte {
	tb.Helper()
	path, ok := mapperCorpusPath()
	if !ok {
		tb.Skipf("private mapper corpus disabled; set %s to its artifact directory to run", mapperCorpusArtifactsEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			tb.Skipf("private mapper corpus index absent at %s (%v) - skipping", path, err)
		}
		tb.Fatalf("cannot read private mapper corpus index at %s: %v", path, err)
	}
	return data
}

func TestMapperCorpusPath(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(mapperCorpusArtifactsEnv, "")
		_ = os.Unsetenv(mapperCorpusArtifactsEnv)
		if path, ok := mapperCorpusPath(); ok || path != "" {
			t.Fatalf("mapperCorpusPath() = %q, %v; want empty, false", path, ok)
		}
	})

	t.Run("blank", func(t *testing.T) {
		t.Setenv(mapperCorpusArtifactsEnv, " \t")
		if path, ok := mapperCorpusPath(); ok || path != "" {
			t.Fatalf("mapperCorpusPath() = %q, %v; want empty, false", path, ok)
		}
	})

	t.Run("configured directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(mapperCorpusArtifactsEnv, dir)
		want := filepath.Join(dir, "rmud_index.json")
		if path, ok := mapperCorpusPath(); !ok || path != want {
			t.Fatalf("mapperCorpusPath() = %q, %v; want %q, true", path, ok, want)
		}
	})
}

// TestMapperCorpusReadErrorFatalProbe is run in a subprocess by
// TestMapperCorpusReadErrorIsFatal. Keeping the Fatalf call in the real helper
// makes the probe cover the production error branch without replacing testing.T.
func TestMapperCorpusReadErrorFatalProbe(t *testing.T) {
	if os.Getenv(mapperCorpusFatalProbeEnv) != "1" {
		return
	}
	readMapperCorpus(t)
}

func TestMapperCorpusReadErrorIsFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rmud_index.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestMapperCorpusReadErrorFatalProbe$")
	cmd.Env = append(os.Environ(),
		mapperCorpusArtifactsEnv+"="+dir,
		mapperCorpusFatalProbeEnv+"=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fatal probe unexpectedly passed; output:\n%s", out)
	}
	if !bytes.Contains(out, []byte("cannot read private mapper corpus index")) {
		t.Fatalf("fatal probe output missing read-error message: %s", out)
	}
}
