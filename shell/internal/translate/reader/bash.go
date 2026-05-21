// Package reader holds the dialect-specific lexer + parser entry
// points for bash, zsh, and fish. Each ReadFoo function turns a
// source string into a translate.Script. Constructs outside the
// MVP scope contract are emitted as translate.Unknown statements —
// they are never silently dropped.
package reader

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// ReadBash parses a bash source string into a translate.Script.
//
// The parser is a two-stage hand-roll:
//  1. lexLines splits source into logical lines (strip trailing `\`
//     continuations and re-merge), preserving the 1-indexed source
//     line of each fragment.
//  2. parseBlock walks the resulting line slice, recursively
//     descending into `if … fi`, `for … done`, `while … done`,
//     `case … esac`, and `name() { … }` blocks.
//
// Comments are preserved as translate.Comment nodes. Anything the
// parser can't classify becomes translate.Unknown with a Reason
// naming the construct.
func ReadBash(src string) (*translate.Script, error) {
	lines := lexLines(src)
	stmts, _ := parseBlock(lines, 0, nil)
	return &translate.Script{
		Dialect:    translate.DialectBash,
		Statements: stmts,
	}, nil
}

// physicalLine carries the original 1-indexed source line and the
// (possibly continuation-merged) text we'll feed to the parser.
type physicalLine struct {
	line int
	text string
}

// lexLines splits raw source into one physicalLine per logical
// statement-line, merging backslash continuations. Trailing `;`
// inside a line is left for the statement parser; we don't try to
// split `cmd; cmd; cmd` here because the bash grammar uses `;`
// inside `if`/`for`/`case` headers and getting it right requires
// the statement-context anyway.
func lexLines(src string) []physicalLine {
	out := make([]physicalLine, 0, 32)
	raw := strings.Split(src, "\n")
	i := 0
	for i < len(raw) {
		ln := raw[i]
		// Strip trailing \r from CRLF inputs.
		ln = strings.TrimRight(ln, "\r")
		startLine := i + 1
		// Backslash continuation: while line ends with `\` (an even
		// number of backslashes does NOT escape; bash is the same).
		for endsWithContinuation(ln) && i+1 < len(raw) {
			ln = ln[:len(ln)-1] + " " + strings.TrimRight(raw[i+1], "\r")
			i++
		}
		out = append(out, physicalLine{line: startLine, text: ln})
		i++
	}
	return out
}

