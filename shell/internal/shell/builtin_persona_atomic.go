// Atomic persona switch — T5 of the v0.3-3 atomic-persona-switch
// plan. `aish persona use <name>` routes through personaSetAtomic
// which:
//
//  1. Acquires the process-local mutex (s.personaSwitchMu) so two
//     concurrent invocations are serialised.
//  2. Stat's the sentinel file ~/.aish/persona-transactions/.lock —
//     a recent sentinel surfaces a warning to stderr but does NOT
//     block (cross-process flock is out of scope for v0.3-3).
//  3. Builds the adapter slice from the persona's ExternalBindings.
//     A persona with no [external] block produces a zero-length
//     slice, which Transaction.Execute treats as a no-op (legacy
//     compatibility for pre-#104 personas).
//  4. Runs Transaction.Execute. On success: writes the active
//     persona via persona.WriteActivePersona, updates s.activePersona,
//     merges any EnvSession outputs into s.env (so child processes
//     inherit AWS_PROFILE), and records a signed persona.use event
//     with the full Outcome embedded inline.
//  5. On failure: surfaces the joined error (original cause +
//     rollback errors) and returns exit code 1. The active persona
//     is NOT updated — pre-#104 behavior persists.

package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/history"
	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/persona/adapter"
)

// personaSwitchMu serialises concurrent `persona use` invocations in
// the same process. The Shell value is a method receiver; the mutex
// lives in this package and is initialised lazily.
var personaSwitchMu sync.Mutex

// sentinelMaxAge is the threshold beyond which a sentinel file is
// considered stale and ignored. Recent sentinels surface a warning.
const sentinelMaxAge = 30 * time.Second

// personaTransactionsDir returns the path to ~/.aish/persona-transactions
// under the given home.
func personaTransactionsDir(home string) string {
	return filepath.Join(home, ".aish", "persona-transactions")
}

// personaSentinelPath returns the path to the persona-transactions
// sentinel lock file.
func personaSentinelPath(home string) string {
	return filepath.Join(personaTransactionsDir(home), ".lock")
}

// touchSentinel creates or updates the mtime of the sentinel file. A
// best-effort write — failure is logged via warning but does not
// halt the transaction.
func touchSentinel(home string) error {
	dir := personaTransactionsDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir sentinel dir: %w", err)
	}
	path := personaSentinelPath(home)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			f, cerr := os.Create(path)
			if cerr != nil {
				return cerr
			}
			_ = f.Close()
			return nil
		}
		return err
	}
	return nil
}

// recentSentinelWarning returns a non-empty warning string when the
// sentinel exists and was modified within sentinelMaxAge.
func recentSentinelWarning(home string) string {
	info, err := os.Stat(personaSentinelPath(home))
	if err != nil {
		return ""
	}
	age := time.Since(info.ModTime())
	if age >= 0 && age < sentinelMaxAge {
		return fmt.Sprintf("recent persona-switch sentinel found (%.0fs ago); proceeding anyway", age.Seconds())
	}
	return ""
}

