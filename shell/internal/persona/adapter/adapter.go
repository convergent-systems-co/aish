package adapter

import (
	"context"
	"errors"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// Snapshot is the opaque per-adapter blob the orchestrator persists
// between Capture and Apply so Rollback can restore the prior state.
// Its bytes are meaningful only to the adapter that produced it; the
// orchestrator treats it as a void* analog.
//
// INVARIANT: Snapshot bytes MUST NOT carry secret material. Each
// adapter's unit-test suite includes an adversarial assertion that
// `bytes.Contains(snapshot, privateKeyOrToken) == false`. See the
// package doc comment for the rationale.
type Snapshot []byte

// PersonaAdapter is the contract every external subsystem implements.
//
// All four methods MUST be safe to call against an absent or
// uninitialized subsystem: if (for example) $SSH_AUTH_SOCK is unset,
// Capture MUST return ErrNoAgent rather than panic. The orchestrator
// treats a "subsystem absent" Capture as "skip this adapter," not
// "fail the transaction" — the persona binding for that subsystem is
// simply not honored on this host.
//
// Implementations MUST be deterministic with respect to the persona
// they're given: identical persona + identical pre-state → identical
// Snapshot and identical post-state. Verify is read-only and
// idempotent.
type PersonaAdapter interface {
	// Name is the short, stable identifier for this adapter:
	// "ssh", "cloud", "kube", "git". Used in Outcome reporting and
	// error wrapping.
	Name() string

	// Capture records the subsystem's current state into a Snapshot.
	// Capture MUST NOT mutate external state. A return of ErrNoAgent
	// (or another sentinel from this package) signals "subsystem
	// absent — skip me" and is not a transaction failure.
	Capture(ctx context.Context) (Snapshot, error)

	// Apply mutates the subsystem so that its state reflects the
	// given persona's relevant ExternalBinding. Apply MUST be a
	// single atomic step at the subsystem's natural granularity; if
	// the subsystem cannot offer atomicity (e.g., three separate
	// `git config` calls), Apply MUST roll back partial progress
	// before returning.
	Apply(ctx context.Context, p persona.Persona) error

	// Verify reads the subsystem post-Apply and confirms its state
	// is consistent with the requested persona. Verify MUST NOT
	// mutate state. A Verify failure is escalated to a transaction
	// failure (Rollback fires on all adapters whose Apply completed).
	Verify(ctx context.Context, p persona.Persona) error

	// Rollback restores the subsystem to the state recorded in snap.
	// Rollback is called only against adapters whose Apply previously
	// completed successfully. Rollback MUST be tolerant of partial
	// post-Apply state (the orchestrator may invoke it after a Verify
	// failure on the same adapter).
	Rollback(ctx context.Context, snap Snapshot) error
}

// Sentinel errors returned by adapter implementations. The
// orchestrator's "skip me, not fail" path keys off ErrNoSubsystem
// (and the subsystem-specific ErrNoAgent / ErrNoCLI variants which
// wrap it via errors.Is).
var (
	// ErrNoSubsystem is the umbrella sentinel meaning "this external
	// subsystem is absent on the host, so this adapter has no work
	// to do and the transaction should proceed without it." Subsystem-
	// specific sentinels below MUST wrap this via fmt.Errorf("%w: …",
	// ErrNoSubsystem, …) so errors.Is(err, ErrNoSubsystem) is the
	// portable check.
	ErrNoSubsystem = errors.New("adapter: external subsystem absent")

	// ErrNoAgent — SSH adapter could not contact an ssh-agent
	// ($SSH_AUTH_SOCK unset or the socket is unreachable).
	ErrNoAgent = errors.New("adapter/ssh: no ssh-agent reachable")

	// ErrNoCLI — Cloud or Git adapter required CLI is not on $PATH
	// (gcloud/aws/az for cloud; git for git). Includes the CLI name
	// in its message.
	ErrNoCLI = errors.New("adapter: required CLI not on PATH")

	// ErrNoBinding — adapter was invoked but the persona declares
	// no relevant ExternalBinding. The orchestrator should never
	// add an adapter to its Adapters slice unless the corresponding
	// binding is set; this sentinel exists for defence-in-depth.
	ErrNoBinding = errors.New("adapter: persona declares no binding for this subsystem")

	// ErrSchema — the persona's binding for this subsystem is
	// present but invalid (empty required field, unsupported scope,
	// etc). Adapters surface schema problems at Apply time so the
	// transaction halts before any mutation.
	ErrSchema = errors.New("adapter: invalid binding schema")
)

// Outcome records the per-adapter result of a Transaction.Execute call.
// One Outcome per Transaction. Embedded inline in the signed
// persona.use history event body (Thomas-approved decision in plan
// §Open questions #3).
type Outcome struct {
	// Applied lists adapter.Name()s whose Apply completed AND whose
	// Verify succeeded. On a fully-successful transaction, Applied
	// holds every adapter; on a failed transaction, Applied is empty
	// (every Applied adapter is rolled back before Execute returns).
	Applied []string

	// RolledBack lists adapter.Name()s whose Apply completed but were
	// then rolled back due to a downstream Apply or Verify failure.
	// RolledBack is empty on success.
	RolledBack []string

	// Skipped lists adapter.Name()s whose Capture returned
	// ErrNoSubsystem (subsystem absent on this host). Skipped
	// adapters are non-fatal — the transaction proceeds as if they
	// weren't declared.
	Skipped []string

	// Warnings collects non-fatal observations surfaced by adapters
	// during Capture / Apply / Verify / Rollback. The kube adapter's
	// sha256-mismatch warning lives here, for example. Warnings do
	// NOT cause transaction failure.
	Warnings []string

	// Cause is the original error that triggered Rollback (Apply or
	// Verify failure). nil on success.
	Cause error

	// RollbackErrors is the per-adapter rollback error list, in the
	// order they occurred (reverse adapter order). Empty on success
	// and on clean rollback. errors.Join'd into the error returned
	// by Execute when present.
	RollbackErrors []error
}
