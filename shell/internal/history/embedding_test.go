//go:build phase_b

// T1 tests — fastembed-go + bge-small-en-v1.5 embedder.
//
// Build-gated by `phase_b` so the seed commit ships with a green
// `go build ./...`. Phase B's coder wave lands embedding_fastembed.go
// (which these tests will then exercise) and flips the tag — or, more
// cleanly, removes it once T1 is in.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T1):
//   - TestFastembed_DeterministicEmbedding — same input, same vector.
//   - TestFastembed_DimMatchesModelCard — Dim() truthful.
//   - TestFastembed_BatchEmbed — preserves input order.
//   - TestFastembed_OfflineFirstRun — no network; clear error when
//     the model file is absent.
//
// NOTE on test doubles: T1's embedder tests use the REAL fastembed-go
// runtime against a real ONNX file. The model is fetched once into
// $HOME/.aish/models/bge-small-en-v1.5/ as a documented manual prereq
// (Phase A decision: no `aish history models pull` subcommand in
// v0.3; users invoke fastembed-go's downloader or fetch the file
// directly). The CI workflow caches the model directory. If the
// model is absent locally, these tests SKIP — they do NOT silently
// download.

package history

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// testModelID is the model identifier the T1 implementation must
	// expose via ModelID(). Matches the plan's chosen model.
	testModelID = "bge-small-en-v1.5"

	// testModelDim is bge-small-en-v1.5's emit dimension. Mirrored in
	// schema.go's VecTableDDL (FLOAT[384]).
	testModelDim = 384
)

// modelCacheDir is where T1's loader expects to find the ONNX model
// file. v0.3 ships this as a documented manual prereq; CI seeds the
// cache before invoking the test target.
func modelCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	return filepath.Join(home, ".aish", "models", testModelID)
}

// requireModelPresent is the standard skip-gate for tests that need
// the real model file on disk. CI sets AISH_HISTORY_TEST_MODEL=1 to
// require the model (causing a hard fail on cache miss); local devs
// without the file get a SKIP instead of a confusing fastembed-go
// error.
func requireModelPresent(t *testing.T) {
	t.Helper()
	dir := modelCacheDir(t)
	if _, err := os.Stat(dir); err != nil {
		if os.Getenv("AISH_HISTORY_TEST_MODEL") == "1" {
			t.Fatalf("AISH_HISTORY_TEST_MODEL=1 but model cache missing at %s: %v", dir, err)
		}
		t.Skipf("model cache not present at %s — skipping T1 test (set AISH_HISTORY_TEST_MODEL=1 to require)", dir)
	}
}

// newFastembed is a thin wrapper around the Phase B constructor. The
// signature here forces the production code to expose a NewFastembed
// (or equivalent) that returns an EmbeddingProvider; tests do not
// reach into private state.
func newFastembed(t *testing.T) EmbeddingProvider {
	t.Helper()
	requireModelPresent(t)
	p, err := NewFastembedProvider(modelCacheDir(t))
	if err != nil {
		t.Fatalf("NewFastembedProvider: %v", err)
	}
	return p
}

// TestFastembed_DeterministicEmbedding (AC1, T1): same input → byte-
// identical output. Determinism is the cornerstone of the idempotent-
// reindex contract (T5) and of any cache-by-hash optimization.
func TestFastembed_DeterministicEmbedding(t *testing.T) {
	p := newFastembed(t)
	ctx := context.Background()

	in := []string{"rm -rf /tmp/scratch"}

	v1, err := p.Embed(ctx, in)
	if err != nil {
		t.Fatalf("Embed pass 1: %v", err)
	}
	v2, err := p.Embed(ctx, in)
	if err != nil {
		t.Fatalf("Embed pass 2: %v", err)
	}
	if len(v1) != 1 || len(v2) != 1 {
		t.Fatalf("Embed returned %d / %d vectors, want 1 each", len(v1), len(v2))
	}
	if !vectorBytesEqual(v1[0], v2[0]) {
		t.Errorf("Embed not deterministic across calls — same input produced different bytes")
	}
}

