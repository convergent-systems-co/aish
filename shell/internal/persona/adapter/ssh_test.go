package adapter

import "testing"

// T1 — SSH agent adapter tests.
//
// Per the seed commit's TDD posture (plan §Test strategy), these
// tests are named and skipped here so Phase B's T1 coder wave has a
// checklist of expected behaviour. The coder wave will:
//
//  1. Land ssh.go declaring NewSSHAdapter, an SSHBinding-driven
//     PersonaAdapter, and the unix-socket fake-agent test helper in
//     ssh_fakeagent_test.go (separate file, OWNS list per plan).
//  2. Replace each t.Skip below with the real assertion body.
//
// The seed defines the interface (PersonaAdapter) and the sentinel
// errors (ErrNoAgent, ErrSchema) those tests will exercise.

// TestSSHAdapter_ApplyAddsKey — fake agent receives Add; key bytes
// match the vault label. (Plan §T1 acceptance.)
func TestSSHAdapter_ApplyAddsKey(t *testing.T) {
	t.Skip("T1 pending: NewSSHAdapter not yet implemented")
}

// TestSSHAdapter_RollbackRestoresAgentState — apply, rollback,
// agent.Client.List() returns the exact pre-call set.
func TestSSHAdapter_RollbackRestoresAgentState(t *testing.T) {
	t.Skip("T1 pending: NewSSHAdapter not yet implemented")
}

// TestSSHAdapter_NoPrivateKeyInSnapshot — adversarial: the Snapshot
// bytes do NOT contain the private-key PEM. Cross-cutting invariant
// from the adapter package's doc.go threat model.
func TestSSHAdapter_NoPrivateKeyInSnapshot(t *testing.T) {
	t.Skip("T1 pending: NewSSHAdapter not yet implemented")
}

// TestSSHAdapter_CaptureNoAgentReturnsErrNoAgent — when
// $SSH_AUTH_SOCK is unset, Capture returns ErrNoAgent and the
// orchestrator's CaptureSkipPropagates path engages.
func TestSSHAdapter_CaptureNoAgentReturnsErrNoAgent(t *testing.T) {
	t.Skip("T1 pending: NewSSHAdapter not yet implemented")
}

// TestSSHAdapter_PrivateKeyBufferZeroedAfterAdd — adversarial: the
// buffer the adapter holds for the private-key bytes is zeroed via
// secrets.Zero() after the agent Add call returns.
func TestSSHAdapter_PrivateKeyBufferZeroedAfterAdd(t *testing.T) {
	t.Skip("T1 pending: NewSSHAdapter not yet implemented")
}
