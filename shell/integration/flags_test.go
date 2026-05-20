package integration

import (
	"strings"
	"testing"
)

// TestFlagVersionLong — `aish --version` writes the version line and exits.
func TestFlagVersionLong(t *testing.T) {
	s := run(t, "", "--version")
	s.assertExit(0)
	// Format: "aish <version> (built <time>)"
	if !strings.HasPrefix(strings.TrimSpace(s.stdout), "aish ") {
		t.Fatalf("expected 'aish <version>' prefix; got:\n%s", s.stdout)
	}
	s.assertStdoutContains("(built ")
}

// TestFlagVersionShort — `aish -v` is identical to `aish --version`.
func TestFlagVersionShort(t *testing.T) {
	s := run(t, "", "-v")
	s.assertExit(0)
	if !strings.HasPrefix(strings.TrimSpace(s.stdout), "aish ") {
		t.Fatalf("expected 'aish <version>' prefix; got:\n%s", s.stdout)
	}
}

// TestFlagHelpLong — `aish --help` writes usage; exits cleanly.
func TestFlagHelpLong(t *testing.T) {
	s := run(t, "", "--help")
	s.assertExit(0)
	s.assertStdoutContains("aish")
	s.assertStdoutContains("Usage")
}

// TestFlagHelpShort — `aish -h` is identical to `aish --help`.
func TestFlagHelpShort(t *testing.T) {
	s := run(t, "", "-h")
	s.assertExit(0)
	s.assertStdoutContains("Usage")
}

// TestFlagsDoNotEnterREPL — neither --version nor --help drops into the
// REPL after printing; stdin is irrelevant.
func TestFlagsDoNotEnterREPL(t *testing.T) {
	// Feed stdin that, if executed, would error; the binary must exit
	// before reading it.
	s := run(t, "this would be a bad command if executed", "--version")
	s.assertExit(0)
	s.assertStdoutNotContains("bad command")
}
