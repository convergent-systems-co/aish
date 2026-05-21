// Package adapter defines the contract every external-identity
// subsystem implements so `aish persona use <name>` can atomically
// swap multiple OS-level identities (SSH agent, cloud CLI profiles,
// kubectl context, git config) or roll all of them back on partial
// failure.
//
// # Design overview
//
// Each external subsystem ships a PersonaAdapter. The Transaction
// orchestrator (orchestrator.go) drives the four-phase state machine:
//
//  1. Capture — every adapter records its current external state into
//     an opaque Snapshot. No mutation occurs in this phase.
//  2. Apply   — each adapter mutates its subsystem in declared order.
//     The first adapter whose Apply errors halts forward progress.
//  3. Verify  — every adapter that Applied is asked to confirm the
//     post-state is consistent with the requested persona. A Verify
//     failure is treated the same as an Apply failure: rollback.
//  4. Rollback — on any Apply or Verify failure, every adapter whose
//     Apply completed has Rollback invoked in REVERSE order against
//     its captured Snapshot. The original failure is wrapped with any
//     rollback errors via errors.Join and returned to the caller.
//
// # Threat model
//
// The orchestrator's atomicity guarantee is bounded by the OS surface
// it cannot lock against. Specifically:
//
//   - TOCTOU between Capture and Apply is NOT in scope. A motivated
//     local adversary with write access to the same files / sockets
//     can mutate external state between phases. The user's own
//     persona switch is the threat model; defending against a
//     concurrent local attacker is not. (See plan §Risk assessment
//     for the full reasoning.) The Kube adapter records a sha256 of
//     the merged config at Capture time and surfaces a non-fatal
//     warning in the Outcome if the digest changed by Rollback time;
//     other adapters' surfaces are too narrow to warrant the same.
//
//   - Snapshots may be persisted briefly in memory but MUST NOT carry
//     secret-bearing bytes. Each adapter's unit test asserts the
//     adversarial property `!bytes.Contains(snapshot, privateKey)`.
//     This is enforced at the adapter level, not the orchestrator's.
//
//   - The persona.use signed history event embeds the Outcome inline
//     (Thomas-approved decision in plan §Open questions). The event
//     body MUST NOT contain Snapshot bytes — only the per-adapter
//     name, applied/rolled-back state, and any error message. Taint
//     redaction applies at the audit boundary (see
//     shell/internal/secrets/taint.go).
//
// # Concurrency
//
// Two concurrent `persona use` invocations in the same process are
// serialised by a process-local mutex held by the shell builtin (see
// builtin_persona_atomic.go in the shell package). A best-effort
// sentinel file under ~/.aish/persona-transactions/.lock is stat'd
// before Apply; a recent sentinel surfaces a warning in the Outcome
// but does NOT block. Cross-process flock is out of scope for v0.3-3.
//
// See .artifacts/plans/104.md for the full design.
package adapter
