package reader

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// ReadPowerShell parses a PowerShell source string into a
// translate.Script.
//
// MVP scope (v1.0-3 task #143):
//
//   - Line comments: `# …`
//   - Block comments: `<# … #>` (one statement, source preserved)
//   - Variable assignment: `$Name = value` (no operators beyond `=`;
//     `+=` and friends become Unknown)
//   - Cmdlet calls + arguments: `Write-Host 'hello'`,
//     `Get-Service spooler` — name is the verb-noun token, args are
//     the remaining tokens with quotes stripped.
//   - Pipelines: `cmd1 | cmd2 | cmd3` — each stage is a Command.
//   - Simple `if (TEST) { BODY } [else { ELSE }]` — single-arm only;
//     `elseif` chains are Unknown (MVP).
//   - `function NAME { … }` definitions.
//
// Everything outside this surface becomes a translate.Unknown node
// with the original source line preserved. We never silently drop
// a construct.
//
// The lexer is a deliberate hand-roll: mvdan.cc/sh has no
// PowerShell support, and vendoring a tree-sitter grammar is
// explicitly deferred per the plan's Alternatives Table.
func ReadPowerShell(src string) (*translate.Script, error) {
	lines := psLogicalLines(src)
	stmts := psParseBlock(lines, 0, len(lines))
	return &translate.Script{
		Dialect:    translate.DialectPowerShell,
		Statements: stmts,
	}, nil
}

// psLogicalLines splits src into one entry per logical line,
// flattening backtick-continuations (`<EOL>`) and merging
// `<# … #>` block comments into a single synthesised line so the
// statement parser sees them as one token.
func psLogicalLines(src string) []physicalLine {
	out := []physicalLine{}
	raw := strings.Split(src, "\n")
	i := 0
	for i < len(raw) {
		ln := strings.TrimRight(raw[i], "\r")
		startLine := i + 1
		// Block comment: collect everything until matching `#>`.
		if strings.Contains(ln, "<#") && !strings.Contains(ln, "#>") {
			var b strings.Builder
			b.WriteString(ln)
			for i+1 < len(raw) {
				i++
				b.WriteString("\n")
				next := strings.TrimRight(raw[i], "\r")
				b.WriteString(next)
				if strings.Contains(next, "#>") {
					break
				}
			}
			out = append(out, physicalLine{line: startLine, text: b.String()})
			i++
			continue
		}
		// Backtick continuation — PowerShell uses ` (backtick) at EOL
		// to join lines. Same treatment as bash `\` continuation.
		for psEndsWithContinuation(ln) && i+1 < len(raw) {
			ln = ln[:len(ln)-1] + " " + strings.TrimRight(raw[i+1], "\r")
			i++
		}
		out = append(out, physicalLine{line: startLine, text: ln})
		i++
	}
	return out
}

// psEndsWithContinuation reports whether s ends with a PowerShell
// line continuation (an unescaped trailing backtick).
func psEndsWithContinuation(s string) bool {
	s = strings.TrimRight(s, " \t")
	return strings.HasSuffix(s, "`")
}

