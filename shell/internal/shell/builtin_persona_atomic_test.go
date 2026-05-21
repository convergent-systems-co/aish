package shell

import "testing"

// T5 — Atomic persona switch integration tests.
//
// These tests wire REAL adapter implementations (from T1..T4) against
// fake external state under one sandbox $HOME. Per plan §Test
// strategy: NOT mocks substituted for the adapter interface — real
// adapter code, fake external state.
//
// Each t.Skip below names the named test from plan §T5 acceptance.
// Phase B's T5 coder wave fills them in once T1..T4 have landed.

// TestAtomicPersonaSwitch_AllFourSucceed — fixture persona with all
// four bindings; sandbox HOME; assert each external state mutated
// (ssh agent has the key, ~/.config/gcloud/active_config = work,
// AWS_PROFILE env set, kubeconfig current-context updated, gitconfig
// user.{name,email,signingkey} set).
func TestAtomicPersonaSwitch_AllFourSucceed(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}

// TestAtomicPersonaSwitch_GitFailureRollsBackAll — inject a failure
// into the Git adapter's Apply (e.g., `git` binary stub that exits
// non-zero); assert SSH/Cloud/Kube state is restored to pre-call
// snapshot; assert the persona is NOT activated
// (persona.ReadActivePersona unchanged); assert exit code non-zero.
func TestAtomicPersonaSwitch_GitFailureRollsBackAll(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}

// TestAtomicPersonaSwitch_NoBindingsPreservesLegacyBehaviour — the
// default.toml persona (no [external] block); assert behaviour
// bit-identical to pre-#104 (no external state touched, persona
// activated via persona.WriteActivePersona, signed persona.use event
// written). This is the backward-compat acceptance gate.
func TestAtomicPersonaSwitch_NoBindingsPreservesLegacyBehaviour(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}

// TestAtomicPersonaSwitch_HistoryEventBodyHasNoSecret — adversarial:
// Apply with a persona whose SSH binding pulls a vault-stored
// private key; read the signed persona.use event back from the
// history store; assert !bytes.Contains(event.body, privateKeyBytes).
// Taint redaction asserted at the audit boundary.
func TestAtomicPersonaSwitch_HistoryEventBodyHasNoSecret(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}

// TestAtomicPersonaSwitch_OutcomeEmbeddedInSignedEvent — per
// Thomas-approved decision (plan §Open questions #3 / brief #4), the
// full Outcome is embedded inline in the persona.use event body
// (Applied / RolledBack / Skipped lists; Cause message). Verify the
// signed event round-trips and the Outcome fields are present.
func TestAtomicPersonaSwitch_OutcomeEmbeddedInSignedEvent(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}

// TestAtomicPersonaSwitch_ConcurrentInvocationsSerialised — per
// Thomas-approved decision (plan §Open questions #5 / brief #6),
// two concurrent `persona use` invocations in the same process are
// serialised by a process-local mutex; a recent sentinel file under
// ~/.aish/persona-transactions/.lock surfaces a warning in the
// Outcome but does NOT block.
func TestAtomicPersonaSwitch_ConcurrentInvocationsSerialised(t *testing.T) {
	t.Skip("T5 pending: personaSetAtomic not yet implemented")
}
