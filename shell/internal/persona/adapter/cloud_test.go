package adapter

import "testing"

// T2 — Cloud profile adapter (gcloud / aws / azure) tests.
//
// Per Thomas's approved leans in plan §Open questions:
//   - gcloud: file-edit ~/.config/gcloud/active_config
//   - aws:    env-var snapshot of AWS_PROFILE (NOT file rewrite)
//   - azure:  shell-out to `az account set --subscription`
//             (sole sub-adapter that shells out from Cloud)
//
// Each t.Skip below names the named test from plan §T2 acceptance.
// Phase B's T2 coder wave fills them in.

// TestCloudAdapter_GcloudActiveConfigSwap — fixture HOME with
// active_config = "personal"; Apply with gcloud_config = "work";
// assert file content; Rollback; assert restored.
func TestCloudAdapter_GcloudActiveConfigSwap(t *testing.T) {
	t.Skip("T2 pending: NewCloudAdapter not yet implemented")
}

// TestCloudAdapter_AWSProfileSwap — env-var snapshot strategy:
// Apply sets AWS_PROFILE in the adapter's session-scoped env;
// Rollback restores the prior value (or unsets if it was unset).
func TestCloudAdapter_AWSProfileSwap(t *testing.T) {
	t.Skip("T2 pending: NewCloudAdapter not yet implemented")
}

// TestCloudAdapter_AzureSubscriptionSwap — shell-out invocation of
// `az account set --subscription <id>` is exercised against a fake
// `az` on $PATH (testdata script that records its argv).
func TestCloudAdapter_AzureSubscriptionSwap(t *testing.T) {
	t.Skip("T2 pending: NewCloudAdapter not yet implemented")
}

// TestCloudAdapter_MissingCLIConfigDirIsNoop — if a sub-CLI's config
// dir is absent AND the persona doesn't bind it, the adapter skips
// cleanly (Capture returns ErrNoSubsystem for that sub-CLI only).
func TestCloudAdapter_MissingCLIConfigDirIsNoop(t *testing.T) {
	t.Skip("T2 pending: NewCloudAdapter not yet implemented")
}

// TestCloudAdapter_AzureMissingCLIErrors — if azure binding is
// declared but `az` is not on $PATH, Apply returns ErrNoCLI and the
// orchestrator surfaces it.
func TestCloudAdapter_AzureMissingCLIErrors(t *testing.T) {
	t.Skip("T2 pending: NewCloudAdapter not yet implemented")
}
