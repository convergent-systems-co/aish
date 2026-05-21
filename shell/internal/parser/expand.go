// Package parser — expansion passes (v0.3-1 follow-up, issue #88).
//
// Three independent expansions run *after* tokenization but *before*
// dispatch. They consume a parsed Pipeline (or, for cmdsub, a raw input
// string ahead of Parse) and yield a new Pipeline whose Args have been
// expanded according to POSIX-ish rules.
//
// Scope (this PR):
//   - Command substitution `$(cmd)` — recursive via an injected
//     CmdSubExecutor (the Shell satisfies this). Backticks NOT
//     supported; nested `$(...)` is supported up to a depth cap.
//   - Brace expansion `{a,b,c}` and numeric ranges `{1..5}`. Combined
//     with prefix/suffix: `pre{a,b}post`. Nested braces NOT supported
//     this PR.
//   - Glob expansion `*.go`, `???.txt`, `cmd/*/main.go` — every token
//     containing an unquoted `*`, `?`, or `[` is matched against the
//     filesystem rooted at the shell's cwd. No match → token preserved
//     literally (POSIX `nullglob`-off / bash default).
//
// Out of scope: nested braces, `**` recursive globs, backticks.
//
// Order of operations (matches POSIX where it matters): the caller
// runs $VAR via env.Expand first; ExpandLine then handles cmdsub on the
// raw string; Parse tokenizes; ExpandPipeline finishes with brace +
// glob per token. Alias rewriting (a separate shell-level concern) runs
// after ExpandPipeline returns.
package parser

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// CmdSubExecutor runs `cmd` as a sub-shell pipeline and returns its
// stdout (newline-trimmed). Satisfied by shell.Shell so the parser
// package never imports shell internals.
//
// The contract is: read no input from caller stdin, write nothing to
// caller stdout; only capture the sub-command's stdout. Stderr may go
// to the supplied stderr writer. A non-nil error halts expansion.
type CmdSubExecutor interface {
	RunForCapture(cmd string, stderr io.Writer) (string, error)
}

// ExpandContext bundles the inputs each pass needs without inflating
// function signatures. Cwd is the shell's working directory (glob
// root). CmdSub may be nil — when nil, `$(...)` is left literal so
// pre-shell unit tests don't need a runtime.
type ExpandContext struct {
	Cwd    string
	CmdSub CmdSubExecutor
	Stderr io.Writer
}

// maxCmdSubDepth caps recursive `$(...)` nesting so a maliciously
// crafted line cannot spawn unbounded children. 16 is well over any
// realistic interactive use.
const maxCmdSubDepth = 16

// ExpandLine runs `$(...)` command substitution on raw input BEFORE
// tokenization. Returns the substituted line. Single-quoted regions
// are left untouched per POSIX; double-quoted regions DO undergo
// cmdsub (matches bash).
//
// `$VAR` is the caller's responsibility (env.Env.Expand handles it
// before this function sees the input).
func ExpandLine(input string, ctx ExpandContext) (string, error) {
	return expandCmdSub(input, ctx, 0)
}

