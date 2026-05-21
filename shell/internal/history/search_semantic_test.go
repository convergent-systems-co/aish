//go:build phase_b

// T4 tests — semantic + hybrid search surface on Store.
//
// Build-gated by `phase_b`; the seed commit ships these inert. Phase
// B's coder wave lands SemanticSearch and HybridSearch as Store
// methods and wires the `--mode={keyword,semantic,hybrid}` flag in
// builtin_history.go.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T4 + AC3,
// AC5, AC10):
//   - TestSemanticSearch_RanksByCosine — paraphrased query retrieves
//     the semantically matching event ahead of the lexically unrelated
//     one.
//   - TestHybridSearch_RRF — RRF k=60 fusion of FTS5 + vector ranks.
//   - TestBuiltinHistorySearch_ModeFlag covers the --mode wiring;
//     lives in shell/internal/shell/ — see test counterpart there.
//   - TestSemanticSearch_NoVectorsYet — friendly "run reindex"
//     message when the DB has no vectors.
//   - TestHybridSearch_DegradesToKeyword — hybrid with no vectors
//     returns keyword-only results, no error.

package history

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fixturePair seeds two events in the store: one whose command is
// LEXICALLY matched by the query (so FTS5 finds it) and one whose
// command is SEMANTICALLY matched (so the vector path finds it).
//
// The stubEmbedder used here returns vectors based on input length
// alone, which is too crude for real cosine semantics. To make
// "semantic match" deterministic for the test, the stub vector store
// is pre-seeded with hand-tuned vectors so the cosine winner is
// known. The real semantic ranking is exercised by T1's fastembed
// tests; T4 only verifies the SURFACE — that SemanticSearch and
// HybridSearch consult the vector store and apply RRF correctly.
func fixturePair(t *testing.T) (s *Store, lexID, semID string, queryVec []float32) {
	t.Helper()
	s = openTestStore(t)

	em := &recordingEmbedder{dim: 4, modelID: "fixture-em"}
	s.WithEmbedder(em)

	vs := newStubVectorStore()
	s.WithVectorStore(vs)

	// Lex match: command contains "delete-tmp-build" — FTS5 will hit.
	lex := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC().Add(-2 * time.Hour),
		Kind:      KindSnapshot,
		Command:   "rm -rf /tmp/delete-tmp-build",
		Cwd:       "/tmp",
	}
	if err := s.Append(lex); err != nil {
		t.Fatalf("Append lex: %v", err)
	}
	if err := s.Finalize(lex.ID, 0, 5*time.Millisecond); err != nil {
		t.Fatalf("Finalize lex: %v", err)
	}

	// Sem match: command lexically unrelated to "delete-tmp-build"
	// but semantically near "scrub a temp directory."
	sem := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC().Add(-1 * time.Hour),
		Kind:      KindSnapshot,
		Command:   "scrub the staging area",
		Cwd:       "/tmp",
	}
	if err := s.Append(sem); err != nil {
		t.Fatalf("Append sem: %v", err)
	}
	if err := s.Finalize(sem.ID, 0, 5*time.Millisecond); err != nil {
		t.Fatalf("Finalize sem: %v", err)
	}

	// Manually overwrite the stub vector store rows so the cosine
	// winner is the SEM event when queried with queryVec.
	queryVec = []float32{1, 0, 0, 0}
	_ = vs.Upsert(context.Background(), lex.ID, []float32{0, 0, 1, 0}, em.ModelID())
	_ = vs.Upsert(context.Background(), sem.ID, []float32{1, 0, 0, 0}, em.ModelID())

	return s, lex.ID, sem.ID, queryVec
}

// TestSemanticSearch_RanksByCosine (T4 AC, AC3): a query routed
// through the vector path returns the semantically-matching event
// ahead of the lex-only-match event.
func TestSemanticSearch_RanksByCosine(t *testing.T) {
	s, _, semID, _ := fixturePair(t)

	// SemanticSearch takes a natural-language query; the store
	// embeds it internally via the attached embedder. The fixture
	// stub embedder happens to produce a vector identical to the
	// pre-seeded sem row, so cosine puts sem at rank 1.
	hits, err := s.SemanticSearch("scrub the staging area", 5)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("SemanticSearch returned 0 hits")
	}
	if hits[0].ID != semID {
		t.Errorf("rank 1: got %s want %s (sem event)", hits[0].ID, semID)
	}
}

