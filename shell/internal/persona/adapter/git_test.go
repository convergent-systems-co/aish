package adapter

import "testing"

// T4 — Git config adapter tests.
//
// Strategy (Thomas-approved): shell-out to `git config --global …`.
// Scope locked to "global" for v0.3-3; "local" / "system" deferred
// to v0.3-fu.
//
// Each t.Skip below names the named test from plan §T4 acceptance.

// TestGitAdapter_ConfigSwap — sandbox $HOME; Apply with all three
// values; Rollback; asserted via `git config --get` calls reading
// the post-state.
func TestGitAdapter_ConfigSwap(t *testing.T) {
	t.Skip("T4 pending: NewGitAdapter not yet implemented")
}

// TestGitAdapter_UnsetIsRestoredAsUnset — sandbox starts with no
// user.signingkey set; Apply sets one; Rollback unsets cleanly via
// `git config --global --unset-all user.signingkey` (does not leave
// an empty string).
func TestGitAdapter_UnsetIsRestoredAsUnset(t *testing.T) {
	t.Skip("T4 pending: NewGitAdapter not yet implemented")
}

// TestGitAdapter_ScopeRefuseLocal — scope = "local" is rejected at
// schema validation time. Apply returns ErrSchema (wrapped) without
// invoking git.
func TestGitAdapter_ScopeRefuseLocal(t *testing.T) {
	t.Skip("T4 pending: NewGitAdapter not yet implemented")
}

// TestGitAdapter_EmptyUserNameRejectedAsSchema — per plan
// §Adversarial self-pass: an [external.git] block declaring
// user_name = "" is a schema error, not "silently set to empty."
func TestGitAdapter_EmptyUserNameRejectedAsSchema(t *testing.T) {
	t.Skip("T4 pending: NewGitAdapter not yet implemented")
}

// TestGitAdapter_GitNotOnPATHReturnsErrNoCLI — if `git` is not on
// $PATH at Capture time, the adapter returns a wrapped ErrNoCLI so
// the orchestrator skips it cleanly.
func TestGitAdapter_GitNotOnPATHReturnsErrNoCLI(t *testing.T) {
	t.Skip("T4 pending: NewGitAdapter not yet implemented")
}
