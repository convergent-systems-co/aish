package integration

import (
	"strings"
	"testing"
)

// TestCatConsumesPipedStdin — regression seatbelt for issue #167.
//
// When aish reads "cat\nline-one\nline-two\n" from stdin, bufio
// prefetches the post-`cat` lines into its private buffer; passing the
// raw stdin to the child caused those lines to be lost (and aish later
// tried to dispatch them as commands). After the fix, cat receives the
// prefetched lines via the multi-reader path in stdinForChild.
func TestCatConsumesPipedStdin(t *testing.T) {
	requireBinary(t, "cat")
	s := run(t, "cat\nfirst line\nsecond line\n")
	s.assertExit(0)
	s.assertStdoutContains("first line")
	s.assertStdoutContains("second line")
	// The misbehaviour mode prints "command not found" because aish tried
	// to dispatch the lines. If that error is present, the fix regressed.
	if strings.Contains(s.stderr, "executable file not found") &&
		(strings.Contains(s.stderr, "first") || strings.Contains(s.stderr, "second")) {
		t.Errorf("aish appears to have dispatched cat's stdin as commands:\n%s", s.stderr)
	}
}

// TestCatPipeStdinFlowsThrough — `cat | tr a-z A-Z` reading from the
// REPL's stdin transforms the input. Same fix as above, exercised via a
// pipeline rather than a single command.
func TestCatPipeStdinFlowsThrough(t *testing.T) {
	requireBinary(t, "cat", "tr")
	s := run(t, "cat | tr a-z A-Z\nfoo bar\n")
	s.assertExit(0)
	s.assertStdoutContains("FOO BAR")
}

// TestStdinDrainedBeforeNextPrompt — once an external command consumes
// the prefetched stdin, the REPL has nothing left to dispatch.
// `echo done` would only appear if the lines after `cat` had been
// dispatched as commands (the pre-fix behaviour). Its absence is the
// positive assertion.
func TestStdinDrainedBeforeNextPrompt(t *testing.T) {
	requireBinary(t, "cat")
	s := run(t, "cat\necho done\nshould-not-execute\n")
	s.assertExit(0)
	// cat consumed both "echo done" and "should-not-execute" as input
	// lines; neither should appear as a missing-command error.
	if strings.Contains(s.stderr, "should-not-execute") {
		t.Errorf("aish dispatched stdin-bound line as a command:\n%s", s.stderr)
	}
	s.assertStdoutContains("echo done")
	s.assertStdoutContains("should-not-execute")
}

// TestHeadReadsStdinThenExits — `head -1` reads one line then exits.
// aish must then resume its REPL with the remaining lines.
//
// SKIPPED pending #168 (PTY work).
//
// Root cause is NOT aish — it's that `head` internally uses a 4 KB
// buffered read of stdin. In scripted-pipe mode the test's input
// "first\necho two\n" is available on the pipe by the time head reads;
// head's first read pulls BOTH lines into head's process memory, prints
// "first", and exits. "echo two\n" is gone with the head process.
//
// In a real interactive terminal (PTY), the user types lines one at a
// time; head's read returns one line because that's all that has
// arrived. The fix has to happen at the test-harness level (use a PTY
// instead of a pipe) and/or alongside v0.2-2 PTY support.
//
// The cat-family tests above pass because cat consumes ALL of stdin —
// no leftover bytes are stolen by the child's internal buffer.
func TestHeadReadsStdinThenExits(t *testing.T) {
	t.Skip("known limitation: see https://github.com/convergent-systems-co/aish/issues/168")
	requireBinary(t, "head", "echo")
	s := run(t, "head -1\nfirst\necho two\n")
	s.assertExit(0)
	s.assertStdoutContains("first")
	s.assertStdoutContains("two")
}
