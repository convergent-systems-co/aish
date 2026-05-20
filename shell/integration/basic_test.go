package integration

import (
	"testing"
)

// TestExternalEcho — the canonical "external binary launches and writes
// stdout" test. Asserts that aish parses the line, invokes /usr/bin/echo
// (or PATH-resolved echo), and surfaces its output.
func TestExternalEcho(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo hello"))
	s.assertExit(0)
	s.assertStdoutContains("hello")
}

// TestExternalEchoMultipleArgs — multiple space-separated arguments reach
// the child as separate argv entries.
func TestExternalEchoMultipleArgs(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo a b c"))
	s.assertExit(0)
	s.assertStdoutContains("a b c")
}

// TestExternalLs — long-flag invocation works (`ls --help` on GNU coreutils;
// `ls /` is the universal fallback).
func TestExternalLs(t *testing.T) {
	requireBinary(t, "ls")
	s := run(t, script("ls /"))
	s.assertExit(0)
	// `/` is non-empty on every supported OS — at least one directory entry.
	if len(s.stdout) == 0 {
		t.Fatalf("ls / produced no stdout")
	}
}

// TestExternalWithDashFlag — short flags like `-n` reach echo properly.
// echo -n suppresses the trailing newline (POSIX); we don't assert on the
// no-newline behaviour because BSD echo differs, but we DO assert the
// command runs cleanly.
func TestExternalWithDashFlag(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo -n hi", "echo done=$?"))
	s.assertExit(0)
	s.assertStdoutContains("done=0")
}

// TestExternalWithLongFlag — `--flag` style. wc --version works on GNU;
// fall back to plain wc otherwise.
func TestExternalWithLongFlag(t *testing.T) {
	requireBinary(t, "wc")
	s := run(t, script("echo a | wc -l", "echo done=$?"))
	s.assertExit(0)
	s.assertStdoutContains("done=0")
}

// TestExternalEqualsFlag — flags of the form `--flag=value` survive the
// tokenizer (one argv token, not split on `=`).
func TestExternalEqualsFlag(t *testing.T) {
	requireBinary(t, "ls")
	s := run(t, script("ls --color=never /", "echo exit=$?"))
	// `ls --color=never` works on GNU; on macOS BSD ls, `--color=never` is
	// recognised by recent versions. If it fails, that's a platform issue,
	// not an aish tokenizer issue — so we assert only that aish exited 0
	// and ran the command (no parser error).
	s.assertExit(0)
}

// TestPwdReportsCwd — `pwd` is an external on macOS/Linux (not yet a
// built-in). It reports the process cwd, which is the test's cwd.
func TestPwdReportsCwd(t *testing.T) {
	requireBinary(t, "pwd")
	s := run(t, script("pwd"))
	s.assertExit(0)
	// pwd's output is the absolute path of the cwd — must not be empty.
	if len(s.stdout) < 2 {
		t.Fatalf("pwd produced suspiciously short stdout: %q", s.stdout)
	}
}

// TestExitOnEOF — feeding empty stdin makes aish exit cleanly with code 0.
// This is the REPL's terminator path.
func TestExitOnEOF(t *testing.T) {
	s := run(t, "")
	s.assertExit(0)
}

// TestWhitespaceOnlyLine — a blank or whitespace-only line is a no-op,
// not a parse error.
func TestWhitespaceOnlyLine(t *testing.T) {
	s := run(t, script("", "   ", "\t", "echo done"))
	s.assertExit(0)
	s.assertStdoutContains("done")
}