// personaSetAtomic is the T5 entry-point routed through from
// personaSet for personas with one-or-more declared external
// bindings. For personas without external bindings the legacy path
// (persona.WriteActivePersona + recordPersonaUse) runs unchanged.
//
// The function holds personaSwitchMu for the duration of Execute so
// two concurrent `persona use` invocations are serialised.
func (s *Shell) personaSetAtomic(name string, stdout, stderr io.Writer) int {
	personaSwitchMu.Lock()
	defer personaSwitchMu.Unlock()

	if _, ok := s.personas.Get(name); !ok {
		fmt.Fprintf(stderr, "persona: unknown persona %q (try `persona list`)\n", name)
		return 1
	}
	p, _ := s.personas.Get(name)
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "persona: $HOME not set; cannot persist active persona")
		return 1
	}

	// Sentinel: warn (not block) when a recent switch is in flight
	// in another process.
	if warn := recentSentinelWarning(home); warn != "" {
		fmt.Fprintf(stderr, "persona: %s\n", warn)
	}
	_ = touchSentinel(home) // best-effort

	// Build the adapter slice. The session-scoped EnvSession is held
	// here so we can merge AWS_PROFILE post-Execute regardless of
	// whether the AWS sub-binding is the only cloud sub-adapter
	// active.
	envSession := s.personaCloudEnv()
	adapters := buildAdapters(p, envSession, s.atomicDeps)

	tx := &adapter.Transaction{Adapters: adapters}
	ctx := context.Background()
	outcome, err := tx.Execute(ctx, p)

	if err != nil {
		// Surface the joined error; cause + any rollback errors.
		fmt.Fprintf(stderr, "persona: %v\n", err)
		// Audit the FAILED outcome as well — partial-failure is a
		// material audit event.
		s.recordPersonaUseWithOutcome(name, outcome, err)
		return 1
	}

	// Success path: persist active persona, update Shell state,
	// merge env outputs, record audit event.
	if err := persona.WriteActivePersona(home, name); err != nil {
		// Mutation already happened — but persistence didn't. This
		// is a degraded state we surface via stderr while still
		// returning success because the running shell IS using the
		// new persona.
		fmt.Fprintf(stderr, "persona: warning: writing active persona to disk failed: %v\n", err)
	}
	s.activePersona = name

	// Merge env session outputs into Shell's env. The cloud adapter
	// stores AWS_PROFILE in the EnvSession; child processes inherit
	// it from the Shell's env after this.
	mergeEnvSession(s, envSession)

	fmt.Fprintf(stdout, "persona: active = %s\n", name)
	if len(outcome.Skipped) > 0 {
		fmt.Fprintf(stdout, "persona: skipped (subsystem absent): %v\n", outcome.Skipped)
	}
	for _, w := range outcome.Warnings {
		fmt.Fprintf(stderr, "persona: warning: %s\n", w)
	}
	s.recordPersonaUseWithOutcome(name, outcome, nil)
	return 0
}

// atomicDeps groups the dependency overrides personaSetAtomic uses
// to construct adapters. Production leaves all fields zero — each
// adapter constructor picks its own host-bound default. Tests
// inject deps via SetAtomicDepsForTesting.
type atomicDeps struct {
	sshKeys        adapter.KeySource
	sshDialer      adapter.AgentDialer
	cloudHome      adapter.HomeProvider
	cloudAzRunner  adapter.AzureRunner
	kubeHome       adapter.HomeProvider
	kubeConfigPath string
	gitRunner      adapter.GitRunner
}

// buildAdapters constructs the ordered adapter slice for a persona.
// Order is SSH → Cloud → Kube → Git, the canonical declaration order
// from plan §T5. Each sub-adapter is added only when the persona
// declares the corresponding binding — a persona with no [external]
// block produces a zero-length slice, which is the legacy-path
// no-op for Transaction.Execute.
func buildAdapters(p persona.Persona, env *adapter.EnvSession, deps atomicDeps) []adapter.PersonaAdapter {
	var out []adapter.PersonaAdapter
	if p.ExternalBindings.SSH != nil {
		out = append(out, adapter.NewSSHAdapter(deps.sshKeys, deps.sshDialer))
	}
	if p.ExternalBindings.Cloud != nil {
		var c *adapter.CloudAdapter
		if deps.cloudHome != nil || deps.cloudAzRunner != nil {
			c = adapter.NewCloudAdapterWithDeps(deps.cloudHome, env, deps.cloudAzRunner)
		} else {
			c = adapter.NewCloudAdapter(env)
		}
		out = append(out, c)
	}
	if p.ExternalBindings.Kube != nil {
		var k *adapter.KubeAdapter
		if deps.kubeHome != nil || deps.kubeConfigPath != "" {
			k = adapter.NewKubeAdapterWithDeps(deps.kubeHome, deps.kubeConfigPath)
		} else {
			k = adapter.NewKubeAdapter()
		}
		out = append(out, k)
	}
	if p.ExternalBindings.Git != nil {
		var g *adapter.GitAdapter
		if deps.gitRunner != nil {
			g = adapter.NewGitAdapterWithRunner(deps.gitRunner)
		} else {
			g = adapter.NewGitAdapter()
		}
		out = append(out, g)
	}
	return out
}