// endsWithContinuation reports whether s ends with an unescaped `\`.
// A `\\` (escaped backslash) at end-of-line is NOT a continuation.
func endsWithContinuation(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// parseBlock walks lines starting at idx until either (a) lines is
// exhausted or (b) a terminator token is encountered. terminators is
// the set of keywords (in the bash header position of the line —
// first token) that end the current block.
//
// Returns the parsed statements and the index *after* the last line
// consumed. The terminator line itself is NOT consumed — the caller
// decides what to do with it.
func parseBlock(lines []physicalLine, idx int, terminators []string) ([]translate.Statement, int) {
	stmts := make([]translate.Statement, 0, 8)
	for idx < len(lines) {
		ln := lines[idx]
		trimmed := strings.TrimSpace(ln.text)
		if trimmed == "" {
			idx++
			continue
		}
		// Strip a leading `#!shebang` if it's the first non-empty line —
		// we treat shebangs as comments.
		// Comments.
		if strings.HasPrefix(trimmed, "#") {
			stmts = append(stmts, translate.Comment{
				Text: trimmed,
			}.WithLine(ln.line))
			idx++
			continue
		}
		// Terminator check — first whitespace-separated token.
		first := firstWord(trimmed)
		for _, t := range terminators {
			if first == t {
				return stmts, idx
			}
		}
		// Block constructs.
		switch first {
		case "if":
			c, next := parseIf(lines, idx)
			stmts = append(stmts, c)
			idx = next
			continue
		case "for":
			l, next := parseFor(lines, idx)
			stmts = append(stmts, l)
			idx = next
			continue
		case "while":
			l, next := parseWhile(lines, idx)
			stmts = append(stmts, l)
			idx = next
			continue
		case "case":
			c, next := parseCase(lines, idx)
			stmts = append(stmts, c)
			idx = next
			continue
		case "function":
			f, next := parseFunctionKeyword(lines, idx)
			stmts = append(stmts, f)
			idx = next
			continue
		}
		// `name() { … }` function definition (bash/zsh).
		if isFunctionDefHeader(trimmed) {
			f, next := parseFunctionParens(lines, idx)
			stmts = append(stmts, f)
			idx = next
			continue
		}
		// Detect MVP-out-of-scope multi-line constructs early so we
		// surface them as Unknown rather than half-parsing.
		if reason, ok := classifyUnsupported(trimmed); ok {
			stmts = append(stmts, translate.Unknown{
				Reason: reason,
				Source: trimmed,
			}.WithLine(ln.line))
			idx++
			continue
		}
		// Single-line statement: assignment, command, or pipe.
		st := parseStatementLine(trimmed, ln.line)
		stmts = append(stmts, st)
		idx++
	}
	return stmts, idx
}

// firstWord returns the leading whitespace-separated token. Used to
// peek at block-keyword position without consuming the line.
func firstWord(s string) string {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

// isFunctionDefHeader matches `name() {` and `name () {` shapes
// (possibly with the `{` on a following line; we accept either).
// Conservative: requires `()` and identifier-like name.
func isFunctionDefHeader(s string) bool {
	open := strings.Index(s, "(")
	if open <= 0 {
		return false
	}
	close := strings.Index(s, ")")
	if close != open+1 {
		return false
	}
	name := strings.TrimSpace(s[:open])
	if !isIdent(name) {
		return false
	}
	return true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// classifyUnsupported recognises out-of-MVP constructs by lexical
// fingerprint and returns a human-readable reason. Returns ok=false
// when the line looks like ordinary parseable territory.
//
// We are deliberately conservative: false-positives here downgrade a
// runnable line to Unknown. So we only flag the obvious markers.
func classifyUnsupported(line string) (string, bool) {
	switch {
	case strings.Contains(line, "<<<"):
		return "here-string (<<<) unsupported", true
	case strings.Contains(line, "<<"):
		return "heredoc (<<) unsupported", true
	case strings.HasPrefix(line, "[[") || strings.Contains(line, " [[ "):
		return "extended test [[ ]] unsupported", true
	case strings.Contains(line, "$(("):
		return "arithmetic expansion $(( )) unsupported", true
	case strings.Contains(line, "((") && !strings.Contains(line, "$(("):
		return "arithmetic command (( )) unsupported", true
	case strings.Contains(line, " && ") || strings.Contains(line, " || "):
		return "short-circuit && / || chains unsupported (MVP)", true
	case strings.Contains(line, "<("):
		return "process substitution <( ) unsupported", true
	case strings.Contains(line, ">("):
		return "process substitution >( ) unsupported", true
	case strings.Contains(line, "`"):
		return "backtick command substitution unsupported (use $(...))", true
	case strings.HasPrefix(line, "select "):
		return "select unsupported", true
	case strings.HasPrefix(line, "trap "):
		return "trap unsupported", true
	}
	return "", false
}

// parseIf consumes `if TEST; then … [elif TEST; then …] [else …] fi`.
// Returns the Cond (as a Statement) and the index of the line AFTER `fi`.
//
// Header form: we accept both
//   - `if TEST; then`   (single-line header)
//   - `if TEST`         (with `then` on the next line)
// — but the body terminator is always the `then`/`else`/`elif`/`fi`
// keyword in first position.
func parseIf(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	cond := translate.Cond{BaseStmt: translate.BaseStmt{Line: startLine}}
	// Consume the header — the part between `if` and `then`/`;`.
	test, next := consumeUntilKeyword(lines, idx, "if", []string{"then"})
	cond.Branches = append(cond.Branches, translate.CondBranch{Test: test})
	idx = next
	for idx < len(lines) {
		// We are positioned at a line whose first token is `then` —
		// possibly with trailing content (`then echo foo`). Advance.
		idx = skipKeywordLine(lines, idx, "then")
		body, next := parseBlock(lines, idx, []string{"elif", "else", "fi"})
		cond.Branches[len(cond.Branches)-1].Body = body
		idx = next
		if idx >= len(lines) {
			break
		}
		head := firstWord(strings.TrimSpace(lines[idx].text))
		switch head {
		case "elif":
			test, next := consumeUntilKeyword(lines, idx, "elif", []string{"then"})
			cond.Branches = append(cond.Branches, translate.CondBranch{Test: test})
			idx = next
		case "else":
			idx = skipKeywordLine(lines, idx, "else")
			body, next := parseBlock(lines, idx, []string{"fi"})
			cond.Else = body
			idx = next
		case "fi":
			idx = skipKeywordLine(lines, idx, "fi")
			return cond, idx
		default:
			// Defensive: unexpected — bail and let the rest re-parse.
			return cond, idx
		}
	}
	return cond, idx
}

// parseFor consumes `for VAR in WORDS; do … done`. C-style `for ((…))`
// is classified Unknown earlier.
func parseFor(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(lines[idx].text)
	// Strip leading `for ` then split off the `; do` (or newline `do`).
	rest := strings.TrimSpace(strings.TrimPrefix(header, "for"))
	rest = strings.TrimSuffix(rest, ";")
	// Peel off a trailing `; do` if present on the same line.
	if i := strings.LastIndex(rest, ";"); i >= 0 {
		tail := strings.TrimSpace(rest[i+1:])
		if tail == "do" || tail == "" {
			rest = strings.TrimSpace(rest[:i])
		}
	}
	// Now rest is `VAR in WORDS` (or `VAR` for the implicit-args form).
	varName := rest
	var words []string
	if i := strings.Index(rest, " in "); i >= 0 {
		varName = strings.TrimSpace(rest[:i])
		wordsPart := strings.TrimSpace(rest[i+4:])
		words = splitWords(wordsPart)
	}
	idx++
	idx = skipKeywordLine(lines, idx, "do")
	body, next := parseBlock(lines, idx, []string{"done"})
	idx = next
	idx = skipKeywordLine(lines, idx, "done")
	return translate.Loop{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Kind:     translate.LoopFor,
		Var:      varName,
		Words:    words,
		Body:     body,
	}, idx
}

func parseWhile(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	test, next := consumeUntilKeyword(lines, idx, "while", []string{"do"})
	idx = next
	idx = skipKeywordLine(lines, idx, "do")
	body, next := parseBlock(lines, idx, []string{"done"})
	idx = next
	idx = skipKeywordLine(lines, idx, "done")
	return translate.Loop{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Kind:     translate.LoopWhile,
		Test:     test,
		Body:     body,
	}, idx
}

// parseCase consumes `case WORD in PAT) … ;; … esac`. Each arm runs
// until `;;` (we accept it on its own line or trailing the body).
func parseCase(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(lines[idx].text)
	rest := strings.TrimSpace(strings.TrimPrefix(header, "case"))
	word := rest
	if i := strings.Index(rest, " in"); i >= 0 {
		word = strings.TrimSpace(rest[:i])
	}
	idx++
	c := translate.Case{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Word:     stripOuterQuotes(word),
	}
	for idx < len(lines) {
		first := firstWord(strings.TrimSpace(lines[idx].text))
		if first == "esac" {
			idx = skipKeywordLine(lines, idx, "esac")
			return c, idx
		}
		arm, next := parseCaseArm(lines, idx)
		c.Arms = append(c.Arms, arm)
		idx = next
	}
	return c, idx
}

func parseCaseArm(lines []physicalLine, idx int) (translate.CaseArm, int) {
	header := strings.TrimSpace(lines[idx].text)
	// Pattern part is everything before `)`. Multiple patterns are `|`-
	// separated (NOT a pipe — this is the case-arm syntax).
	end := strings.Index(header, ")")
	if end < 0 {
		// Malformed — treat the whole line as a single unknown arm so
		// the surrounding parse can continue.
		idx++
		return translate.CaseArm{
			Patterns: []string{header},
			Body:     []translate.Statement{translate.Unknown{Reason: "case arm without closing )", Source: header}.WithLine(lines[idx-1].line)},
		}, idx
	}
	pats := strings.Split(header[:end], "|")
	for i := range pats {
		pats[i] = strings.TrimSpace(pats[i])
	}
	// Remainder of the line after `)` is the start of the body.
	bodyRest := strings.TrimSpace(header[end+1:])
	idx++
	// If the body is on the same line and ends with `;;`, it's a one-
	// liner.
	if strings.HasSuffix(bodyRest, ";;") {
		inner := strings.TrimSpace(strings.TrimSuffix(bodyRest, ";;"))
		body := []translate.Statement{}
		if inner != "" {
			body = append(body, parseStatementLine(inner, lines[idx-1].line))
		}
		return translate.CaseArm{Patterns: pats, Body: body}, idx
	}
	// Multi-line body — collect until `;;`.
	body := []translate.Statement{}
	if bodyRest != "" {
		body = append(body, parseStatementLine(bodyRest, lines[idx-1].line))
	}
	for idx < len(lines) {
		ln := lines[idx]
		trimmed := strings.TrimSpace(ln.text)
		if trimmed == ";;" {
			idx++
			break
		}
		if strings.HasSuffix(trimmed, ";;") {
			inner := strings.TrimSpace(strings.TrimSuffix(trimmed, ";;"))
			if inner != "" {
				body = append(body, parseStatementLine(inner, ln.line))
			}
			idx++
			break
		}
		// Defensive: `esac` ends a missing `;;` arm.
		if firstWord(trimmed) == "esac" {
			break
		}
		body = append(body, parseStatementLine(trimmed, ln.line))
		idx++
	}
	return translate.CaseArm{Patterns: pats, Body: body}, idx
}

// parseFunctionKeyword consumes the `function NAME { … }` form.
func parseFunctionKeyword(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(lines[idx].text)
	rest := strings.TrimSpace(strings.TrimPrefix(header, "function"))
	name := rest
	if i := strings.IndexAny(rest, "({ "); i >= 0 {
		name = strings.TrimSpace(rest[:i])
	}
	idx++
	// If the header had a `{` we're already inside; otherwise consume
	// the next `{` line.
	idx = skipOpenBrace(lines, idx)
	body, next := parseBlock(lines, idx, []string{"}"})
	idx = next
	idx = skipCloseBrace(lines, idx)
	return translate.FuncDef{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Name:     name,
		Body:     body,
	}, idx
}

// parseFunctionParens consumes `name() { … }`.
func parseFunctionParens(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(lines[idx].text)
	name := strings.TrimSpace(header[:strings.Index(header, "(")])
	idx++
	idx = skipOpenBrace(lines, idx)
	body, next := parseBlock(lines, idx, []string{"}"})
	idx = next
	idx = skipCloseBrace(lines, idx)
	return translate.FuncDef{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Name:     name,
		Body:     body,
	}, idx
}

func skipOpenBrace(lines []physicalLine, idx int) int {
	if idx >= len(lines) {
		return idx
	}
	t := strings.TrimSpace(lines[idx].text)
	if t == "{" {
		return idx + 1
	}
	return idx
}

func skipCloseBrace(lines []physicalLine, idx int) int {
	if idx >= len(lines) {
		return idx
	}
	t := strings.TrimSpace(lines[idx].text)
	if t == "}" {
		return idx + 1
	}
	return idx
}

// consumeUntilKeyword reads the header of a control-flow construct:
//   `KW <words…> [; KW2]`
// or, when KW2 is on the next line, just `KW <words…>` with KW2 on
// the following line's first token. Returns the parsed Test statement
// and the index of the line carrying KW2.
func consumeUntilKeyword(lines []physicalLine, idx int, headKW string, terminators []string) (translate.Statement, int) {
	header := strings.TrimSpace(lines[idx].text)
	header = strings.TrimSpace(strings.TrimPrefix(header, headKW))
	// Does the header have a `;` followed by a terminator?
	for _, term := range terminators {
		if i := strings.LastIndex(header, "; "+term); i >= 0 {
			head := strings.TrimSpace(header[:i])
			st := parseStatementLine(head, lines[idx].line)
			return st, idx + 1 // caller will skip term keyword
		}
		if strings.HasSuffix(header, ";"+term) {
			head := strings.TrimSpace(strings.TrimSuffix(header, ";"+term))
			st := parseStatementLine(head, lines[idx].line)
			return st, idx + 1
		}
		if strings.HasSuffix(header, "; "+term) {
			// matched above
		}
	}
	// Terminator is on a following line. Consume the rest of the
	// header (drop any trailing `;`).
	header = strings.TrimSuffix(header, ";")
	header = strings.TrimSpace(header)
	st := parseStatementLine(header, lines[idx].line)
	return st, idx + 1
}

func skipKeywordLine(lines []physicalLine, idx int, kw string) int {
	if idx >= len(lines) {
		return idx
	}
	t := strings.TrimSpace(lines[idx].text)
	if t == kw {
		return idx + 1
	}
	if strings.HasPrefix(t, kw+" ") || strings.HasPrefix(t, kw+"\t") {
		// Replace this line with its remainder so the body parser sees
		// the trailing content. Mutating the slice is safe because
		// each parser invocation gets its own []physicalLine.
		rest := strings.TrimSpace(strings.TrimPrefix(t, kw))
		lines[idx].text = rest
		return idx
	}
	return idx
}

// parseStatementLine parses a single non-block source line into a
// Statement: Assign, Command, or Pipe (and Unknown for fall-through).
// Pipes are detected first because they outscope assignments and
// commands.
func parseStatementLine(line string, sourceLine int) translate.Statement {
	tokens, err := tokenizeBash(line)
	if err != nil {
		return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
	}
	if len(tokens) == 0 {
		return translate.Unknown{Reason: "empty statement", Source: line}.WithLine(sourceLine)
	}
	// Pipe?
	stages := splitOnPipe(tokens)
	if len(stages) > 1 {
		pipe := translate.Pipe{}
		for _, st := range stages {
			c, err := tokensToCommand(st, sourceLine)
			if err != nil {
				return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
			}
			pipe.Stages = append(pipe.Stages, c)
		}
		return pipe.WithLine(sourceLine)
	}
	// Assignment? `NAME=value` with no spaces inside the NAME=value
	// token AND that is the only token on the line.
	if len(tokens) == 1 {
		if name, val, ok := splitAssignment(tokens[0].value); ok {
			return translate.Assign{
				Name:  name,
				Value: val,
			}.WithLine(sourceLine)
		}
	}
	c, err := tokensToCommand(tokens, sourceLine)
	if err != nil {
		return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
	}
	return c
}

// splitAssignment splits `NAME=value` into (NAME, value, true) iff the
// LHS is a valid identifier. Otherwise (_, _, false).
func splitAssignment(tok string) (string, string, bool) {
	eq := strings.Index(tok, "=")
	if eq <= 0 {
		return "", "", false
	}
	name := tok[:eq]
	if !isIdent(name) {
		return "", "", false
	}
	val := tok[eq+1:]
	val = stripOuterQuotes(val)
	return name, val, true
}

// tokensToCommand collects argv + redirects from a flat token list.
// Returns an error when a redirect operator has no target (e.g.
// trailing `>`).
func tokensToCommand(tokens []bashToken, sourceLine int) (translate.Command, error) {
	cmd := translate.Command{}
	argv := []string{}
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		if t.kind == bashTokRedir {
			if i+1 >= len(tokens) {
				return cmd, &parseError{msg: "redirect operator with no target"}
			}
			target := tokens[i+1].value
			cmd.Redirects = append(cmd.Redirects, translate.Redirect{
				Op:     opFromString(t.value),
				Target: target,
			})
			i += 2
			continue
		}
		argv = append(argv, t.value)
		i++
	}
	if len(argv) == 0 {
		return cmd, &parseError{msg: "command with no name"}
	}
	cmd.Name = argv[0]
	cmd.Args = argv[1:]
	cmd = cmd.WithLine(sourceLine)
	return cmd, nil
}

func opFromString(op string) translate.RedirectOp {
	switch op {
	case "<":
		return translate.RedirectIn
	case ">":
		return translate.RedirectOut
	case ">>":
		return translate.RedirectAppend
	case "2>":
		return translate.RedirectErrOut
	case "2>&1":
		return translate.RedirectErrToOut
	default:
		return translate.RedirectOut
	}
}

// parseError carries the human-readable reason for an Unknown node.
type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

// splitOnPipe splits a token slice on `|` separators.
func splitOnPipe(tokens []bashToken) [][]bashToken {
	out := [][]bashToken{}
	cur := []bashToken{}
	for _, t := range tokens {
		if t.kind == bashTokPipe {
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

// bashToken is the tokenization output. We mark pipes and redirects
// separately so the statement parser can route them without
// re-scanning the string.
type bashToken struct {
	kind  bashTokKind
	value string
}

type bashTokKind int

const (
	bashTokWord bashTokKind = iota
	bashTokPipe
	bashTokRedir
)

// tokenizeBash splits a single logical line into tokens. Handles
// single + double-quoted strings, `$(...)` (kept verbatim in the
// token), `|`, and the redirection operators we recognise.
func tokenizeBash(line string) ([]bashToken, error) {
	out := []bashToken{}
	var cur strings.Builder
	inWord := false
	runes := []rune(line)
	flush := func() {
		if inWord {
			out = append(out, bashToken{kind: bashTokWord, value: cur.String()})
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '\'' {
					end = j
					break
				}
			}
			if end == -1 {
				return nil, &parseError{msg: "unterminated single quote"}
			}
			cur.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case r == '"':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '\\' {
					j++ // skip escaped char
					continue
				}
				if runes[j] == '"' {
					end = j
					break
				}
			}
			if end == -1 {
				return nil, &parseError{msg: "unterminated double quote"}
			}
			inner := string(runes[i+1 : end])
			// Unescape `\"` → `"` inside double quotes.
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			cur.WriteString(inner)
			inWord = true
			i = end
		case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
			// `$(...)` — collect through matching `)` (nesting-aware).
			depth := 1
			j := i + 2
			for j < len(runes) && depth > 0 {
				switch runes[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			if depth != 0 {
				return nil, &parseError{msg: "unterminated $(...) substitution"}
			}
			cur.WriteString(string(runes[i : j+1]))
			inWord = true
			i = j
		case r == '|':
			flush()
			out = append(out, bashToken{kind: bashTokPipe})
		case r == '>' && i+1 < len(runes) && runes[i+1] == '>':
			flush()
			out = append(out, bashToken{kind: bashTokRedir, value: ">>"})
			i++
		case r == '>':
			flush()
			out = append(out, bashToken{kind: bashTokRedir, value: ">"})
		case r == '<':
			flush()
			out = append(out, bashToken{kind: bashTokRedir, value: "<"})
		case r == '2' && i+1 < len(runes) && runes[i+1] == '>':
			// `2>&1` or `2> file`.
			if i+3 < len(runes) && runes[i+2] == '&' && runes[i+3] == '1' {
				flush()
				out = append(out, bashToken{kind: bashTokRedir, value: "2>&1"})
				// 2>&1 needs no target — synthesise an empty one so the
				// statement parser's redirect-with-target loop doesn't
				// trip.
				out = append(out, bashToken{kind: bashTokWord, value: ""})
				i += 3
				continue
			}
			// Otherwise: 2>... — treat as `2>` token.
			if r == '2' && i+1 < len(runes) && runes[i+1] == '>' && !inWord {
				flush()
				out = append(out, bashToken{kind: bashTokRedir, value: "2>"})
				i++
				continue
			}
			cur.WriteRune(r)
			inWord = true
		case r == ' ' || r == '\t':
			flush()
		case r == ';':
			// A bare `;` inside a single statement-line ends the
			// statement — but we're being called per logical line, so
			// trailing `;` is just stripped. Anything after `;` on the
			// same line falls into the "two statements on one line"
			// territory, which the MVP does NOT split (we surface
			// the second part by leaving the `;` as a word, which lets
			// the caller see it).
			flush()
			// Stop here — discard everything after.
			return out, nil
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out, nil
}

// splitWords is a lightweight tokenizer for the `for VAR in WORDS`
// header: splits on whitespace, respecting quotes. Returns words with
// quotes stripped.
func splitWords(s string) []string {
	toks, err := tokenizeBash(s)
	if err != nil {
		return []string{s}
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if t.kind == bashTokWord {
			out = append(out, t.value)
		}
	}
	return out
}

// stripOuterQuotes peels one layer of matching single or double
// quotes off s.
func stripOuterQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
