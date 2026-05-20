package integration

import (
	"strings"
	"testing"
)

// TestErrorMissingBinary — invoking a non-existent name surfaces an error
// on stderr AND sets $? to 127. REPL keeps running afterward.
func TestErrorMissingBinary(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		"definitely-not-a-real-binary-aish-test-xyzzy",
		"echo afterwards=$?",
	))
	s.assertExit(0)
	// Error to stderr — accept any of the common idioms.
	combined := s.stderr + s.stdout
	if !strings.Contains(combined, "not found") &&
		!strings.Contains(combined, "no such") &&
		!strings.Contains(combined, "executable") {
		t.Fatalf("expected an error mentioning not-found; got\nstderr:\n%s\nstdout:\n%s",
			s.stderr, s.stdout)
	}
	s.assertStdoutContains("afterwards=127")
}

// TestErrorCdNonexistent — `cd /no/such` reports a problem; the REPL stays
// alive and subsequent commands run.
func TestErrorCdNonexistent(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		"cd /no/such/dir/please-be-absent-aish-test",
		"echo still_alive=yes",
	))
	s.assertExit(0)
	s.assertStdoutContains("still_alive=yes")
}

// TestErrorRecoversFromUnclosedQuote — a malformed line (unclosed quote)
// must NOT crash aish. It should report the error and continue.
func TestErrorRecoversFromUnclosedQuote(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		`echo "unterminated`,
		"echo recovered=yes",
	))
	s.assertExit(0)
	s.assertStdoutContains("recovered=yes")
}

// TestErrorEmptyExportNoEquals — `export FOO` without `=VALUE`. The
// behavior is unspecified in v0.1-1; the contract here is just "no crash".
func TestErrorEmptyExportNoEquals(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		"export NOVAL",
		"echo still_alive=yes",
	))
	s.assertExit(0)
	s.assertStdoutContains("still_alive=yes")
}

// TestErrorPipeWithMissingFirstBinary — when the first stage of a pipe is
// missing, aish reports the error and exits the pipeline; REPL recovers.
func TestErrorPipeWithMissingFirstBinary(t *testing.T) {
	requireBinary(t, "cat", "echo")
	s := run(t, script(
		"definitely-not-real-aish-test | cat",
		"echo recovered=yes",
	))
	s.assertExit(0)
	s.assertStdoutContains("recovered=yes")
}
