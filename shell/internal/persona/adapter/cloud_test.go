package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// T2 — Cloud profile adapter (gcloud / aws / azure) tests.
//
// Strategy per plan:
//   - gcloud: file-edit ~/.config/gcloud/active_config
//   - aws:    env-var snapshot of AWS_PROFILE (NOT file rewrite)
//   - azure:  shell-out to `az account set --subscription` against a
//             test-runtime stub recorded via recordingAzureRunner.

// personaWithCloud builds a persona with the given CloudBinding.
func personaWithCloud(b persona.CloudBinding) persona.Persona {
	p := persona.Persona{
		Name:         "cloud-test",
		Version:      persona.SchemaVersion,
		SystemPrompt: "test",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.Cloud = &b
	return p
}

// recordingAzureRunner records every Run invocation for inspection.
type recordingAzureRunner struct {
	available bool
	runErr    error
	calls     [][]string
}

func (r *recordingAzureRunner) Available(ctx context.Context) bool { return r.available }
func (r *recordingAzureRunner) Run(ctx context.Context, args ...string) error {
	dup := append([]string(nil), args...)
	r.calls = append(r.calls, dup)
	return r.runErr
}

// sandboxHome creates a fresh tempdir and returns its path. The
// caller treats it as the persona's $HOME for cloud-adapter purposes.
func sandboxHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// seedGcloudActive writes the given value into the sandbox's
// ~/.config/gcloud/active_config (mimicking a real gcloud install).
func seedGcloudActive(t *testing.T, home, value string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "gcloud")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "active_config")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readGcloudActive returns the trimmed file content (or "" if absent).
func readGcloudActive(t *testing.T, home string) (string, bool) {
	t.Helper()
	path := filepath.Join(home, ".config", "gcloud", "active_config")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(string(b)), true
}

// TestCloudAdapter_GcloudActiveConfigSwap — fixture HOME with
// active_config = "personal"; Apply with gcloud_config = "work";
// assert file content; Rollback; assert restored.
func TestCloudAdapter_GcloudActiveConfigSwap(t *testing.T) {
	t.Parallel()
	home := sandboxHome(t)
	seedGcloudActive(t, home, "personal")

	env := NewEnvSession()
	az := &recordingAzureRunner{available: true}
	ad := NewCloudAdapterWithDeps(fixedHome{Path: home}, env, az)

	ctx := context.Background()
	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithCloud(persona.CloudBinding{GcloudConfig: "work"})
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, ok := readGcloudActive(t, home)
	if !ok || got != "work" {
		t.Fatalf("post-apply active_config = %q exists=%v; want work", got, ok)
	}
	if err := ad.Verify(ctx, p); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got, ok = readGcloudActive(t, home)
	if !ok || got != "personal" {
		t.Fatalf("post-rollback active_config = %q exists=%v; want personal",
			got, ok)
	}
}

// TestCloudAdapter_AWSProfileSwap — env-var snapshot strategy: Apply
// sets AWS_PROFILE in the adapter's EnvSession; Rollback restores the
// prior value (or unsets if it was unset).
func TestCloudAdapter_AWSProfileSwap(t *testing.T) {
	t.Parallel()
	home := sandboxHome(t)
	env := NewEnvSession()
	// Pre-state: AWS_PROFILE was unset.
	az := &recordingAzureRunner{available: true}
	ad := NewCloudAdapterWithDeps(fixedHome{Path: home}, env, az)

	ctx := context.Background()
	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithCloud(persona.CloudBinding{AWSProfile: "work-sso"})
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	v, ok := env.Get("AWS_PROFILE")
	if !ok || v != "work-sso" {
		t.Fatalf("post-apply AWS_PROFILE = %q set=%v; want work-sso", v, ok)
	}
	if err := ad.Verify(ctx, p); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if v, ok := env.Get("AWS_PROFILE"); ok {
		t.Fatalf("post-rollback AWS_PROFILE = %q; want unset", v)
	}

	// Second cycle: pre-state has a value; Rollback must restore it.
	env2 := NewEnvSession()
	env2.values["AWS_PROFILE"] = "personal"
	ad2 := NewCloudAdapterWithDeps(fixedHome{Path: home}, env2, az)
	snap2, err := ad2.Capture(ctx)
	if err != nil {
		t.Fatalf("capture2: %v", err)
	}
	if err := ad2.Apply(ctx, p); err != nil {
		t.Fatalf("apply2: %v", err)
	}
	if err := ad2.Rollback(ctx, snap2); err != nil {
		t.Fatalf("rollback2: %v", err)
	}
	if v, _ := env2.Get("AWS_PROFILE"); v != "personal" {
		t.Fatalf("post-rollback2 AWS_PROFILE = %q; want personal", v)
	}
}

// TestCloudAdapter_AzureSubscriptionSwap — shell-out invocation of
// `az account set --subscription <id>` is exercised against a
// recording AzureRunner.
func TestCloudAdapter_AzureSubscriptionSwap(t *testing.T) {
	t.Parallel()
	home := sandboxHome(t)
	env := NewEnvSession()
	az := &recordingAzureRunner{available: true}
	ad := NewCloudAdapterWithDeps(fixedHome{Path: home}, env, az)

	ctx := context.Background()
	if _, err := ad.Capture(ctx); err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithCloud(persona.CloudBinding{AzureSubscription: "sub-uuid-1234"})
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(az.calls) != 1 {
		t.Fatalf("expected 1 az call, got %d", len(az.calls))
	}
	got := az.calls[0]
	want := []string{"account", "set", "--subscription", "sub-uuid-1234"}
	if !equalStrings(got, want) {
		t.Fatalf("az argv = %v; want %v", got, want)
	}
}

// TestCloudAdapter_MissingCLIConfigDirIsNoop — when the persona binds
// only AWS (no gcloud, no azure), no gcloud file is touched and no az
// call is made. The adapter cleanly Applies / Verifies / Rolls back
// on the single bound sub-adapter.
func TestCloudAdapter_MissingCLIConfigDirIsNoop(t *testing.T) {
	t.Parallel()
	home := sandboxHome(t)
	env := NewEnvSession()
	az := &recordingAzureRunner{available: false} // even if unavail, persona doesn't ask for az
	ad := NewCloudAdapterWithDeps(fixedHome{Path: home}, env, az)

	ctx := context.Background()
	if _, err := ad.Capture(ctx); err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithCloud(persona.CloudBinding{AWSProfile: "work"})
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := readGcloudActive(t, home); ok {
		t.Fatal("gcloud active_config was created when persona did not bind it")
	}
	if len(az.calls) != 0 {
		t.Fatalf("az was called when persona did not bind it; got %d", len(az.calls))
	}
}

// TestCloudAdapter_AzureMissingCLIErrors — if azure binding is
// declared but `az` is not on $PATH (Available reports false), Apply
// returns ErrNoCLI.
func TestCloudAdapter_AzureMissingCLIErrors(t *testing.T) {
	t.Parallel()
	home := sandboxHome(t)
	env := NewEnvSession()
	az := &recordingAzureRunner{available: false}
	ad := NewCloudAdapterWithDeps(fixedHome{Path: home}, env, az)

	ctx := context.Background()
	if _, err := ad.Capture(ctx); err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithCloud(persona.CloudBinding{AzureSubscription: "sub-1"})
	err := ad.Apply(ctx, p)
	if err == nil {
		t.Fatal("expected ErrNoCLI when az is missing")
	}
	if !errors.Is(err, ErrNoCLI) {
		t.Fatalf("expected ErrNoCLI; got %v", err)
	}
}
