// T3 tests — write-path embed-on-Append integration.
//
// Acceptance criteria covered (from .artifacts/plans/112.md T3 + AC1, AC4):
//   - TestAppend_EmbedsNonTainted — normal event → vector row exists.
//   - TestAppend_SkipsTainted — RedactedTainted command → ZERO vector
//     rows. (AC4 taint-safety, also probed by adversarial_test.go in
//     a follow-up tester wave.)
//   - TestAppend_EmbedderNil_NoOp — pre-#112 behavior recovered when
//     no embedder attached.
//   - TestAppend_EmbeddingFailure_DoesNotBlockWrite — embedder error
//     does NOT roll back the event row.

package history

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// errEmbedder is a deterministic-failure EmbeddingProvider used to
// verify the "embedding is best-effort, not blocking" contract.
type errEmbedder struct{}

func (errEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	return nil, errors.New("embed: simulated failure")
}
func (errEmbedder) ModelID() string { return "err-embedder" }
func (errEmbedder) Dim() int        { return 4 }

// recordingEmbedder captures the inputs it was called with so tests
// can assert "Embed was / was not invoked for the tainted event."
type recordingEmbedder struct {
	dim     int
	modelID string
	calls   [][]string
}

func (r *recordingEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	cp := make([]string, len(inputs))
	copy(cp, inputs)
	r.calls = append(r.calls, cp)
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = make([]float32, r.dim)
		for j := range out[i] {
			out[i][j] = float32(len(inputs[i]) + j)
		}
	}
	return out, nil
}
func (r *recordingEmbedder) ModelID() string { return r.modelID }
func (r *recordingEmbedder) Dim() int        { return r.dim }

// newStoreWithEmbedAndVec builds a Store wired with a recording
// embedder and an in-memory stub vector store. T3 production code
// uses these via the EmbeddingProvider / VectorStore interfaces —
// the test only cares about the call/upsert side-effects, not the
// sqlite-vec internals.
func newStoreWithEmbedAndVec(t *testing.T, em EmbeddingProvider) (*Store, *stubVectorStore) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	vs := newStubVectorStore()
	s.WithEmbedder(em)
	s.WithVectorStore(vs)
	return s, vs
}

// TestAppend_EmbedsNonTainted (T3 AC, AC1): a normal event causes
// exactly one vector row to land for its event_id.
func TestAppend_EmbedsNonTainted(t *testing.T) {
	em := &recordingEmbedder{dim: 4, modelID: "rec-A"}
	s, vs := newStoreWithEmbedAndVec(t, em)

	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "rm -rf /tmp/build",
		Cwd:       "/tmp",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Embedder was called with the command string.
	if len(em.calls) != 1 || len(em.calls[0]) != 1 || em.calls[0][0] != ev.Command {
		t.Errorf("embedder.Embed not invoked with command, calls=%v", em.calls)
	}

	// Vector store has the row.
	has, err := vs.HasEvent(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if !has {
		t.Errorf("vector row missing for non-tainted event")
	}
}

// TestAppend_SkipsTainted (T3 AC, AC4): an event whose command equals
// RedactedTainted produces ZERO vector rows. The embedder is also
// NOT called — the skip happens at the write-path gate, not inside
// the embedder.
func TestAppend_SkipsTainted(t *testing.T) {
	em := &recordingEmbedder{dim: 4, modelID: "rec-A"}
	s, vs := newStoreWithEmbedAndVec(t, em)

	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   RedactedTainted,
		Cwd:       "/tmp",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append tainted: %v", err)
	}

	// Embedder NOT called.
	if len(em.calls) != 0 {
		t.Errorf("embedder.Embed was called for tainted event: %v", em.calls)
	}

	// No vector row.
	has, err := vs.HasEvent(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if has {
		t.Errorf("vector row exists for tainted event (taint leak)")
	}
}

// TestAppend_EmbedderNil_NoOp (T3 AC): pre-#112 behavior is fully
// recovered when no embedder is attached. Append must NOT touch the
// vector store, even when one is attached — the gate is
// `embedder != nil && vec != nil` together (per the nil-safety
// contract on Store).
func TestAppend_EmbedderNil_NoOp(t *testing.T) {
	s := openTestStore(t)
	vs := newStubVectorStore()
	s.WithVectorStore(vs) // vec attached, embedder is NOT

	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "ls -la",
		Cwd:       "/tmp",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	has, err := vs.HasEvent(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if has {
		t.Errorf("vector row written with no embedder attached")
	}
}

// TestAppend_VecNil_NoOp is the symmetric case: embedder attached,
// vec NOT. Append still succeeds (the event row lands) and no panic
// occurs.
func TestAppend_VecNil_NoOp(t *testing.T) {
	s := openTestStore(t)
	em := &recordingEmbedder{dim: 4, modelID: "rec-A"}
	s.WithEmbedder(em) // embedder attached, vec is NOT

	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "ls -la",
		Cwd:       "/tmp",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Event row landed normally — pre-#112 surface intact.
	got, err := s.Get(ev.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("event row missing after Append with no vec store")
	}
}

// TestAppend_EmbeddingFailure_DoesNotBlockWrite (T3 AC): when
// embedder.Embed() returns an error, the event row STILL lands.
// History write is mandatory; embedding is best-effort and
// recoverable via reindex.
func TestAppend_EmbeddingFailure_DoesNotBlockWrite(t *testing.T) {
	s, vs := newStoreWithEmbedAndVec(t, errEmbedder{})

	ev := &Event{
		ID:        NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      KindSnapshot,
		Command:   "echo will-not-embed",
		Cwd:       "/tmp",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append must succeed despite embedder error, got %v", err)
	}

	// Event row landed.
	got, err := s.Get(ev.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("event row missing after embedder-error Append")
	}

	// No vector row (embed failed).
	has, err := vs.HasEvent(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if has {
		t.Errorf("vector row exists after embedder-error path; should be missing for reindex backfill")
	}
}

// TestAppend_NonRedactedNonCommand verifies a Checkpoint event (whose
// command is "checkpoint <name>" — not a user-tainted line) does get
// embedded. The taint gate is on the literal RedactedTainted string,
// not on event kind.
func TestAppend_NonRedactedNonCommand(t *testing.T) {
	em := &recordingEmbedder{dim: 4, modelID: "rec-A"}
	s, vs := newStoreWithEmbedAndVec(t, em)

	cp, err := s.Checkpoint("named-checkpoint")
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	has, err := vs.HasEvent(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if !has {
		t.Errorf("checkpoint event not embedded")
	}
}
