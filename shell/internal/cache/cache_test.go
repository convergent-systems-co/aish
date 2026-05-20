package cache

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// newCacheNoPlugin opens a cache with a real Store and no plugin —
// used to exercise the cache-only + ErrNoPlugin paths.
func newCacheNoPlugin(t *testing.T) *Cache {
	t.Helper()
	store := openTemp(t)
	return New(store, nil)
}

// newCacheWithStub opens a cache wired to a freshly-spawned stub
// plugin (compiled by TestMain in plugin_test.go).
func newCacheWithStub(t *testing.T) *Cache {
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
	return New(store, plugin)
}

func TestResolveCacheHitSkipsPlugin(t *testing.T) {
	c := newCacheWithStub(t)
	// Pre-populate the cache so Resolve takes the hit path. Then close
	// the plugin to prove we never called it on the hit.
	if err := c.store.Write("list files", "darwin", "ls -la", 1.0); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := c.plugin.Close(); err != nil {
		t.Fatalf("Close plugin: %v", err)
	}
	c.plugin = nil // drop reference so Resolve cannot ask it

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, fromCache, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !fromCache {
		t.Error("fromCache = false, want true")
	}
	if got != "ls -la" {
		t.Errorf("invocation = %q, want %q", got, "ls -la")
	}
}

func TestResolveCacheMissAsksPluginAndWritesBack(t *testing.T) {
	c := newCacheWithStub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First call misses cache → plugin returns "echo <intent>".
	got1, fromCache1, err := c.Resolve(ctx, "say hi", "darwin")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if fromCache1 {
		t.Error("first call: fromCache = true, want false")
	}
	if want := "echo say hi"; got1 != want {
		t.Errorf("first invocation = %q, want %q", got1, want)
	}

	// Second call must hit the cache — write-back proved itself.
	got2, fromCache2, err := c.Resolve(ctx, "say hi", "darwin")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if !fromCache2 {
		t.Error("second call: fromCache = false, want true")
	}
	if got2 != got1 {
		t.Errorf("second invocation = %q, want %q (same as first)", got2, got1)
	}

	stats, err := c.store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 1 || stats.Misses != 1 || stats.Entries != 1 {
		t.Errorf("stats = %+v, want hits=1 misses=1 entries=1", stats)
	}
}

func TestResolveNoPluginReturnsErrNoPlugin(t *testing.T) {
	c := newCacheNoPlugin(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := c.Resolve(ctx, "anything", "darwin")
	if err == nil {
		t.Fatal("Resolve: expected error, got nil")
	}
	if !errors.Is(err, ErrNoPlugin) {
		t.Errorf("Resolve: err = %v, want ErrNoPlugin", err)
	}
}

func TestResolveNilStoreFails(t *testing.T) {
	c := &Cache{}
	if _, _, err := c.Resolve(context.Background(), "intent", "darwin"); err == nil {
		t.Error("Resolve on empty Cache: expected error, got nil")
	}
}

func TestResolvePluginErrorPropagates(t *testing.T) {
	c := newCacheWithStub(t)
	// Close the plugin to force an Infer failure; the read goroutine
	// will see EOF and close the per-request channel, surfacing as a
	// "stream closed" error.
	if err := c.plugin.Close(); err != nil {
		t.Fatalf("Close plugin: %v", err)
	}
	// Give the reader a moment to drain its EOF state.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := c.Resolve(ctx, "an intent we never cached", "darwin")
	if err == nil {
		t.Fatal("Resolve: expected error after plugin Close, got nil")
	}
	if errors.Is(err, ErrNoPlugin) {
		t.Errorf("Resolve: err = %v, want plugin error (not ErrNoPlugin)", err)
	}
}
