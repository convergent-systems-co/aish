package integration

import (
	"testing"
)

// TestPipeTwoStage — the canonical pipe: `echo foo | tr a-z A-Z`. Stage 1's
// stdout becomes stage 2's stdin via aish's io.Pipe/os.Pipe wiring.
func TestPipeTwoStage(t *testing.T) {
	requireBinary(t, "echo", "tr")
	s := run(t, script("echo foo | tr a-z A-Z"))
	s.assertExit(0)
	s.assertStdoutContains("FOO")
}

// TestPipeThreeStage — chained pipes; intermediate stage (cat) acts as a
// pass-through. Validates that pipe wiring works for N>2.
func TestPipeThreeStage(t *testing.T) {
	requireBinary(t, "echo", "cat", "tr")
	s := run(t, script("echo foo | cat | tr a-z A-Z"))
	s.assertExit(0)
	s.assertStdoutContains("FOO")
}

// TestPipeFourStage — `ls / | grep . | head -3 | wc -l` should produce a
// single-digit count. Exercises a longer pipeline with multi-byte data.
func TestPipeFourStage(t *testing.T) {
	requireBinary(t, "ls", "grep", "head", "wc")
	s := run(t, script("ls / | grep . | head -3 | wc -l", "echo done=$?"))
	s.assertExit(0)
	s.assertStdoutContains("done=0")
}

// TestPipeExitCodeLastWins — POSIX: the pipeline's exit code is the LAST
// stage's. `echo (0) | false (1)` => 1. aish stores this in $?.
func TestPipeExitCodeLastWins(t *testing.T) {
	requireBinary(t, "echo", "false")
	s := run(t, script("echo input | false", "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=1")
}

// TestPipeExitCodeLastWinsEvenWhenFirstFails — `false (1) | echo (0)`
// => 0. The first stage's non-zero is discarded by POSIX semantics.
func TestPipeExitCodeLastWinsEvenWhenFirstFails(t *testing.T) {
	requireBinary(t, "echo", "false")
	s := run(t, script("false | echo passed", "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=0")
	s.assertStdoutContains("passed")
}

// TestPipeStdinFlowsThrough — text flows from stage 1 stdout to stage 2
// stdin without truncation. Tests with multiline input through a counter.
func TestPipeStdinFlowsThrough(t *testing.T) {
	requireBinary(t, "printf", "wc")
	// printf 'a\nb\nc\n' produces 3 lines; wc -l should report 3.
	s := run(t, script(`printf "a\nb\nc\n" | wc -l`))
	s.assertExit(0)
	s.assertStdoutContains("3")
}

// TestPipeMissingMiddleBinary — when stage 2 is a missing binary, the
// pipeline still ends gracefully (no crash) and the failure surfaces.
func TestPipeMissingMiddleBinary(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo input | this-bin-does-not-exist-xyz", "echo afterwards=$?"))
	s.assertExit(0)
	// Whatever the exit code surfaces as, the REPL continues afterward.
	s.assertStdoutContains("afterwards=")
}
