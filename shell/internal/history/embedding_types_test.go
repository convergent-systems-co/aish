// Seed-level tests for the #112 embedding + vector-store contract
// surface. These tests assert the SHAPE — interface satisfaction, the
// VectorHit struct's exported fields, and the Store's nil-safe
// accessors. They DO NOT test behavior of any concrete embedder or
// vector store; those tests live behind the `phase_b` build tag.
//
// Test doubles in this file are deliberate stubs: they exist to let
// the seed prove its nil-safety story (Embedder()/VectorStore()
// returning nil pre-wire-up; returning the attached implementation
// post-wire-up) without dragging in fastembed-go or sqlite-vec.
// Per Common.md §11 "no mocks at the integration boundary", the
// stubs here are NOT a substitute for real testing of the eventual
// fastembed-go and sqlite-vec implementations — those land in T1 and
// T2 of Phase B with no-mock integration tests against the real
// libraries.
//
// Acceptance criteria covered (from .artifacts/plans/112.md seed):
//   - Interfaces defined: EmbeddingProvider, VectorStore, VectorHit.
//   - Store has unexported embedder and vec fields, wired through
//     WithEmbedder / WithVectorStore.
//   - Nil-safety: a Store with neither field set behaves as pre-#112.

package history

import (
	"context"
	"testing"
)

// stubEmbedder is a deterministic, zero-dependency EmbeddingProvider
// used to verify the Store wiring. Embed returns one fixed-dim vector
// per input, populated with the input's length so equal-length inputs
// produce equal vectors (sufficient for the seed's interface-
// satisfaction test). Real determinism + paraphrase semantics are T1's
// problem.
type stubEmbedder struct {
	modelID string
	dim     int
}

func (s *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v := make([]float32, s.dim)
		for j := range v {
			v[j] = float32(len(in))
		}
		out[i] = v
	}
	return out, nil
}

func (s *stubEmbedder) ModelID() string { return s.modelID }
func (s *stubEmbedder) Dim() int        { return s.dim }

// stubVectorStore is an in-memory VectorStore used to verify the
// Store wiring. Not a substitute for the real sqlite-vec wrapper.
type stubVectorStore struct {
	rows map[string][]float32
}

func newStubVectorStore() *stubVectorStore {
	return &stubVectorStore{rows: map[string][]float32{}}
}

func (s *stubVectorStore) Upsert(_ context.Context, eventID string, vec []float32, _ string) error {
	cp := make([]float32, len(vec))
	copy(cp, vec)
	s.rows[eventID] = cp
	return nil
}

func (s *stubVectorStore) Query(_ context.Context, _ []float32, k int) ([]VectorHit, error) {
	out := make([]VectorHit, 0, len(s.rows))
	for id := range s.rows {
		out = append(out, VectorHit{EventID: id, Score: 1.0})
		if len(out) >= k {
			break
		}
	}
	return out, nil
}

func (s *stubVectorStore) Delete(_ context.Context, eventID string) error {
	delete(s.rows, eventID)
	return nil
}

func (s *stubVectorStore) HasEvent(_ context.Context, eventID string) (bool, error) {
	_, ok := s.rows[eventID]
	return ok, nil
}

// Compile-time assertions that the stubs (and, transitively, anything
// shaped like them) satisfy the interfaces declared in
// embedding_types.go. If the interface signature drifts, these lines
// fail at `go build` — which is the point.
var (
	_ EmbeddingProvider = (*stubEmbedder)(nil)
	_ VectorStore       = (*stubVectorStore)(nil)
)

// TestVectorHitFields pins the exported shape of VectorHit. A future
// rename (EventID → ID, Score → Similarity) would break Phase B's
// search.go consumers; this test forces the rename to land as a
// reviewable change rather than a silent drift.
func TestVectorHitFields(t *testing.T) {
	h := VectorHit{EventID: "evt_abc", Score: 0.87}
	if h.EventID != "evt_abc" {
		t.Errorf("EventID: got %q want %q", h.EventID, "evt_abc")
	}
	if h.Score != 0.87 {
		t.Errorf("Score: got %v want %v", h.Score, float32(0.87))
	}
}

// TestStore_EmbedderNilByDefault asserts the pre-#112 default: a
// freshly-opened Store has no embedder attached. AC10's migration
// safety leans on this — semantic search is opt-in, never auto-
// activated on Open.
func TestStore_EmbedderNilByDefault(t *testing.T) {
	s := openTestStore(t)
	if s.Embedder() != nil {
		t.Errorf("Embedder() on fresh store: got %T, want nil", s.Embedder())
	}
}

// TestStore_VectorStoreNilByDefault is the symmetric assertion for
// the vector-store field. Same rationale as Embedder.
func TestStore_VectorStoreNilByDefault(t *testing.T) {
	s := openTestStore(t)
	if s.VectorStore() != nil {
		t.Errorf("VectorStore() on fresh store: got %T, want nil", s.VectorStore())
	}
}

// TestStore_WithEmbedderAttaches verifies WithEmbedder stores the
// provider and Embedder() returns it. Calling twice replaces the
// prior provider (idempotent in the "last write wins" sense — the
// chain `s.WithEmbedder(a).WithEmbedder(b)` ends with b).
func TestStore_WithEmbedderAttaches(t *testing.T) {
	s := openTestStore(t)
	a := &stubEmbedder{modelID: "stub-A", dim: 4}
	b := &stubEmbedder{modelID: "stub-B", dim: 4}

	s.WithEmbedder(a)
	if got := s.Embedder(); got != a {
		t.Errorf("after WithEmbedder(a): got %v want %v", got, a)
	}
	s.WithEmbedder(b)
	if got := s.Embedder(); got != b {
		t.Errorf("after WithEmbedder(b): got %v want %v", got, b)
	}
	s.WithEmbedder(nil)
	if got := s.Embedder(); got != nil {
		t.Errorf("after WithEmbedder(nil): got %v want nil", got)
	}
}

// TestStore_WithVectorStoreAttaches is the symmetric assertion for
// the vector-store wiring.
func TestStore_WithVectorStoreAttaches(t *testing.T) {
	s := openTestStore(t)
	v := newStubVectorStore()

	s.WithVectorStore(v)
	if got := s.VectorStore(); got != v {
		t.Errorf("after WithVectorStore: got %v want %v", got, v)
	}
	s.WithVectorStore(nil)
	if got := s.VectorStore(); got != nil {
		t.Errorf("after WithVectorStore(nil): got %v want nil", got)
	}
}

// TestStore_NilReceiverSafe verifies the nil-Store fluent chain
// matches the prior Signer surface. A nil *Store returned from a
// failed Open or a deliberately-zero value must not panic when the
// caller chains WithEmbedder / WithVectorStore / Embedder /
// VectorStore. (The pre-#112 WithSigner already had this contract;
// this test pins that the new accessors keep it.)
func TestStore_NilReceiverSafe(t *testing.T) {
	var s *Store
	// None of these should panic.
	if got := s.WithEmbedder(&stubEmbedder{}); got != nil {
		t.Errorf("nil-Store.WithEmbedder: got %v want nil", got)
	}
	if got := s.WithVectorStore(newStubVectorStore()); got != nil {
		t.Errorf("nil-Store.WithVectorStore: got %v want nil", got)
	}
	if got := s.Embedder(); got != nil {
		t.Errorf("nil-Store.Embedder: got %v want nil", got)
	}
	if got := s.VectorStore(); got != nil {
		t.Errorf("nil-Store.VectorStore: got %v want nil", got)
	}
}
