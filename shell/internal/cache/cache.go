package cache

import (
	"context"
	"errors"
	"fmt"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// CommunityLookup is the contract Cache uses to consult an opened
// L3 community bundle. *community.Bundle satisfies this interface;
// expressed here so tests can substitute fakes without spinning up a
// signed bundle on disk.
type CommunityLookup interface {
	Lookup(intent, os string) (string, bool, error)
}

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
	community           CommunityLookup
	similarityThreshold float64
	// systemPromptSource returns the system prompt to inject into
	// Infer calls — v0.3-5 persona seam. nil means "no injection,"
	// preserving v0.1-2 behaviour exactly.
	systemPromptSource func() string
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

// WithSystemPromptSource installs a callback the cache invokes ahead
// of every Infer call to fetch the persona-derived system prompt.
// When the callback returns an empty string OR the source is nil,
// the cache calls plugin.Infer with no system prompt (identical to
// v0.1-2 behaviour). v0.3-5 persona seam — see GOALS.md §Epic v0.3-5.
//
// The callback is invoked per Resolve call so a `persona set` mid-
// session takes effect on the next intent.
func (c *Cache) WithSystemPromptSource(src func() string) *Cache {
	if c == nil {
		return nil
	}
	c.systemPromptSource = src
	return c
}

// WithCommunityBundle attaches an L3 community bundle that Resolve
// will consult on every L1 miss BEFORE walking the inference plugin.
// Pass nil to detach. Returns the receiver for chaining.
//
// The bundle is consulted read-only; on an L3 hit the row is promoted
// to L1 with source=SourceCommunity so subsequent calls land on the
// existing exact-hash path. L1 takes priority over L3 — an
// already-resolved intent in L1 never goes near the community bundle.
func (c *Cache) WithCommunityBundle(b CommunityLookup) *Cache {
	if c == nil {
		return nil
	}
	c.community = b
	return c
}

// CommunityBundle returns the attached community-bundle lookup, if
// any. Exposed so the `aish community info` built-in can report
// whether the L3 tier is wired without reaching past the cache.
func (c *Cache) CommunityBundle() CommunityLookup {
	if c == nil {
		return nil
	}
	return c.community
}

// promoteFromCommunity writes a community-bundle row into L1 with
// source=SourceCommunity. Called inline from Resolve on an L3 hit so
// the next call for the same intent lands on the existing
// exact-hash path. Returns the underlying Store.WriteWithSource
// error unchanged.
func (c *Cache) promoteFromCommunity(intent, os, invocation string) error {
	if c == nil || c.store == nil {
		return errors.New("cache: promoteFromCommunity: nil store")
	}
	return c.store.WriteWithSource(intent, os, invocation, 1.0, nil, SourceCommunity)
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
	// L1 wins over L3 by construction: an intent already present in
	// L1 (whether plugin-sourced or previously promoted from
	// community) never re-consults the bundle.
	invocation, hit, err := c.store.Lookup(intent, os)
	if err != nil {
		return "", false, fmt.Errorf("cache: Resolve: lookup: %w", err)
	}
	if hit {
		return invocation, true, nil
	}

	// Tier 1.5 — L3 community bundle. Read-only signed corpus that
	// pre-populates the cache for fresh installs. On hit, promote
	// the row to L1 with source=SourceCommunity so subsequent calls
	// land on the existing exact-hash path. Best-effort: any L3
	// error falls through to the plugin path so the bundle never
	// degrades v0.1-2 behaviour.
	if c.community != nil {
		l3Invocation, l3Hit, l3Err := c.community.Lookup(intent, os)
		if l3Err == nil && l3Hit {
			if werr := c.promoteFromCommunity(intent, os, l3Invocation); werr != nil {
				return "", false, fmt.Errorf("cache: Resolve: community promote: %w", werr)
			}
			return l3Invocation, true, nil
		}
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
	//
	// v0.3-5 persona seam: when a systemPromptSource is installed and
	// returns a non-empty string, prefix it onto the intent so the
	// plugin gateway sees the persona's voice/tone instructions.
	// Workaround for the proto.InferParams extension that's deferred
	// to v0.3-5.1; see .artifacts/plans/v0.3-5.md.
	inferIntent := intent
	if c.systemPromptSource != nil {
		if sp := c.systemPromptSource(); sp != "" {
			inferIntent = sp + "\n\n<user>\n" + intent + "\n</user>"
		}
	}
	invocation, confidence, err := c.plugin.Infer(ctx, inferIntent, os)
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
