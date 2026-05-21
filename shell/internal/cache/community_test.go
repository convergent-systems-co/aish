package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeCommunity is an in-memory CommunityLookup. Avoids spinning up
// a signed bundle on disk for the Cache-level integration tests; the
// community subpackage has its own coverage of the verify/load path.
type fakeCommunity struct {
	rows  map[string]string // key = intent+"|"+os
	err   error
	calls int
}

func (f *fakeCommunity) Lookup(intent, os string) (string, bool, error) {
	f.calls++
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.rows[intent+"|"+os]
	return v, ok, nil
}

func newCacheWithCommunity(t *testing.T, rows map[string]string) (*Cache, *fakeCommunity) {
	t.Helper()
	c := newCacheNoPlugin(t)
	fc := &fakeCommunity{rows: rows}
	c.WithCommunityBundle(fc)
	return c, fc
}

func TestResolveCommunityHitPromotesToL1(t *testing.T) {
	c, fc := newCacheWithCommunity(t, map[string]string{
		"list files|darwin": "ls -la",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, hit, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !hit {
		t.Error("hit = false; want true (L3 hit reports as cache hit)")
	}
	if got != "ls -la" {
		t.Errorf("invocation = %q; want %q", got, "ls -la")
	}
	if fc.calls != 1 {
		t.Errorf("community Lookup calls = %d; want 1", fc.calls)
	}

	// Second call must skip L3 entirely — L1 wins.
	got2, hit2, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if !hit2 {
		t.Error("second hit = false; want true (promotion proved itself)")
	}
	if got2 != "ls -la" {
		t.Errorf("second invocation = %q; want %q", got2, "ls -la")
	}
	if fc.calls != 1 {
		t.Errorf("community Lookup calls after L1 hit = %d; want 1 (L3 must be skipped)", fc.calls)
	}

	// Provenance: the promoted row carries source=SourceCommunity.
	bySrc, err := c.store.EntriesBySource()
	if err != nil {
		t.Fatalf("EntriesBySource: %v", err)
	}
	if got := bySrc[SourceCommunity]; got != 1 {
		t.Errorf("community-sourced entries = %d; want 1 (bySource = %+v)", got, bySrc)
	}
}

func TestResolveL1WinsOverCommunity(t *testing.T) {
	c, fc := newCacheWithCommunity(t, map[string]string{
		"list files|darwin": "L3 invocation",
	})
	// Pre-populate L1 with a different invocation.
	if err := c.store.Write("list files", "darwin", "L1 invocation", 1.0, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, hit, err := c.Resolve(ctx, "list files", "darwin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !hit {
		t.Error("hit = false; want true")
	}
	if got != "L1 invocation" {
		t.Errorf("invocation = %q; want %q (L1 must win over L3)", got, "L1 invocation")
	}
	if fc.calls != 0 {
		t.Errorf("community Lookup calls = %d; want 0 (L3 must not be consulted on L1 hit)", fc.calls)
	}
}

func TestResolveCommunityMissReturnsErrNoPlugin(t *testing.T) {
	// No plugin, empty community bundle, L1 miss → ErrNoPlugin (same
	// contract as the existing TestResolveNoPluginReturnsErrNoPlugin,
	// extended for the L3 wiring).
	c, _ := newCacheWithCommunity(t, map[string]string{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Resolve(ctx, "novel intent", "darwin")
	if !errors.Is(err, ErrNoPlugin) {
		t.Errorf("err = %v; want ErrNoPlugin", err)
	}
}

func TestResolveCommunityErrorDoesNotFail(t *testing.T) {
	c, fc := newCacheWithCommunity(t, nil)
	fc.err = errors.New("synthetic bundle read failure")
	// No plugin either — so the resolver falls through to ErrNoPlugin
	// after the L3 path soft-fails, NOT a bundle-read error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Resolve(ctx, "anything", "darwin")
	if !errors.Is(err, ErrNoPlugin) {
		t.Errorf("err = %v; want ErrNoPlugin (bundle errors must degrade silently)", err)
	}
}

func TestMigrateSourceColumnIdempotent(t *testing.T) {
	store := openTemp(t)
	// Run migration explicitly twice — the second call must be a
	// no-op because the column already exists.
	if err := migrateSourceColumn(store.db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := migrateSourceColumn(store.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	// And Write still works.
	if err := store.WriteWithSource("a", "darwin", "echo a", 1.0, nil, SourceCommunity); err != nil {
		t.Fatalf("WriteWithSource: %v", err)
	}
	bySrc, err := store.EntriesBySource()
	if err != nil {
		t.Fatalf("EntriesBySource: %v", err)
	}
	if bySrc[SourceCommunity] != 1 {
		t.Errorf("entries by source = %+v; want community=1", bySrc)
	}
}

func TestWriteSetsDefaultSourcePlugin(t *testing.T) {
	// Existing v0.1-2 callers of Store.Write expect their rows to
	// carry source='plugin' implicitly.
	store := openTemp(t)
	if err := store.Write("intent", "darwin", "echo x", 1.0, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bySrc, err := store.EntriesBySource()
	if err != nil {
		t.Fatalf("EntriesBySource: %v", err)
	}
	if bySrc[SourcePlugin] != 1 {
		t.Errorf("entries by source = %+v; want plugin=1", bySrc)
	}
}
