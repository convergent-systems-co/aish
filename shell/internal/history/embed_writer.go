// Write-path embed-on-Append hook for v0.3-4 #112.
//
// The hook lives at the tail end of Store.Append: after the SQL
// INSERT commits, after Append has returned its event row to the
// caller successfully, the embedder + vector store (when both
// attached) get called to embed the command text and upsert the
// resulting vector keyed by event_id.
//
// Three load-bearing rules (AC1, AC4, T3):
//
//   1. Best-effort. An embedder error MUST NOT block the history
//      write. The event row has already committed by the time this
//      runs; an embedding failure is recoverable via reindex.
//   2. Tainted-skip. Events whose command equals RedactedTainted
//      ([REDACTED:tainted]) are NOT embedded. Embedding the
//      placeholder would cluster every tainted event into one near-
//      perfect cosine neighborhood — a side channel that defeats
//      the v0.3-fu redaction guarantee. The skip is at the gate,
//      before the embedder runs; the embedder never sees the
//      tainted command line either.
//   3. Nil-safe. When either embedder or vec is nil, the hook is a
//      no-op. Pre-#112 behavior (no semantic surface) is recovered
//      by leaving WithEmbedder / WithVectorStore unset.
//
// Concurrency: this runs synchronously on the Append caller's
// goroutine, after Commit. Tests can rely on a vector being
// queryable immediately after Append returns. The latency is the
// embedder's per-call cost (~5-30ms with fastembed-go + bge-small-
// en-v1.5 on the AC8 target hardware) plus a few microseconds for
// the chromem-go Add and the SQLite meta upsert. Append on the
// shell hot path adds that cost only for events that already paid
// the cost of being recorded; non-destructive commands don't reach
// Append at all.

package history

import (
	"context"
)

// embedAndStore runs the optional embed-on-write step for one event.
// Returns nil and logs nothing on the happy path; returns nil and
// silently absorbs any embedder error so Append's caller does not
// see a "history write failed" symptom for a strictly semantic
// concern. The recovery path is `aish history reindex`.
//
// ctx is the caller-supplied (or background) context — Append today
// uses context.Background internally for the SQL writes, so we mirror
// that here. A future "context-bearing Append" change would naturally
// thread a real ctx in. The chromem upsert and the meta SQL both
// accept ctx.
//
// The function is intentionally small. Append calls it as a one-line
// addition at the tail; keeping the logic here makes the policy
// (taint-skip, nil-safe, best-effort) reviewable in isolation.
func (s *Store) embedAndStore(ctx context.Context, ev *Event) {
	// Nil-safe entry gates. Both fields nil = pre-#112 behavior.
	if s == nil || s.embedder == nil || s.vec == nil {
		return
	}
	if ev == nil || ev.ID == "" {
		return
	}
	// Tainted-skip. RedactedTainted is the only placeholder the
	// History engine writes for a redacted command line; matching on
	// the literal keeps the gate auditable. Compare directly to
	// avoid a substring match that would also redact legitimate
	// commands mentioning the placeholder string (vanishingly rare,
	// but the equality form is unambiguous).
	if ev.Command == RedactedTainted {
		return
	}

	// Embed the command text. The batch API takes a slice; we pass a
	// one-element slice. Embedder failure absorbs into best-effort:
	// the event row is already committed, semantic search degrades
	// gracefully (this id is missing from the vector store until
	// reindex runs).
	vecs, err := s.embedder.Embed(ctx, []string{ev.Command})
	if err != nil {
		return
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return
	}

	// Upsert via the attached vector store. Same best-effort
	// treatment — a Upsert error here is recoverable via reindex.
	_ = s.vec.Upsert(ctx, ev.ID, vecs[0], s.embedder.ModelID())
}
