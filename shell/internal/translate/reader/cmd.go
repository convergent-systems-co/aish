package reader

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// ReadCmd parses a cmd / .bat / .cmd script string into a
// translate.Script.
//
// MVP scope (v1.0-3 task #144):
//
//   - Line comments: `REM …`, `:: …`
//   - `@echo off` and bare `echo …` — modeled as a Command.
//   - `set NAME=VALUE` — modeled as Assign.
//   - `if [not] COND ( BODY ) [else ( ELSE )]` — single-arm; the
//     body is a single statement (cmd's idiomatic shape).
//   - `goto :EOF` — modeled as a Command (no-op semantics at run
//     time for the migrate output).
//   - Pipelines via `|`.
//
// Out of MVP scope: `for /F …`, `call`, labels other than `:EOF`,
// `setlocal`/`endlocal`, parenthesised multi-statement bodies past
// a single line. Each surfaces as a translate.Unknown so the user
// sees the gap rather than a silent half-translation.
func ReadCmd(src string) (*translate.Script, error) {
	lines := strings.Split(src, "\n")
	stmts := []translate.Statement{}
	for i, raw := range lines {
		ln := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(ln)
		sourceLine := i + 1
		if trimmed == "" {
			continue
		}
		// Strip a leading `@` (cmd convention to suppress echo on
		// individual lines). It's purely a presentation flag; the
		// semantic command underneath is unchanged.
		if strings.HasPrefix(trimmed, "@") {
			trimmed = strings.TrimSpace(trimmed[1:])
		}
		lower := strings.ToLower(trimmed)
		// Comment forms.
		if strings.HasPrefix(lower, "rem") && (len(lower) == 3 || lower[3] == ' ' || lower[3] == '\t') {
			stmts = append(stmts, translate.Comment{Text: trimmed}.WithLine(sourceLine))
			continue
		}
		if strings.HasPrefix(trimmed, "::") {
			stmts = append(stmts, translate.Comment{Text: trimmed}.WithLine(sourceLine))
			continue
		}
		// `set NAME=VALUE` assignment.
		if strings.HasPrefix(lower, "set ") {
			rest := strings.TrimSpace(trimmed[4:])
			if name, val, ok := cmdSplitAssignment(rest); ok {
				stmts = append(stmts, translate.Assign{Name: name, Value: val}.WithLine(sourceLine))
				continue
			}
			// `set` with no `=` is a print-variable form — surface as
			// a regular command so `aish run` falls back to passthrough.
		}
		// `if [not] COND ( BODY ) [else ( ELSE )]` — single-line form.
		if strings.HasPrefix(lower, "if ") || strings.HasPrefix(lower, "if(") {
			stmts = append(stmts, cmdParseIfLine(trimmed, sourceLine))
			continue
		}
		// `goto :EOF` is the well-known "exit" idiom — surface as a
		// recognized Command so the explain engine reads it as "exit".
		if strings.HasPrefix(lower, "goto ") {
			rest := strings.TrimSpace(trimmed[5:])
			if strings.EqualFold(rest, ":eof") {
				stmts = append(stmts, translate.Command{
					Name: "goto",
					Args: []string{":EOF"},
				}.WithLine(sourceLine))
				continue
			}
			// Any other goto label is out of MVP scope.
			stmts = append(stmts, translate.Unknown{
				Reason: "goto labels other than :EOF unsupported (MVP)",
				Source: trimmed,
			}.WithLine(sourceLine))
			continue
		}
		// `for /F` / `call` / `setlocal` / labels — surface as Unknown.
		if reason, ok := cmdClassifyUnsupported(lower, trimmed); ok {
			stmts = append(stmts, translate.Unknown{
				Reason: reason,
				Source: trimmed,
			}.WithLine(sourceLine))
			continue
		}
		// Everything else — pipeline / command.
		stmts = append(stmts, cmdParseStatementLine(trimmed, sourceLine))
	}
	return &translate.Script{
		Dialect:    translate.DialectCmd,
		Statements: stmts,
	}, nil
}

// cmdSplitAssignment splits `NAME=VALUE` from a set statement
// (cmd accepts spaces around `=` only inconsistently — we mirror
// the `set` documented behavior of trimming the value).
func cmdSplitAssignment(rest string) (string, string, bool) {
	eq := strings.Index(rest, "=")
	if eq <= 0 {
		return "", "", false
	}
	name := strings.TrimSpace(rest[:eq])
	if name == "" {
		return "", "", false
	}
	val := strings.TrimSpace(rest[eq+1:])
	val = stripOuterQuotes(val)
	return name, val, true
}