// psParseBlock walks a [start, end) slice of physicalLines and
// returns the parsed statements. `end` is exclusive so the function
// can be reused for braced blocks (the brace-walker passes the
// inside-range only).
func psParseBlock(lines []physicalLine, start, end int) []translate.Statement {
	stmts := []translate.Statement{}
	for i := start; i < end; {
		ln := lines[i]
		trimmed := strings.TrimSpace(ln.text)
		if trimmed == "" {
			i++
			continue
		}
		// Block comment — preserved verbatim.
		if strings.HasPrefix(trimmed, "<#") {
			stmts = append(stmts, translate.Comment{Text: trimmed}.WithLine(ln.line))
			i++
			continue
		}
		// Line comment.
		if strings.HasPrefix(trimmed, "#") {
			stmts = append(stmts, translate.Comment{Text: trimmed}.WithLine(ln.line))
			i++
			continue
		}
		// `function NAME { … }` or `function NAME (params) { … }`.
		if strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "function\t") {
			fn, next := psParseFunction(lines, i, end)
			stmts = append(stmts, fn)
			i = next
			continue
		}
		// `if (TEST) { … } [else { … }]`.
		if strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "if(") {
			cond, next := psParseIf(lines, i, end)
			stmts = append(stmts, cond)
			i = next
			continue
		}
		// `elseif` outside an `if` chain — surface as Unknown; the
		// MVP doesn't support multi-arm conditionals.
		if strings.HasPrefix(trimmed, "elseif") {
			stmts = append(stmts, translate.Unknown{
				Reason: "elseif chains unsupported (MVP)",
				Source: trimmed,
			}.WithLine(ln.line))
			i++
			continue
		}
		// `$VAR = value` assignment.
		if name, val, ok := psSplitAssignment(trimmed); ok {
			stmts = append(stmts, translate.Assign{Name: name, Value: val}.WithLine(ln.line))
			i++
			continue
		}
		// Anything else — try to parse as a pipeline / command.
		stmts = append(stmts, psParseStatementLine(trimmed, ln.line))
		i++
	}
	return stmts
}

// psParseFunction consumes `function NAME [(args)] { BODY }`. If the
// opening `{` is not on the same line we still find it; the body
// runs until the matching `}` (depth-tracked).
func psParseFunction(lines []physicalLine, idx, end int) (translate.Statement, int) {
	header := strings.TrimSpace(lines[idx].text)
	startLine := lines[idx].line
	rest := strings.TrimSpace(strings.TrimPrefix(header, "function"))
	// Name is everything up to the first space, paren, or brace.
	name := rest
	for j, r := range rest {
		if r == ' ' || r == '(' || r == '{' || r == '\t' {
			name = strings.TrimSpace(rest[:j])
			break
		}
	}
	bodyStart, bodyEnd := psFindBracedBlock(lines, idx, end)
	if bodyStart < 0 {
		// No opening brace found — surface as Unknown rather than
		// half-parsed function.
		return translate.Unknown{
			Reason: "function body without `{`",
			Source: header,
		}.WithLine(startLine), idx + 1
	}
	body := psParseBlock(lines, bodyStart, bodyEnd)
	return translate.FuncDef{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Name:     name,
		Body:     body,
	}, bodyEnd + 1
}

// psParseIf consumes `if (TEST) { THEN } [else { ELSE }]`. Single
// arm only; elseif chains are reported elsewhere as Unknown.
func psParseIf(lines []physicalLine, idx, end int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(lines[idx].text)
	// Extract the test expression inside the first `(...)`.
	openParen := strings.Index(header, "(")
	closeParen := psMatchParen(header, openParen)
	if openParen < 0 || closeParen < 0 {
		return translate.Unknown{
			Reason: "if without `(TEST)` parens",
			Source: header,
		}.WithLine(startLine), idx + 1
	}
	test := strings.TrimSpace(header[openParen+1 : closeParen])
	// Body braced block.
	bodyStart, bodyEnd := psFindBracedBlock(lines, idx, end)
	if bodyStart < 0 {
		return translate.Unknown{
			Reason: "if without `{ BODY }`",
			Source: header,
		}.WithLine(startLine), idx + 1
	}
	thenBody := psParseBlock(lines, bodyStart, bodyEnd)
	cond := translate.Cond{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Branches: []translate.CondBranch{{
			Test: psParseStatementLine(test, startLine),
			Body: thenBody,
		}},
	}
	// `else` arm — check first whether `else` follows on the same
	// line as the closing `}` (the `} else {` idiom). Then fall back
	// to the next physical line.
	next := bodyEnd + 1
	tailLine := lines[bodyEnd]
	tail := strings.TrimSpace(tailLine.text)
	if afterClose := afterFirstCloseBrace(tail); afterClose != "" {
		// `}` is followed by something on the same line.
		afterClose = strings.TrimSpace(afterClose)
		if afterClose == "else" || strings.HasPrefix(afterClose, "else ") || strings.HasPrefix(afterClose, "else{") {
			// Splice a synthetic line starting at `else …` and
			// recurse. Easiest: walk forward looking for the new
			// braced block beginning at bodyEnd.
			lines[bodyEnd].text = afterClose // rewrite for the scanner
			elseStart, elseEnd := psFindBracedBlock(lines, bodyEnd, end)
			if elseStart >= 0 {
				cond.Else = psParseBlock(lines, elseStart, elseEnd)
				return cond, elseEnd + 1
			}
		} else if strings.HasPrefix(afterClose, "elseif") {
			// elseif chains are out of MVP scope. Replace the
			// statement with an Unknown so the caller surfaces it
			// rather than half-parsing the chain.
			return translate.Unknown{
				Reason: "elseif chains unsupported (MVP)",
				Source: strings.TrimSpace(lines[idx].text),
			}.WithLine(startLine), bodyEnd + 1
		}
	}
	if next < end {
		nt := strings.TrimSpace(lines[next].text)
		if nt == "else" || strings.HasPrefix(nt, "else ") || strings.HasPrefix(nt, "else{") {
			elseStart, elseEnd := psFindBracedBlock(lines, next, end)
			if elseStart >= 0 {
				cond.Else = psParseBlock(lines, elseStart, elseEnd)
				next = elseEnd + 1
			}
		} else if strings.HasPrefix(nt, "elseif") {
			return translate.Unknown{
				Reason: "elseif chains unsupported (MVP)",
				Source: strings.TrimSpace(lines[idx].text),
			}.WithLine(startLine), next + 1
		}
	}
	return cond, next
}

