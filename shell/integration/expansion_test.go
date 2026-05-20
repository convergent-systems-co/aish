package integration

import (
	"testing"
)

// TestExpansionVar — bare `$VAR` expands to the bound value.
func TestExpansionVar(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("export X=foo", "echo $X"))
	s.assertExit(0)
	s.assertStdoutContains("foo")
}

// TestExpansionBracedVar — `${VAR}` expands identically to `$VAR`.
func TestExpansionBracedVar(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("export X=foo", "echo ${X}"))
	s.assertExit(0)
	s.assertStdoutContains("foo")
}

// TestExpansionUndefined — an unset variable expands to the empty string,
// not an error. Matches POSIX shell behaviour with no `set -u`.
func TestExpansionUndefined(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo before$UNDEFINED_AISH_VAR_xyz after"))
	s.assertExit(0)
	s.assertStdoutContains("before after")
}

// TestExpansionLastExit — `$?` reflects the last command's exit code.
// Tests the initial-zero value AND post-false update.
func TestExpansionLastExit(t *testing.T) {
	requireBinary(t, "true", "false", "echo")
	s := run(t, script(
		"true",
		"echo after_true=$?",
		"false",
		"echo after_false=$?",
	))
	s.assertExit(0)
	s.assertStdoutContains("after_true=0")
	s.assertStdoutContains("after_false=1")
}

// TestExpansionLastExitInitial — `$?` before any command runs reports 0.
func TestExpansionLastExitInitial(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("echo initial=$?"))
	s.assertExit(0)
	s.assertStdoutContains("initial=0")
}

// TestExpansionMidString — `$VAR` mid-token concatenates without quotes.
func TestExpansionMidString(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("export X=middle", "echo prefix${X}suffix"))
	s.assertExit(0)
	s.assertStdoutContains("prefixmiddlesuffix")
}

// TestExpansionInDoubleQuotes — double-quoted strings expand `$VAR`.
func TestExpansionInDoubleQuotes(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script("export X=expanded", `echo "x=$X"`))
	s.assertExit(0)
	s.assertStdoutContains("x=expanded")
}

// TestExpansionInSingleQuotes — single-quoted strings preserve `$VAR`
// literally (no expansion). POSIX semantic.
//
// SKIPPED pending fix for #163: aish currently expands variables on the
// whole line before tokenization, which breaks single-quote literal
// semantics. Remove the t.Skip when #163 lands.
func TestExpansionInSingleQuotes(t *testing.T) {
	t.Skip("known defect: see https://github.com/convergent-systems-co/aish/issues/163")
	requireBinary(t, "echo")
	s := run(t, script("export X=expanded", `echo 'x=$X'`))
	s.assertExit(0)
	s.assertStdoutContains("x=$X")
	s.assertStdoutNotContains("x=expanded")
}

// TestExpansionLastExitFromMissingBinary — invoking a non-existent
// command sets `$?` to 127 (POSIX "command not found").
func TestExpansionLastExitFromMissingBinary(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(
		"this-binary-does-not-exist-aish-test",
		"echo after_missing=$?",
	))
	s.assertExit(0)
	s.assertStdoutContains("after_missing=127")
}