// cmdParseIfLine handles the single-line `if [not] COND ( BODY )
// [else ( ELSE )]` shape. Multi-line if-blocks are out of MVP
// scope and surface as Unknown.
func cmdParseIfLine(line string, sourceLine int) translate.Statement {
	// Find the first `(` — everything before it is the if-condition,
	// preceded by `if` (and optional `not`).
	openParen := strings.Index(line, "(")
	if openParen < 0 {
		return translate.Unknown{
			Reason: "multi-line `if` blocks unsupported (MVP)",
			Source: line,
		}.WithLine(sourceLine)
	}
	header := strings.TrimSpace(line[:openParen])
	// Strip the leading `if` (case-insensitive).
	header = strings.TrimSpace(header[2:])
	// Optional `not`.
	negate := false
	if strings.HasPrefix(strings.ToLower(header), "not ") {
		header = strings.TrimSpace(header[4:])
		negate = true
	}
	test := translate.Command{
		Name: "if",
		Args: []string{header},
	}.WithLine(sourceLine)
	if negate {
		test.Args = []string{"not", header}
	}
	// Body lives inside the first balanced `(...)`.
	closeParen := matchCmdParen(line, openParen)
	if closeParen < 0 {
		return translate.Unknown{
			Reason: "if body missing closing `)`",
			Source: line,
		}.WithLine(sourceLine)
	}
	bodyText := strings.TrimSpace(line[openParen+1 : closeParen])
	body := []translate.Statement{}
	if bodyText != "" {
		body = append(body, cmdParseStatementLine(bodyText, sourceLine))
	}
	cond := translate.Cond{
		BaseStmt: translate.BaseStmt{Line: sourceLine},
		Branches: []translate.CondBranch{{Test: test, Body: body}},
	}
	// Optional ` else ( ... )` immediately after the closing paren.
	tail := strings.TrimSpace(line[closeParen+1:])
	if strings.HasPrefix(strings.ToLower(tail), "else") {
		tail = strings.TrimSpace(tail[4:])
		if strings.HasPrefix(tail, "(") {
			elseClose := matchCmdParen(tail, 0)
			if elseClose > 0 {
				elseText := strings.TrimSpace(tail[1:elseClose])
				if elseText != "" {
					cond.Else = []translate.Statement{
						cmdParseStatementLine(elseText, sourceLine),
					}
				} else {
					cond.Else = []translate.Statement{}
				}
			}
		}
	}
	return cond
}

// matchCmdParen returns the index of the `)` matching the `(` at
// open in s, or -1 when unmatched.
func matchCmdParen(s string, open int) int {
	if open < 0 || open >= len(s) || s[open] != '(' {
		return -1
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// cmdClassifyUnsupported recognises out-of-MVP cmd constructs.
// `lower` is the lowercased trimmed line; `original` is preserved
// for the Unknown.Source field.
func cmdClassifyUnsupported(lower, original string) (string, bool) {
	switch {
	case strings.HasPrefix(lower, "for "):
		return "for loops unsupported (MVP)", true
	case strings.HasPrefix(lower, "call "):
		return "call unsupported (MVP)", true
	case strings.HasPrefix(lower, "setlocal"), strings.HasPrefix(lower, "endlocal"):
		return "setlocal/endlocal unsupported (MVP)", true
	case strings.HasPrefix(original, ":") && !strings.HasPrefix(original, "::"):
		// `:label` defines a goto label. Not in MVP.
		return "labels unsupported (MVP)", true
	}
	return "", false
}

// cmdParseStatementLine parses one cmd statement — command or
// pipeline. We deliberately keep this minimal: no redirect
// detection (cmd's `>`/`>>`/`<` are valid but uncommon in admin
// scripts; explicitly deferred to v1.1).
func cmdParseStatementLine(line string, sourceLine int) translate.Statement {
	tokens, err := cmdTokenize(line)
	if err != nil {
		return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
	}
	if len(tokens) == 0 {
		return translate.Unknown{Reason: "empty statement", Source: line}.WithLine(sourceLine)
	}
	stages := cmdSplitOnPipe(tokens)
	if len(stages) > 1 {
		pipe := translate.Pipe{}
		for _, st := range stages {
			if len(st) == 0 {
				return translate.Unknown{Reason: "empty pipeline stage", Source: line}.WithLine(sourceLine)
			}
			pipe.Stages = append(pipe.Stages, translate.Command{
				Name: st[0],
				Args: append([]string{}, st[1:]...),
			}.WithLine(sourceLine))
		}
		return pipe.WithLine(sourceLine)
	}
	cmd := translate.Command{
		Name: tokens[0],
		Args: append([]string{}, tokens[1:]...),
	}.WithLine(sourceLine)
	return cmd
}

// cmdTokenize splits a line into tokens, honoring double quotes
// (cmd's preferred quote form) and `|` as a pipe separator. Cmd
// has no concept of single quotes; backticks are not its escape
// character.
func cmdTokenize(line string) ([]string, error) {
	out := []string{}
	var cur strings.Builder
	inWord := false
	runes := []rune(line)
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '"':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '"' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, &parseError{msg: "unterminated double quote"}
			}
			cur.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case r == '|':
			flush()
			out = append(out, "|")
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out, nil
}

// cmdSplitOnPipe slices a token list on `|` separators.
func cmdSplitOnPipe(tokens []string) [][]string {
	out := [][]string{}
	cur := []string{}
	for _, t := range tokens {
		if t == "|" {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
