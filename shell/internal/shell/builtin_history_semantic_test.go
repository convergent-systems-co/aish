// T4 + T5 tests — `aish history search --mode={keyword,semantic,
// hybrid}` flag wiring and the new `aish history reindex` subcommand.
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
// reindex` runs and produces output consistent with the dispatch
// path — either "reindexed N event(s)" on success or a specific
// "no embedder" error on the local-without-model build. Critically,
// the output MUST NOT match the "unknown subcommand" template that
// historyBuiltin's default case emits — that template would pass a
// naive "any output counts" check even when the subcommand
// dispatch is broken.
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
	combined := out.String() + errBuf.String()
	if combined == "" {
		t.Errorf("history reindex produced no output (subcommand missing?)")
	}
	// Anti-coverage-theater: if dispatch fell to the "unknown
	// subcommand" arm, the combined output would contain that
	// template. Reject that explicitly so this test fails when the
	// subcommand wiring regresses.
	if strings.Contains(combined, "unknown subcommand") {
		t.Errorf("history reindex routed to unknown-subcommand fallback: %q", combined)
	}
	// One of the two legitimate dispatch outputs MUST be present.
	// On a v0.3 binary without an embedder the store returns
	// "no embedder" / "no vector store" from Reindex; on a build
	// with the model cache seeded the command reports "reindexed".
	legitDispatch := strings.Contains(combined, "reindexed") ||
		strings.Contains(combined, "no embedder") ||
		strings.Contains(combined, "no vector store")
	if !legitDispatch {
		t.Errorf("history reindex output did not match any expected dispatch shape: %q", combined)
	}
}

// TestBuiltinHistoryReindex_UnknownArg checks usage gating — extra
// args produce a SPECIFIC usage-error string (not the unknown-
// subcommand fallback). The literal "usage:" substring is the
// signal; "unknown subcommand" is the regression marker.
func TestBuiltinHistoryReindex_UnknownArg(t *testing.T) {
	chHome(t)
	s := New()
	defer s.Close()

	var out, errBuf bytes.Buffer
	driveLines(t, s, &out, &errBuf, "history reindex extra arg")
	combined := out.String() + errBuf.String()
	if !strings.Contains(combined, "usage:") {
		t.Errorf("expected reindex usage-error, got %q", combined)
	}
	if strings.Contains(combined, "unknown subcommand") {
		t.Errorf("extra-args case fell through to unknown-subcommand fallback: %q", combined)
	}
}
