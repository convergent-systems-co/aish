// Shared helpers for the Phase B test suite (T1..T5). Used by
// the embedding, vector-store, embed-writer, search, and reindex
// tests; safe to compile alongside seed-level tests because the
// helpers here are pure functions, not test functions.

package history

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// copyFixtureToTemp copies a committed fixture into the test's
// temp directory and returns the destination path. Tests use this so
// the fixture file in shell/internal/history/testdata/ remains
// immutable across runs — `go test` would otherwise leave a
// mutated DB on disk on the first crash.
func copyFixtureToTemp(t *testing.T, fixture string) string {
	t.Helper()

	src, err := os.Open(fixture)
	if err != nil {
		t.Skipf("fixture %s not present: %v", fixture, err)
	}
	defer src.Close()

	dst := filepath.Join(t.TempDir(), filepath.Base(fixture))
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}
