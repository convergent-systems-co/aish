// Package parser tokenizes raw aish input into a structured pipeline.
//
// v0.1-1 scope (sub-issue #4): single/double-quoted args, simple flags
// (`-x`, `--flag`, `--flag=value`), and `|` between commands. No subshells,
// no backticks, no globbing, no redirection operators yet.
//
// The Pipeline type is the seam that internal/exec consumes; downstream
// packages MUST NOT introduce a parallel representation.
package parser

import (
	"fmt"
	"strings"
)

// Command is a single program invocation: argv[0] is the program name,
// the remaining elements are its arguments after quote stripping and
// flag splitting.
type Command struct {
	// Name is argv[0] — the command to look up on PATH or as a built-in.
	Name string
	// Args are argv[1:] — flags and positional args in left-to-right order.
	Args []string
	// Tainted is set when this command's argv (Name or any element of
	// Args) carries a value known to have originated from a secret —
	// e.g. the captured stdout of `$(secret get NAME)`. The flag is
	// purely informational at the parser layer; downstream consumers
	// (the history interceptor) read it to redact the recorded command
	// line. See v0.3-fu-secrets §"Acceptance Criteria #96/#98/#99".
	//
	// The flag is conservatively additive — a false value matches the
	// pre-v0.3-fu behavior exactly. Only the shell sets it; the parser
	// itself never tags a command tainted.
	Tainted bool
}

// Pipeline is the parsed shape of one shell line: one or more Commands
// joined by `|`. A pipeline with a single Command represents a non-piped
// invocation.
type Pipeline struct {
	// Commands is left-to-right; Commands[0] is the producer, Commands[len-1]
	// is the consumer whose exit code becomes the pipeline's exit code.
	Commands []Command
	// Background is set when the input line ended with an unquoted `&`.
	// The shell runtime spawns the pipeline as a background job and
	// returns to the prompt without waiting (v0.3-1 follow-up #83/#84).
	// Mid-line `&` is rejected as a syntax error in this PR — the POSIX
	// statement-separator form is future work.
	Background bool
	// Tainted is the sticky-bit propagation flag (v0.3-fu-secrets #99).
	// True when ANY Command in the pipeline is itself Tainted. The
	// rationale is the unix-pipe semantics: a tainted stage's stdout
	// flows into the next stage's stdin, so the secret transits every
	// downstream stage and the *whole line* MUST be redacted from
	// history, telemetry, or any other persistent log.
	//
	// As with Command.Tainted, this is purely additive — pipelines
	// constructed without a tainted source are untouched.
	Tainted bool
}

// Parse converts a single line of input into a Pipeline.
//
// Errors are returned for unterminated quotes and other lexical defects.
// An empty or whitespace-only input returns an empty Pipeline (no commands)
// and a nil error — the REPL treats that as a no-op.
func Parse(input string) (Pipeline, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Pipeline{}, err
	}
	if len(tokens) == 0 {
		return Pipeline{}, nil
	}

	// v0.3-1 follow-up: peel off a single trailing `&` (background
	// marker) before splitting on pipes. Mid-line `&` is rejected;
	// only the absolute-last token may be the background marker.
	background := false
	for i, tok := range tokens {
		if tok.kind != tokAmp {
			continue
		}
		if i != len(tokens)-1 {
			return Pipeline{}, fmt.Errorf("syntax error: '&' must be the last token (mid-line `&` not supported)")
		}
		background = true
		tokens = tokens[:i]
	}

	// Split the token stream on the pipe separator to form pipeline stages.
	// An empty stage (no tokens between two pipes, or a leading/trailing
	// pipe) is a syntax error — POSIX-equivalent shells reject the same.
	var stages [][]string
	current := []string{}
	sawAnyStage := false
	for _, tok := range tokens {
		if tok.kind == tokPipe {
			if len(current) == 0 {
				return Pipeline{}, fmt.Errorf("syntax error: empty pipeline stage")
			}
			stages = append(stages, current)
			current = []string{}
			sawAnyStage = true
			continue
		}
		current = append(current, tok.value)
	}
	if len(current) == 0 {
		if sawAnyStage {
			return Pipeline{}, fmt.Errorf("syntax error: empty pipeline stage")
		}
		if background {
			// Bare `&` with nothing in front of it.
			return Pipeline{}, fmt.Errorf("syntax error: '&' with no command")
		}
		// No tokens at all after filtering — treated as empty input.
		return Pipeline{}, nil
	}
	stages = append(stages, current)

	commands := make([]Command, 0, len(stages))
	for _, stage := range stages {
		commands = append(commands, Command{
			Name: stage[0],
			Args: stage[1:],
		})
	}
	return Pipeline{Commands: commands, Background: background}, nil
}

// tokKind distinguishes a literal token from the pipe separator. The
// separator must remain distinct from a literal "|" inside quotes, hence
// the explicit kind rather than a string-compare in Parse.
type tokKind int

const (
	tokWord tokKind = iota
	tokPipe
	// tokAmp is an unquoted `&` — the background-job marker. Only
	// legal as the absolute-last token in a line; mid-line `&`
	// (POSIX statement separator) is rejected in this PR.
	tokAmp
)

type token struct {
	kind  tokKind
	value string
}

// tokenize walks the input rune-by-rune, emitting words separated by
// unquoted whitespace and the pipe operator. Single quotes preserve their
// contents literally; double quotes preserve whitespace but otherwise
// behave like single quotes for v0.1-1 (variable expansion is handled
// before Parse by the shell layer).
//
// An unterminated quote is a hard error — surfacing it satisfies the
// "no silent swallowing" rule from Code.md §1.
func tokenize(input string) ([]token, error) {
	var tokens []token
	var current strings.Builder
	inWord := false
	runes := []rune(input)

	flush := func() {
		if inWord {
			tokens = append(tokens, token{kind: tokWord, value: current.String()})
			current.Reset()
			inWord = false
		}
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'':
			// Find matching close quote; everything in between is literal.
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '\'' {
					end = j
					break
				}
			}
			if end == -1 {
				return nil, fmt.Errorf("unterminated single quote in input")
			}
			current.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case r == '"':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '"' {
					end = j
					break
				}
			}
			if end == -1 {
				return nil, fmt.Errorf("unterminated double quote in input")
			}
			current.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case r == '|':
			flush()
			tokens = append(tokens, token{kind: tokPipe})
		case r == '&':
			flush()
			tokens = append(tokens, token{kind: tokAmp})
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return tokens, nil
}
