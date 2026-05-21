// chromem-go vector store for v0.3-4 #112.
//
// Why chromem-go (post-Phase-A swap):
//   - modernc.org/sqlite (the project's pure-Go SQLite driver) cannot
//     load C extensions, so sqlite-vec/sqlite-vss were unreachable
//     from `shell/`. Phase A's `events_vec` virtual-table migration
//     probe still ships (it's a confirmed no-op on this driver — the
//     CREATE returns "no such module: vec0" and we swallow it).
//   - chromem-go is pure Go, persists to disk via gob, supports
//     cosine-similarity queries with normalized vectors, and exposes
//     Add/QueryEmbedding/Delete primitives that map cleanly onto the
//     `VectorStore` interface defined in embedding_types.go.
//
// What lives where after the swap:
//
//	events_vec_meta (SQLite sidecar)
//	  → (event_id, model_id, ts) per row. Lives in history.db
//	    alongside the FTS5 path. THIS is the source of truth for
//	    "is this event embedded?" (HasEvent) and "under which model?"
//	    (model_id, AC7). The Reindex path queries this table to
//	    decide which event_ids need re-embedding.
//
//	chromem-go DB (in-memory by default; persistent in production)
//	  → vector data only. Keyed by event_id; the only operation
//	    chromem-go drives is cosine top-k. No model_id metadata is
//	    stored on the chromem side — that would split the source of
//	    truth and create a sync surface no one wants to maintain.
//
// The two stores are kept consistent by chromemVectorStore: every
// Upsert writes BOTH the events_vec_meta row AND the chromem-go
// document; every Delete removes from both. Concurrency posture
// mirrors Store: SQLite enforces single-writer via MaxOpenConns=1,
// chromem-go's own RWMutex covers its in-memory map.

package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	chromem "github.com/philippgille/chromem-go"
)

// chromemCollectionName is the name of the single collection inside
// the chromem-go DB this store maintains. One DB, one collection;
// per-event-id documents inside it. Constant so a persistent store
// reopened later finds the same collection.
const chromemCollectionName = "aish-history-events"

// chromemVectorStore is the production VectorStore backed by
// chromem-go for vector data and events_vec_meta for model_id
// metadata. The two are kept in lockstep — every write goes through
// both — so a chromem-only state is unreachable through the public
// methods.
type chromemVectorStore struct {
	db   *sql.DB
	dim  int
	cdb  *chromem.DB
	coll *chromem.Collection
}

// NewVectorStore constructs an in-memory chromem-go vector store
// associated with the supplied *sql.DB (the history.db carrying the
// events_vec_meta sidecar). dim is the expected vector dimension —
// Upsert refuses mismatched-width vectors so a model swap that
// forgets to bump fastembedDim cannot corrupt the index.
//
// Used by tests (per the seed's TestVectorStore_* fixture that calls
// NewVectorStore(s.db, testModelDim)) and by production code that
// does not need persistence across restarts. Production callers
// SHOULD use NewPersistentVectorStore — see the constructor below.
func NewVectorStore(db *sql.DB, dim int) (VectorStore, error) {
	if db == nil {
		return nil, errors.New("history: NewVectorStore: db is nil")
	}
	if dim <= 0 {
		return nil, errors.New("history: NewVectorStore: dim must be positive")
	}
	cdb := chromem.NewDB()
	return newChromemVectorStoreFromDB(db, dim, cdb)
}

// NewPersistentVectorStore constructs a chromem-go vector store
// whose vector data persists at persistDir (one directory per the
// chromem-go gob format). The events_vec_meta sidecar in db remains
// the source of truth for membership and model_id; persistDir is
// purely the vector data. Production wire-up (shell.openHistory)
// passes ~/.aish/history-vectors/ here.
//
// Pre-existing directory at persistDir is loaded; absent directory
// is created with permissions 0700 (chromem-go's NewPersistentDB
// default). The collection is created lazily — we don't pre-write
// any documents on Open.
func NewPersistentVectorStore(db *sql.DB, dim int, persistDir string) (VectorStore, error) {
	if db == nil {
		return nil, errors.New("history: NewPersistentVectorStore: db is nil")
	}
	if dim <= 0 {
		return nil, errors.New("history: NewPersistentVectorStore: dim must be positive")
	}
	if persistDir == "" {
		return nil, errors.New("history: NewPersistentVectorStore: persistDir is empty")
	}
	persistDir = filepath.Clean(persistDir)
	cdb, err := chromem.NewPersistentDB(persistDir, false)
	if err != nil {
		return nil, fmt.Errorf("history: chromem NewPersistentDB %s: %w", persistDir, err)
	}
	return newChromemVectorStoreFromDB(db, dim, cdb)
}

// newChromemVectorStoreFromDB is the shared constructor body. Pulled
// out so the two public constructors above can share collection
// setup without duplicating the GetOrCreateCollection dance.
//
// The collection's EmbeddingFunc is a no-op stub: chromem-go calls
// it only when AddDocument receives a Document with empty Embedding
// and non-empty Content. We never pass content — all callers in
// this file pre-compute vectors and pass them via the embeddings
// slice — so the stub is never invoked. We still must register one
// because GetOrCreateCollection rejects a nil embedding func via its
// default fallback.
func newChromemVectorStoreFromDB(db *sql.DB, dim int, cdb *chromem.DB) (VectorStore, error) {
	noopEmbed := func(_ context.Context, _ string) ([]float32, error) {
		return nil, errors.New("history: chromem embedding func should not be called — pass pre-computed embeddings")
	}
	coll, err := cdb.GetOrCreateCollection(chromemCollectionName, nil, noopEmbed)
	if err != nil {
		return nil, fmt.Errorf("history: chromem GetOrCreateCollection: %w", err)
	}
	return &chromemVectorStore{
		db:   db,
		dim:  dim,
		cdb:  cdb,
		coll: coll,
	}, nil
}

