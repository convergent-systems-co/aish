package cache

import (
	"context"
	"errors"
	"fmt"
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
	store  *Store
	plugin *PluginClient
}

// New wires a Store and optional PluginClient together. The Store is
// required; passing nil here is a programmer error and panics at
// Resolve time rather than returning a less helpful error.
func New(store *Store, plugin *PluginClient) *Cache {
	return &Cache{store: store, plugin: plugin}
}

// Resolve compiles `intent` for `os` to a shell-ready invocation,
// preferring the local cache before falling back to the inference
// plugin.
//
// Three return shapes:
//
//	(invocation, true,  nil) — cache hit; no plugin call was made.
//	(invocation, false, nil) — cache miss; plugin produced the result
//	                            and it was written back for next time.
//	("",         false, err) — cache miss with no plugin (err =
//	                            ErrNoPlugin), or the plugin failed.
//
// The write-back after a successful plugin call uses confidence as
// returned by the plugin. A write-back failure is logged via the
// returned error path — we still hand the invocation back to the
// caller, but the next call for the same intent will miss again.
// Actually, on second thought: a write-back failure indicates a bigger
// problem (DB closed, disk full) and we surface it; callers that want
// to ignore write-back faults can branch on errors.Is.
func (c *Cache) Resolve(ctx context.Context, intent, os string) (string, bool, error) {
	if c == nil || c.store == nil {
		return "", false, errors.New("cache: Resolve: nil store")
	}

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

	invocation, confidence, err := c.plugin.Infer(ctx, intent, os)
	if err != nil {
		return "", false, fmt.Errorf("cache: Resolve: infer: %w", err)
	}

	// Write-back. A failure here is unusual (SQLite is local) but real;
	// surface it so the user knows the cache is unhealthy. The
	// invocation itself is still valid — callers that want it anyway
	// can `errors.Is(err, somethingSpecific)` in the future. For now
	// we treat write-back failure as a hard error.
	if err := c.store.Write(intent, os, invocation, confidence); err != nil {
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