// TestHybridSearch_RRF (T4 AC, AC5): both events surface in the
// top-K. The RRF (k=60) ranking — where each event's score is the
// sum of 1/(60 + rank) across the two surfaces — gives a documented
// merged order regardless of which surface ranked the event higher.
func TestHybridSearch_RRF(t *testing.T) {
	s, lexID, semID, _ := fixturePair(t)

	hits, err := s.HybridSearch("delete-tmp-build", 5)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, h := range hits {
		gotIDs[h.ID] = true
	}
	if !gotIDs[lexID] {
		t.Errorf("HybridSearch missing FTS-only-match lex event %s", lexID)
	}
	if !gotIDs[semID] {
		t.Errorf("HybridSearch missing vector-only-match sem event %s", semID)
	}
}

// TestSemanticSearch_NoVectorsYet (T4 AC, AC10): a Store with no
// vec attached returns a clear "run `aish history reindex`" error.
// The CLI layer surfaces this as a user-facing message; the Store
// returns ErrNoVectors so the caller can distinguish a genuine
// failure from "feature not enabled yet."
func TestSemanticSearch_NoVectorsYet(t *testing.T) {
	s := openTestStore(t) // no embedder, no vec

	_, err := s.SemanticSearch("anything", 5)
	if err == nil {
		t.Fatal("expected error from SemanticSearch with no vec store, got nil")
	}
	if !strings.Contains(err.Error(), "reindex") {
		t.Errorf("error must reference reindex: %v", err)
	}
}

// TestHybridSearch_DegradesToKeyword (T4 AC, AC10): hybrid mode with
// no vec attached returns keyword-only results — no error.
// Existing Search() output is the canonical reference.
func TestHybridSearch_DegradesToKeyword(t *testing.T) {
	s := openTestStore(t) // no vec

	// Seed one FTS-matchable event.
	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "find /var/log -name '*.log'",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Finalize(ev.ID, 0, 1*time.Millisecond); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	hits, err := s.HybridSearch("find", 5)
	if err != nil {
		t.Fatalf("HybridSearch must not error in degraded mode, got %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("HybridSearch in degraded mode returned 0 hits; FTS path should still serve")
	}
}

// TestSemanticSearch_NoEmbedderAttached covers the "vec attached but
// embedder NOT" edge case — happens if a user sets up the vec store
// but the model isn't installed yet. Same behavior as no-vec:
// friendly error pointing at the prereq.
func TestSemanticSearch_NoEmbedderAttached(t *testing.T) {
	s := openTestStore(t)
	s.WithVectorStore(newStubVectorStore())
	// embedder deliberately NOT set

	_, err := s.SemanticSearch("anything", 5)
	if err == nil {
		t.Fatal("expected error with no embedder, got nil")
	}
}

// TestRRF_Math is a pure-function test of the rank-fusion math.
// k=60 is the documented constant (plan AC5). Lives here so the
// hybrid-search code path's central numerical invariant is pinned
// independent of any DB or embedder.
//
// Given:
//   fts ranks = [A=1, B=2]
//   vec ranks = [B=1, C=2]
// expected fused scores:
//   A = 1/(60+1) = 0.01639...
//   B = 1/(60+2) + 1/(60+1) = 0.03253...
//   C = 1/(60+2) = 0.01612...
// order: B > A > C.
func TestRRF_Math(t *testing.T) {
	fts := []string{"A", "B"}
	vec := []string{"B", "C"}

	merged := reciprocalRankFusion(fts, vec, 60)

	if len(merged) != 3 {
		t.Fatalf("expected 3 merged entries, got %d", len(merged))
	}
	if merged[0] != "B" {
		t.Errorf("RRF top: got %s want B", merged[0])
	}
	if merged[1] != "A" {
		t.Errorf("RRF rank 2: got %s want A", merged[1])
	}
	if merged[2] != "C" {
		t.Errorf("RRF rank 3: got %s want C", merged[2])
	}
}
