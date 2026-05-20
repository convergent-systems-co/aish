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
func Parse(input string) (Pipeline, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Pipeline{}, err
	}
	if len(tokens) == 0 {
		return Pipeline{}, nil
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
	return Pipeline{Commands: commands}, nil
}

// tokKind distinguishes a literal token from the pipe separator. The
// separator must remain distinct from a literal "|" inside quotes, hence
// the explicit kind rather than a string-compare in Parse.
type tokKind int

const (
	tokWord tokKind = iota
	tokPipe
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
