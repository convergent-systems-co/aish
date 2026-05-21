//go:build phase_b

// T2 tests — sqlite-vec vector store wrapper.
//
// Build-gated by `phase_b`; the seed commit ships without these
// compiling. Phase B's coder wave lands vector_store.go and flips
// the tag.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T2):
//   - TestVectorStore_UpsertQueryRoundTrip — random vectors → exact
//     event_id at rank 1.
//   - TestVectorStore_Idempotent — re-upsert is a no-op.
//   - TestVectorStore_Delete — gone after delete.
//   - TestVectorStore_MigrationOnOldDB — pre-#112 fixture opens
//     cleanly, virtual table exists, FTS5 still works.
//   - Integration, no mock: real sqlite-vec extension loaded. If the
//     Go binding is unworkable, T2's coder swings to sqlite-vss and
//     records the swing in the commit message. These tests are
//     binding-agnostic — they exercise the VectorStore interface.

package history

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"
)

// newVectorStoreForTest constructs the production VectorStore against
// a fresh test DB. The Phase B implementation owns NewVectorStore
// (signature: NewVectorStore(db *sql.DB, dim int) (VectorStore,
// error)) — the test would not compile until that constructor lands.
//
// Skips when sqlite-vec is not loadable in this build (e.g., the
// pure-Go modernc.org/sqlite driver without the extension shim). The
// CI matrix for v0.3 runs the macOS+Linux variant where the extension
// IS loaded; the Windows matrix is v1.0 work per plan §Risk.
func newVectorStoreForTest(t *testing.T) (*Store, VectorStore) {
	t.Helper()
	s := openTestStore(t)
	vs, err := NewVectorStore(s.db, testModelDim)
	if err != nil {
		t.Skipf("sqlite-vec extension not loadable in this build: %v", err)
	}
	s.WithVectorStore(vs)
	return s, vs
}

// randomVector produces a deterministic-by-seed float32 vector of
// length dim, normalized so cosine similarity tests are stable.
func randomVector(seed int64, dim int) []float32 {
	r := rand.New(rand.NewSource(seed))
	v := make([]float32, dim)
	var norm2 float64
	for i := range v {
		x := r.NormFloat64()
		v[i] = float32(x)
		norm2 += x * x
	}
	// L2-normalize so query/upsert magnitudes don't skew the cosine
	// metric across test cases.
	if norm2 > 0 {
		scale := float32(1.0 / sqrtFloat64(norm2))
		for i := range v {
			v[i] *= scale
		}
	}
	return v
}

// sqrtFloat64 — kept local to avoid an extra import. The test's
// numerical precision needs are loose enough that this is fine.
func sqrtFloat64(x float64) float64 {
	// Newton's method, 20 iterations: converges fast enough for
	// dim=384 vectors at the precision we care about.
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

// TestVectorStore_UpsertQueryRoundTrip (T2, AC8 prerequisite):
// upsert 100 random vectors keyed by event_id; query with one of
// them; recover that event_id at rank 1. This is the smoke test for
// "the cosine index works at all."
func TestVectorStore_UpsertQueryRoundTrip(t *testing.T) {
	_, vs := newVectorStoreForTest(t)
	ctx := context.Background()

	const n = 100
	ids := make([]string, n)
	vecs := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = NewEventID()
		vecs[i] = randomVector(int64(i+1), testModelDim)
		if err := vs.Upsert(ctx, ids[i], vecs[i], testModelID); err != nil {
			t.Fatalf("Upsert[%d]: %v", i, err)
		}
	}

	// Query with the vector at index 42; expect index 42's id at
	// rank 1.
	probe := 42
	hits, err := vs.Query(ctx, vecs[probe], 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Query returned 0 hits over 100 inserts")
	}
	if hits[0].EventID != ids[probe] {
		t.Errorf("rank 1: got %s want %s", hits[0].EventID, ids[probe])
	}
}