// personaCloudEnv returns the EnvSession the cloud adapter mutates.
// Pre-seeded with the Shell's current AWS_PROFILE so Capture can
// snapshot it.
func (s *Shell) personaCloudEnv() *adapter.EnvSession {
	es := adapter.NewEnvSession()
	if v, ok := s.env.Get("AWS_PROFILE"); ok {
		es.SetForTest("AWS_PROFILE", v)
	}
	return es
}

// mergeEnvSession writes the EnvSession's current values into the
// Shell's env so child processes inherit them.
//
// This runs only on successful Execute — the Shell's env reflects
// the post-Apply state of every adapter. AWS_PROFILE is the only
// env-var the cloud adapter exposes for v0.3-3; future bindings can
// extend this merge surface.
func mergeEnvSession(s *Shell, es *adapter.EnvSession) {
	if v, ok := es.Get("AWS_PROFILE"); ok {
		_ = s.env.Set("AWS_PROFILE", v)
	}
}

// recordPersonaUseWithOutcome writes a signed history event capturing
// the persona switch attempt, including the full Outcome. The
// Outcome is JSON-encoded inline in the event body (plan §T5 — ~2KB
// acceptable). On a failed transaction the failure cause is
// included via the err parameter.
func (s *Shell) recordPersonaUseWithOutcome(name string, outcome adapter.Outcome, txErr error) {
	if s.history == nil {
		return
	}
	store := s.history.Store()
	if store == nil {
		return
	}
	body := outcomeBody{
		Persona: name,
		Outcome: outcome,
	}
	if txErr != nil {
		body.Error = txErr.Error()
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		// Fall back to the original (name-only) event so the audit
		// trail isn't lost completely.
		s.recordPersonaUse(name)
		return
	}

	// Embed the encoded outcome into the Command field. The signed
	// history pipeline canonicalises Command verbatim, so the
	// outcome rides through unchanged and is recoverable by parsing
	// the JSON suffix.
	//
	// Format: "persona use <name>\x00<json-outcome>" — the NUL
	// separator is shell-illegal so it cannot collide with a real
	// command body. Readers split on the first NUL.
	cmd := "persona use " + name + "\x00" + string(encoded)
	ev := history.Event{
		ID:        history.NewEventID(),
		Timestamp: time.Now().UTC(),
		Kind:      history.Kind("persona.use"),
		Command:   cmd,
		Cwd:       s.cwd,
	}
	zero := 0
	if txErr != nil {
		one := 1
		ev.ExitCode = &one
	} else {
		ev.ExitCode = &zero
	}
	_ = store.Append(&ev)
}

// outcomeBody is the JSON-serialised body of the persona.use event.
type outcomeBody struct {
	Persona string          `json:"persona"`
	Outcome adapter.Outcome `json:"outcome"`
	Error   string          `json:"error,omitempty"`
}

// SetAtomicDepsForTesting injects adapter dependencies. Production
// code calls personaSetAtomic with deps zero-value; tests use this
// to wire fake-agent dialers, sandbox HOMEs, recording AzureRunners,
// stub GitRunners.
func (s *Shell) SetAtomicDepsForTesting(
	sshKeys adapter.KeySource,
	sshDialer adapter.AgentDialer,
	cloudHome adapter.HomeProvider,
	cloudAzRunner adapter.AzureRunner,
	kubeHome adapter.HomeProvider,
	kubeConfigPath string,
	gitRunner adapter.GitRunner,
) {
	s.atomicDeps = atomicDeps{
		sshKeys:        sshKeys,
		sshDialer:      sshDialer,
		cloudHome:      cloudHome,
		cloudAzRunner:  cloudAzRunner,
		kubeHome:       kubeHome,
		kubeConfigPath: kubeConfigPath,
		gitRunner:      gitRunner,
	}
}
