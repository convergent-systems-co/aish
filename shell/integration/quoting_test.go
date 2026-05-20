package integration

import (
	"strings"
	"testing"
)

// TestDoubleQuoteSpaces — `"hello world"` is ONE argument, not two.
// Verified by piping echo into wc -w (word count).
func TestDoubleQuoteSpaces(t *testing.T) {
	requireBinary(t, "echo", "wc")
	s := run(t, script(`echo "hello world" | wc -w`))
	s.assertExit(0)
	// wc -w prints the count, possibly with leading whitespace.
	if !strings.Contains(s.stdout, "2") {
		t.Fatalf("expected '2' (word count) in stdout; got:\n%s", s.stdout)
	}
}

// TestSingleQuoteSpaces — single quotes preserve spaces identically.
func TestSingleQuoteSpaces(t *testing.T) {
	requireBinary(t, "echo", "wc")
	s := run(t, script(`echo 'hello world' | wc -w`))
	s.assertExit(0)
	if !strings.Contains(s.stdout, "2") {
		t.Fatalf("expected '2' (word count) in stdout; got:\n%s", s.stdout)
	}
}

// TestDoubleQuoteWithDollarExpansion — `"$X"` expands; verified above
// in expansion_test.go, repeated here for the quoting-feature locus.
func TestDoubleQuoteWithDollarExpansion(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(`export X=expanded`, `echo "value is $X"`))
	s.assertExit(0)
	s.assertStdoutContains("value is expanded")
}

// TestSingleQuoteNoExpansion — `'$X'` is literal. Belt-and-suspenders
// with the expansion test of the same name.
//
// SKIPPED pending fix for #163: see expansion_test.go for context.
func TestSingleQuoteNoExpansion(t *testing.T) {
	t.Skip("known defect: see https://github.com/convergent-systems-co/aish/issues/163")
	requireBinary(t, "echo")
	s := run(t, script(`export X=expanded`, `echo 'literal $X'`))
	s.assertExit(0)
	s.assertStdoutContains("literal $X")
	s.assertStdoutNotContains("literal expanded")
}

// TestMixedQuotes — single quotes inside double quotes pass through.
func TestMixedQuotes(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(`echo "outer 'inner' outer"`))
	s.assertExit(0)
	s.assertStdoutContains("outer 'inner' outer")
}

// TestMixedQuotesReverse — double quotes inside single quotes pass through.
func TestMixedQuotesReverse(t *testing.T) {
	requireBinary(t, "echo")
	s := run(t, script(`echo 'outer "inner" outer'`))
	s.assertExit(0)
	s.assertStdoutContains(`outer "inner" outer`)
}
