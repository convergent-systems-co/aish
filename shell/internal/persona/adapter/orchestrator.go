package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// Transaction is the state machine that drives N adapters through
// Capture → Apply → Verify → Rollback. One Transaction per
// `aish persona use <name>` invocation.
//
// The Adapters slice is ordered: Apply runs front-to-back, Rollback
// runs back-to-front. Order is significant — the shell builtin's
// integration site declares it (SSH, Cloud, Kube, Git is the canonical
// declaration order for v0.3-3).
//
// A zero-Adapters Transaction is legal: Execute returns immediately
// with an empty Outcome and nil error. This preserves the pre-#104
// behaviour for personas with no ExternalBindings declared.
type Transaction struct {
	// Adapters is the ordered slice of PersonaAdapter implementations
	// to drive. May be empty.
	Adapters []PersonaAdapter

	// Logger receives per-phase, per-adapter debug records. May be
	// nil; the orchestrator no-logs in that case via slog.Default()
	// at the discard level.
	Logger *slog.Logger
}

// Execute runs the four-phase state machine across t.Adapters with
// the given persona. Returns (Outcome, nil) on full success; returns
// (Outcome, non-nil) when any adapter's Apply or Verify failed AFTER
// rolling back every adapter that previously Applied.
//
// The returned Outcome is populated even when err is non-nil so the
// caller (shell builtin / signed-event recorder) can surface the
// per-adapter audit trail. err carries the original cause plus any
// rollback errors joined via errors.Join — callers needing the
// individual rollback errors should read Outcome.RollbackErrors.
//
// Phases:
//
//  1. Capture-all. Iterate t.Adapters front-to-back. For each adapter,
//     call Capture(ctx). On ErrNoSubsystem (or any error that wraps
//     ErrNoSubsystem) the adapter is recorded under Outcome.Skipped
//     and excluded from the remaining phases. Any other Capture error
//     halts the transaction immediately — no Apply has run yet, so no
//     Rollback is needed; Outcome.Cause carries the error.
//
//  2. Apply-in-order. For each non-skipped adapter, call Apply(ctx, p).
//     On the FIRST Apply error, halt and proceed to Phase 4 against
//     adapters [0..i-1] (Apply completed for those). The failing
//     adapter is NOT rolled back — it never reached a fully-applied
//     state.
//
//  3. Verify-all. After every adapter has Applied, iterate again and
//     call Verify(ctx, p). On the FIRST Verify error, halt and
//     proceed to Phase 4 against EVERY non-skipped adapter (including
//     the one that failed Verify — Apply did complete for it).
//
//  4. Rollback-in-reverse. For each adapter requiring rollback, in
//     reverse declared order, call Rollback(ctx, snapshot). Each
//     rollback error is appended to Outcome.RollbackErrors; rollback
//     proceeds across all adapters regardless of individual failures
//     (best-effort cleanup).
//
// On success, every Applied adapter's name is recorded under
// Outcome.Applied. Outcome.Cause and Outcome.RollbackErrors are nil.
func (t *Transaction) Execute(ctx context.Context, p persona.Persona) (Outcome, error) {
	log := t.Logger
	if log == nil {
		log = slog.Default()
	}

	out := Outcome{}

	// Empty-adapter shortcut: legacy-path preservation.
	if len(t.Adapters) == 0 {
		return out, nil
	}

	// Phase 1 — Capture every adapter. snapshots[i] is the Snapshot
	// for t.Adapters[i] when active[i] is true; otherwise the adapter
	// is skipped and snapshot is nil.
	snapshots := make([]Snapshot, len(t.Adapters))
	active := make([]bool, len(t.Adapters))
	for i, a := range t.Adapters {
		name := a.Name()
		snap, err := a.Capture(ctx)
		if err != nil {
			if errors.Is(err, ErrNoSubsystem) {
				log.Debug("adapter capture skipped (subsystem absent)",
					"adapter", name, "err", err)
				out.Skipped = append(out.Skipped, name)
				continue
			}
			// Hard Capture error — no mutation has occurred. Surface
			// and return; no rollback needed.
			out.Cause = fmt.Errorf("capture %s: %w", name, err)
			return out, out.Cause
		}
		snapshots[i] = snap
		active[i] = true
	}

	// Phase 2 — Apply in declared order, halting on first failure.
	// applied[i] flips true when Apply returns nil for that adapter.
	applied := make([]bool, len(t.Adapters))
	var applyErr error
	var applyFailIdx = -1
	for i, a := range t.Adapters {
		if !active[i] {
			continue
		}
		name := a.Name()
		if err := a.Apply(ctx, p); err != nil {
			applyErr = fmt.Errorf("apply %s: %w", name, err)
			applyFailIdx = i
			log.Debug("adapter apply failed",
				"adapter", name, "err", err)
			break
		}
		applied[i] = true
		log.Debug("adapter apply ok", "adapter", name)
	}

	// Phase 3 — Verify every adapter that Applied, but only if Apply
	// itself fully succeeded. Track the first Verify failure to drive
	// rollback decisions.
	var verifyErr error
	if applyErr == nil {
		for i, a := range t.Adapters {
			if !applied[i] {
				continue
			}
			name := a.Name()
			if err := a.Verify(ctx, p); err != nil {
				verifyErr = fmt.Errorf("verify %s: %w", name, err)
				log.Debug("adapter verify failed",
					"adapter", name, "err", err)
				break
			}
			log.Debug("adapter verify ok", "adapter", name)
		}
	}

	// Success: every active adapter Applied AND Verified.
	if applyErr == nil && verifyErr == nil {
		for i, a := range t.Adapters {
			if applied[i] {
				out.Applied = append(out.Applied, a.Name())
			}
		}
		return out, nil
	}

	// Failure path: roll back every adapter whose Apply completed,
	// in reverse declared order. The adapter at applyFailIdx itself
	// is NOT in the applied[] set (Apply errored), so it's correctly
	// excluded.
	out.Cause = applyErr
	if out.Cause == nil {
		out.Cause = verifyErr
	}
	_ = applyFailIdx // retained for clarity / future logging

	for i := len(t.Adapters) - 1; i >= 0; i-- {
		if !applied[i] {
			continue
		}
		a := t.Adapters[i]
		name := a.Name()
		if err := a.Rollback(ctx, snapshots[i]); err != nil {
			rbErr := fmt.Errorf("rollback %s: %w", name, err)
			out.RollbackErrors = append(out.RollbackErrors, rbErr)
			log.Debug("adapter rollback failed",
				"adapter", name, "err", err)
			continue
		}
		out.RolledBack = append(out.RolledBack, name)
		log.Debug("adapter rollback ok", "adapter", name)
	}

	// Surface original cause + any rollback errors via errors.Join.
	joined := []error{out.Cause}
	joined = append(joined, out.RollbackErrors...)
	return out, errors.Join(joined...)
}
