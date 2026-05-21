package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/inference"
)

// binaryPath is the path to the freshly-built aish-inference-cloud
// binary used by every test in this package. Populated by TestMain.
var binaryPath string

const (
	// defaultTimeout caps how long a single test may wait on the
	// subprocess. SSE-streaming tests typically finish in < 100ms;
	// missing-key tests finish near-instantly. 10s is a generous
	// ceiling that still kills hangs in CI.
	defaultTimeout = 10 * time.Second

	// cmdImport is the Go import path of the binary under test,
	// relative to the test package directory.
	cmdImport = "../cmd/aish-inference-cloud"

	// fakeAPIKey is the synthetic token every test uses for the
	// $CS_API_KEY env var (or its back-compat alias). It is a literal
	// string with no upstream meaning; the redaction tests assert this
	// exact value never appears in any user-visible output. It MUST
	// appear ONLY in plugins/cloud/integration/*_test.go per
	// Common.md §4. The "cs_" prefix mirrors the real token shape the
	// auth-proxy issues (per core-infra/README.md §"Obtaining a token").
	fakeAPIKey = "cs_test_integration_AAAA"
)

// TestMain builds aish-inference-cloud once into a temp dir, then runs
// the tests. The binary is shared across all tests; each test spawns
// its own subprocess, so REPL state never leaks between tests.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "aish-inference-cloud-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: failed to create tempdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binaryName := "aish-inference-cloud"
	if runtime.GOOS == "windows" {
		binaryName = "aish-inference-cloud.exe"
	}
	binaryPath = filepath.Join(tmpDir, binaryName)

	// -buildvcs=false: when this checkout is a git worktree (or any
	// non-canonical .git location), `go build` would otherwise fail with
	// "error obtaining VCS status". The integration test binary's
	// embedded version stamp is not load-bearing for behaviour, so we
	// skip VCS stamping unconditionally here.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binaryPath, cmdImport)
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: go build failed:\n%s\n", out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// session is the result of running aish-inference-cloud once with
// scripted input.
type session struct {
	t        *testing.T
	stdout   string
	stderr   string
	exitCode int
}

// runOpts configures one subprocess run.
type runOpts struct {
	// stdin is fed to the plugin verbatim. The harness closes the
	// pipe after writing so the plugin sees EOF and exits cleanly.
	stdin string
	// env is the subprocess environment. nil means "do not set Env"
	// (inherit parent). The harness does NOT auto-set ANTHROPIC_API_KEY —
	// each test sets it explicitly to keep secret intent obvious.
	env []string
	// args are appended to the binary invocation. Use for --version /
	// --help / --api-url <URL>.
	args []string
	// timeout overrides defaultTimeout. Zero uses the default.
	timeout time.Duration
}

// run spawns the plugin, feeds stdin, waits, and returns a session.
func run(t *testing.T, opts runOpts) *session {
	t.Helper()

	cmd := exec.Command(binaryPath, opts.args...)
	if opts.env != nil {
		cmd.Env = opts.env
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("integration: os.Pipe for stdin: %v", err)
	}
	defer stdinR.Close()
	cmd.Stdin = stdinR

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		_ = stdinW.Close()
		t.Fatalf("integration: failed to start aish-inference-cloud: %v", err)
	}

	go func() {
		_, _ = io.WriteString(stdinW, opts.stdin)
		_ = stdinW.Close()
	}()

	timeout := opts.timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		s := &session{
			t:      t,
			stdout: stdout.String(),
			stderr: stderr.String(),
		}
		switch e := err.(type) {
		case nil:
			s.exitCode = 0
		case *exec.ExitError:
			s.exitCode = e.ExitCode()
		default:
			t.Fatalf("integration: unexpected wait error: %v", e)
		}
		return s
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("integration: subprocess hung — killed after %s.\nstdout:\n%s\nstderr:\n%s",
			timeout, stdout.String(), stderr.String())
		return nil
	}
}

// envWithKey returns a minimal env slice with CS_API_KEY set to
// fakeAPIKey and PATH/HOME preserved from the parent process so the
// subprocess can still locate go-build-time runtime files and (when
// needed) the cost-log directory.
//
// Note: the plugin also accepts the legacy ANTHROPIC_API_KEY for
// back-compat. Tests use the canonical CS_API_KEY going forward.
func envWithKey() []string {
	return envWithKeyAndExtras()
}

// envWithKeyAndExtras returns envWithKey plus any additional KEY=VALUE
// strings the caller supplies. Useful for CS_BASE_URL / back-compat
// alias tests.
func envWithKeyAndExtras(extras ...string) []string {
	env := []string{
		"CS_API_KEY=" + fakeAPIKey,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	env = append(env, extras...)
	return env
}

// envWithoutKey returns an env slice deliberately missing both the
// CS_API_KEY var and its legacy ANTHROPIC_API_KEY alias. Used by the
// fail-fast missing-key test.
func envWithoutKey() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
}

// readResponses parses every NDJSON line in stdout into proto.Response
// values. Blank lines are skipped. Returns the parsed slice and the
// number of unparseable lines (a non-zero count typically indicates
// stdout corruption — the test should fail).
func readResponses(t *testing.T, stdout string) []proto.Response {
	t.Helper()
	var out []proto.Response
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r proto.Response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("integration: failed to parse stdout line %q: %v\nfull stdout:\n%s", line, err, stdout)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("integration: scan stdout: %v", err)
	}
	return out
}

// marshalRequest is the inverse of readResponses on the inbound side.
func marshalRequest(t *testing.T, r proto.Request) string {
	t.Helper()
	b, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("integration: marshal request: %v", err)
	}
	return string(b) + "\n"
}

// assertExit fails the test unless the subprocess exited with want.
func (s *session) assertExit(want int) {
	s.t.Helper()
	if s.exitCode != want {
		s.t.Fatalf("plugin exit=%d, want %d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, want, s.stdout, s.stderr)
	}
}

// assertExitNonZero fails the test if the subprocess exited 0.
func (s *session) assertExitNonZero() {
	s.t.Helper()
	if s.exitCode == 0 {
		s.t.Fatalf("plugin exit=0, want non-zero\nstdout:\n%s\nstderr:\n%s",
			s.stdout, s.stderr)
	}
}

// assertStdoutContains fails the test if stdout does not contain want.
func (s *session) assertStdoutContains(want string) {
	s.t.Helper()
	if !strings.Contains(s.stdout, want) {
		s.t.Fatalf("stdout does not contain %q\nstdout:\n%s", want, s.stdout)
	}
}

// assertStderrContains fails the test if stderr does not contain want.
func (s *session) assertStderrContains(want string) {
	s.t.Helper()
	if !strings.Contains(s.stderr, want) {
		s.t.Fatalf("stderr does not contain %q\nstderr:\n%s", want, s.stderr)
	}
}

// assertStderrDoesNotContain fails the test if stderr contains unwant.
// Used to verify that no API-key value leaks into diagnostic output.
func (s *session) assertStderrDoesNotContain(unwant string) {
	s.t.Helper()
	if strings.Contains(s.stderr, unwant) {
		s.t.Fatalf("stderr unexpectedly contains %q\nstderr:\n%s", unwant, s.stderr)
	}
}

// assertStdoutDoesNotContain fails the test if stdout contains unwant.
func (s *session) assertStdoutDoesNotContain(unwant string) {
	s.t.Helper()
	if strings.Contains(s.stdout, unwant) {
		s.t.Fatalf("stdout unexpectedly contains %q\nstdout:\n%s", unwant, s.stdout)
	}
}
