package shell

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolatedHome points $HOME at a fresh tempdir. Every test in this file
// uses one — cache.Open creates ~/.aish/cache.db, and we want each test
// in its own DB. Resolves symlinks because macOS turns /var into
// /private/var, which messes with later filepath comparisons.
func isolatedHome(t *testing.T) string {
	t.Helper()
	raw := t.TempDir()
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		resolved = raw
	}
	t.Setenv("HOME", resolved)
	// Defensively ensure no inherited API key triggers plugin spawn.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("AISH_INFERENCE_PLUGIN", "")
	return resolved
}

// TestNew_OpensCacheDB asserts that constructing a Shell with a writable
// $HOME creates ~/.aish/cache.db on disk. Gates the v0.1-2 "store
// always present" contract.
func TestNew_OpensCacheDB(t *testing.T) {
	home := isolatedHome(t)
	s := New()
	defer s.Close()

	if s.cache == nil {
		t.Fatalf("expected cache to open under $HOME=%q", home)
	}
	if s.cacheStore == nil {
		t.Fatal("expected cacheStore to be non-nil after New()")
	}
	if _, err := os.Stat(filepath.Join(home, ".aish", "cache.db")); err != nil {
		t.Errorf("expected cache.db under HOME; got err: %v", err)
	}
}

// TestNew_NoPlugin_WhenAPIKeyMissing confirms the shell does NOT spawn
// a plugin when no bearer key is configured — important so that fresh
// users don't see a crash from a child that immediately exits.
func TestNew_NoPlugin_WhenAPIKeyMissing(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()

	if s.cachePlugin != nil {
		t.Errorf("expected no plugin when ANTHROPIC_API_KEY is empty; got %v", s.cachePlugin)
	}
}

// TestClose_Idempotent verifies Close can be called multiple times
// without panicking. Defensive for the defer pattern in main.go.
func TestClose_Idempotent(t *testing.T) {
	isolatedHome(t)
	s := New()
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestCacheBuiltin_StatsZeroEntries renders the empty-store stats line.
// Gates task #21: `cache stats` prints `Hits | Misses | Hit rate | Entries`.
func TestCacheBuiltin_StatsZeroEntries(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()

	var stdout, stderr bytes.Buffer
	rc := s.cacheBuiltin([]string{"stats"}, &stdout, &stderr)
	if rc != 0 {
		t.Errorf("rc = %d, want 0; stderr=%q", rc, stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{"Hits:", "Misses:", "Hit rate:", "Entries:"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats output missing %q; got %q", want, got)
		}
	}
	if !strings.Contains(got, "n/a") {
		t.Errorf("empty-store hit rate should print n/a; got %q", got)
	}
}

// TestCacheBuiltin_ClearTruncates verifies `cache clear` empties the
// store and the next stats call sees zero entries.
func TestCacheBuiltin_ClearTruncates(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()
	// Pre-populate with a row so Clear has something to truncate.
	if err := s.cacheStore.Write("ls all files", runtime.GOOS, "ls -la", 0.9, nil); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	var out, errBuf bytes.Buffer
	if rc := s.cacheBuiltin([]string{"clear"}, &out, &errBuf); rc != 0 {
		t.Fatalf("clear rc = %d, want 0; stderr=%q", rc, errBuf.String())
	}
	st, err := s.cacheStore.Stats()
	if err != nil {
		t.Fatalf("stats after clear: %v", err)
	}
	if st.Entries != 0 {
		t.Errorf("Entries after clear = %d, want 0", st.Entries)
	}
}

// TestCacheBuiltin_Usage covers bare `cache` and unknown subcommands.
func TestCacheBuiltin_Usage(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()

	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"bare", nil, 2},
		{"unknown", []string{"sync"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			rc := s.cacheBuiltin(tc.args, &out, &errBuf)
			if rc != tc.want {
				t.Errorf("rc = %d, want %d (stderr=%q)", rc, tc.want, errBuf.String())
			}
			if errBuf.Len() == 0 {
				t.Errorf("expected stderr message; got empty")
			}
		})
	}
}

// TestDispatch_KnownBinary_TakesPassthrough confirms that a literal
// shell command (here `true`) goes through the parser+exec path and
// does NOT consult the cache. We assert by setting a cache row that
// would otherwise hijack it.
func TestDispatch_KnownBinary_TakesPassthrough(t *testing.T) {
	if _, err := lookOnPath("true"); err != nil {
		t.Skipf("`true` not on PATH (env-dependent): %v", err)
	}
	isolatedHome(t)
	s := New()
	defer s.Close()

	// Seed a poison row so that if the cache fired, exit would be 7.
	if err := s.cacheStore.Write("true", runtime.GOOS, "false", 1.0, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	script := strings.NewReader("true\n")
	var out, errBuf bytes.Buffer
	if err := s.Run(script, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.LastExit() != 0 {
		t.Errorf("`true` should pass through and exit 0; got %d (stderr=%q)",
			s.LastExit(), errBuf.String())
	}
}

// TestDispatch_CacheHit_RunsCachedInvocation seeds the cache with an
// intent → invocation mapping and verifies typing the intent runs the
// cached invocation. This is the END-TO-END v0.1-2 acceptance path.
func TestDispatch_CacheHit_RunsCachedInvocation(t *testing.T) {
	if _, err := lookOnPath("echo"); err != nil {
		t.Skipf("echo not on PATH (env-dependent): %v", err)
	}
	isolatedHome(t)
	s := New()
	defer s.Close()

	// First token MUST be something not on PATH or the known-binary tier
	// hijacks it before the cache is ever consulted. "list" / "show" /
	// "say" all resolve on at least one of the supported OSes.
	const intent = "wibble-fake-intent hello to the cache"
	const invocation = "echo cache-hit-OK"
	if err := s.cacheStore.Write(intent, runtime.GOOS, invocation, 1.0, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	script := strings.NewReader(intent + "\n")
	var stdout, stderr bytes.Buffer
	if err := s.Run(script, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "cache-hit-OK") {
		t.Errorf("expected cached invocation to fire; stdout=%q stderr=%q",
			stdout.String(), stderr.String())
	}
	// hit_count should have incremented.
	st, _ := s.cacheStore.Stats()
	if st.Hits < 1 {
		t.Errorf("expected at least 1 hit; got %d", st.Hits)
	}
}

// TestDispatch_NoPluginNoCache_FallsThrough verifies the legacy
// "command not found" path still fires when the cache misses and no
// plugin is configured. Backward compatibility for v0.1-1 behavior.
func TestDispatch_NoPluginNoCache_FallsThrough(t *testing.T) {
	isolatedHome(t)
	s := New()
	defer s.Close()

	script := strings.NewReader("definitely-not-a-real-command-xyz\n")
	var out, errBuf bytes.Buffer
	if err := s.Run(script, io.Discard, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = out
	if s.LastExit() == 0 {
		t.Errorf("expected non-zero exit on unknown command; got %d", s.LastExit())
	}
}

// TestFirstToken covers the lightweight tokeniser used to peek at the
// known-binary check.
func TestFirstToken(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"ls", "ls"},
		{"ls -la", "ls"},
		{"  ls -la", "ls"},
		{"echo\thello", "echo"},
		{"/usr/bin/cat file.md", "/usr/bin/cat"},
	}
	for _, c := range cases {
		if got := firstToken(c.in); got != c.want {
			t.Errorf("firstToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
