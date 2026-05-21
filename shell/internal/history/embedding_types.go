// Package history — embedding + vector-store contract surface.
//
// This file lands the SEED for #112 (v0.3-4 Semantic history search).
// It defines the interfaces the embedder and the vector store will
// implement, plus the result type returned from a vector query. The
// concrete implementations land in follow-up commits (Phase B):
//
//	embedding_fastembed.go — fastembed-go + bge-small-en-v1.5 (T1)
//	vector_store.go        — sqlite-vec virtual-table wrapper (T2)
//	embed_writer.go        — Append-path embed-on-write hook  (T3)
//	reindex.go             — `aish history reindex` backfill   (T5)
//
// Wiring on Store (the unexported `embedder` and `vec` fields added in
// store.go) is nil-safe by contract: when either field is nil the Store
// behaves exactly as pre-#112 — no embedding occurs on Append, no
// vector path runs on Search. Activation lives in T3/T4; the seed only
// ships the shape.
//
// See `.artifacts/plans/112.md` for the full design rationale, the
// Alternatives Table for the chosen backends, and the acceptance
// criteria each sub-task covers.

package history

import "context"

// EmbeddingProvider produces fixed-dimension float32 vectors for a
// batch of input strings. Implementations MUST be deterministic — the
// same input string at the same model version MUST yield the same
// vector — so test fixtures and idempotent reindex can rely on
// byte-identical re-runs (AC1, T1's TestFastembed_DeterministicEmbedding).
//
// Embed is a batch API by design. The fastembed-go runtime amortizes
// model-load and tokenization overhead across a batch; calling Embed
// once with N inputs is significantly cheaper than N calls with one
// input each. The Append-path call site (T3) embeds one string at a
// time because event writes are one at a time; the reindex path (T5)
// batches over the whole history.
//
// ModelID returns a stable identifier persisted alongside each vector
// row (AC7). When the active embedder's ModelID differs from a stored
// vector's model_id, the vector store treats them as incompatible —
// queries reject mixed-model result sets and reindex re-embeds the
// mismatched rows.
//
// Dim returns the vector dimension the model emits. The vector store
// uses this to size its virtual-table column and to validate inbound
// Upserts.
type EmbeddingProvider interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	ModelID() string
	Dim() int
}

// VectorStore is the persistence + cosine-search interface over the
// `events_vec` virtual table. The seed defines the surface only; the
// concrete sqlite-vec wrapper (and the sqlite-vss fallback, if
// sqlite-vec's Go binding proves unworkable on macOS+Linux per the T2
// spike) lands in vector_store.go.
//
// Concurrency posture mirrors Store: one writer at a time via the
// shared sql.DB with MaxOpenConns=1; WAL keeps readers non-blocking.
// Each Upsert / Delete runs in its own SQL transaction so reindex
// remains resumable after a kill-9 (AC6, T5's TestReindex_Resumable).
//
// Upsert is idempotent: re-upserting the same (eventID, vec, model)
// triple is a no-op (T2's TestVectorStore_Idempotent). The model
// argument is the EmbeddingProvider.ModelID() at the time of write —
// the store persists it on the row so a later model change is visible
// and re-embeddable.
//
// Query returns the top-k cosine-similarity matches as VectorHit
// pairs, ordered by descending Score. The query vector MUST have been
// produced by the same model whose ID was used at Upsert time;
// mixed-model results are rejected at the row level by the caller's
// model_id filter, not silently merged.
//
// Delete removes a single event_id's vector row. Used by reindex when
// model-mismatched rows are re-embedded (AC7) and by tainted-event
// retroactive cleanup if a post-hoc taint is ever supported.
//
// HasEvent answers "is there already a vector row for this event_id?"
// — the reindex path uses it to skip already-embedded events, making
// the backfill idempotent (AC6).
type VectorStore interface {
	Upsert(ctx context.Context, eventID string, vec []float32, model string) error
	Query(ctx context.Context, vec []float32, k int) ([]VectorHit, error)
	Delete(ctx context.Context, eventID string) error
	HasEvent(ctx context.Context, eventID string) (bool, error)
}

// VectorHit is one row of a vector-store Query result: the matching
// event's id and its cosine-similarity score against the query vector.
//
// Score is the raw cosine similarity in [-1.0, 1.0], NOT a normalized
// rank. Higher is closer. The hybrid-search ranker (T4) converts hits
// to rank positions before fusing with FTS5 results via RRF (k=60),
// so the absolute Score magnitude does not affect the final ranking —
// only the order does. EventID is the `events.id` of the matching
// row, suitable for passing to Store.Get(id) to retrieve the full
// Event.
type VectorHit struct {
	EventID string
	Score   float32
}
