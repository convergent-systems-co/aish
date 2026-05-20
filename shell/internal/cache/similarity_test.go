package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newCacheWithStubAtThreshold opens a cache wired to the stub plugin
// with a non-default similarity threshold. The stub plugin's StubEmbed
// produces deterministic unit-norm vectors from the intent text, so
// tests can dial the threshold around the actual cosine between two
// chosen intents.
func newCacheWithStubAtThreshold(t *testing.T, threshold float64) *Cache {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	plugin, err := Start(PluginConfig{BinaryPath: stubBinary})
	if err != nil {
		t.Fatalf("plugin Start: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close() })
	return New(store, plugin).WithSimilarityThreshold(threshold)
}

// TestResolve_ExactMatchTakesPriorityOverSimilarity — the most
// important regression guard. After v0.1-2 (PR #174), exact-hash hits
// are zero-RPC. The similarity branch MUST NOT regress that.
//
// Setup: pre-populate the cache with an exact-hash row. Close the
// plugin (so any RPC the similarity branch tries to make would fail
// loudly). Resolve must still return the cached invocation.
func TestResolve_ExactMatchTakesPriorityOverSimilarity(t *testing.T) {
	c := newCacheWithStubAtThreshold(t, DefaultSimilarityThreshold)
	if err := c.store.Write("list files", "darwin", "ls -la", 1.0, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Close the plugin and drop the reference — any RPC attempt would
	// surface as an error. A correctly-implemented Resolve never gets
	// there because the exact-hash hit short-circuits.
	if err := c.plugin.Close(); err != nil {
		t.Fatalf("Close plugin: %v", err)
	}
	c.plugin = nil

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, fromCache, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !fromCache {
		t.Error("fromCache = false, want true (exact hash hit should win)")
	}
	if got != "ls -la" {
		t.Errorf("got %q, want %q", got, "ls -la")
	}
}

// TestResolve_SimilarityHit_AtUnity_PromotesToExactNext — when the
// query vector matches a stored row at cosine 1.0 (here, by storing
// the row with the SAME intent's StubEmbed vector under a different
// intent key), the similarity branch returns the matched invocation
// AND writes the queried intent back so the next call lands on
// exact-hash.
func TestResolve_SimilarityHit_AtUnity_PromotesToExactNext(t *testing.T) {
	c := newCacheWithStubAtThreshold(t, 0.0) // accept any positive similarity

	// Pre-populate the store with row R under intent "canonical". Use
	// the StubEmbed of "paraphrase" as R's embedding so the first
	// Resolve("paraphrase") will compute a query vector that matches
	// R's stored vector at cosine 1.0.
	canonicalInvocation := "echo canonical-resolution"
	if err := c.store.Write("canonical", "darwin", canonicalInvocation, 1.0, stubEmbedHelper("paraphrase")); err != nil {
		t.Fatalf("Write canonical: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First call: exact-hash miss on "paraphrase" → Embed("paraphrase")
	// → LookupNearest finds canonical at similarity 1.0 → returns
	// canonical's invocation. fromCache = true (the canonical answer
	// came from the cache, not from a fresh Infer RPC).
	got, fromCache, err := c.Resolve(ctx, "paraphrase", "darwin")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if !fromCache {
		t.Error("first call: fromCache = false, want true (similarity branch should report a cache hit)")
	}
	if got != canonicalInvocation {
		t.Errorf("first call got = %q, want %q", got, canonicalInvocation)
	}

	// Second call: the similarity hit was promoted to an exact-hash
	// hit on the write-back. Hit counters should be 1 hits / 1 misses
	// after this. (The first miss was the exact-hash miss before the
	// similarity branch ran.)
	got2, fromCache2, err := c.Resolve(ctx, "paraphrase", "darwin")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if !fromCache2 {
		t.Error("second call: fromCache = false, want true (promoted exact-hash hit)")
	}
	if got2 != canonicalInvocation {
		t.Errorf("second call got = %q, want %q", got2, canonicalInvocation)
	}
}

// TestResolve_SimilarityBelowThreshold_FallsThroughToInfer — when the
// query vector is below the threshold, the similarity branch MUST NOT
// surface a match; Resolve falls through to Infer.
//
// Setup: pre-populate with a row whose stored embedding is the
// StubEmbed of "shore" (some arbitrary text). The query is "moon"
// (different text → different vector → low cosine). With a
// near-unity threshold, the similarity branch should reject and
// Resolve should hit the Infer path (which the stub-plugin handles
// by returning "echo <intent>").
func TestResolve_SimilarityBelowThreshold_FallsThroughToInfer(t *testing.T) {
	c := newCacheWithStubAtThreshold(t, 0.999) // very tight — only near-self-similarity wins

	if err := c.store.Write("shore", "darwin", "echo shore-resolution", 1.0, stubEmbedHelper("shore")); err != nil {
		t.Fatalf("Write shore: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "moon" → embed → similarity to "shore" embed → almost certainly
	// below 0.999 (sha256-derived vectors are near-orthogonal in
	// expectation). Resolve should fall through to Infer, whose stub
	// returns "echo moon".
	got, fromCache, err := c.Resolve(ctx, "moon", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fromCache {
		t.Error("fromCache = true, want false (threshold should have rejected the similarity hit)")
	}
	if got != "echo moon" {
		t.Errorf("got = %q, want %q (Infer path stub)", got, "echo moon")
	}
}

// TestResolve_PluginInferWriteBackIncludesEmbedding — after a miss
// triggers Infer, the row is written back WITH its embedding so the
// next call for a paraphrase can land via the similarity branch.
func TestResolve_PluginInferWriteBackIncludesEmbedding(t *testing.T) {
	c := newCacheWithStubAtThreshold(t, 0.0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := c.Resolve(ctx, "novel intent", "darwin"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Read the embedding column back directly.
	var blob []byte
	if err := c.store.db.QueryRow(
		`SELECT embedding FROM intents WHERE intent='novel intent'`,
	).Scan(&blob); err != nil {
		t.Fatalf("query embedding: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("post-Infer row has no embedding; want a populated BLOB")
	}
	decoded, err := decodeEmbedding(blob)
	if err != nil {
		t.Fatalf("decodeEmbedding: %v", err)
	}
	want := stubEmbedHelper("novel intent")
	if len(decoded) != len(want) {
		t.Fatalf("vector len = %d, want %d", len(decoded), len(want))
	}
	for i := range want {
		if decoded[i] != want[i] {
			t.Errorf("vector[%d] = %v, want %v", i, decoded[i], want[i])
			break
		}
	}
}

// TestResolve_PerOSIsolationInSimilarityBranch — when an embedding
// exists under one OS, it must NOT be returned for a query targeting
// a different OS.
func TestResolve_PerOSIsolationInSimilarityBranch(t *testing.T) {
	c := newCacheWithStubAtThreshold(t, 0.0)

	// Pre-populate under darwin with the embed of "x" so a query
	// vector for the same text would match at 1.0.
	if err := c.store.Write("x", "darwin", "echo darwin-x", 1.0, stubEmbedHelper("x")); err != nil {
		t.Fatalf("Write darwin: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query under linux — must NOT return the darwin row. Resolve
	// should fall through to Infer (which the stub answers).
	got, fromCache, err := c.Resolve(ctx, "x", "linux")
	if err != nil {
		t.Fatalf("Resolve linux: %v", err)
	}
	if fromCache {
		t.Error("linux query reported fromCache=true; per-OS isolation broken")
	}
	if got != "echo x" {
		// The stub returns "echo <intent>" regardless of OS — verifies
		// the path was Infer-tier, not a darwin cache leak.
		t.Errorf("got = %q, want %q", got, "echo x")
	}
}

// TestCache_WithSimilarityThreshold_ClampsToUnitInterval — defensive
// guard so a caller passing -0.5 or 1.5 doesn't break the LookupNearest
// contract.
func TestCache_WithSimilarityThreshold_ClampsToUnitInterval(t *testing.T) {
	c := newCacheNoPlugin(t)
	c.WithSimilarityThreshold(-0.5)
	if c.SimilarityThreshold() != 0 {
		t.Errorf("threshold = %v, want 0 (clamped)", c.SimilarityThreshold())
	}
	c.WithSimilarityThreshold(1.5)
	if c.SimilarityThreshold() != 1 {
		t.Errorf("threshold = %v, want 1 (clamped)", c.SimilarityThreshold())
	}
	c.WithSimilarityThreshold(0.7)
	if c.SimilarityThreshold() != 0.7 {
		t.Errorf("threshold = %v, want 0.7", c.SimilarityThreshold())
	}
}

// TestCache_NewDefaultsThreshold — ensure New uses the documented
// DefaultSimilarityThreshold rather than zero.
func TestCache_NewDefaultsThreshold(t *testing.T) {
	c := newCacheNoPlugin(t)
	if c.SimilarityThreshold() != DefaultSimilarityThreshold {
		t.Errorf("default threshold = %v, want %v", c.SimilarityThreshold(), DefaultSimilarityThreshold)
	}
}
