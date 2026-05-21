package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// minimalPersona returns a valid persona for orchestrator tests. The
// orchestrator does not inspect persona fields directly — only the
// adapters do — so any valid persona suffices.
func minimalPersona() persona.Persona {
	return persona.Persona{
		Name:         "test",
		Version:      persona.SchemaVersion,
		SystemPrompt: "test prompt",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
}

// TestTransaction_Execute_NoAdapters_LegacyPath ensures the
// zero-adapter case is a no-op. This is the backward-compatibility
// shim: a persona with no ExternalBindings produces a zero-adapter
// transaction, which must return successfully with an empty Outcome.
func TestTransaction_Execute_NoAdapters_LegacyPath(t *testing.T) {
	t.Parallel()
	tx := &Transaction{}
	out, err := tx.Execute(context.Background(), minimalPersona())
	if err != nil {
		t.Fatalf("zero-adapter Execute must succeed; got %v", err)
	}
	if len(out.Applied) != 0 || len(out.RolledBack) != 0 || len(out.Skipped) != 0 {
		t.Fatalf("zero-adapter Outcome must be empty; got %+v", out)
	}
}

// TestTransaction_Execute_AllSucceed exercises the happy path: every
// adapter Captures, Applies, and Verifies cleanly. The Outcome lists
// every adapter under Applied in declared order; no rollback fires.
func TestTransaction_Execute_AllSucceed(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("snap-ssh")}
	b := &fakeAdapter{name: "kube", captureSnap: Snapshot("snap-kube")}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("snap-git")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c}}

	out, err := tx.Execute(context.Background(), minimalPersona())
	if err != nil {
		t.Fatalf("happy path must succeed; got %v", err)
	}
	want := []string{"ssh", "kube", "git"}
	if !equalStrings(out.Applied, want) {
		t.Fatalf("Applied = %v; want %v", out.Applied, want)
	}
	if len(out.RolledBack) != 0 {
		t.Fatalf("RolledBack must be empty on success; got %v", out.RolledBack)
	}
	if a.applyCalls != 1 || b.applyCalls != 1 || c.applyCalls != 1 {
		t.Fatalf("each adapter must Apply once; ssh=%d kube=%d git=%d",
			a.applyCalls, b.applyCalls, c.applyCalls)
	}
	if a.verifyCalls != 1 || b.verifyCalls != 1 || c.verifyCalls != 1 {
		t.Fatalf("each adapter must Verify once; ssh=%d kube=%d git=%d",
			a.verifyCalls, b.verifyCalls, c.verifyCalls)
	}
	if len(a.rollbackArgs) != 0 || len(b.rollbackArgs) != 0 || len(c.rollbackArgs) != 0 {
		t.Fatalf("no rollback on success; ssh=%d kube=%d git=%d",
			len(a.rollbackArgs), len(b.rollbackArgs), len(c.rollbackArgs))
	}
}

// TestTransaction_Execute_ApplyFailRollsBackPriorInReverseOrder
// covers the partial-failure rollback contract from acceptance
// criterion #1: if adapter N's Apply fails, adapters 1..N-1 are
// rolled back in REVERSE order. The failing adapter is NOT rolled
// back (Apply never completed for it).
func TestTransaction_Execute_ApplyFailRollsBackPriorInReverseOrder(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("snap-ssh")}
	b := &fakeAdapter{name: "kube", captureSnap: Snapshot("snap-kube")}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("snap-git"),
		applyErr: errors.New("git config blew up")}
	d := &fakeAdapter{name: "cloud", captureSnap: Snapshot("snap-cloud")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c, d}}

	out, err := tx.Execute(context.Background(), minimalPersona())
	if err == nil {
		t.Fatal("Execute must return an error when an Apply fails")
	}
	// Rollback called on a and b only — c never fully Applied and d
	// never reached Apply.
	if len(a.rollbackArgs) != 1 || len(b.rollbackArgs) != 1 {
		t.Fatalf("ssh and kube must each be rolled back exactly once; "+
			"got ssh=%d kube=%d", len(a.rollbackArgs), len(b.rollbackArgs))
	}
	if len(c.rollbackArgs) != 0 {
		t.Fatalf("failing adapter (git) must NOT be rolled back; got %d",
			len(c.rollbackArgs))
	}
	if d.applyCalls != 0 {
		t.Fatalf("adapter after failure must not be Applied; got %d",
			d.applyCalls)
	}
	// Outcome surfaces rolled-back adapters in REVERSE order.
	want := []string{"kube", "ssh"}
	if !equalStrings(out.RolledBack, want) {
		t.Fatalf("RolledBack order = %v; want %v (reverse)", out.RolledBack, want)
	}
	if len(out.Applied) != 0 {
		t.Fatalf("Applied must be empty on failure; got %v", out.Applied)
	}
	if out.Cause == nil {
		t.Fatal("Outcome.Cause must carry the original Apply error")
	}
	if !strings.Contains(out.Cause.Error(), "git config blew up") {
		t.Fatalf("Cause must wrap original; got %v", out.Cause)
	}
}

