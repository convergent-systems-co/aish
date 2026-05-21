package cache

import (
	"context"
	"errors"
	"fmt"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// ErrNoPlugin is returned by Resolve when the cache misses and no
// PluginClient is configured. The shell dispatch loop treats this as a
// "command not found"-style condition: aish has nothing in cache and
// no inference plugin to ask, so the user sees the equivalent of a
// normal shell's "command not found" message.
//
// Exposed as a sentinel so the shell can distinguish "no plugin" from
// "plugin returned an error" — the former is a configuration issue,
// the latter is a runtime fault that may deserve different UI.
var ErrNoPlugin = errors.New("cache: no inference plugin configured")

// Cache is the high-level orchestrator the shell talks to. It owns a
// Store (always present) and an optional PluginClient (nil = cache-only
// mode for offline use or testing). The Resolve method is the single
// entry point the shell dispatcher invokes per intent.
//
// Cache does not spawn the plugin itself; the caller (cmd/aish/main.go)
// calls cache.Start, holds the resulting *PluginClient for the life of
// the shell session, and passes it to cache.New. This keeps the
// lifecycle boundary explicit and lets tests inject nil for cache-only
// behaviour.
type Cache struct {
	store               *Store
	plugin              *PluginClient
	similarityThreshold float64
}

// New wires a Store and optional PluginClient together. The Store is
// required; passing nil here is a programmer error and panics at
// Resolve time rather than returning a less helpful error.
//
// The similarity threshold defaults to DefaultSimilarityThreshold;
// override with WithSimilarityThreshold.
func New(store *Store, plugin *PluginClient) *Cache {
	return &Cache{
		store:               store,
		plugin:              plugin,
		similarityThreshold: DefaultSimilarityThreshold,
	}
}

// WithSimilarityThreshold sets the cosine-similarity floor at or above
// which a LookupNearest match is treated as a cache hit. Returns the
// Cache for chaining. Values outside [0, 1] are clamped — a negative
// threshold would let anti-aligned vectors masquerade as matches, and
// a threshold > 1 would suppress every possible match.
func (c *Cache) WithSimilarityThreshold(t float64) *Cache {
	if c == nil {
		return nil
	}
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	c.similarityThreshold = t
	return c
}

// SimilarityThreshold returns the current threshold. Exposed mainly for
// tests and diagnostic output (`aish cache stats` in a future revision
// may print it).
func (c *Cache) SimilarityThreshold() float64 {
	if c == nil {
		return 0
	}
	return c.similarityThreshold
}

// Resolve compiles `intent` for `os` to a shell-ready invocation,
// preferring the local cache before falling back to the inference
// plugin.
//
// Lookup order:
//
//  1. Exact-hash Lookup (the v0.1-2 cache L1 path; counters bump here).
//  2. If miss and the plugin supports embeddings:
//     a. Plugin.Embed(intent) → query vector.
//     b. Store.LookupNearest(vector, threshold, os) → similarity hit.
//     c. On similarity hit, write the freshly-embedded intent back
//     under its own exact-hash key (with the matched row's
//     invocation) so the next call lands on the exact-hash path
//     in tier (1) above.
//  3. Plugin.Infer for genuinely novel intents; write back with the
//     freshly-generated embedding so the next call benefits.
//
// Three return shapes (unchanged from the v0.1-2 contract):
//
//	(invocation, true,  nil) — cache hit (exact or similarity).
//	(invocation, false, nil) — plugin produced the result and it was
//	                            written back for next time.
//	("",         false, err) — cache miss with no plugin (err =
//	                            ErrNoPlugin), or the plugin failed.
//
// On embedding-pipeline failure (Embed call errors, LookupNearest
// errors), Resolve degrades gracefully to the plain Infer path — the
// cache should never be worse than v0.1-2's behavior even when the
// similarity branch malfunctions.
func (c *Cache) Resolve(ctx context.Context, intent, os string) (string, bool, error) {
	if c == nil || c.store == nil {
		return "", false, errors.New("cache: Resolve: nil store")
	}

	// Tier 1 — exact-hash cache hit takes priority. Same path as v0.1-2.
	invocation, hit, err := c.store.Lookup(intent, os)
	if err != nil {
		return "", false, fmt.Errorf("cache: Resolve: lookup: %w", err)
	}
	if hit {
		return invocation, true, nil
	}

	if c.plugin == nil {
		return "", false, ErrNoPlugin
	}

	// Tier 2 — embedding similarity. Best-effort: any failure here
	// (Embed errors, LookupNearest errors) falls through to Infer
	// rather than failing the whole Resolve. We capture the query
	// vector here so it can be persisted with the freshly-inferred
	// invocation in tier 3 — saves a second Embed call.
	//
	// Special case: when the plugin signals that its gateway does not
	// implement embeddings (proto.ErrEmbedNotImplemented sentinel —
	// see #178), there is no point retrying on every Resolve. We treat
	// it the same as a soft Embed error (skip the similarity branch)
	// and the queryVector stays nil — write-back in tier 3 records the
	// row without an embedding, which is the documented v0.1-2 "no
	// embedding for this row" path.
	queryVector, embErr := c.plugin.Embed(ctx, intent)
	if errors.Is(embErr, proto.ErrEmbedNotImplemented) {
		// Explicit no-op: skip similarity, fall straight through to Infer.
		queryVector = nil
	} else if embErr == nil && len(queryVector) > 0 {
		_, simInvocation, _, simHit, simErr := c.store.LookupNearest(queryVector, c.similarityThreshold, os)
		if simErr == nil && simHit {
			// Promote the similarity hit to an exact-hash hit for next
			// time by writing the queried intent under its own hash key
			// with the matched invocation + the freshly-issued
			// embedding. Confidence carries through from the matched
			// row at 1.0 — the similarity match itself is the
			// confidence signal here.
			if werr := c.store.Write(intent, os, simInvocation, 1.0, queryVector); werr != nil {
				return "", false, fmt.Errorf("cache: Resolve: similarity write-back: %w", werr)
			}
			return simInvocation, true, nil
		}
	}

	// Tier 3 — Infer for genuinely novel intents.
	invocation, confidence, err := c.plugin.Infer(ctx, intent, os)
	if err != nil {
		return "", false, fmt.Errorf("cache: Resolve: infer: %w", err)
	}

	// Write-back. Use the previously-captured queryVector if we have
	// one, so the next call lands on either the exact-hash path or
	// the similarity branch.
	if err := c.store.Write(intent, os, invocation, confidence, queryVector); err != nil {
		return "", false, fmt.Errorf("cache: Resolve: write-back: %w", err)
	}
	return invocation, false, nil
}

// Store returns the underlying Store. Exposed so the `aish cache stats`
// built-in can read the counters without a redundant indirection.
// Callers MUST NOT Close the returned store; that is the Cache owner's
// responsibility.
func (c *Cache) Store() *Store {
	if c == nil {
		return nil
	}
	return c.store
}
