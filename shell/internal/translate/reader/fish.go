package reader

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// ReadFish parses a fish source string. Fish syntax differs from
// bash in three material ways for MVP:
//
//   - `set [-l|-g|-x] VAR VALUE…` instead of `VAR=VALUE`.
//   - Block constructs end with `end`, not `fi`/`done`/`esac`.
//   - Command substitution is `(cmd)`, not `$(cmd)`.
//
// We share the tokenizer with bash (close enough for the surface
// we cover) but use a fish-specific block parser.
func ReadFish(src string) (*translate.Script, error) {
	lines := lexLines(src)
	stmts, _ := parseFishBlock(lines, 0, nil)
	return &translate.Script{
		Dialect:    translate.DialectFish,
		Statements: stmts,
	}, nil
}

func parseFishBlock(lines []physicalLine, idx int, terminators []string) ([]translate.Statement, int) {
	stmts := make([]translate.Statement, 0, 8)
	for idx < len(lines) {
		ln := lines[idx]
		trimmed := strings.TrimSpace(ln.text)
		if trimmed == "" {
			idx++
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			stmts = append(stmts, translate.Comment{Text: trimmed}.WithLine(ln.line))
			idx++
			continue
		}
		first := firstWord(trimmed)
		for _, t := range terminators {
			if first == t {
				return stmts, idx
			}
		}
		switch first {
		case "if":
			c, next := parseFishIf(lines, idx)
			stmts = append(stmts, c)
			idx = next
			continue
		case "for":
			l, next := parseFishFor(lines, idx)
			stmts = append(stmts, l)
			idx = next
			continue
		case "while":
			l, next := parseFishWhile(lines, idx)
			stmts = append(stmts, l)
			idx = next
			continue
		case "function":
			f, next := parseFishFunction(lines, idx)
			stmts = append(stmts, f)
			idx = next
			continue
		case "switch":
			c, next := parseFishSwitch(lines, idx)
			stmts = append(stmts, c)
			idx = next
			continue
		case "set":
			stmts = append(stmts, parseFishSet(trimmed, ln.line))
			idx++
			continue
		}
		// Fish-specific Unknown classifications.
		if reason, ok := classifyFishUnsupported(trimmed); ok {
			stmts = append(stmts, translate.Unknown{Reason: reason, Source: trimmed}.WithLine(ln.line))
			idx++
			continue
		}
		// Plain statement / pipe.
		stmts = append(stmts, parseStatementLine(trimmed, ln.line))
		idx++
	}
	return stmts, idx
}

func parseFishIf(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	cond := translate.Cond{BaseStmt: translate.BaseStmt{Line: startLine}}
	// Fish: `if TEST` (no `then`). Body runs until `else`/`else if`/`end`.
	header := strings.TrimSpace(lines[idx].text)
	header = strings.TrimSpace(strings.TrimPrefix(header, "if"))
	cond.Branches = append(cond.Branches, translate.CondBranch{
		Test: parseStatementLine(header, startLine),
	})
	idx++
	for idx < len(lines) {
		body, next := parseFishBlock(lines, idx, []string{"else", "end"})
		cond.Branches[len(cond.Branches)-1].Body = body
		idx = next
		if idx >= len(lines) {
			break
		}
		t := strings.TrimSpace(lines[idx].text)
		first := firstWord(t)
		switch first {
		case "else":
			rest := strings.TrimSpace(strings.TrimPrefix(t, "else"))
			if strings.HasPrefix(rest, "if") {
				header := strings.TrimSpace(strings.TrimPrefix(rest, "if"))
				cond.Branches = append(cond.Branches, translate.CondBranch{
					Test: parseStatementLine(header, lines[idx].line),
				})
				idx++
				continue
			}
			idx++
			body, next := parseFishBlock(lines, idx, []string{"end"})
			cond.Else = body
			idx = next
		case "end":
			idx++
			return cond, idx
		default:
			return cond, idx
		}
	}
	return cond, idx
}