// TestTransaction_Execute_VerifyFailRollsBackAllApplied: when Verify
// fails after every adapter has Applied, EVERY Applied adapter
// (including the one that failed Verify — Apply did complete) is
// rolled back in reverse order.
func TestTransaction_Execute_VerifyFailRollsBackAllApplied(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("ssh")}
	b := &fakeAdapter{name: "kube", captureSnap: Snapshot("kube"),
		verifyErr: errors.New("kube context not active post-apply")}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("git")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c}}

	out, err := tx.Execute(context.Background(), minimalPersona())
	if err == nil {
		t.Fatal("Execute must return an error when Verify fails")
	}
	if len(a.rollbackArgs) != 1 || len(b.rollbackArgs) != 1 || len(c.rollbackArgs) != 1 {
		t.Fatalf("every applied adapter must roll back on Verify failure; "+
			"ssh=%d kube=%d git=%d",
			len(a.rollbackArgs), len(b.rollbackArgs), len(c.rollbackArgs))
	}
	want := []string{"git", "kube", "ssh"}
	if !equalStrings(out.RolledBack, want) {
		t.Fatalf("RolledBack order = %v; want %v", out.RolledBack, want)
	}
}

// TestTransaction_Execute_RollbackErrorsSurfaceMulti: a Rollback that
// itself fails must be surfaced via Outcome.RollbackErrors AND
// joined into the returned error. The orchestrator MUST continue
// rolling back the rest of the chain on a per-adapter rollback
// failure (best-effort cleanup, plan §Risk assessment).
func TestTransaction_Execute_RollbackErrorsSurfaceMulti(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("ssh")}
	b := &fakeAdapter{name: "kube", captureSnap: Snapshot("kube"),
		rollbackErr: errors.New("kube socket vanished mid-rollback")}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("git"),
		applyErr: errors.New("git failed")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c}}

	_, err := tx.Execute(context.Background(), minimalPersona())
	if err == nil {
		t.Fatal("Execute must return an error")
	}
	if !strings.Contains(err.Error(), "git failed") {
		t.Fatalf("joined err must contain original cause; got %v", err)
	}
	if !strings.Contains(err.Error(), "kube socket vanished") {
		t.Fatalf("joined err must contain rollback failure; got %v", err)
	}
	// Even though kube's rollback failed, ssh's rollback must still
	// have been attempted (best-effort).
	if len(a.rollbackArgs) != 1 {
		t.Fatalf("ssh rollback must run despite kube rollback failure; got %d",
			len(a.rollbackArgs))
	}
}

// TestTransaction_Execute_CaptureSkipPropagates: a Capture that
// returns ErrNoSubsystem records the adapter as Skipped and proceeds.
// Apply / Verify / Rollback are never called for the skipped adapter.
func TestTransaction_Execute_CaptureSkipPropagates(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("ssh")}
	b := &fakeAdapter{name: "cloud", captureErr: ErrNoSubsystem}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("git")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c}}

	out, err := tx.Execute(context.Background(), minimalPersona())
	if err != nil {
		t.Fatalf("skipped Capture must not fail the transaction; got %v", err)
	}
	if b.applyCalls != 0 || b.verifyCalls != 0 || len(b.rollbackArgs) != 0 {
		t.Fatalf("skipped adapter must not Apply/Verify/Rollback; "+
			"apply=%d verify=%d rollback=%d",
			b.applyCalls, b.verifyCalls, len(b.rollbackArgs))
	}
	wantSkipped := []string{"cloud"}
	if !equalStrings(out.Skipped, wantSkipped) {
		t.Fatalf("Skipped = %v; want %v", out.Skipped, wantSkipped)
	}
	wantApplied := []string{"ssh", "git"}
	if !equalStrings(out.Applied, wantApplied) {
		t.Fatalf("Applied = %v; want %v", out.Applied, wantApplied)
	}
}

// TestTransaction_Execute_CaptureHardErrorHaltsBeforeMutation: a
// Capture error that is NOT ErrNoSubsystem must halt the transaction
// immediately, with no Apply having run (no mutation, no rollback).
func TestTransaction_Execute_CaptureHardErrorHaltsBeforeMutation(t *testing.T) {
	t.Parallel()
	a := &fakeAdapter{name: "ssh", captureSnap: Snapshot("ssh")}
	b := &fakeAdapter{name: "kube", captureErr: errors.New("read kubeconfig: permission denied")}
	c := &fakeAdapter{name: "git", captureSnap: Snapshot("git")}
	tx := &Transaction{Adapters: []PersonaAdapter{a, b, c}}

	out, err := tx.Execute(context.Background(), minimalPersona())
	if err == nil {
		t.Fatal("Execute must surface a hard Capture error")
	}
	if a.applyCalls != 0 || c.applyCalls != 0 {
		t.Fatalf("no Apply may run when Capture fails; ssh=%d git=%d",
			a.applyCalls, c.applyCalls)
	}
	if !strings.Contains(out.Cause.Error(), "permission denied") {
		t.Fatalf("Cause must wrap original Capture error; got %v", out.Cause)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