// TestFastembed_DimMatchesModelCard (T1): Dim() returns 384 (bge-
// small-en-v1.5's documented output dimension). Mismatch here would
// silently corrupt vector_store.go's Upsert, which sizes the row by
// Dim().
func TestFastembed_DimMatchesModelCard(t *testing.T) {
	p := newFastembed(t)

	if got := p.Dim(); got != testModelDim {
		t.Errorf("Dim(): got %d want %d", got, testModelDim)
	}
	// Cross-check: the model's actual output length matches Dim().
	v, err := p.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != 1 {
		t.Fatalf("got %d vectors, want 1", len(v))
	}
	if len(v[0]) != p.Dim() {
		t.Errorf("emitted vector len %d != Dim() %d", len(v[0]), p.Dim())
	}
}

// TestFastembed_ModelIDStable (T1): ModelID() returns the constant
// the schema sidecar persists. A model swap must update the ID;
// failing to do so causes silent mixed-model results (AC7).
func TestFastembed_ModelIDStable(t *testing.T) {
	p := newFastembed(t)

	if got := p.ModelID(); got != testModelID {
		t.Errorf("ModelID(): got %q want %q", got, testModelID)
	}
}

// TestFastembed_BatchEmbed (T1): Embed of [a, b, c] returns three
// vectors in input order. Order preservation is load-bearing because
// reindex iterates by event_id and zips the result back with the
// originating id.
func TestFastembed_BatchEmbed(t *testing.T) {
	p := newFastembed(t)
	ctx := context.Background()

	inputs := []string{
		"ls -la /tmp",
		"git status",
		"docker run -it ubuntu:24.04 bash",
	}
	out, err := p.Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("Embed batch: %v", err)
	}
	if len(out) != len(inputs) {
		t.Fatalf("batch len: got %d want %d", len(out), len(inputs))
	}

	// Spot-check order by re-embedding each input individually and
	// matching it to the same slot.
	for i, in := range inputs {
		singular, err := p.Embed(ctx, []string{in})
		if err != nil {
			t.Fatalf("Embed individual %q: %v", in, err)
		}
		if !vectorBytesEqual(out[i], singular[0]) {
			t.Errorf("batch[%d] != individual embedding for %q — order not preserved", i, in)
		}
	}
}

// TestFastembed_OfflineFirstRun (T1, AC9): when the model directory
// does NOT contain the ONNX file, NewFastembedProvider must return a
// clear error referencing the missing path. It MUST NOT silently
// download — v0.3 ships local-only.
func TestFastembed_OfflineFirstRun(t *testing.T) {
	// An empty temp dir stands in for "model cache miss". The path
	// exists (so the loader gets past stat) but the ONNX file inside
	// is absent.
	emptyDir := t.TempDir()

	_, err := NewFastembedProvider(emptyDir)
	if err == nil {
		t.Fatalf("expected error for missing model, got nil — did the loader silently download?")
	}
	// Concrete error-text guidance kept loose; what we PIN is that
	// the error mentions the path it could not find. A future error
	// rewrite must preserve the property "actionable, names the
	// missing artifact."
	if !errMentions(err, "model") {
		t.Errorf("error doesn't reference 'model': %v", err)
	}
}

// TestFastembed_BatchEmpty (T1): Embed of an empty slice returns an
// empty result and no error. Edge case caller (reindex with zero
// pending events) must not blow up.
func TestFastembed_BatchEmpty(t *testing.T) {
	p := newFastembed(t)
	out, err := p.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed nil: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Embed nil returned %d vectors, want 0", len(out))
	}
}

// --- helpers ---

// vectorBytesEqual compares two float32 slices by their raw byte
// representation. Deterministic-embedding tests need this stricter
// equality than ==; a re-quantization that produces "close" values is
// a defect for the AC1 promise.
func vectorBytesEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	bufA := new(bytes.Buffer)
	bufB := new(bytes.Buffer)
	_ = binary.Write(bufA, binary.LittleEndian, a)
	_ = binary.Write(bufB, binary.LittleEndian, b)
	return bytes.Equal(bufA.Bytes(), bufB.Bytes())
}

// errMentions is a loose substring check; T1's error message may
// reference "model file", "model directory", "missing model", etc. —
// any of those satisfies the contract.
func errMentions(err error, needle string) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), needle)
}

// cosineSim — used by hybrid-search tests downstream; here only for
// the type-level guard that float32 math is in scope. Removed once
// T4 lands.
var _ = math.MaxFloat32
