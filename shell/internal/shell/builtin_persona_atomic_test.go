package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/persona/adapter"
)

// T5 — Atomic persona switch integration tests. The tests wire REAL
// adapter implementations from T1..T4 against fake external state
// under a single sandbox $HOME. Synthetic doubles appear only at
// boundaries the production code itself abstracts (the SSH
// AgentDialer, the Cloud AzureRunner) — not at the
// orchestrator/adapter interface.

// stagePersona writes a TOML file under ~/.aish/personas/<name>.toml
// so the Shell's loader picks it up on the next New() call.
func stagePersona(t *testing.T, home, name, toml string) {
	t.Helper()
	dir := filepath.Join(home, ".aish", "personas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// newShellWithStagedPersona constructs a tempdir HOME, stages the
// persona TOML AT that HOME before creating the Shell, then returns
// the resulting Shell + HOME path. Required because the persona
// loader runs at New() — staging after construction would leave the
// loader's byName map empty for the new persona.
func newShellWithStagedPersona(t *testing.T, name, toml string) (*Shell, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	stagePersona(t, tmp, name, toml)
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	return s, tmp
}

// fakeAgent — minimal unix-socket SSH agent for T5. Mirrors the
// adapter-package helper but lives in shell-package test code to
// avoid a circular dep. We start one and dial it via the
// SSHAdapter's KeySource + AgentDialer.
type fakeAgent struct {
	t        *testing.T
	socket   string
	keyring  interface{}
	listener net.Listener
}

// recordingAz records argv invocations.
type recordingAz struct{ calls [][]string }

func (r *recordingAz) Available(ctx context.Context) bool { return true }
func (r *recordingAz) Run(ctx context.Context, args ...string) error {
	dup := append([]string(nil), args...)
	r.calls = append(r.calls, dup)
	return nil
}

// failingAz simulates `az` returning non-zero — used to inject Apply
// failure for the rollback test.
type failingAz struct{}

func (failingAz) Available(ctx context.Context) bool { return true }
func (failingAz) Run(ctx context.Context, args ...string) error {
	return errors.New("simulated az failure")
}

// stubKeySource returns a fixed PEM for any label requested.
type stubKeySource struct{ pem []byte }

func (s *stubKeySource) GetPrivateKey(label string) ([]byte, error) {
	out := make([]byte, len(s.pem))
	copy(out, s.pem)
	return out, nil
}

// stubAgentDialer dials a pre-existing unix socket.
type stubAgentDialer struct{ sock string }

func (s stubAgentDialer) Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", s.sock)
}

// allFourTOML returns the TOML body for a persona declaring all four
// external bindings. Test helper — paired with newShellWithStagedPersona.
func allFourTOML(gcloudCfg, awsProf, azureSub, kubeCtx, gitName, gitEmail string) string {
	return `name = "all-four"
version = 1
system_prompt = "test"

[tone]
verbosity = "medium"
formality = "neutral"

[external.ssh]
key_label = "test-ssh-key"
add_lifetime_seconds = 0

[external.cloud]
gcloud_config = "` + gcloudCfg + `"
aws_profile = "` + awsProf + `"
azure_subscription = "` + azureSub + `"

[external.kube]
context = "` + kubeCtx + `"

[external.git]
scope = "global"
user_name = "` + gitName + `"
user_email = "` + gitEmail + `"
signing_key = ""
`
}

// stagePersonaCloudOnly writes a persona binding only the cloud
// sub-adapter (azure-shell-out), used to drive partial-failure
// rollback tests.
func stagePersonaCloudOnly(t *testing.T, home, gcloudCfg string) string {
	t.Helper()
	toml := `name = "cloud-only"
version = 1
system_prompt = "test"

[tone]
verbosity = "medium"
formality = "neutral"

[external.cloud]
gcloud_config = "` + gcloudCfg + `"
`
	stagePersona(t, home, "cloud-only", toml)
	return "cloud-only"
}

// startFakeAgent runs a unix-socket SSH agent serving an in-memory
// keyring on a tempdir. Each call registers a fresh keyring keyed
// off the listener's socket path so cross-test residue cannot leak.
func startFakeAgent(t *testing.T) (sock string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ashsh")
	if err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock = filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	registerAgentKeyring(sock)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go serveSSHAgent(conn)
		}
	}()
	return sock
}

