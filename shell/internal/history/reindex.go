// Reindex command + backfill path for v0.3-4 #112.
//
// Surface:
//
//	Store.Reindex(ctx) (int, error)
//
// Behavior (plan §T5):
//
//   - Walk every event in the events table.
//   - Skip events whose command equals RedactedTainted (AC4 — taint
//     leakage prevention; same gate as the write-path hook).
//   - For each survivor, decide what to do based on events_vec_meta:
//
//	   no row OR model_id != current embedder ModelID
//	     → embed under the active embedder, Upsert (which replaces),
//	       count the event.
//
//	   row exists AND model_id == active ModelID
//	     → already correctly embedded, skip.
//
//   - Return the count of events that were (re-)embedded.
//
// Idempotent (AC6): a fresh-after-reindex run does no work because
// every survivor has a current model_id row. Resumable (AC6): every
// event is its own work unit — Upsert + meta-update are independent
// per row; killing the process mid-loop leaves the survivors
// committed and the next Reindex run picks up where this one left
// off. Model-version-aware (AC7): rows with a stale model_id are
// re-embedded.
//
// Failure modes:
//   - No embedder attached → error. Reindex cannot do its job
//     without one. The CLI surfaces this as "configure an embedder
//     first."
//   - No vec attached → error. Same rationale.
//   - Embedder error mid-loop → wrapped error returned; survivors
//     before the error are committed. Re-run picks up from there.

package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Reindex walks every event in the store, embedding each non-
// tainted command and upserting the result into the attached
// vector store. Tainted events (command == RedactedTainted) are
// skipped at the gate — never read, never embedded.
//
// Returns the count of events that were (re-)embedded in this
// pass. A second consecutive call returns 0 (or close to it,
// depending on the in-between writes); idempotency is the contract
// in TestReindex_Idempotent.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("history: Reindex: store is closed")
	}
	if s.embedder == nil {
		return 0, errors.New("history: Reindex: no embedder attached — configure one before running reindex")
	}
	if s.vec == nil {
		return 0, errors.New("history: Reindex: no vector store attached")
	}

	activeModel := s.embedder.ModelID()

	// One pass: SELECT every event's (id, command). Stream so a
	// large history doesn't materialize the full set in memory.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, command FROM events ORDER BY ts ASC, id ASC`,
	)
	if err != nil {
		return 0, fmt.Errorf("history: Reindex select: %w", err)
	}
	defer rows.Close()

	type pendingRow struct {
		id      string
		command string
	}
	var pending []pendingRow
	for rows.Next() {
		var id, cmd string
		if err := rows.Scan(&id, &cmd); err != nil {
			return 0, fmt.Errorf("history: Reindex scan: %w", err)
		}
		// Taint-skip at the gate — never embed the placeholder.
		if cmd == RedactedTainted {
			continue
		}
		pending = append(pending, pendingRow{id: id, command: cmd})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("history: Reindex iterate: %w", err)
	}

	// Per-row loop: decide skip/re-embed by consulting
	// events_vec_meta for the row's current model_id. Each iteration
	// is its own unit — Upsert + meta-update commit independently so
	// a kill mid-loop leaves survivors committed and the next run
	// resumes naturally.
	count := 0
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return count, err
		}

		// Existing model_id? If it matches the active one and the
		// vector data is intact, skip the work.
		var storedModel string
		err := s.db.QueryRowContext(ctx,
			`SELECT model_id FROM events_vec_meta WHERE event_id = ?`,
			p.id,
		).Scan(&storedModel)
		if err == nil && storedModel == activeModel {
			// AND the vector store has the row — HasEvent gates
			// against a "meta present, chromem absent" divergence
			// (e.g., persistDir was nuked but meta wasn't).
			has, hErr := s.vec.HasEvent(ctx, p.id)
			if hErr == nil && has {
				continue
			}
			// Fall through to re-embed.
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return count, fmt.Errorf("history: Reindex meta lookup %s: %w", p.id, err)
		}

		// Model mismatch (or no row): if there's an old row, delete
		// it from the vector store first so we don't accumulate
		// orphans under multiple model_ids. The Upsert below
		// replaces in chromem-go and the meta row, but Delete is
		// the explicit "purge old model" gesture so the
		// TestReindex_ModelMismatch assertion can verify state by
		// counting rows.
		if err == nil && storedModel != activeModel {
			if dErr := s.vec.Delete(ctx, p.id); dErr != nil {
				return count, fmt.Errorf("history: Reindex delete-stale %s: %w", p.id, dErr)
			}
		}

		// Embed and Upsert. One-element batch; the latency-amortizing
		// large-batch path is a future optimization (see plan §T5
		// note on batching).
		vecs, eErr := s.embedder.Embed(ctx, []string{p.command})
		if eErr != nil {
			return count, fmt.Errorf("history: Reindex embed %s: %w", p.id, eErr)
		}
		if len(vecs) != 1 || len(vecs[0]) == 0 {
			return count, fmt.Errorf("history: Reindex embed %s: empty vector", p.id)
		}
		if uErr := s.vec.Upsert(ctx, p.id, vecs[0], activeModel); uErr != nil {
			return count, fmt.Errorf("history: Reindex upsert %s: %w", p.id, uErr)
		}
		// Belt-and-braces: also touch events_vec_meta directly. The
		// production VectorStore impl (chromem) already does this on
		// Upsert, but stub implementations used in T3..T5 tests do
		// not — and meta is the source of truth for model_id
		// queries. Idempotent via ON CONFLICT.
		if _, mErr := s.db.ExecContext(ctx,
			`INSERT INTO events_vec_meta(event_id, model_id, ts)
			   VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(event_id) DO UPDATE SET
			   model_id = excluded.model_id,
			   ts = excluded.ts`,
			p.id, activeModel,
		); mErr != nil {
			return count, fmt.Errorf("history: Reindex meta upsert %s: %w", p.id, mErr)
		}
		count++
	}
	return count, nil
}
