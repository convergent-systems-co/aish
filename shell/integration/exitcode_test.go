package integration

import (
	"testing"
)

// TestExitCodeTrue — `/bin/true` (or PATH true) exits 0.
func TestExitCodeTrue(t *testing.T) {
	requireBinary(t, "true", "echo")
	s := run(t, script("true", "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=0")
}

// TestExitCodeFalse — `/bin/false` (or PATH false) exits 1.
func TestExitCodeFalse(t *testing.T) {
	requireBinary(t, "false", "echo")
	s := run(t, script("false", "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=1")
}

// TestExitCodeMissingBinary — invoking a name not on PATH sets `$?` to
// 127 (POSIX "command not found").
func TestExitCodeMissingBinary(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		"this-binary-is-not-real-aish-test-xyz",
		"echo exit=$?",
	))
	s.assertExit(0)
	s.assertStdoutContains("exit=127")
}

// TestExitCodeShExitN — `sh -c "exit 42"` lets the test pin an exact code.
// Verifies aish reads `Cmd.ProcessState.ExitCode()` correctly.
func TestExitCodeShExitN(t *testing.T) {
	requireBinary(t, "sh", "echo")
	s := run(t, script(`sh -c "exit 42"`, "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=42")
}

// TestExitCodeShExitMaxByte — POSIX exit codes are 0–255. `exit 255` is
// the boundary; aish must surface it untruncated.
func TestExitCodeShExitMaxByte(t *testing.T) {
	requireBinary(t, "sh", "echo")
	s := run(t, script(`sh -c "exit 255"`, "echo exit=$?"))
	s.assertExit(0)
	s.assertStdoutContains("exit=255")
}

// TestExitCodePersistsOnlyToNextCommand — `$?` carries the LAST command's
// exit code. After a `false` and then a passing command, `$?` reflects
// the LATER command's exit (because echo itself ran).
//
// false        -> $? = 1
// echo at_false=$?  (runs ok, prints "at_false=1")
// echo got=$?  (this `echo` saw `at_false`'s exit code 0, so prints got=0)
func TestExitCodePersistsOnlyToNextCommand(t *testing.T) {
	requireBinary(t, "false", "echo")
	s := run(t, script(
		"false",
		"echo at_false=$?",
		"echo got=$?",
	))
	s.assertExit(0)
	s.assertStdoutContains("at_false=1")
	s.assertStdoutContains("got=0")
}