// genTestEd25519PEM returns a fresh ed25519 OpenSSH PEM key. Shared
// across T5 tests.
func genTestEd25519PEM(t *testing.T) []byte {
	t.Helper()
	return generateEd25519PEM(t)
}

// stageKubeconfig writes a two-context kubeconfig to <home>/.kube/config.
func stageKubeconfig(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".kube")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir kube: %v", err)
	}
	path := filepath.Join(dir, "config")
	body := []byte(`apiVersion: v1
kind: Config
current-context: personal-cluster
clusters:
- name: personal-cluster
  cluster:
    server: https://personal.k8s.test
    insecure-skip-tls-verify: true
- name: work-cluster
  cluster:
    server: https://work.k8s.test
    insecure-skip-tls-verify: true
contexts:
- name: personal-cluster
  context: {cluster: personal-cluster, user: personal-user}
- name: work-cluster
  context: {cluster: work-cluster, user: work-user}
users:
- name: personal-user
  user: {token: tok-personal}
- name: work-user
  user: {token: tok-work}
preferences: {}
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

// readKubeCurrentContext greps a kubeconfig for current-context.
func readKubeCurrentContext(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "current-context:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "current-context:"))
		}
	}
	return ""
}

// readGitGlobal reads a key from the sandbox HOME's .gitconfig.
func readGitGlobal(t *testing.T, home, key string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", "config", "--global", "--get", key)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	out, err := cmd.Output()
	if err != nil {
		if ex, ok := err.(*exec.ExitError); ok && ex.ExitCode() == 1 {
			return "", false
		}
		t.Fatalf("git config: %v", err)
	}
	return strings.TrimRight(string(out), "\n"), true
}

// readGcloudActive reads the gcloud active_config marker.
func readGcloudActive(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".config", "gcloud", "active_config"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		t.Fatalf("read gcloud: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// requireGitCLI skips the test if git is missing.
func requireGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — skipping (CI is responsible for installing git per plan §11)")
	}
}

// ---------- the six T5 tests ----------

// TestAtomicPersonaSwitch_AllFourSucceed — all four bindings fire,
// every external state is mutated, persona is activated.
func TestAtomicPersonaSwitch_AllFourSucceed(t *testing.T) {
	requireGitCLI(t)
	tomlBody := allFourTOML("work", "work-sso", "sub-uuid-1234",
		"work-cluster", "Work Identity", "work@example.test")
	s, home := newShellWithStagedPersona(t, "all-four", tomlBody)
	pemBytes := genTestEd25519PEM(t)
	sock := startFakeAgent(t)
	stageKubeconfig(t, home)
	// Seed prior gitconfig so capture/rollback have something to
	// restore.
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Personal\n\temail = personal@example.test\n"), 0o644)

	azStub := &recordingAz{}
	keys := &stubKeySource{pem: pemBytes}
	dialer := stubAgentDialer{sock: sock}

	s.SetAtomicDepsForTesting(
		keys, dialer,
		fixedHomeShim{Path: home}, azStub,
		fixedHomeShim{Path: home}, "",
		newHomeBoundGitRunner(home),
	)

	var outBuf, errBuf bytes.Buffer
	code := s.personaSet([]string{"all-four"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("persona set all-four: exit=%d stderr=%s", code, errBuf.String())
	}

	// Assert every external state mutated.
	if got := readGcloudActive(t, home); got != "work" {
		t.Errorf("gcloud active_config = %q; want work", got)
	}
	if got, _ := s.env.Get("AWS_PROFILE"); got != "work-sso" {
		t.Errorf("AWS_PROFILE = %q; want work-sso", got)
	}
	if len(azStub.calls) != 1 || azStub.calls[0][2] != "--subscription" {
		t.Errorf("az was not called as expected: %v", azStub.calls)
	}
	if got := readKubeCurrentContext(t, filepath.Join(home, ".kube", "config")); got != "work-cluster" {
		t.Errorf("kubectl current-context = %q; want work-cluster", got)
	}
	if got, _ := readGitGlobal(t, home, "user.name"); got != "Work Identity" {
		t.Errorf("git user.name = %q; want Work Identity", got)
	}
	if got, _ := readGitGlobal(t, home, "user.email"); got != "work@example.test" {
		t.Errorf("git user.email = %q; want work@example.test", got)
	}
	if s.activePersona != "all-four" {
		t.Errorf("activePersona = %q; want all-four", s.activePersona)
	}
	if name := persona.ReadActivePersona(home); name != "all-four" {
		t.Errorf("ReadActivePersona = %q; want all-four", name)
	}
}

// TestAtomicPersonaSwitch_GitFailureRollsBackAll — Git Apply fails,
// every prior adapter (ssh, cloud, kube) is rolled back; activePersona
// remains unchanged; exit code non-zero.
func TestAtomicPersonaSwitch_GitFailureRollsBackAll(t *testing.T) {
	requireGitCLI(t)
	// user_name = "" → GitAdapter.Apply returns ErrSchema.
	tomlBody := allFourTOML("work", "work-sso", "sub-uuid",
		"work-cluster", "", "work@example.test")
	s, home := newShellWithStagedPersona(t, "all-four", tomlBody)
	pemBytes := genTestEd25519PEM(t)
	sock := startFakeAgent(t)
	kubePath := stageKubeconfig(t, home)
	// Pre-state seeded — capture should record these.
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Personal\n\temail = personal@example.test\n"), 0o644)
	// Pre-state for gcloud.
	gcdir := filepath.Join(home, ".config", "gcloud")
	_ = os.MkdirAll(gcdir, 0o755)
	_ = os.WriteFile(filepath.Join(gcdir, "active_config"), []byte("personal\n"), 0o644)

	azStub := &recordingAz{}
	keys := &stubKeySource{pem: pemBytes}
	dialer := stubAgentDialer{sock: sock}
	s.SetAtomicDepsForTesting(
		keys, dialer,
		fixedHomeShim{Path: home}, azStub,
		fixedHomeShim{Path: home}, "",
		newHomeBoundGitRunner(home),
	)

	var outBuf, errBuf bytes.Buffer
	code := s.personaSet([]string{"all-four"}, &outBuf, &errBuf)
	if code == 0 {
		t.Fatalf("expected non-zero exit on git Apply failure; got 0, stdout=%s", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "user_name") {
		t.Errorf("stderr should mention the schema failure: %s", errBuf.String())
	}

	// Rollback assertions.
	if got := readGcloudActive(t, home); got != "personal" {
		t.Errorf("gcloud not rolled back: %q want personal", got)
	}
	if got := readKubeCurrentContext(t, kubePath); got != "personal-cluster" {
		t.Errorf("kube not rolled back: %q want personal-cluster", got)
	}
	if s.activePersona == "all-four" {
		t.Errorf("activePersona was switched despite failure")
	}
}

// TestAtomicPersonaSwitch_NoBindingsPreservesLegacyBehaviour — a
// persona without [external] takes the legacy path bit-identically.
func TestAtomicPersonaSwitch_NoBindingsPreservesLegacyBehaviour(t *testing.T) {
	s, home := newTestShellForPersona(t)
	// "default" is bundled and has no external bindings.
	var outBuf, errBuf bytes.Buffer
	code := s.personaSet([]string{"default"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("persona set default exit=%d stderr=%s", code, errBuf.String())
	}
	if s.activePersona != "default" {
		t.Errorf("activePersona = %q; want default", s.activePersona)
	}
	if name := persona.ReadActivePersona(home); name != "default" {
		t.Errorf("ReadActivePersona = %q; want default", name)
	}
	// No external state was touched — kubeconfig absent, gcloud
	// active_config absent.
	if _, err := os.Stat(filepath.Join(home, ".config", "gcloud", "active_config")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy path created gcloud active_config when persona had no bindings")
	}
}

// TestAtomicPersonaSwitch_HistoryEventBodyHasNoSecret — adversarial:
// after a successful all-four switch, scan every persona.use event
// in the history store for the raw private-key bytes.
func TestAtomicPersonaSwitch_HistoryEventBodyHasNoSecret(t *testing.T) {
	requireGitCLI(t)
	tomlBody := allFourTOML("work", "work-sso", "sub",
		"work-cluster", "W", "w@x.test")
	s, home := newShellWithStagedPersona(t, "all-four", tomlBody)
	if s.history == nil {
		t.Skip("history not wired in test shell — cannot assert event-body redaction")
	}
	pemBytes := genTestEd25519PEM(t)
	sock := startFakeAgent(t)
	stageKubeconfig(t, home)
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = P\n\temail = p@x.test\n"), 0o644)

	az := &recordingAz{}
	keys := &stubKeySource{pem: pemBytes}
	dialer := stubAgentDialer{sock: sock}
	s.SetAtomicDepsForTesting(
		keys, dialer,
		fixedHomeShim{Path: home}, az,
		fixedHomeShim{Path: home}, "",
		newHomeBoundGitRunner(home),
	)

	var outBuf, errBuf bytes.Buffer
	if code := s.personaSet([]string{"all-four"}, &outBuf, &errBuf); code != 0 {
		t.Fatalf("persona set: %d %s", code, errBuf.String())
	}

	// Read the latest events from history; assert no event body
	// contains the PEM key bytes.
	store := s.history.Store()
	events, err := store.List(50)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == "persona.use" {
			found = true
			if bytes.Contains([]byte(ev.Command), pemBytes) {
				t.Errorf("event %s Command contains PEM private-key bytes", ev.ID)
			}
			// Also check the marker substring.
			if bytes.Contains([]byte(ev.Command), []byte("PRIVATE KEY")) {
				t.Errorf("event %s Command contains 'PRIVATE KEY' substring", ev.ID)
			}
		}
	}
	if !found {
		t.Error("no persona.use event recorded — audit trail incomplete")
	}
}

// TestAtomicPersonaSwitch_OutcomeEmbeddedInSignedEvent — the
// persona.use event's Command field embeds the JSON-serialised
// Outcome.
func TestAtomicPersonaSwitch_OutcomeEmbeddedInSignedEvent(t *testing.T) {
	requireGitCLI(t)
	tomlBody := allFourTOML("work", "work-sso", "sub",
		"work-cluster", "W", "w@x.test")
	s, home := newShellWithStagedPersona(t, "all-four", tomlBody)
	if s.history == nil {
		t.Skip("history not wired")
	}
	pemBytes := genTestEd25519PEM(t)
	sock := startFakeAgent(t)
	stageKubeconfig(t, home)
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = P\n\temail = p@x.test\n"), 0o644)

	az := &recordingAz{}
	s.SetAtomicDepsForTesting(
		&stubKeySource{pem: pemBytes}, stubAgentDialer{sock: sock},
		fixedHomeShim{Path: home}, az,
		fixedHomeShim{Path: home}, "",
		newHomeBoundGitRunner(home),
	)
	var outBuf, errBuf bytes.Buffer
	if code := s.personaSet([]string{"all-four"}, &outBuf, &errBuf); code != 0 {
		t.Fatalf("exit=%d err=%s", code, errBuf.String())
	}

	events, err := s.history.Store().List(20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var personaEv *outcomeBody
	for _, ev := range events {
		if ev.Kind == "persona.use" {
			parts := strings.SplitN(ev.Command, "\x00", 2)
			if len(parts) != 2 {
				continue
			}
			var ob outcomeBody
			if err := json.Unmarshal([]byte(parts[1]), &ob); err != nil {
				t.Errorf("unmarshal outcome body: %v", err)
				continue
			}
			personaEv = &ob
			break
		}
	}
	if personaEv == nil {
		t.Fatal("no persona.use event carries an embedded outcome body")
	}
	if personaEv.Persona != "all-four" {
		t.Errorf("outcome.persona = %q want all-four", personaEv.Persona)
	}
	wantApplied := []string{"ssh", "cloud", "kube", "git"}
	if !equalStrSlice(personaEv.Outcome.Applied, wantApplied) {
		t.Errorf("Applied = %v want %v", personaEv.Outcome.Applied, wantApplied)
	}
}

// TestAtomicPersonaSwitch_ConcurrentInvocationsSerialised — two
// concurrent personaSetAtomic calls are serialised by the
// process-local mutex.
func TestAtomicPersonaSwitch_ConcurrentInvocationsSerialised(t *testing.T) {
	requireGitCLI(t)
	tomlBody := allFourTOML("work", "work-sso", "sub",
		"work-cluster", "W", "w@x.test")
	s, home := newShellWithStagedPersona(t, "all-four", tomlBody)
	pemBytes := genTestEd25519PEM(t)
	sock := startFakeAgent(t)
	stageKubeconfig(t, home)
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = P\n\temail = p@x.test\n"), 0o644)

	s.SetAtomicDepsForTesting(
		&stubKeySource{pem: pemBytes}, stubAgentDialer{sock: sock},
		fixedHomeShim{Path: home}, &recordingAz{},
		fixedHomeShim{Path: home}, "",
		newHomeBoundGitRunner(home),
	)

	// Fire two goroutines; each does a persona use of the same name.
	// Serialisation is via personaSwitchMu; correctness manifests as
	// "both succeed without interleaving".
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var ob, eb bytes.Buffer
			done <- s.personaSet([]string{"all-four"}, &ob, &eb)
		}()
	}
	for i := 0; i < 2; i++ {
		code := <-done
		if code != 0 {
			t.Errorf("concurrent persona set #%d returned %d", i, code)
		}
	}
}

// ---------- shared helpers used across the T5 tests ----------

// fixedHomeShim is a HomeProvider returning a constant path. Mirrors
// adapter.fixedHome (which is test-only inside the adapter package);
// duplicated here because cross-package test types are not exported.
type fixedHomeShim struct{ Path string }

func (f fixedHomeShim) Home() string { return f.Path }

// newHomeBoundGitRunner returns a GitRunner that shells out to the
// real `git` binary with HOME pointed at the sandbox path.
func newHomeBoundGitRunner(home string) adapter.GitRunner {
	return &homeBoundGitRunner{home: home}
}

type homeBoundGitRunner struct{ home string }

func (h *homeBoundGitRunner) Available(ctx context.Context) bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func (h *homeBoundGitRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// Pin the child CWD to the sandbox so a prior test's t.Chdir to
	// a tempdir that no longer exists cannot crash git with "Unable
	// to read current working directory".
	cmd.Dir = h.home
	cmd.Env = append(os.Environ(),
		"HOME="+h.home,
		"XDG_CONFIG_HOME="+filepath.Join(h.home, ".config"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ex, ok := err.(*exec.ExitError); ok && ex.ExitCode() == 1 && stderr.Len() == 0 && hasGetTest(args) {
			return "", adapter.ErrGitKeyNotPresent
		}
		if ex, ok := err.(*exec.ExitError); ok && ex.ExitCode() == 5 {
			return "", errors.New("git: exit status 5")
		}
		return "", errors.New("git " + strings.Join(args, " ") + ": " + err.Error() + " stderr: " + stderr.String())
	}
	return stdout.String(), nil
}

func hasGetTest(args []string) bool {
	for _, a := range args {
		if a == "--get" {
			return true
		}
	}
	return false
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
