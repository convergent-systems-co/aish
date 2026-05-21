package adapter

import "testing"

// T3 — Kube context adapter tests.
//
// Strategy (Thomas-approved): k8s.io/client-go/tools/clientcmd
// against $KUBECONFIG / ~/.kube/config. If transitive binary swell
// crosses ~50MB on darwin, T3 coder wave may fall back to direct
// YAML edit via gopkg.in/yaml.v3 — document the swing in the
// T3 commit message.
//
// Each t.Skip below names the named test from plan §T3 acceptance.

// TestKubeAdapter_ContextSwap — fixture kubeconfig with two
// contexts; Apply; Rollback; both verified via re-reading
// current-context.
func TestKubeAdapter_ContextSwap(t *testing.T) {
	t.Skip("T3 pending: NewKubeAdapter not yet implemented")
}

// TestKubeAdapter_UnknownContextErrors — persona binds a context
// that does not exist in the kubeconfig; Apply returns ErrSchema;
// orchestrator-level rollback path is exercised (no Apply completed,
// so no rollback fires — but the Cause is ErrSchema-wrapped).
func TestKubeAdapter_UnknownContextErrors(t *testing.T) {
	t.Skip("T3 pending: NewKubeAdapter not yet implemented")
}

// TestKubeAdapter_ConcurrentMutationWarns — between Capture and
// Rollback, mutate the kubeconfig externally (different
// current-context, different cluster); assert Rollback succeeds AND
// the Outcome.Warnings slice carries a sha-mismatch entry.
func TestKubeAdapter_ConcurrentMutationWarns(t *testing.T) {
	t.Skip("T3 pending: NewKubeAdapter not yet implemented")
}

// TestKubeAdapter_NoKubeconfigReturnsErrNoSubsystem — when neither
// $KUBECONFIG nor ~/.kube/config exists, Capture returns a wrapped
// ErrNoSubsystem so the orchestrator skips cleanly.
func TestKubeAdapter_NoKubeconfigReturnsErrNoSubsystem(t *testing.T) {
	t.Skip("T3 pending: NewKubeAdapter not yet implemented")
}
