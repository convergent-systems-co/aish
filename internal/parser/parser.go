// Package parser tokenizes raw aish input into a structured pipeline.
//
// v0.1-1 scope (sub-issue #4): single/double-quoted args, simple flags
// (`-x`, `--flag`, `--flag=value`), and `|` between commands. No subshells,
// no backticks, no globbing, no redirection operators yet.
//
// The Pipeline type is the seam that internal/exec consumes; downstream
// packages MUST NOT introduce a parallel representation.
package parser

// Command is a single program invocation: argv[0] is the program name,
// the remaining elements are its arguments after quote stripping and
// flag splitting.
type Command struct {
	// Name is argv[0] — the command to look up on PATH or as a built-in.
	Name string
	// Args are argv[1:] — flags and positional args in left-to-right order.
	Args []string
}

// Pipeline is the parsed shape of one shell line: one or more Commands
// joined by `|`. A pipeline with a single Command represents a non-piped
// invocation.
type Pipeline struct {
	// Commands is left-to-right; Commands[0] is the producer, Commands[len-1]
	// is the consumer whose exit code becomes the pipeline's exit code.
	Commands []Command
}

// Parse converts a single line of input into a Pipeline.
//
// Errors are returned for unterminated quotes and other lexical defects.
// An empty or whitespace-only input returns an empty Pipeline (no commands)
// and a nil error — the REPL treats that as a no-op.
//
// Implementation lives in the v0.1-1 coder T1 sub-task; this stub returns
// a zero Pipeline so the test file compiles.
func Parse(input string) (Pipeline, error) {
	return Pipeline{}, nil
}
