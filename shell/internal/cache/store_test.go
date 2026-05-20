package cache

import (
	"path/filepath"
	"testing"
)

// openTemp opens a fresh Store at <tempdir>/cache.db. The Store is
// closed automatically when the test exits.
func openTemp(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestOpenCreatesDB(t *testing.T) {
	store := openTemp(t)
	// Stats should return zero counters on a fresh DB — proves the DDL
	// ran and the stats rows were seeded.
	got, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if want := (Stats{}); got != want {
		t.Errorf("fresh stats = %+v, want %+v", got, want)
	}
}

func TestLookupMissThenHit(t *testing.T) {
	store := openTemp(t)
	const (
		intent = "list files in cwd"
		os     = "darwin"
		invoc  = "ls -la"
	)

	if _, hit, err := store.Lookup(intent, os); err != nil || hit {
		t.Fatalf("first Lookup: hit=%v err=%v; want miss, no error", hit, err)
	}
	if got, err := store.Stats(); err != nil {
		t.Fatalf("Stats after miss: %v", err)
	} else if got.Misses != 1 || got.Hits != 0 || got.TotalQueries != 1 {
		t.Fatalf("post-miss stats = %+v, want misses=1 hits=0 total=1", got)
	}

	if err := store.Write(intent, os, invoc, 0.9, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for i := 1; i <= 3; i++ {
		got, hit, err := store.Lookup(intent, os)
		if err != nil || !hit || got != invoc {
			t.Fatalf("Lookup #%d: got=%q hit=%v err=%v", i, got, hit, err)
		}
	}
	gotStats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats after hits: %v", err)
	}
	if gotStats.Hits != 3 || gotStats.Misses != 1 || gotStats.TotalQueries != 4 || gotStats.Entries != 1 {
		t.Errorf("final stats = %+v, want hits=3 misses=1 total=4 entries=1", gotStats)
	}
}

func TestWriteUpserts(t *testing.T) {
	store := openTemp(t)
	const (
		intent = "delete logs"
		os     = "linux"
	)

	if err := store.Write(intent, os, "rm -f /var/log/*.log", 0.7, nil); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	// Hit once so hit_count is non-zero.
	if _, hit, _ := store.Lookup(intent, os); !hit {
		t.Fatal("expected hit after first Write")
	}

	// Second Write replaces invocation + confidence.
	if err := store.Write(intent, os, "find /var/log -name '*.log' -delete", 0.95, nil); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	got, hit, err := store.Lookup(intent, os)
	if err != nil || !hit {
		t.Fatalf("Lookup post-upsert: hit=%v err=%v", hit, err)
	}
	if want := "find /var/log -name '*.log' -delete"; got != want {
		t.Errorf("invocation = %q, want %q", got, want)
	}

	// Stats survived the upsert (we accumulated 2 hits + 0 misses now;
	// wait — the first Lookup-before-Write was a hit, second hit
	// follows the second Write. We made: Lookup hit, Lookup hit. So
	// hits=2, misses=0, total=2.).
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 2 || stats.Misses != 0 || stats.Entries != 1 {
		t.Errorf("upsert stats = %+v, want hits=2 misses=0 entries=1", stats)
	}
}

func TestPerOSIsolation(t *testing.T) {
	store := openTemp(t)
	const intent = "remove a directory recursively"
	cases := []struct {
		os    string
		invoc string
	}{
		{"darwin", "rm -rf"},
		{"linux", "rm -rf"},
		{"windows", "Remove-Item -Recurse -Force"},
	}
	for _, tc := range cases {
		if err := store.Write(intent, tc.os, tc.invoc, 1.0, nil); err != nil {
			t.Fatalf("Write(%s): %v", tc.os, err)
		}
	}
	for _, tc := range cases {
		got, hit, err := store.Lookup(intent, tc.os)
		if err != nil || !hit {
			t.Fatalf("Lookup(%s): hit=%v err=%v", tc.os, hit, err)
		}
		if got != tc.invoc {
			t.Errorf("Lookup(%s) = %q, want %q", tc.os, got, tc.invoc)
		}
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Entries != 3 {
		t.Errorf("entries = %d, want 3 (one per OS)", stats.Entries)
	}
}

func TestStatsCountersRoundTrip(t *testing.T) {
	store := openTemp(t)
	const os = "darwin"
	// 5 distinct intents → 5 misses.
	intents := []string{"a", "b", "c", "d", "e"}
	for _, i := range intents {
		if _, hit, _ := store.Lookup(i, os); hit {
			t.Fatalf("Lookup(%q): unexpected hit", i)
		}
	}
	// Populate two of them and hit them twice each.
	for _, i := range []string{"a", "b"} {
		if err := store.Write(i, os, "echo "+i, 1.0, nil); err != nil {
			t.Fatalf("Write(%q): %v", i, err)
		}
		for j := 0; j < 2; j++ {
			if _, hit, _ := store.Lookup(i, os); !hit {
				t.Fatalf("Lookup(%q) #%d: expected hit", i, j)
			}
		}
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	const wantHits, wantMisses int64 = 4, 5
	if stats.Hits != wantHits || stats.Misses != wantMisses {
		t.Errorf("counters = %+v, want hits=%d misses=%d", stats, wantHits, wantMisses)
	}
	if stats.TotalQueries != wantHits+wantMisses {
		t.Errorf("total = %d, want %d", stats.TotalQueries, wantHits+wantMisses)
	}
	if stats.Entries != 2 {
		t.Errorf("entries = %d, want 2", stats.Entries)
	}
}

func TestClearTruncates(t *testing.T) {
	store := openTemp(t)
	for _, i := range []string{"x", "y", "z"} {
		if err := store.Write(i, "darwin", "echo "+i, 1.0, nil); err != nil {
			t.Fatalf("Write(%q): %v", i, err)
		}
		// One lookup each to push counters above zero.
		if _, hit, _ := store.Lookup(i, "darwin"); !hit {
			t.Fatalf("Lookup(%q): expected hit", i)
		}
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if want := (Stats{}); stats != want {
		t.Errorf("post-clear stats = %+v, want %+v", stats, want)
	}
	// And lookups are misses again.
	if _, hit, _ := store.Lookup("x", "darwin"); hit {
		t.Error("post-clear Lookup unexpectedly hit")
	}
}

func TestNormalizationCollapsesCosmeticDifferences(t *testing.T) {
	store := openTemp(t)
	if err := store.Write("List Files", "darwin", "ls -la", 1.0, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Trailing space + uppercase + leading space should all hit.
	for _, variant := range []string{"list files", "  List Files  ", "LIST FILES"} {
		got, hit, err := store.Lookup(variant, "darwin")
		if err != nil || !hit {
			t.Errorf("Lookup(%q): hit=%v err=%v", variant, hit, err)
		}
		if got != "ls -la" {
			t.Errorf("Lookup(%q) = %q, want %q", variant, got, "ls -la")
		}
	}
}

func TestConfidenceClampedToUnit(t *testing.T) {
	store := openTemp(t)
	// Negative and >1 confidences are clamped silently. We verify by
	// peeking at the column directly; the public API doesn't expose it.
	if err := store.Write("a", "darwin", "echo a", -0.5, nil); err != nil {
		t.Fatalf("Write -0.5: %v", err)
	}
	if err := store.Write("b", "darwin", "echo b", 1.5, nil); err != nil {
		t.Fatalf("Write 1.5: %v", err)
	}
	var confA, confB float64
	if err := store.db.QueryRow(`SELECT confidence FROM intents WHERE intent = 'a'`).Scan(&confA); err != nil {
		t.Fatalf("query a: %v", err)
	}
	if err := store.db.QueryRow(`SELECT confidence FROM intents WHERE intent = 'b'`).Scan(&confB); err != nil {
		t.Fatalf("query b: %v", err)
	}
	if confA != 0 {
		t.Errorf("confA = %v, want 0 (clamped)", confA)
	}
	if confB != 1 {
		t.Errorf("confB = %v, want 1 (clamped)", confB)
	}
}

func TestWriteValidatesInputs(t *testing.T) {
	store := openTemp(t)
	cases := []struct {
		name, intent, os, invoc string
	}{
		{"empty intent", "", "darwin", "echo"},
		{"empty os", "list", "", "echo"},
		{"empty invocation", "list", "darwin", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Write(tc.intent, tc.os, tc.invoc, 1.0, nil); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