// afterFirstCloseBrace returns the substring of s following the
// first unmatched `}`. Used by psParseIf to detect the `} else {`
// idiom where else lives on the same line as the closing brace.
// Returns "" when s contains no top-level `}`.
func afterFirstCloseBrace(s string) string {
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return s[i+1:]
			}
		}
	}
	return ""
}

// psFindBracedBlock locates the `{ … }` immediately following idx
// (possibly on the same line as the header) and returns the
// half-open range [bodyStart, bodyEnd) of its interior lines. The
// `bodyEnd` line is the line carrying the matching `}`. Returns
// (-1, -1) when no opening brace is found within range.
func psFindBracedBlock(lines []physicalLine, idx, end int) (int, int) {
	// Find the opening `{`.
	openIdx := -1
	for j := idx; j < end; j++ {
		if strings.Contains(lines[j].text, "{") {
			openIdx = j
			break
		}
	}
	if openIdx < 0 {
		return -1, -1
	}
	// Walk depth across remaining lines until we close the brace.
	depth := 0
	for j := openIdx; j < end; j++ {
		for _, r := range lines[j].text {
			switch r {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					// Body is the half-open range [openIdx+1, j).
					return openIdx + 1, j
				}
			}
		}
	}
	return -1, -1
}

// psMatchParen returns the index of the `)` matching the `(` at
// open, or -1 if unmatched.
func psMatchParen(s string, open int) int {
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

// psSplitAssignment recognises `$NAME = VALUE` (and `[type]$NAME =
// VALUE`, MVP strips the type sigil). Returns (name, value, true)
// only when the LHS is a single `$Name` token. Other operators
// (`+=`, `-=`) fall through to the statement parser, which will
// produce an Unknown.
func psSplitAssignment(line string) (string, string, bool) {
	// Strip a leading `[type]` sigil if present.
	rest := line
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end >= 0 {
			rest = strings.TrimSpace(rest[end+1:])
		}
	}
	if !strings.HasPrefix(rest, "$") {
		return "", "", false
	}
	eq := strings.Index(rest, "=")
	if eq <= 0 {
		return "", "", false
	}
	// Reject `==`, `+=`, `-=`, `*=`, `/=`.
	if eq+1 < len(rest) && rest[eq+1] == '=' {
		return "", "", false
	}
	if eq > 0 {
		switch rest[eq-1] {
		case '+', '-', '*', '/', '!', '<', '>':
			return "", "", false
		}
	}
	lhs := strings.TrimSpace(rest[:eq])
	rhs := strings.TrimSpace(rest[eq+1:])
	// LHS must be `$Name` with an identifier.
	if !strings.HasPrefix(lhs, "$") {
		return "", "", false
	}
	name := strings.TrimPrefix(lhs, "$")
	if !isIdent(name) {
		return "", "", false
	}
	return name, stripOuterQuotes(rhs), true
}

