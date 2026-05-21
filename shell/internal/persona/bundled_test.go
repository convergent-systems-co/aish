package persona

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBundledMatchesDataDir — the canonical persona TOML files live at
// data/personas/ (repo root). The embed directive requires them to be
// inside the package, so we mirror them under
// shell/internal/persona/builtin/. This test ensures the two
// directories stay in sync — drift between them is a build-time defect.
//
// Skipped when the canonical data dir cannot be resolved (e.g. running
// inside a stripped binary on an isolated test machine).
func TestBundledMatchesDataDir(t *testing.T) {
	t.Parallel()

	// Walk up from this source file to the repo root, then to
	// data/personas/. runtime.Caller gives us the source path of this
	// test file when the test binary is built with debug info (Go
	// default).
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller(0) unavailable; cannot locate data/personas/")
	}
	// .../shell/internal/persona/bundled_test.go → repo root
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..", "..")
	dataDir := filepath.Join(repoRoot, "data", "personas")
	dataEntries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Skipf("data/personas/ not reachable from %s: %v", dataDir, err)
	}

	builtinDir := filepath.Join(filepath.Dir(file), "builtin")
	builtinEntries, err := os.ReadDir(builtinDir)
	if err != nil {
		t.Fatalf("read builtin/: %v", err)
	}

	// Build sha256 maps keyed by filename. We compare byte-for-byte;
	// any drift fails the test.
	dataHashes := tomlHashes(t, dataDir, dataEntries)
	builtinHashes := tomlHashes(t, builtinDir, builtinEntries)

	for name, dh := range dataHashes {
		bh, ok := builtinHashes[name]
		if !ok {
			t.Errorf("data/personas/%s has no mirror in shell/internal/persona/builtin/", name)
			continue
		}
		if dh != bh {
			t.Errorf("drift between data/personas/%s and shell/internal/persona/builtin/%s — re-sync them", name, name)
		}
	}
	for name := range builtinHashes {
		if _, ok := dataHashes[name]; !ok {
			t.Errorf("shell/internal/persona/builtin/%s has no upstream in data/personas/", name)
		}
	}
}

func tomlHashes(t *testing.T, dir string, entries []os.DirEntry) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s/%s: %v", dir, e.Name(), err)
		}
		h := sha256.Sum256(data)
		out[e.Name()] = string(h[:])
	}
	return out
}
