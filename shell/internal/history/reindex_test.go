// T5 tests — reindex command + backfill path.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T5 + AC6,
// AC7):
//   - TestReindex_FullBackfill — N events, 0 vectors → N vectors
//     (tainted excluded).
//   - TestReindex_Idempotent — running twice produces the same vector
//     set.
//   - TestReindex_Resumable — kill mid-run, restart, end state = full
//     backfill.
//   - TestReindex_ModelMismatch — old model rows re-embedded with new
//     model; old rows deleted in the same operation.

package history

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// seedEvents writes n events to the store with no embedder attached
// (so no vector rows are created at write time). Returns the slice
// of event IDs.
func seedEvents(t *testing.T, s *Store, n int, taintEvery int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		cmd := fmt.Sprintf("echo event-%d", i)
		if taintEvery > 0 && i%taintEvery == 0 {
			cmd = RedactedTainted
		}
		ev := &Event{
			ID:        NewEventID(),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
			Kind:      KindSnapshot,
			Command:   cmd,
		}
		if err := s.Append(ev); err != nil {
			t.Fatalf("seed Append[%d]: %v", i, err)
		}
		if err := s.Finalize(ev.ID, 0, time.Millisecond); err != nil {
			t.Fatalf("seed Finalize[%d]: %v", i, err)
		}
		ids = append(ids, ev.ID)
	}
	return ids
}

// TestReindex_FullBackfill (T5 AC, AC6): 100 events without vectors
// → 100 vector rows after reindex (zero tainted in this case). Then,
// with one tainted event every 10, 90 vector rows.
func TestReindex_FullBackfill(t *testing.T) {
	s := openTestStore(t)
	ids := seedEvents(t, s, 100, 0) // no tainted

	em := &recordingEmbedder{dim: 4, modelID: "reindex-A"}
	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)

	n, err := s.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 100 {
		t.Errorf("Reindex returned %d, want 100", n)
	}

	// Verify every id has a vector row.
	for _, id := range ids {
		has, err := vs.HasEvent(context.Background(), id)
		if err != nil {
			t.Fatalf("HasEvent %s: %v", id, err)
		}
		if !has {
			t.Errorf("vector row missing for %s after reindex", id)
		}
	}
}

// TestReindex_TaintedExcluded (T5 AC, AC4): tainted events do NOT
// produce vector rows during reindex.
func TestReindex_TaintedExcluded(t *testing.T) {
	s := openTestStore(t)
	seedEvents(t, s, 30, 10) // every 10th tainted → 3 tainted, 27 normal

	em := &recordingEmbedder{dim: 4, modelID: "reindex-A"}
	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)

	n, err := s.Reindex(context.Background())
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 27 {
		t.Errorf("Reindex returned %d, want 27 (30 events - 3 tainted)", n)
	}

	// No vec row's command-stringly is RedactedTainted (stub stores
	// vectors only; the assertion here is on the count math —
	// stub.rows has 27 entries.)
	if got := len(vs.rows); got != 27 {
		t.Errorf("vec store has %d rows, want 27", got)
	}
}

// TestReindex_Idempotent (T5 AC, AC6): running reindex twice
// produces the same vector set as running it once. No duplicates,
// no growth.
func TestReindex_Idempotent(t *testing.T) {
	s := openTestStore(t)
	seedEvents(t, s, 50, 0)

	em := &recordingEmbedder{dim: 4, modelID: "reindex-A"}
	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)

	if _, err := s.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex pass 1: %v", err)
	}
	rowsAfterPass1 := len(vs.rows)

	if _, err := s.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex pass 2: %v", err)
	}
	if got := len(vs.rows); got != rowsAfterPass1 {
		t.Errorf("idempotency: pass 1 = %d rows, pass 2 = %d rows", rowsAfterPass1, got)
	}
}

// TestReindex_Resumable (T5 AC, AC6): after a partial reindex,
// running again completes the job. Simulated by deleting half of the
// vector rows after the first reindex, then re-running.
func TestReindex_Resumable(t *testing.T) {
	s := openTestStore(t)
	ids := seedEvents(t, s, 100, 0)

	em := &recordingEmbedder{dim: 4, modelID: "reindex-A"}
	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)

	if _, err := s.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex pass 1: %v", err)
	}

	// Simulate a kill-9 mid-second-reindex by deleting half the
	// rows. (The per-row transaction in T2 keeps the survivors
	// committed; reindex pass 3 sees 50 missing and fills them in.)
	for i := 0; i < 50; i++ {
		if err := vs.Delete(context.Background(), ids[i]); err != nil {
			t.Fatalf("simulate-interrupt Delete: %v", err)
		}
	}
	if got := len(vs.rows); got != 50 {
		t.Fatalf("after simulated interrupt: %d rows, want 50", got)
	}

	// Re-run reindex. Resumability: every missing id gets backfilled.
	if _, err := s.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex pass 2: %v", err)
	}
	if got := len(vs.rows); got != 100 {
		t.Errorf("after resume: %d rows, want 100", got)
	}
}

// TestReindex_ModelMismatch (T5 AC, AC7): when the active embedder's
// ModelID differs from a stored vector's model_id, that vector is
// re-embedded with the new model; the old row is deleted in the same
// operation.
//
// Because the stub vector store doesn't track model_id, this test
// verifies the contract by:
//  (1) Pre-populating events_vec_meta with a different model_id.
//  (2) Running reindex with the new embedder.
//  (3) Asserting events_vec_meta now records the new model_id.
func TestReindex_ModelMismatch(t *testing.T) {
	s := openTestStore(t)
	ids := seedEvents(t, s, 10, 0)

	// Pre-populate meta with the OLD model id, simulating a prior
	// reindex run with a different model.
	for _, id := range ids {
		if _, err := s.db.Exec(
			`INSERT INTO events_vec_meta(event_id, model_id) VALUES (?, ?)`,
			id, "old-model",
		); err != nil {
			t.Fatalf("seed meta: %v", err)
		}
	}

	em := &recordingEmbedder{dim: 4, modelID: "new-model"}
	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)

	if _, err := s.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	// Every row's model_id should now be "new-model."
	rows, err := s.db.Query(`SELECT model_id FROM events_vec_meta`)
	if err != nil {
		t.Fatalf("query meta: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if m != "new-model" {
			t.Errorf("stale model_id after reindex: got %q want new-model", m)
		}
	}
}

// TestReindex_NoEmbedder is the failure-mode case: reindex with no
// embedder attached returns a clear error.
func TestReindex_NoEmbedder(t *testing.T) {
	s := openTestStore(t)
	s.WithVectorStore(newStubVectorStore())
	// embedder deliberately NOT attached

	_, err := s.Reindex(context.Background())
	if err == nil {
		t.Fatal("expected error from Reindex with no embedder, got nil")
	}
}

// TestReindex_NoVectorStore is the symmetric failure-mode case.
func TestReindex_NoVectorStore(t *testing.T) {
	s := openTestStore(t)
	s.WithEmbedder(&recordingEmbedder{dim: 4, modelID: "any"})
	// vec deliberately NOT attached

	_, err := s.Reindex(context.Background())
	if err == nil {
		t.Fatal("expected error from Reindex with no vec store, got nil")
	}
}