// psClassifyUnsupported recognises out-of-MVP PowerShell constructs
// by lexical fingerprint. Returns a human-readable reason and ok=true
// when the line should be classified Unknown rather than parsed as
// a command.
func psClassifyUnsupported(line string) (string, bool) {
	switch {
	case strings.Contains(line, " += "):
		return "compound assignment += unsupported (MVP)", true
	case strings.Contains(line, " -= "):
		return "compound assignment -= unsupported (MVP)", true
	case strings.Contains(line, " *= "):
		return "compound assignment *= unsupported (MVP)", true
	case strings.Contains(line, " /= "):
		return "compound assignment /= unsupported (MVP)", true
	case strings.HasPrefix(strings.TrimSpace(line), "try "), strings.HasPrefix(strings.TrimSpace(line), "try{"):
		return "try/catch unsupported (MVP)", true
	case strings.HasPrefix(strings.TrimSpace(line), "param("):
		return "param() blocks unsupported (MVP)", true
	}
	return "", false
}

// psParseStatementLine parses a single non-block PowerShell line —
// command / pipeline / Unknown.
func psParseStatementLine(line string, sourceLine int) translate.Statement {
	// Compound assignment (`+=`, `-=`, `*=`, `/=`) is out of MVP
	// scope — surface as Unknown rather than half-parse as a
	// command with operator arguments.
	if reason, ok := psClassifyUnsupported(line); ok {
		return translate.Unknown{Reason: reason, Source: line}.WithLine(sourceLine)
	}
	tokens, err := psTokenize(line)
	if err != nil {
		return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
	}
	if len(tokens) == 0 {
		return translate.Unknown{Reason: "empty statement", Source: line}.WithLine(sourceLine)
	}
	// Pipeline?
	stages := psSplitOnPipe(tokens)
	if len(stages) > 1 {
		pipe := translate.Pipe{}
		for _, st := range stages {
			c, err := psTokensToCommand(st, sourceLine)
			if err != nil {
				return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
			}
			pipe.Stages = append(pipe.Stages, c)
		}
		return pipe.WithLine(sourceLine)
	}
	c, err := psTokensToCommand(tokens, sourceLine)
	if err != nil {
		return translate.Unknown{Reason: err.Error(), Source: line}.WithLine(sourceLine)
	}
	return c
}

// psTokensToCommand collects argv from a flat PS token slice.
// PowerShell has no shell-style redirection in the MVP — `>` /
// `>>` are uncommon in the kinds of admin scripts we care about,
// and full support belongs with the rest of the >v1.0 surface.
func psTokensToCommand(tokens []string, sourceLine int) (translate.Command, error) {
	if len(tokens) == 0 {
		return translate.Command{}, &parseError{msg: "command with no name"}
	}
	cmd := translate.Command{
		Name: tokens[0],
		Args: append([]string{}, tokens[1:]...),
	}
	return cmd.WithLine(sourceLine), nil
}

// psSplitOnPipe slices a token list on bare `|` separators.
func psSplitOnPipe(tokens []string) [][]string {
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

// psTokenize splits a single line into PowerShell tokens. Handles:
//   - Single quotes (no escapes — PS literal-string semantics).
//   - Double quotes (`\"` escapes a quote; `$var` left verbatim).
//   - Bare words split on whitespace.
//   - `|` emitted as its own token.
func psTokenize(line string) ([]string, error) {
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
		case r == '\'':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '\'' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, &parseError{msg: "unterminated single quote"}
			}
			cur.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case r == '"':
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '`' {
					// `"` escape via backtick (PowerShell convention).
					j++
					continue
				}
				if runes[j] == '"' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, &parseError{msg: "unterminated double quote"}
			}
			inner := string(runes[i+1 : end])
			inner = strings.ReplaceAll(inner, "`\"", "\"")
			cur.WriteString(inner)
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