func expandCmdSub(input string, ctx ExpandContext, depth int) (string, error) {
	if depth > maxCmdSubDepth {
		return "", fmt.Errorf("command substitution: nesting deeper than %d", maxCmdSubDepth)
	}
	if !strings.Contains(input, "$(") {
		return input, nil
	}
	if ctx.CmdSub == nil {
		// No executor wired (e.g. parser-only test). Leave $(...)
		// literal so the caller can decide whether to fail or pass.
		return input, nil
	}

	var b strings.Builder
	b.Grow(len(input))
	runes := []rune(input)
	var inSingle bool
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		// Single quotes: nothing inside expands. Copy through verbatim,
		// including the quotes (the tokenizer downstream strips them).
		if r == '\'' && !inSingle {
			inSingle = true
			b.WriteRune(r)
			continue
		}
		if r == '\'' && inSingle {
			inSingle = false
			b.WriteRune(r)
			continue
		}
		if inSingle {
			b.WriteRune(r)
			continue
		}
		// Escaped `$(` — pass literally.
		if r == '\\' && i+1 < len(runes) {
			b.WriteRune(r)
			b.WriteRune(runes[i+1])
			i++
			continue
		}
		if r == '$' && i+1 < len(runes) && runes[i+1] == '(' {
			// Walk to the matching ')' with depth tracking so a nested
			// $(...) inside the body doesn't terminate us early.
			end := findMatchingParen(runes, i+1)
			if end < 0 {
				return "", fmt.Errorf("command substitution: unterminated $(")
			}
			inner := string(runes[i+2 : end])
			// Recurse: the inner command may itself contain $(...).
			expanded, err := expandCmdSub(inner, ctx, depth+1)
			if err != nil {
				return "", err
			}
			out, err := ctx.CmdSub.RunForCapture(expanded, ctx.Stderr)
			if err != nil {
				return "", fmt.Errorf("command substitution %q: %w", inner, err)
			}
			// Trim trailing newlines (POSIX rule for $(cmd)).
			out = strings.TrimRight(out, "\n")
			b.WriteString(out)
			i = end
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// findMatchingParen returns the index of the `)` that matches the `(`
// at runes[startParen]. Returns -1 if unbalanced. Skips over quoted
// regions so `$(echo ")")` parses correctly.
func findMatchingParen(runes []rune, startParen int) int {
	if startParen >= len(runes) || runes[startParen] != '(' {
		return -1
	}
	depth := 1
	var inSingle, inDouble bool
	for i := startParen + 1; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '(':
			if !inSingle && !inDouble {
				depth++
			}
		case ')':
			if !inSingle && !inDouble {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// ExpandPipeline runs brace + glob expansion on every Arg of every
// Command in p. The Name field is left untouched — POSIX shells don't
// brace/glob the command name itself (rare exceptions exist; we follow
// the dominant convention).
//
// Brace expansion runs first because it produces multiple tokens from
// one input; glob then runs on each resulting token.
func ExpandPipeline(p Pipeline, ctx ExpandContext) (Pipeline, error) {
	out := Pipeline{Commands: make([]Command, 0, len(p.Commands))}
	for _, c := range p.Commands {
		nc := Command{Name: c.Name}
		for _, a := range c.Args {
			braceExpanded := expandBrace(a)
			for _, b := range braceExpanded {
				globbed := expandGlob(b, ctx.Cwd)
				nc.Args = append(nc.Args, globbed...)
			}
		}
		out.Commands = append(out.Commands, nc)
	}
	return out, nil
}

// expandBrace returns one or more tokens produced by expanding a SINGLE
// brace group in input. Inputs with no `{`/`,`/`}` return [input]
// unchanged. Numeric ranges `{1..5}` produce `1 2 3 4 5`. Nested braces
// are NOT supported this PR — the outer brace is expanded; nested
// groups remain literal characters.
//
// Examples:
//
//	"foo"           -> ["foo"]
//	"{a,b,c}"       -> ["a", "b", "c"]
//	"pre{a,b}post"  -> ["preapost", "prebpost"]
//	"{1..3}"        -> ["1", "2", "3"]
//	"x{1..3}y"      -> ["x1y", "x2y", "x3y"]
func expandBrace(input string) []string {
	open := strings.IndexByte(input, '{')
	if open < 0 {
		return []string{input}
	}
	close := strings.IndexByte(input[open:], '}')
	if close < 0 {
		// No matching close — treat as literal.
		return []string{input}
	}
	close += open
	prefix := input[:open]
	body := input[open+1 : close]
	suffix := input[close+1:]
	parts := braceParts(body)
	if len(parts) == 0 {
		// `{}` or `{1..1}` collapse to literal.
		return []string{input}
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// Recurse on suffix so `{a,b}{c,d}` produces the full cross
		// product. Suffix may carry its own brace group.
		for _, tail := range expandBrace(suffix) {
			out = append(out, prefix+p+tail)
		}
	}
	return out
}

// braceParts splits the body of a brace group into its expansion list.
// Handles two forms:
//
//   - Comma list:  `a,b,c`   -> ["a", "b", "c"]
//   - Numeric:     `1..5`    -> ["1", "2", "3", "4", "5"]
//
// Returns nil for malformed bodies (empty, single comma-less token,
// or non-numeric range).
func braceParts(body string) []string {
	if body == "" {
		return nil
	}
	if strings.Contains(body, "..") {
		lo, hi, ok := numericRange(body)
		if !ok {
			return nil
		}
		out := make([]string, 0, abs(hi-lo)+1)
		if lo <= hi {
			for i := lo; i <= hi; i++ {
				out = append(out, strconv.Itoa(i))
			}
		} else {
			for i := lo; i >= hi; i-- {
				out = append(out, strconv.Itoa(i))
			}
		}
		return out
	}
	if !strings.Contains(body, ",") {
		// `{single}` is NOT a brace expansion in bash either.
		return nil
	}
	return strings.Split(body, ",")
}

// numericRange parses "L..H" forms. Returns (lo, hi, ok=true) on a
// valid integer pair; (_, _, false) otherwise.
func numericRange(body string) (int, int, bool) {
	parts := strings.SplitN(body, "..", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	hi, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// expandGlob runs filepath.Glob on token rooted at cwd. When token has
// no glob metacharacters (`*`, `?`, `[`), returns [token] unchanged.
// When there are no matches, also returns [token] unchanged — matches
// bash's default (no `failglob`).
//
// The returned paths are relative to cwd when the input was relative;
// absolute when the input was absolute. We do NOT collapse `./` or
// remove duplicates — POSIX glob output is sorted lexicographically by
// filepath.Glob already.
func expandGlob(token, cwd string) []string {
	if !strings.ContainsAny(token, "*?[") {
		return []string{token}
	}
	pattern := token
	if !filepath.IsAbs(pattern) && cwd != "" {
		pattern = filepath.Join(cwd, token)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return []string{token}
	}
	// If we joined with cwd, strip the cwd prefix so callers see
	// the same relative form they typed.
	if pattern != token && cwd != "" {
		prefix := cwd + string(filepath.Separator)
		out := make([]string, len(matches))
		for i, m := range matches {
			out[i] = strings.TrimPrefix(m, prefix)
		}
		return out
	}
	return matches
}
