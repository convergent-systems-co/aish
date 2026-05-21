package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// fakeAdapter is the synthetic PersonaAdapter test double used by
// orchestrator-level unit tests. Per plan §Test strategy, synthetic
// doubles are acceptable at the orchestrator → adapter boundary for
// state-machine unit tests; T5 integration tests wire real adapters.
type fakeAdapter struct {
	name         string
	captureSnap  Snapshot
	captureErr   error
	applyErr     error
	verifyErr    error
	rollbackErr  error
	applyCalls   int
	verifyCalls  int
	rollbackArgs []Snapshot
	captureCalls int
}

func (f *fakeAdapter) Name() string { return f.name }

func (f *fakeAdapter) Capture(ctx context.Context) (Snapshot, error) {
	f.captureCalls++
	return f.captureSnap, f.captureErr
}

func (f *fakeAdapter) Apply(ctx context.Context, p persona.Persona) error {
	f.applyCalls++
	return f.applyErr
}

func (f *fakeAdapter) Verify(ctx context.Context, p persona.Persona) error {
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeAdapter) Rollback(ctx context.Context, s Snapshot) error {
	f.rollbackArgs = append(f.rollbackArgs, s)
	return f.rollbackErr
}

// TestPersonaAdapter_InterfaceCompiles is a compile-time guard that
// the synthetic *fakeAdapter satisfies the PersonaAdapter interface.
// If the interface drifts, this test stops compiling — which is the
// point.
func TestPersonaAdapter_InterfaceCompiles(t *testing.T) {
	t.Parallel()
	var _ PersonaAdapter = (*fakeAdapter)(nil)
}

// TestSentinelErrors confirms the sentinel errors are real values
// (not nil) and that the subsystem-specific sentinels wrap
// ErrNoSubsystem-style umbrella semantics correctly when applicable.
func TestSentinelErrors(t *testing.T) {
	t.Parallel()
	if ErrNoSubsystem == nil {
		t.Fatal("ErrNoSubsystem must be a non-nil sentinel")
	}
	if ErrNoAgent == nil {
		t.Fatal("ErrNoAgent must be a non-nil sentinel")
	}
	if ErrNoCLI == nil {
		t.Fatal("ErrNoCLI must be a non-nil sentinel")
	}
	if ErrSchema == nil {
		t.Fatal("ErrSchema must be a non-nil sentinel")
	}
	if ErrNoBinding == nil {
		t.Fatal("ErrNoBinding must be a non-nil sentinel")
	}
	// Sentinels are distinct identities.
	if errors.Is(ErrNoAgent, ErrSchema) {
		t.Fatal("ErrNoAgent must not wrap ErrSchema")
	}
}

// TestSnapshotZeroValue asserts a nil Snapshot is a legal zero value
// (used by the orchestrator when an adapter is skipped).
func TestSnapshotZeroValue(t *testing.T) {
	t.Parallel()
	var s Snapshot
	if s != nil {
		t.Fatalf("zero-value Snapshot must be nil; got %v", s)
	}
	if len(s) != 0 {
		t.Fatalf("zero-value Snapshot must have len 0; got %d", len(s))
	}
}

// TestOutcomeZeroValue asserts a zero-value Outcome has empty slices
// (not nil-different-from-empty surprises in downstream consumers).
func TestOutcomeZeroValue(t *testing.T) {
	t.Parallel()
	o := Outcome{}
	if len(o.Applied) != 0 {
		t.Fatalf("Outcome.Applied must default empty; got %v", o.Applied)
	}
	if len(o.RolledBack) != 0 {
		t.Fatalf("Outcome.RolledBack must default empty; got %v", o.RolledBack)
	}
	if len(o.Skipped) != 0 {
		t.Fatalf("Outcome.Skipped must default empty; got %v", o.Skipped)
	}
	if o.Cause != nil {
		t.Fatalf("Outcome.Cause must default nil; got %v", o.Cause)
	}
}
