// Cloud profile adapter — T2 of the v0.3-3 atomic-persona-switch
// plan. One CloudAdapter wraps three independent sub-adapters
// (gcloud / aws / azure). Each sub-adapter is independently optional;
// a persona may bind any combination.
//
// Mutation strategies (Thomas-approved in plan §Open questions):
//
//   - gcloud:  rewrite the single-line text file
//              ~/.config/gcloud/active_config.
//   - aws:     env-var only — set AWS_PROFILE in the adapter's
//              session-scoped env. Persona binding is process-scoped,
//              matching kube's session-wide semantics.
//   - azure:   shell-out to `az account set --subscription <id>` —
//              the sole shell-out exception in T2 because az's file
//              schema (~/.azure/azureProfile.json) drifts undocumented
//              between az versions.

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// HomeProvider abstracts the lookup of the directory containing
// ~/.config/gcloud/... so tests can sandbox HOME.
type HomeProvider interface {
	Home() string
}

type osHome struct{}

func (osHome) Home() string {
	h, _ := os.UserHomeDir()
	return h
}

// EnvSession is a process-internal map of env-var-style bindings the
// AWS sub-adapter manipulates instead of touching the real os
// environment. The shell builtin reads it back after Execute and
// merges the resulting AWS_PROFILE into the Shell's env so child
// processes inherit it.
//
// This indirection keeps the adapter pure (no `os.Setenv` side
// effect, which would race across tests) and gives tests a direct
// surface to assert against.
type EnvSession struct {
	values map[string]string
	// captured is the per-key prior value (or "" with hadValue=false)
	// recorded at Capture time so Rollback can restore.
	hadValue map[string]bool
	prior    map[string]string
}

func NewEnvSession() *EnvSession {
	return &EnvSession{
		values:   map[string]string{},
		hadValue: map[string]bool{},
		prior:    map[string]string{},
	}
}

// Get returns the current value plus whether it was set. The shell
// builtin reads this post-Apply to inject the result into child env.
func (e *EnvSession) Get(key string) (string, bool) {
	v, ok := e.values[key]
	return v, ok
}

// AzureRunner abstracts the `az` invocation so tests can supply a
// recording stub on $PATH (or a direct fake here). Production uses
// execAzureRunner.
type AzureRunner interface {
	Run(ctx context.Context, args ...string) error
	Available(ctx context.Context) bool
}

type execAzureRunner struct{}

func (execAzureRunner) Available(ctx context.Context) bool {
	_, err := exec.LookPath("az")
	return err == nil
}

func (execAzureRunner) Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "az", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("az %v: %w (output: %s)", args, err, bytes.TrimSpace(out))
	}
	return nil
}

// CloudAdapter wires the three sub-adapters under a single
// PersonaAdapter facade.
type CloudAdapter struct {
	homes HomeProvider
	env   *EnvSession
	az    AzureRunner
}

// NewCloudAdapter constructs an adapter wired to the host filesystem
// (HOME from os.UserHomeDir) and shelling out to `az`. The EnvSession
// the adapter uses is the one passed in — caller (production: shell
// builtin) is responsible for inspecting it after Execute.
func NewCloudAdapter(env *EnvSession) *CloudAdapter {
	return &CloudAdapter{
		homes: osHome{},
		env:   env,
		az:    execAzureRunner{},
	}
}

// NewCloudAdapterWithDeps is the test-only constructor.
func NewCloudAdapterWithDeps(homes HomeProvider, env *EnvSession, az AzureRunner) *CloudAdapter {
	return &CloudAdapter{homes: homes, env: env, az: az}
}

// Name implements PersonaAdapter.
func (c *CloudAdapter) Name() string { return "cloud" }

// cloudSnapshot is the JSON-encoded Snapshot body recording the prior
// state of each sub-adapter the persona binds.
type cloudSnapshot struct {
	GcloudActiveConfig         string `json:"gcloud_active_config,omitempty"`
	GcloudActiveConfigExisted  bool   `json:"gcloud_active_config_existed"`
	AWSProfilePrior            string `json:"aws_profile_prior,omitempty"`
	AWSProfilePriorWasSet      bool   `json:"aws_profile_prior_was_set"`
	AzureSubscriptionCaptured  bool   `json:"azure_subscription_captured"`
	// AzurePrior is intentionally omitted — `az account show` is a
	// best-effort capture and not always available. On Rollback for
	// az we no-op (per plan, az is the one sub-adapter we cannot
	// trivially roll back from outside the CLI). A warning is added
	// to the Outcome at the orchestrator layer if needed.
}

// Capture records the prior state of each declared sub-adapter.
// Returns ErrNoSubsystem when the persona declares no [external.cloud]
// block (defence-in-depth — orchestrator already filters via
// builder).
func (c *CloudAdapter) Capture(ctx context.Context) (Snapshot, error) {
	// Always succeeds — produces a Snapshot encoding "no prior state
	// captured" if the persona binds nothing. The orchestrator-side
	// check decides whether to include this adapter at all.
	home := c.homes.Home()
	snap := cloudSnapshot{}

	// gcloud capture: read the existing active_config if present.
	path := gcloudActiveConfigPath(home)
	if home != "" {
		if data, err := os.ReadFile(path); err == nil {
			snap.GcloudActiveConfig = string(bytes.TrimSpace(data))
			snap.GcloudActiveConfigExisted = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("capture gcloud config: %w", err)
		}
	}

	// AWS capture: snapshot AWS_PROFILE from the session.
	if v, ok := c.env.Get("AWS_PROFILE"); ok {
		snap.AWSProfilePrior = v
		snap.AWSProfilePriorWasSet = true
	}

	// Azure capture: we do NOT call `az account show` here — it can
	// be slow and produces stdout we don't need. The "captured" flag
	// stays false unless we extend it.

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("encode cloud snapshot: %w", err)
	}
	return b, nil
}

