// Benchmark + AC8 SLO assertion for the chromem-go vector store.
//
// AC8 (plan §Acceptance Criteria): p50 < 50ms, p95 < 150ms for a
// top-10 query over a 10K-event store on a 2023 MBP M2. The bench
// here is a CI-blocking SLO check: regressions push p50/p95 over
// the budget and fail the test.
//
// Why a test, not just a Benchmark*: go test -bench reports raw ns/op
// numbers but does not fail on regression. AC8 is a contract — the
// test must fail when the SLO is broken so CI catches the regression
// before merge. The benchmark function is preserved alongside so
// `go test -bench=Query10K` still produces a noisy histogram for
// performance investigation.

package history

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"
)

// bench10KSize is the corpus size AC8 specifies. Kept as a const so a
// future "100K" variant lands as a second test, not a magic number
// drift in this one.
const bench10KSize = 10000

// bench10KK is the top-K AC8 specifies (10 results).
const bench10KK = 10

// benchP50LimitMS / benchP95LimitMS are the AC8 SLO ceilings. Values
// in milliseconds.
const (
	benchP50LimitMS = 50
	benchP95LimitMS = 150
)

// TestVectorStore_Query10K_LatencySLO_AC8 is the CI-blocking
// assertion: build a 10K-event store, run 200 top-10 queries against
// random vectors, assert p50 < 50ms and p95 < 150ms.
//
// Skipped by default in -short mode because seeding 10K vectors
// costs ~5s. CI runs without -short. Local dev can opt in via
// AISH_HISTORY_BENCH=1.
func TestVectorStore_Query10K_LatencySLO_AC8(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K bench in -short mode")
	}
	if os.Getenv("AISH_HISTORY_BENCH") == "" && !ciEnv() {
		t.Skip("skipping 10K bench locally; set AISH_HISTORY_BENCH=1 to run")
	}

	s, vs := newVectorStoreForTest(t)
	_ = s
	ctx := context.Background()

	// Seed 10K random vectors.
	for i := 0; i < bench10KSize; i++ {
		id := NewEventID()
		v := randomVector(int64(i+1), testModelDim)
		if err := vs.Upsert(ctx, id, v, testModelID); err != nil {
			t.Fatalf("seed Upsert[%d]: %v", i, err)
		}
	}

	// 200 query samples — enough to give p95 a stable estimate
	// without burning a minute.
	const samples = 200
	times := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		probe := randomVector(int64(bench10KSize+i+1), testModelDim)
		start := time.Now()
		hits, err := vs.Query(ctx, probe, bench10KK)
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("Query[%d]: %v", i, err)
		}
		if len(hits) != bench10KK {
			t.Fatalf("Query[%d]: got %d hits, want %d", i, len(hits), bench10KK)
		}
		times = append(times, dur)
	}

	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p50 := times[len(times)/2]
	p95 := times[(len(times)*95)/100]

	t.Logf("Query10K latency: p50=%v p95=%v (n=%d)", p50, p95, samples)

	if p50 > time.Duration(benchP50LimitMS)*time.Millisecond {
		t.Errorf("AC8 violated: p50 = %v > %dms", p50, benchP50LimitMS)
	}
	if p95 > time.Duration(benchP95LimitMS)*time.Millisecond {
		t.Errorf("AC8 violated: p95 = %v > %dms", p95, benchP95LimitMS)
	}
}

// BenchmarkVectorStore_Query10K is the raw-numbers companion for
// performance investigation. Run with:
//
//	go test -bench=BenchmarkVectorStore_Query10K -benchtime=200x ./internal/history/
//
// The TestVectorStore_Query10K_LatencySLO_AC8 above asserts the SLO;
// this Benchmark gives the noise floor for tuning.
func BenchmarkVectorStore_Query10K(b *testing.B) {
	// Seed once outside the timed loop.
	dir := b.TempDir()
	_ = dir // openTestStore uses its own tempdir; placeholder kept for symmetry.
	s := openTestStoreB(b)
	vs, err := NewVectorStore(s.db, testModelDim)
	if err != nil {
		b.Fatalf("NewVectorStore: %v", err)
	}
	s.WithVectorStore(vs)

	ctx := context.Background()
	for i := 0; i < bench10KSize; i++ {
		id := NewEventID()
		v := randomVector(int64(i+1), testModelDim)
		if err := vs.Upsert(ctx, id, v, testModelID); err != nil {
			b.Fatalf("seed Upsert[%d]: %v", i, err)
		}
	}

	probe := randomVector(99999, testModelDim)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vs.Query(ctx, probe, bench10KK); err != nil {
			b.Fatalf("Query: %v", err)
		}
	}
}

// openTestStoreB is the *testing.B sibling of openTestStore. Pulled
// out so the benchmark can build the store without the *testing.T-
// specific Helper/Cleanup calls.
func openTestStoreB(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	s, err := Open(dir + "/history.db")
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// ciEnv returns true when the test is running under GitHub Actions
// (the project's CI). Used to default the 10K bench to on under CI
// without forcing local devs to opt in.
func ciEnv() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("CI") == "true"
}