// Upsert writes a vector for eventID into the chromem-go collection
// AND writes the (event_id, model_id) row into events_vec_meta.
// Idempotent: re-Upserting the same (eventID, vec, model) triple
// produces no growth in either store — chromem-go's underlying map
// is keyed by document ID (same ID overwrites), and the meta INSERT
// uses ON CONFLICT DO UPDATE so the meta row is refreshed in place.
//
// Vector width is validated against the dim the store was
// constructed with. A dim mismatch is the fastest path to silent
// rank corruption — any downstream queryEmbedding call would
// happily return scores from the wider/narrower vector without
// flagging the inconsistency.
func (s *chromemVectorStore) Upsert(ctx context.Context, eventID string, vec []float32, model string) error {
	if s == nil {
		return errors.New("history: chromem Upsert: nil receiver")
	}
	if eventID == "" {
		return errors.New("history: chromem Upsert: empty eventID")
	}
	if model == "" {
		return errors.New("history: chromem Upsert: empty model id")
	}
	if len(vec) != s.dim {
		return fmt.Errorf("history: chromem Upsert: vector dim %d != store dim %d", len(vec), s.dim)
	}

	// Defensive copy so a caller mutating its vec slice post-Upsert
	// cannot reach into our stored embedding. chromem-go also clones
	// internally via slices.Clone, but the cost is negligible and the
	// guarantee survives library changes.
	embedding := make([]float32, len(vec))
	copy(embedding, vec)

	if err := s.coll.AddDocument(ctx, chromem.Document{
		ID:        eventID,
		Embedding: embedding,
	}); err != nil {
		return fmt.Errorf("history: chromem AddDocument: %w", err)
	}

	// Sidecar meta: upsert (event_id, model_id) with ON CONFLICT
	// updating the timestamp + model so a model-change reindex sees
	// the new value next pass.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO events_vec_meta(event_id, model_id, ts)
		   VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(event_id) DO UPDATE SET
		   model_id = excluded.model_id,
		   ts = excluded.ts`,
		eventID, model,
	); err != nil {
		return fmt.Errorf("history: events_vec_meta upsert: %w", err)
	}
	return nil
}

// Query returns the top-k cosine-similarity matches as VectorHit
// pairs, ordered by descending Score. Empty store → empty slice
// (not an error — semantic-on-fresh-DB is a normal state per AC10).
//
// chromem-go's QueryEmbedding requires nResults <= Count(); we
// clamp k accordingly so a caller asking for 50 hits against a
// 10-row store gets 10 hits rather than an error. This matches
// the FTS5 path's contract: a query that finds fewer hits returns
// fewer hits, not a failure.
func (s *chromemVectorStore) Query(ctx context.Context, vec []float32, k int) ([]VectorHit, error) {
	if s == nil {
		return nil, errors.New("history: chromem Query: nil receiver")
	}
	if len(vec) != s.dim {
		return nil, fmt.Errorf("history: chromem Query: vector dim %d != store dim %d", len(vec), s.dim)
	}
	if k <= 0 {
		return nil, nil
	}
	count := s.coll.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}

	results, err := s.coll.QueryEmbedding(ctx, vec, k, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("history: chromem QueryEmbedding: %w", err)
	}
	out := make([]VectorHit, 0, len(results))
	for _, r := range results {
		out = append(out, VectorHit{EventID: r.ID, Score: r.Similarity})
	}
	return out, nil
}

// Delete removes the document for eventID from chromem-go AND the
// row from events_vec_meta. Missing-id is a no-op (matches the
// chromem-go behavior; we mirror it for the meta table so reindex's
// model-mismatch path can issue a blind Delete without first
// checking HasEvent).
func (s *chromemVectorStore) Delete(ctx context.Context, eventID string) error {
	if s == nil {
		return errors.New("history: chromem Delete: nil receiver")
	}
	if eventID == "" {
		return errors.New("history: chromem Delete: empty eventID")
	}
	// chromem Delete with ids... preserves the rest of the collection.
	// nil where / whereDocument means "ids-only" — what we want.
	if err := s.coll.Delete(ctx, nil, nil, eventID); err != nil {
		return fmt.Errorf("history: chromem Delete: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM events_vec_meta WHERE event_id = ?`,
		eventID,
	); err != nil {
		return fmt.Errorf("history: events_vec_meta delete: %w", err)
	}
	return nil
}

// HasEvent answers "is there a vector row for this event_id?" by
// consulting events_vec_meta — NOT the chromem store directly.
// Rationale: events_vec_meta is the durable source of truth (lives
// in the same history.db FTS5 lives in), survives a corrupted
// chromem state, and is queryable without instantiating a chromem
// reader. A divergent state (meta present, chromem absent) would
// surface during the next Query and is repairable via reindex.
func (s *chromemVectorStore) HasEvent(ctx context.Context, eventID string) (bool, error) {
	if s == nil {
		return false, errors.New("history: chromem HasEvent: nil receiver")
	}
	if eventID == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM events_vec_meta WHERE event_id = ?`,
		eventID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("history: events_vec_meta lookup: %w", err)
	}
	return n > 0, nil
}

// Compile-time assertion that chromemVectorStore satisfies the
// VectorStore interface. Surface drift in embedding_types.go breaks
// here at `go build`.
var _ VectorStore = (*chromemVectorStore)(nil)