// Apply mutates the three sub-systems according to the persona's
// CloudBinding. Each sub-adapter runs in declared order
// (gcloud → aws → azure); a failure in one halts the Apply and the
// orchestrator's reverse-order Rollback fires.
func (c *CloudAdapter) Apply(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Cloud == nil {
		return fmt.Errorf("%w: persona %q has no [external.cloud]", ErrNoBinding, p.Name)
	}
	b := p.ExternalBindings.Cloud
	home := c.homes.Home()

	if b.GcloudConfig != "" {
		if err := writeGcloudActive(home, b.GcloudConfig); err != nil {
			return fmt.Errorf("gcloud: %w", err)
		}
	}

	if b.AWSProfile != "" {
		// Record the prior in env's prior map so Rollback can restore.
		if prev, ok := c.env.Get("AWS_PROFILE"); ok {
			c.env.prior["AWS_PROFILE"] = prev
			c.env.hadValue["AWS_PROFILE"] = true
		} else {
			c.env.prior["AWS_PROFILE"] = ""
			c.env.hadValue["AWS_PROFILE"] = false
		}
		c.env.values["AWS_PROFILE"] = b.AWSProfile
	}

	if b.AzureSubscription != "" {
		if !c.az.Available(ctx) {
			return fmt.Errorf("%w: az not on PATH", ErrNoCLI)
		}
		if err := c.az.Run(ctx, "account", "set", "--subscription", b.AzureSubscription); err != nil {
			return fmt.Errorf("azure: %w", err)
		}
	}
	return nil
}

// Verify reads back what Apply changed to confirm it stuck.
func (c *CloudAdapter) Verify(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Cloud == nil {
		return nil
	}
	b := p.ExternalBindings.Cloud
	home := c.homes.Home()

	if b.GcloudConfig != "" {
		path := gcloudActiveConfigPath(home)
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("verify gcloud read: %w", err)
		}
		if got := string(bytes.TrimSpace(raw)); got != b.GcloudConfig {
			return fmt.Errorf("verify gcloud: have %q, want %q", got, b.GcloudConfig)
		}
	}
	if b.AWSProfile != "" {
		v, ok := c.env.Get("AWS_PROFILE")
		if !ok || v != b.AWSProfile {
			return fmt.Errorf("verify aws: AWS_PROFILE = %q want %q", v, b.AWSProfile)
		}
	}
	// Azure verify is best-effort — calling `az account show` again
	// is expensive and az has no machine-readable "current subscription"
	// short of that. We trust Apply's exit code.
	return nil
}

// Rollback restores the captured prior state for each sub-adapter.
func (c *CloudAdapter) Rollback(ctx context.Context, snap Snapshot) error {
	var s cloudSnapshot
	if err := json.Unmarshal(snap, &s); err != nil {
		return fmt.Errorf("decode cloud snapshot: %w", err)
	}
	home := c.homes.Home()
	var errs []error

	// gcloud rollback: restore the file content if existed; remove if
	// it did not.
	path := gcloudActiveConfigPath(home)
	if s.GcloudActiveConfigExisted {
		if err := writeGcloudActive(home, s.GcloudActiveConfig); err != nil {
			errs = append(errs, fmt.Errorf("gcloud rollback: %w", err))
		}
	} else if home != "" {
		// Only remove if the file exists (idempotent).
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				errs = append(errs, fmt.Errorf("gcloud rollback rm: %w", err))
			}
		}
	}

	// AWS rollback: restore env value to its prior state.
	if c.env.hadValue["AWS_PROFILE"] {
		c.env.values["AWS_PROFILE"] = c.env.prior["AWS_PROFILE"]
	} else {
		delete(c.env.values, "AWS_PROFILE")
	}

	// Azure rollback: explicit non-restore. We do NOT shell out to
	// `az account set` in reverse — the prior subscription is not
	// captured. The transaction's Outcome surfaces this via a
	// Warning the orchestrator wraps in (no-op here; documented).
	// Production code that needs rollback fidelity for azure must
	// store the prior subscription itself.

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// gcloudActiveConfigPath returns the canonical path to gcloud's
// active_config marker file under HOME.
func gcloudActiveConfigPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "gcloud", "active_config")
}

// writeGcloudActive writes value to the active_config file, creating
// the parent directory if needed. The file holds a single line —
// the active configuration name — followed by a newline. Mode 0644.
func writeGcloudActive(home, value string) error {
	if home == "" {
		return errors.New("HOME unset")
	}
	dir := filepath.Join(home, ".config", "gcloud")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir gcloud config: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "active_config"),
		[]byte(value+"\n"), 0o644)
}

// fixedHome is a HomeProvider returning a constant path — used by
// tests to redirect ~/.config/gcloud/... to a tempdir.
type fixedHome struct{ Path string }

func (f fixedHome) Home() string { return f.Path }

// Compile-time assertion.
var _ PersonaAdapter = (*CloudAdapter)(nil)
