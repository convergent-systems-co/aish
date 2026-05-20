package exec

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/internal/parser"
)

// requireBinary skips the test if a real binary the integration test depends
// on is not present on $PATH. Per the v0.1-1 plan: real binaries, not mocks.
// Skipping is allowed only with an environmental reason.
func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("required binary %q not on PATH (env-dependent integration): %v", name, err)
	}
}

// TestRunSingleCommand runs a real `echo` and verifies stdout receives the
// expected output and exit code is 0. Gates sub-issue #5.
func TestRunSingleCommand(t *testing.T) {
	requireBinary(t, "echo")
	p := parser.Pipeline{
		Commands: []parser.Command{{Name: "echo", Args: []string{"hello"}}},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

// TestRunTwoStagePipe runs `echo foo | tr a-z A-Z` against the real
// binaries and asserts the consumer's output reaches stdout uppercased.
// Gates sub-issue #6 (pipe semantics).
func TestRunTwoStagePipe(t *testing.T) {
	requireBinary(t, "echo")
	requireBinary(t, "tr")
	p := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "echo", Args: []string{"foo"}},
			{Name: "tr", Args: []string{"a-z", "A-Z"}},
		},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "FOO" {
		t.Errorf("pipeline stdout = %q, want %q (stderr=%q)", got, "FOO", stderr.String())
	}
}

// TestRunStdinFlowsToFirstCommand verifies stdin feeds the producer in
// the pipeline. Uses `cat` to echo stdin back to stdout.
func TestRunStdinFlowsToFirstCommand(t *testing.T) {
	requireBinary(t, "cat")
	p := parser.Pipeline{
		Commands: []parser.Command{{Name: "cat", Args: nil}},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p, nil,
		strings.NewReader("piped-input"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout.String() != "piped-input" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "piped-input")
	}
}

// TestRunExitCodeIsLastCommand asserts the POSIX semantic: the pipeline's
// exit code is the LAST command's, not the first. `false` is a coreutil
// guaranteed to exit non-zero.
func TestRunExitCodeIsLastCommand(t *testing.T) {
	requireBinary(t, "echo")
	requireBinary(t, "false")
	p := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "echo", Args: []string{"ignored"}},
			{Name: "false", Args: nil},
		},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero (last command was `false`)")
	}
}

// TestRunNonZeroExitNoError verifies that a child process exiting non-zero
// is surfaced via exitCode, NOT via err. err is reserved for pipeline-setup
// failures (missing binary, pipe creation failure). Required by the plan's
// `Run` contract.
func TestRunNonZeroExitNoError(t *testing.T) {
	requireBinary(t, "false")
	p := parser.Pipeline{
		Commands: []parser.Command{{Name: "false", Args: nil}},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Errorf("Run returned err for non-zero child exit: %v (want nil)", err)
	}
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
}

// TestRunMissingBinaryReturnsError is the negative path: a command not on
// PATH must surface as a non-nil error. Required by Code.md §1 (no silent
// swallowing of error paths) and the plan's acceptance list.
func TestRunMissingBinaryReturnsError(t *testing.T) {
	p := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "this-binary-definitely-does-not-exist-aish-test", Args: nil},
		},
	}
	var stdout, stderr bytes.Buffer
	_, err := Run(context.Background(), p, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("Run with missing binary returned nil error, want non-nil")
	}
}

// TestRunEmptyPipelineIsNoOp confirms that an empty Pipeline (no commands)
// returns (0, nil) without touching stdout/stderr. The REPL passes empty
// pipelines for bare-Enter keypresses; they must be cheap and silent.
func TestRunEmptyPipelineIsNoOp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), parser.Pipeline{}, nil,
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Errorf("Run on empty pipeline returned err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 for empty pipeline", code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("empty pipeline wrote output: stdout=%q stderr=%q",
			stdout.String(), stderr.String())
	}
}

// TestRunEnvPassedToChild verifies env [] passed to Run reaches the child
// process. Uses `sh -c 'echo $AISH_TEST_VAR'` because shell var expansion
// is the most portable way to assert env was received.
func TestRunEnvPassedToChild(t *testing.T) {
	requireBinary(t, "sh")
	p := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "sh", Args: []string{"-c", "echo $AISH_TEST_VAR"}},
		},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), p,
		[]string{"AISH_TEST_VAR=carried"},
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got := strings.TrimRight(stdout.String(), "\n"); got != "carried" {
		t.Errorf("child saw AISH_TEST_VAR=%q, want %q", got, "carried")
	}
}
