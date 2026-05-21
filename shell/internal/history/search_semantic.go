// Semantic + hybrid search surface for v0.3-4 #112.
//
// Adds two Store methods alongside the pre-#112 Search:
//
//	SemanticSearch(query, limit) — vector-only top-K by cosine.
//	HybridSearch(query, limit)   — RRF k=60 fusion of FTS5 + vector.
//
// Pre-#112 Search is preserved unchanged (search.go). The
// `--mode={keyword,semantic,hybrid}` flag in builtin_history.go
// routes to one of the three methods.
//
// Failure modes follow the migration-safety contract (AC10):
//
//   - SemanticSearch with no vec OR no embedder attached returns
//     ErrNoVectors so the CLI layer can render a "run `aish history
//     reindex`" hint. The error string contains the literal
//     "reindex" so the shell-side test asserts on it.
//
//   - HybridSearch with no vec OR no embedder attached silently
//     degrades to the FTS5 path — pre-#112 behavior. No error, no
//     "reindex" message. This is the default mode for users with a
//     stock binary and no vector store yet.
//
// RRF math (AC5): Reciprocal Rank Fusion with k=60. Each surface
// (FTS, vector) emits a ranked list; an event's fused score is the
// sum of 1/(60+rank) across the two lists. Events present in only
// one list still score on that list. The constant 60 is the
// published default and matches the test fixture's expected math.

package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrNoVectors is returned by SemanticSearch when the Store has no
// vector store attached, no embedder attached, or both. The CLI
// layer (builtin_history.go) checks for this sentinel and renders a
// friendly "run `aish history reindex`" hint. The error message
// embeds "reindex" so both the Go-side test (errors.Is + string
// contains) and the shell-side test (combined stdout/stderr
// substring match) can assert on it.
var ErrNoVectors = errors.New("history: semantic search unavailable — no vector store; run `aish history reindex` after configuring an embedder")

// SemanticSearch embeds the natural-language query via the attached
// EmbeddingProvider, then runs a top-K cosine query against the
// attached VectorStore. Returns the matching Events in descending
// score order.
//
// limit ≤ 0 → default 50 (mirrors Search's contract).
//
// Failure modes:
//   - Closed store → "store is closed" error (pre-existing pattern).
//   - Empty query → "empty query" error (matches Search).
//   - No vec / no embedder → ErrNoVectors (the CLI hint signal).
//   - Embedder error → wrapped error; recovery is "fix the embedder."
//   - Vector store empty → empty result slice (not an error).
func (s *Store) SemanticSearch(query string, limit int) ([]*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: SemanticSearch: store is closed")
	}
	if query == "" {
		return nil, errors.New("history: SemanticSearch: empty query")
	}
	if s.vec == nil || s.embedder == nil {
		return nil, ErrNoVectors
	}
	if limit <= 0 {
		limit = 50
	}

	ctx := context.Background()
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("history: SemanticSearch embed: %w", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, errors.New("history: SemanticSearch: embedder returned no vector")
	}

	hits, err := s.vec.Query(ctx, vecs[0], limit)
	if err != nil {
		return nil, fmt.Errorf("history: SemanticSearch query: %w", err)
	}
	return s.loadEventsByID(extractIDs(hits))
}

// HybridSearch runs both Search (FTS5 keyword) and SemanticSearch
// (cosine), fuses the two ranked lists via Reciprocal Rank Fusion
// (k=60), and returns the merged top-K Events.
//
// Degraded path (AC10): when no vector store or embedder is
// attached, HybridSearch transparently returns the Search result —
// no error, no "reindex" hint. This is by design: the user has not
// asked for semantic ranking explicitly, hybrid is the default mode
// post-#112, and the FTS path is always available.
//
// Embedder-error inside the semantic branch is also absorbed: the
// hybrid result degrades to FTS-only with a logged-but-not-returned
// embedder error. The user's query is more important than the
// semantic signal.
func (s *Store) HybridSearch(query string, limit int) ([]*Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("history: HybridSearch: store is closed")
	}
	if query == "" {
		return nil, errors.New("history: HybridSearch: empty query")
	}
	if limit <= 0 {
		limit = 50
	}

	// FTS path always runs. Errors here are real — the FTS surface
	// is the canonical fallback.
	ftsHits, err := s.Search(query, limit)
	if err != nil {
		return nil, fmt.Errorf("history: HybridSearch fts: %w", err)
	}

	// Degraded path: no semantic surface. Return FTS as-is.
	if s.vec == nil || s.embedder == nil {
		return ftsHits, nil
	}

	// Semantic branch — best-effort. Errors do not propagate; we
	// just lose the semantic signal for this query.
	ctx := context.Background()
	vecs, embedErr := s.embedder.Embed(ctx, []string{query})
	if embedErr != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
		return ftsHits, nil
	}
	vecHits, qErr := s.vec.Query(ctx, vecs[0], limit)
	if qErr != nil {
		return ftsHits, nil
	}

	ftsIDs := make([]string, 0, len(ftsHits))
	for _, e := range ftsHits {
		ftsIDs = append(ftsIDs, e.ID)
	}
	vecIDs := extractIDs(vecHits)

	merged := reciprocalRankFusion(ftsIDs, vecIDs, 60)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return s.loadEventsByID(merged)
}

// reciprocalRankFusion fuses two ranked-id lists into one ranked
// list via the RRF formula:
//
//	score(id) = sum over lists of 1.0 / (k + rank(id, list))
//
// where rank is 1-based. Ids missing from a list contribute 0 from
// that list (equivalent to the sum stopping before the missing id).
// The output is sorted by descending score; ties break on the order
// in which the ids first appeared across the two input lists, so
// the result is deterministic given fixed inputs.
//
// k is the smoothing constant; AC5 pins it to 60 (the published
// default that downweights long-tail ranks without zeroing them
// out). Lower k weights the top of each list more aggressively;
// higher k flattens the contribution curve.
func reciprocalRankFusion(fts, vec []string, k int) []string {
	scores := make(map[string]float64)
	firstSeen := make(map[string]int)
	order := 0

	add := func(list []string) {
		for i, id := range list {
			rank := i + 1
			scores[id] += 1.0 / float64(k+rank)
			if _, ok := firstSeen[id]; !ok {
				firstSeen[id] = order
				order++
			}
		}
	}
	add(fts)
	add(vec)

	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return firstSeen[ids[i]] < firstSeen[ids[j]]
	})
	return ids
}

// extractIDs pulls the EventID field from a VectorHit slice. Small
// helper; lives here because both SemanticSearch and HybridSearch
// need the same conversion.
func extractIDs(hits []VectorHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.EventID)
	}
	return out
}

// loadEventsByID re-hydrates a slice of ids into Events by reading
// each row's payload column. Preserves the input order — ids[0] is
// the highest-ranked event, the caller has already decided the
// final ordering. Missing ids (e.g., an event was Purged but its
// vector survived) are silently dropped from the output.
//
// Implemented as a per-id SELECT rather than `WHERE id IN (...)`
// because IN clauses don't preserve order on SQLite — sorting the
// IN result back into the input order would itself require a
// secondary pass, and the per-id cost on n≤50 events is negligible.
func (s *Store) loadEventsByID(ids []string) ([]*Event, error) {
	out := make([]*Event, 0, len(ids))
	for _, id := range ids {
		var payload string
		err := s.db.QueryRow(`SELECT payload FROM events WHERE id = ?`, id).Scan(&payload)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("history: loadEventsByID %s: %w", id, err)
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		out = append(out, &ev)
	}
	return out, nil
}
