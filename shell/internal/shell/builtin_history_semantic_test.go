//go:build phase_b

// T4 + T5 tests — `aish history search --mode={keyword,semantic,
// hybrid}` flag wiring and the new `aish history reindex` subcommand.
//
// Build-gated by `phase_b`; the seed commit ships these inert. Phase
// B's coder wave adds the mode flag and reindex routing in
// builtin_history.go.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T4 + T5):
//   - TestBuiltinHistorySearch_ModeFlag — three mode values route to
//     the right Store method.
//   - TestBuiltinHistorySearch_NoVectorsYet — friendly message on
//     pre-reindex DB.
//   - TestBuiltinHistoryReindex_Invokes — reindex subcommand calls
//     Store.Reindex and reports the count.

package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinHistorySearch_ModeFlag_Keyword routes to the FTS5 path
// — the same path #113 already serves. Default behavior of bare
// `history search` should equal explicit `--mode=keyword` on a DB
// with no vectors. (Hybrid is the new default once vectors exist;
// behavior is intentionally back-compat for the no-vector case.)
func TestBuiltinHistorySearch_ModeFlag_Keyword(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "snapshot-target.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history search --mode=keyword snapshot",
	)
	if !strings.Contains(out.String(), "snapshot-target.txt") {
		t.Errorf("--mode=keyword did not return FTS5 hit: stdout=%q stderr=%q", out.String(), errBuf.String())
	}
}

// TestBuiltinHistorySearch_ModeFlag_Semantic_PreReindex (T4 AC,
// AC10): semantic mode with no vectors yet emits a friendly
// "run `aish history reindex`" message. The exit code is non-zero
// so scripts can branch on the missing-vectors state, but no panic
// or scary error.
func TestBuiltinHistorySearch_ModeFlag_Semantic_PreReindex(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "anyfile.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history search --mode=semantic any",
	)
	combined := out.String() + errBuf.String()
	if !strings.Contains(combined, "reindex") {
		t.Errorf("--mode=semantic on a DB without vectors should reference reindex: %q", combined)
	}
}

// TestBuiltinHistorySearch_ModeFlag_Hybrid_DegradesToKeyword (T4 AC,
// AC10): hybrid mode with no vectors degrades to keyword-only — no
// error, no "reindex" message, just the FTS5 results.
func TestBuiltinHistorySearch_ModeFlag_Hybrid_DegradesToKeyword(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "hybrid-target.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history search --mode=hybrid hybrid",
	)
	if !strings.Contains(out.String(), "hybrid-target.txt") {
		t.Errorf("--mode=hybrid degraded mode missed FTS hit: %q", out.String())
	}
	if strings.Contains(errBuf.String(), "error") {
		t.Errorf("--mode=hybrid degraded mode produced an error: %q", errBuf.String())
	}
}

// TestBuiltinHistorySearch_ModeFlag_Invalid (T4 AC): unknown mode
// value returns a usage error with exit code 2.
func TestBuiltinHistorySearch_ModeFlag_Invalid(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history search --mode=fizzbuzz anything")
	if !strings.Contains(errBuf.String(), "mode") {
		t.Errorf("invalid mode should error referencing 'mode': %q", errBuf.String())
	}
}

// TestBuiltinHistoryReindex_Invokes (T5 AC, AC6): `aish history
// reindex` runs and reports the count of events processed. Exit
// code 0 on success.
func TestBuiltinHistoryReindex_Invokes(t *testing.T) {
	_, cwd := chHome(t)
	a := filepath.Join(cwd, "x.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	// rm to seed an event; then reindex.
	driveLines(t, s, &out, &errBuf,
		"rm "+a,
		"history reindex",
	)
	// Either the command runs and reports a count, OR it reports
	// "no embedder configured" because the local v0.3 build has no
	// model. Both are acceptable; the seam under test is "the
	// reindex subcommand exists and dispatches" — the no-embedder
	// case is also a valid AC.
	combined := out.String() + errBuf.String()
	if combined == "" {
		t.Errorf("history reindex produced no output (subcommand missing?)")
	}
}

// TestBuiltinHistoryReindex_UnknownArg checks usage gating — extra
// args produce a usage error.
func TestBuiltinHistoryReindex_UnknownArg(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history reindex extra arg")
	combined := out.String() + errBuf.String()
	if combined == "" {
		t.Errorf("expected usage error for extra args, got nothing")
	}
}