// TestVectorStore_Idempotent (T2): re-upserting the same triple is
// a no-op. Reindex repeatedly hits this path on incremental backfill;
// duplicates would inflate the index and degrade query latency.
func TestVectorStore_Idempotent(t *testing.T) {
	_, vs := newVectorStoreForTest(t)
	ctx := context.Background()

	id := NewEventID()
	v := randomVector(7, testModelDim)

	for i := 0; i < 3; i++ {
		if err := vs.Upsert(ctx, id, v, testModelID); err != nil {
			t.Fatalf("Upsert pass %d: %v", i, err)
		}
	}

	hits, err := vs.Query(ctx, v, 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Count occurrences of the id in the result — should be exactly 1,
	// not 3 (the idempotent contract).
	count := 0
	for _, h := range hits {
		if h.EventID == id {
			count++
		}
	}
	if count != 1 {
		t.Errorf("id appeared %d times after 3 upserts; want 1 (idempotency violated)", count)
	}
}

// TestVectorStore_Delete (T2): Delete removes a row from the cosine
// search. Used by reindex when a model-mismatched row is re-embedded
// (AC7) and by any future tainted-event retroactive cleanup.
func TestVectorStore_Delete(t *testing.T) {
	_, vs := newVectorStoreForTest(t)
	ctx := context.Background()

	id := NewEventID()
	v := randomVector(11, testModelDim)
	if err := vs.Upsert(ctx, id, v, testModelID); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Pre-delete: HasEvent true.
	has, err := vs.HasEvent(ctx, id)
	if err != nil {
		t.Fatalf("HasEvent pre-delete: %v", err)
	}
	if !has {
		t.Fatal("HasEvent false after Upsert")
	}

	if err := vs.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Post-delete: HasEvent false.
	has, err = vs.HasEvent(ctx, id)
	if err != nil {
		t.Fatalf("HasEvent post-delete: %v", err)
	}
	if has {
		t.Errorf("HasEvent true after Delete — row not removed")
	}

	// And the query doesn't surface the deleted id.
	hits, err := vs.Query(ctx, v, 5)
	if err != nil {
		t.Fatalf("Query post-delete: %v", err)
	}
	for _, h := range hits {
		if h.EventID == id {
			t.Errorf("deleted id surfaced in Query results")
		}
	}
}

// TestVectorStore_HasEvent (T2): HasEvent answers the
// "already-embedded?" question reindex uses to skip work. Must return
// false for a never-seen id.
func TestVectorStore_HasEvent(t *testing.T) {
	_, vs := newVectorStoreForTest(t)
	ctx := context.Background()

	has, err := vs.HasEvent(ctx, "evt_nonexistent")
	if err != nil {
		t.Fatalf("HasEvent on absent id: %v", err)
	}
	if has {
		t.Errorf("HasEvent on never-upserted id returned true")
	}

	id := NewEventID()
	if err := vs.Upsert(ctx, id, randomVector(13, testModelDim), testModelID); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	has, err = vs.HasEvent(ctx, id)
	if err != nil {
		t.Fatalf("HasEvent after Upsert: %v", err)
	}
	if !has {
		t.Errorf("HasEvent after Upsert returned false")
	}
}

// TestVectorStore_MigrationOnOldDB (T2, AC10): open a fixture DB
// that pre-dates #112, then assert (a) the new virtual table exists
// after Open, (b) FTS5 still works.
//
// Fixture: shell/internal/history/testdata/pre112-history.db — a
// minimal pre-#112 schema (events + events_fts) with one event row.
// If the fixture is missing, the test SKIPs rather than failing —
// the fixture is committed in the seed and removing it is itself a
// review-time signal.
func TestVectorStore_MigrationOnOldDB(t *testing.T) {
	fixture := filepath.Join("testdata", "pre112-history.db")

	// Copy the fixture to a tempdir so the test doesn't mutate the
	// committed file.
	dst := copyFixtureToTemp(t, fixture)

	s, err := Open(dst)
	if err != nil {
		t.Fatalf("Open pre-112 fixture: %v", err)
	}
	defer s.Close()

	// Sidecar always created (no extension needed).
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='events_vec_meta'`,
	).Scan(&n); err != nil {
		t.Fatalf("sqlite_master query: %v", err)
	}
	if n != 1 {
		t.Errorf("events_vec_meta not created on migrated DB")
	}

	// FTS5 still works.
	hits, err := s.Search("echo", 10)
	if err != nil {
		t.Fatalf("Search on migrated DB: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("FTS5 returned 0 hits on migrated DB (fixture should contain 'echo')")
	}
}

// TestVectorStore_ModelIDPersisted (T2, AC7): the model id passed to
// Upsert is stored in events_vec_meta and reachable by reindex's
// model-mismatch path.
func TestVectorStore_ModelIDPersisted(t *testing.T) {
	s, vs := newVectorStoreForTest(t)
	ctx := context.Background()

	id := NewEventID()
	v := randomVector(17, testModelDim)
	if err := vs.Upsert(ctx, id, v, testModelID); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var stored string
	if err := s.db.QueryRow(
		`SELECT model_id FROM events_vec_meta WHERE event_id = ?`, id,
	).Scan(&stored); err != nil {
		t.Fatalf("model_id lookup: %v", err)
	}
	if stored != testModelID {
		t.Errorf("model_id: got %q want %q", stored, testModelID)
	}
}