func parseFishFor(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[idx].text), "for"))
	// `VAR in WORDS`
	varName := header
	var words []string
	if i := strings.Index(header, " in "); i >= 0 {
		varName = strings.TrimSpace(header[:i])
		words = splitWords(strings.TrimSpace(header[i+4:]))
	}
	idx++
	body, next := parseFishBlock(lines, idx, []string{"end"})
	idx = next
	if idx < len(lines) && firstWord(strings.TrimSpace(lines[idx].text)) == "end" {
		idx++
	}
	return translate.Loop{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Kind:     translate.LoopFor,
		Var:      varName,
		Words:    words,
		Body:     body,
	}, idx
}

func parseFishWhile(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[idx].text), "while"))
	test := parseStatementLine(header, startLine)
	idx++
	body, next := parseFishBlock(lines, idx, []string{"end"})
	idx = next
	if idx < len(lines) && firstWord(strings.TrimSpace(lines[idx].text)) == "end" {
		idx++
	}
	return translate.Loop{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Kind:     translate.LoopWhile,
		Test:     test,
		Body:     body,
	}, idx
}

func parseFishFunction(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[idx].text), "function"))
	name := header
	if i := strings.IndexAny(header, " \t"); i >= 0 {
		name = strings.TrimSpace(header[:i])
	}
	idx++
	body, next := parseFishBlock(lines, idx, []string{"end"})
	idx = next
	if idx < len(lines) && firstWord(strings.TrimSpace(lines[idx].text)) == "end" {
		idx++
	}
	return translate.FuncDef{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Name:     name,
		Body:     body,
	}, idx
}

func parseFishSwitch(lines []physicalLine, idx int) (translate.Statement, int) {
	startLine := lines[idx].line
	header := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[idx].text), "switch"))
	word := stripOuterQuotes(header)
	idx++
	c := translate.Case{
		BaseStmt: translate.BaseStmt{Line: startLine},
		Word:     word,
	}
	for idx < len(lines) {
		t := strings.TrimSpace(lines[idx].text)
		first := firstWord(t)
		if first == "end" {
			idx++
			return c, idx
		}
		if first == "case" {
			pats := splitWords(strings.TrimSpace(strings.TrimPrefix(t, "case")))
			idx++
			body, next := parseFishBlock(lines, idx, []string{"case", "end"})
			idx = next
			c.Arms = append(c.Arms, translate.CaseArm{Patterns: pats, Body: body})
			continue
		}
		// Defensive: unexpected — skip.
		idx++
	}
	return c, idx
}

// parseFishSet renders `set [-flag…] NAME VALUE…` as a translate.Assign.
// Multiple values join into a single space-separated string (close
// enough for MVP; arrays are deferred).
func parseFishSet(line string, sourceLine int) translate.Statement {
	parts := splitWords(strings.TrimSpace(strings.TrimPrefix(line, "set")))
	exported := false
	i := 0
	for ; i < len(parts); i++ {
		p := parts[i]
		if !strings.HasPrefix(p, "-") {
			break
		}
		// fish `-x` exports; `-l`/`-g` set scope; `-e` erases (defer);
		// `-q` queries (defer).
		if strings.Contains(p, "x") {
			exported = true
		}
		if strings.Contains(p, "e") || strings.Contains(p, "q") {
			return translate.Unknown{
				BaseStmt: translate.BaseStmt{Line: sourceLine},
				Reason:   "fish `set -e` / `set -q` unsupported",
				Source:   line,
			}
		}
	}
	if i >= len(parts) {
		return translate.Unknown{
			BaseStmt: translate.BaseStmt{Line: sourceLine},
			Reason:   "fish `set` without variable name",
			Source:   line,
		}
	}
	name := parts[i]
	value := strings.Join(parts[i+1:], " ")
	return translate.Assign{
		BaseStmt: translate.BaseStmt{Line: sourceLine},
		Name:     name,
		Value:    value,
		Exported: exported,
	}
}

// classifyFishUnsupported flags fish-only out-of-scope shapes.
func classifyFishUnsupported(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "begin"):
		return "fish `begin` block unsupported", true
	case strings.HasPrefix(line, "string "):
		// Many fish scripts use `string` builtin; the simple forms
		// (`string trim`, `string split`) are translatable later but
		// out of MVP.
		return "fish `string` builtin unsupported (MVP)", true
	}
	// Backtick or `&&`/`||` handled by classifyUnsupported via the
	// shared statement parser.
	if reason, ok := classifyUnsupported(line); ok {
		return reason, true
	}
	return "", false
}
